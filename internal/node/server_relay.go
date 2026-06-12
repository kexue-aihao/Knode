package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
	"kray/pkg/kless"
	"kray/pkg/relay"
)

type serverRelay struct {
	cfg      config.ServerKLESSConfig
	signing  ed25519.PrivateKey
	store    *dynamicClientStore
	logger   *log.Logger
	metrics  *Metrics
	dialer   net.Dialer
	listenID string
	access   *accessLogBuffer
}

func newServerRelay(listenID string, cfg config.ServerKLESSConfig, logger *log.Logger, metrics *Metrics, store *dynamicClientStore, access *accessLogBuffer) (*serverRelay, error) {
	privateKey, err := cfg.ServerSigningPrivateBytes()
	if err != nil {
		return nil, err
	}
	if store == nil {
		store = newDynamicClientStore()
	}
	staticCredentials, err := staticClientCredentials(cfg)
	if err != nil {
		return nil, err
	}
	store.AddStatic(staticCredentials)
	return &serverRelay{
		cfg:      cfg,
		signing:  privateKey,
		store:    store,
		logger:   logger,
		metrics:  metrics,
		listenID: listenID,
		dialer:   net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
		access:   access,
	}, nil
}

func staticClientCredentials(cfg config.ServerKLESSConfig) ([]ClientCredential, error) {
	credentials := make([]ClientCredential, 0, len(cfg.Clients)+1)
	if cfg.ClientID != "" && cfg.ClientSecret != "" {
		secret, err := cfg.ClientSecretBytes()
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, ClientCredential{
			ClientID: cfg.ClientID,
			Secret:   secret,
		})
	}
	for _, client := range cfg.Clients {
		secret, err := client.ClientSecretBytes()
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, ClientCredential{
			ClientID: client.ClientID,
			Secret:   secret,
		})
	}
	return credentials, nil
}

func (r *serverRelay) Handle(ctx context.Context, raw net.Conn) {
	secure, info, err := kless.ServerHandshake(raw, r.serverConfig())
	if err != nil {
		r.metrics.addHandshakeError()
		r.logger.Printf("inbound %s kless server handshake failed: %v", r.listenID, err)
		return
	}
	defer secure.Close()

	req, err := readRelayRequest(secure)
	if err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s relay request failed: %v", r.listenID, info.ClientID, err)
		return
	}

	switch req.Command {
	case relay.CommandTCPConnect:
		r.handleTCPConnect(ctx, raw, secure, info.ClientID, req.Address)
	case relay.CommandUDPAssociate:
		r.handleUDPAssociate(ctx, raw, secure, info.ClientID)
	default:
		r.metrics.addProxyError()
		_ = relay.WriteResponse(secure, relay.Response{Status: relay.StatusUnsupportedCommand, Message: "unsupported relay command"})
		r.logger.Printf("inbound %s client %s unsupported relay command %d", r.listenID, info.ClientID, req.Command)
	}
}

func (r *serverRelay) handleTCPConnect(ctx context.Context, raw net.Conn, secure io.ReadWriteCloser, clientID string, target RelayTarget) {
	if r.access != nil {
		userID := r.store.UserID(clientID)
		if ok := r.access.Record(accessLogItem(clientID, userID, target, "tcp", raw.RemoteAddr().String(), time.Now())); !ok {
			r.logger.Printf("inbound %s client %s access log queue is full or invalid", r.listenID, clientID)
		}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	upstream, err := r.dialer.DialContext(dialCtx, "tcp", target.String())
	cancel()
	if err != nil {
		r.metrics.addTransportError()
		_ = writeConnectResponse(secure, err.Error())
		r.logger.Printf("inbound %s client %s target %s failed: %v", r.listenID, clientID, target.String(), err)
		return
	}
	defer upstream.Close()

	if err := writeConnectResponse(secure, ""); err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s response failed: %v", r.listenID, clientID, err)
		return
	}

	stopOnCancel := context.AfterFunc(ctx, func() {
		_ = upstream.Close()
		_ = secure.Close()
	})
	defer stopOnCancel()

	if err := r.pipe(secure, upstream); err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s target %s closed with error: %v", r.listenID, clientID, target.String(), err)
	}
}

func (r *serverRelay) handleUDPAssociate(ctx context.Context, raw net.Conn, secure io.ReadWriteCloser, clientID string) {
	udp, err := net.ListenPacket("udp", ":0")
	if err != nil {
		r.metrics.addTransportError()
		_ = relay.WriteResponse(secure, relay.Response{Status: relay.StatusDialFailed, Message: err.Error()})
		r.logger.Printf("inbound %s client %s udp associate failed: %v", r.listenID, clientID, err)
		return
	}
	defer udp.Close()

	if err := relay.WriteResponse(secure, relay.Response{Status: relay.StatusOK}); err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s udp associate response failed: %v", r.listenID, clientID, err)
		return
	}

	stopOnCancel := context.AfterFunc(ctx, func() {
		_ = udp.Close()
		_ = secure.Close()
	})
	defer stopOnCancel()

	if err := r.pipeUDPAssociate(ctx, raw, secure, udp, clientID); err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s udp associate closed with error: %v", r.listenID, clientID, err)
	}
}

