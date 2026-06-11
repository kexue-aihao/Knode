package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
	"kray/pkg/kless"
)

type serverRelay struct {
	cfg      config.ServerKLESSConfig
	signing  ed25519.PrivateKey
	store    *dynamicClientStore
	logger   *log.Logger
	metrics  *Metrics
	dialer   net.Dialer
	listenID string
}

func newServerRelay(listenID string, cfg config.ServerKLESSConfig, logger *log.Logger, metrics *Metrics, store *dynamicClientStore) (*serverRelay, error) {
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

	target, err := readConnectRequest(secure)
	if err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s relay request failed: %v", r.listenID, info.ClientID, err)
		return
	}

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	upstream, err := r.dialer.DialContext(dialCtx, "tcp", target.Address())
	cancel()
	if err != nil {
		r.metrics.addTransportError()
		_ = writeConnectResponse(secure, err.Error())
		r.logger.Printf("inbound %s client %s target %s failed: %v", r.listenID, info.ClientID, target.Address(), err)
		return
	}
	defer upstream.Close()

	if err := writeConnectResponse(secure, ""); err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s response failed: %v", r.listenID, info.ClientID, err)
		return
	}

	stopOnCancel := context.AfterFunc(ctx, func() {
		_ = upstream.Close()
		_ = secure.Close()
	})
	defer stopOnCancel()

	if err := r.pipe(secure, upstream); err != nil {
		r.metrics.addProxyError()
		r.logger.Printf("inbound %s client %s target %s closed with error: %v", r.listenID, info.ClientID, target.Address(), err)
	}
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
