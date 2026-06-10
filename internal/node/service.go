package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
)

type Service struct {
	cfg     config.Config
	logger  *log.Logger
	metrics *Metrics

	dialers map[string]*UpstreamDialer

	mu               sync.RWMutex
	adminAddr        string
	inboundAddrs     map[string]string
	startedListeners int
	connections      sync.WaitGroup
}

type Status struct {
	NodeID    string            `json:"node_id"`
	Admin     string            `json:"admin"`
	Inbounds  map[string]string `json:"inbounds"`
	Upstreams []string          `json:"upstreams"`
	Metrics   MetricsSnapshot   `json:"metrics"`
}

func New(cfg config.Config, logger *log.Logger) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}

	dialers := make(map[string]*UpstreamDialer, len(cfg.Upstreams))
	for _, upstream := range cfg.Upstreams {
		dialer, err := NewUpstreamDialer(upstream)
		if err != nil {
			return nil, fmt.Errorf("upstream %q: %w", upstream.Name, err)
		}
		dialers[upstream.Name] = dialer
	}

	return &Service{
		cfg:          cfg,
		logger:       logger,
		metrics:      NewMetrics(),
		dialers:      dialers,
		inboundAddrs: make(map[string]string, len(cfg.Inbounds)),
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(s.cfg.Inbounds)+1)
	var wg sync.WaitGroup
	var closers []io.Closer

	for _, inbound := range s.cfg.Inbounds {
		listener, err := net.Listen("tcp", inbound.Listen)
		if err != nil {
			cancel()
			closeAll(closers)
			wg.Wait()
			s.waitConnections()
			return fmt.Errorf("listen inbound %q: %w", inbound.Name, err)
		}
		closers = append(closers, listener)
		s.setInboundAddress(inbound.Name, listener.Addr().String())
		wg.Add(1)
		go func(inbound config.InboundConfig, listener net.Listener) {
			defer wg.Done()
			if err := s.acceptLoop(ctx, inbound, listener); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}(inbound, listener)
	}

	adminServer, adminListener, err := s.startAdmin(ctx)
	if err != nil {
		cancel()
		closeAll(closers)
		wg.Wait()
		s.waitConnections()
		return err
	}
	if adminServer != nil {
		closers = append(closers, adminListener)
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := adminServer.Serve(adminListener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("admin: %w", err)
			}
		}()
	}

	s.logger.Printf("knode %s started", s.cfg.NodeID)
	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case runErr = <-errCh:
	}

	cancel()
	closeAll(closers)
	if adminServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), s.cfg.ShutdownGraceDuration())
		_ = adminServer.Shutdown(shutdownCtx)
		shutdownCancel()
	}
	wg.Wait()
	s.waitConnections()
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return nil
	}
	return runErr
}

func (s *Service) Metrics() MetricsSnapshot {
	return s.metrics.Snapshot()
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inbounds := make(map[string]string, len(s.inboundAddrs))
	for name, address := range s.inboundAddrs {
		inbounds[name] = address
	}
	upstreams := make([]string, 0, len(s.cfg.Upstreams))
	for _, upstream := range s.cfg.Upstreams {
		upstreams = append(upstreams, upstream.Name)
	}
	return Status{
		NodeID:    s.cfg.NodeID,
		Admin:     s.adminAddr,
		Inbounds:  inbounds,
		Upstreams: upstreams,
		Metrics:   s.metrics.Snapshot(),
	}
}

func (s *Service) InboundAddress(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inboundAddrs[name]
}

func (s *Service) AdminAddress() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.adminAddr
}

func (s *Service) acceptLoop(ctx context.Context, inbound config.InboundConfig, listener net.Listener) error {
	s.logger.Printf("inbound %s listening on %s", inbound.Name, listener.Addr())
	var sem chan struct{}
	if inbound.MaxConnections > 0 {
		sem = make(chan struct{}, inbound.MaxConnections)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return context.Canceled
			}
			return fmt.Errorf("inbound %q accept: %w", inbound.Name, err)
		}
		if sem != nil {
			select {
			case sem <- struct{}{}:
			default:
				s.metrics.addRejected()
				_ = conn.Close()
				continue
			}
		}
		s.connections.Add(1)
		go func() {
			defer s.connections.Done()
			if sem != nil {
				defer func() { <-sem }()
			}
			s.handleConnection(ctx, inbound, conn)
		}()
	}
}

func (s *Service) handleConnection(ctx context.Context, inbound config.InboundConfig, local net.Conn) {
	s.metrics.beginConnection()
	defer s.metrics.endConnection()
	defer local.Close()

	dialer := s.dialers[inbound.Upstream]
	secure, err := dialer.Dial(ctx)
	if err != nil {
		var dialErr *DialError
		if errors.As(err, &dialErr) && dialErr.Stage == StageHandshake {
			s.metrics.addHandshakeError()
		} else {
			s.metrics.addTransportError()
		}
		s.logger.Printf("inbound %s upstream %s failed: %v", inbound.Name, inbound.Upstream, err)
		return
	}
	defer secure.Close()
	stopOnCancel := context.AfterFunc(ctx, func() {
		_ = local.Close()
		_ = secure.Close()
	})
	defer stopOnCancel()

	if err := s.pipe(local, secure); err != nil {
		s.metrics.addProxyError()
		s.logger.Printf("inbound %s proxy closed with error: %v", inbound.Name, err)
	}
}

func (s *Service) pipe(local net.Conn, upstream io.ReadWriteCloser) error {
	errCh := make(chan error, 2)
	go func() {
		n, err := io.Copy(upstream, local)
		s.metrics.addUpBytes(uint64(n))
		errCh <- normalizeCopyError(err)
	}()
	go func() {
		n, err := io.Copy(local, upstream)
		s.metrics.addDownBytes(uint64(n))
		errCh <- normalizeCopyError(err)
	}()

	err := <-errCh
	_ = upstream.Close()
	_ = local.Close()
	second := <-errCh
	if err != nil {
		return err
	}
	return second
}

func (s *Service) startAdmin(ctx context.Context) (*http.Server, net.Listener, error) {
	if s.cfg.Admin.Address == "" {
		return nil, nil, nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/status", s.handleStatus)

	listener, err := net.Listen("tcp", s.cfg.Admin.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("listen admin: %w", err)
	}
	s.setAdminAddress(listener.Addr().String())
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	return server, listener, nil
}

func (s *Service) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleReady(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	ready := s.startedListeners == len(s.cfg.Inbounds)
	s.mu.RUnlock()
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "starting"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Service) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics.Snapshot())
}

func (s *Service) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Status())
}

func (s *Service) setInboundAddress(name, address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inboundAddrs[name] = address
	s.startedListeners++
}

func (s *Service) setAdminAddress(address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adminAddr = address
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func closeAll(closers []io.Closer) {
	for _, closer := range closers {
		if closer != nil {
			_ = closer.Close()
		}
	}
}

func (s *Service) waitConnections() {
	done := make(chan struct{})
	go func() {
		s.connections.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(s.cfg.ShutdownGraceDuration()):
		s.logger.Print("shutdown grace elapsed with active connections")
	}
}

func normalizeCopyError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