func (r *serverRelay) pipeUDPAssociate(ctx context.Context, raw net.Conn, secure io.ReadWriteCloser, udp net.PacketConn, clientID string) error {
	errCh := make(chan error, 2)
	var writeMu sync.Mutex
	seenTargets := make(map[string]struct{})
	var seenMu sync.Mutex

	go func() {
		for {
			datagram, err := relay.ReadDatagram(secure)
			if err != nil {
				errCh <- normalizeCopyError(err)
				return
			}
			target, err := resolveUDPAddress(datagram.Address)
			if err != nil {
				errCh <- err
				return
			}
			n, err := udp.WriteTo(datagram.Payload, target)
			if n > 0 {
				r.metrics.addUpBytes(uint64(n))
			}
			if err != nil {
				errCh <- normalizeCopyError(err)
				return
			}
			r.recordUDPAccess(clientID, raw.RemoteAddr().String(), datagram.Address, seenTargets, &seenMu)
		}
	}()

	go func() {
		buf := make([]byte, relay.MaxDatagramSize)
		for {
			n, addr, err := udp.ReadFrom(buf)
			if err != nil {
				errCh <- normalizeCopyError(err)
				return
			}
			target, ok := relayAddressFromUDPAddr(addr)
			if !ok {
				continue
			}
			payload := append([]byte(nil), buf[:n]...)
			writeMu.Lock()
			err = relay.WriteDatagram(secure, relay.Datagram{
				Address: target,
				Payload: payload,
			})
			writeMu.Unlock()
			if n > 0 {
				r.metrics.addDownBytes(uint64(n))
			}
			if err != nil {
				errCh <- normalizeCopyError(err)
				return
			}
		}
	}()

	select {
	case err := <-errCh:
		_ = udp.Close()
		_ = secure.Close()
		second := <-errCh
		if err != nil {
			return err
		}
		return second
	case <-ctx.Done():
		_ = udp.Close()
		_ = secure.Close()
		return nil
	}
}

func (r *serverRelay) recordUDPAccess(clientID, remoteAddr string, target RelayTarget, seen map[string]struct{}, mu *sync.Mutex) {
	if r.access == nil {
		return
	}
	key := strings.ToLower(target.String())
	mu.Lock()
	if _, ok := seen[key]; ok {
		mu.Unlock()
		return
	}
	seen[key] = struct{}{}
	mu.Unlock()

	userID := r.store.UserID(clientID)
	if ok := r.access.Record(accessLogItem(clientID, userID, target, "udp", remoteAddr, time.Now())); !ok {
		r.logger.Printf("inbound %s client %s udp access log queue is full or invalid", r.listenID, clientID)
	}
}

func resolveUDPAddress(address relay.Address) (*net.UDPAddr, error) {
	if strings.TrimSpace(address.Host) == "" || address.Port == 0 {
		return nil, relay.ErrInvalidAddress
	}
	return net.ResolveUDPAddr("udp", address.String())
}

func relayAddressFromUDPAddr(addr net.Addr) (relay.Address, bool) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok || udpAddr == nil || udpAddr.IP == nil || udpAddr.Port <= 0 || udpAddr.Port > 65535 {
		return relay.Address{}, false
	}
	return relay.Address{
		Host: udpAddr.IP.String(),
		Port: uint16(udpAddr.Port),
	}, true
}

func (r *serverRelay) serverConfig() kless.ServerConfig {
	caps, _ := r.cfg.CapabilityMask()
	return kless.ServerConfig{
		SigningKey:       r.signing,
		ClientStore:      r.store,
		Capabilities:     caps,
		MaxFramePayload:  defaultRelayInt(r.cfg.MaxFramePayload, config.DefaultMaxFramePayload),
		PaddingMin:       r.cfg.PaddingMin,
		PaddingMax:       r.cfg.PaddingMax,
		MaxHandshakeSkew: r.cfg.MaxHandshakeSkewDuration(),
		HandshakeTimeout: r.cfg.HandshakeTimeoutDuration(),
	}
}

func defaultRelayInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func (r *serverRelay) pipe(client io.ReadWriteCloser, upstream net.Conn) error {
	errCh := make(chan copyResult, 2)
	go func() {
		n, err := io.Copy(upstream, client)
		r.metrics.addUpBytes(uint64(n))
		errCh <- copyResult{err: normalizeCopyError(err)}
	}()
	go func() {
		n, err := io.Copy(client, upstream)
		r.metrics.addDownBytes(uint64(n))
		errCh <- copyResult{err: normalizeCopyError(err)}
	}()

	first := <-errCh
	_ = upstream.Close()
	_ = client.Close()
	second := <-errCh
	if first.err != nil {
		return first.err
	}
	return second.err
}

type copyResult struct {
	err error
}

func requireKLESSServerRelay(inbound config.InboundConfig) error {
	if inbound.Mode != config.InboundModeKLESSServer {
		return fmt.Errorf("inbound %q is not kless-server", inbound.Name)
	}
	if inbound.KLESS.ServerSigningPrivate == "" {
		return errors.New("server_signing_private is required")
	}
	return nil
}
