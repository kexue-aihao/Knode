package node

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/kexue-aihao/Knode/internal/config"
	"kray/pkg/kless"
	"kray/pkg/kless/transport/grpc"
	"kray/pkg/kless/transport/httpstream"
	"kray/pkg/kless/transport/httpupgrade"
	"kray/pkg/kless/transport/tcp"
	ktls "kray/pkg/kless/transport/tls"
	"kray/pkg/kless/transport/websocket"
	"kray/pkg/kless/transport/xhttp"
)

const (
	StageTransport = "transport"
	StageHandshake = "handshake"
)

type DialError struct {
	Stage string
	Err   error
}

func (e *DialError) Error() string {
	return e.Stage + ": " + e.Err.Error()
}

func (e *DialError) Unwrap() error {
	return e.Err
}

type UpstreamDialer struct {
	cfg config.UpstreamConfig
}

func NewUpstreamDialer(cfg config.UpstreamConfig) (*UpstreamDialer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &UpstreamDialer{cfg: cfg}, nil
}

func (d *UpstreamDialer) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	raw, err := d.dialRaw(ctx)
	if err != nil {
		return nil, &DialError{Stage: StageTransport, Err: err}
	}
	clientCfg, err := d.cfg.KLESS.ClientConfig(d.cfg.Transport)
	if err != nil {
		_ = raw.Close()
		return nil, &DialError{Stage: StageHandshake, Err: err}
	}
	secure, err := kless.ClientHandshake(raw, clientCfg)
	if err != nil {
		_ = raw.Close()
		return nil, &DialError{Stage: StageHandshake, Err: err}
	}
	return secure, nil
}

func (d *UpstreamDialer) dialRaw(ctx context.Context) (io.ReadWriteCloser, error) {
	tlsConfig, err := d.tlsConfig()
	if err != nil {
		return nil, err
	}
	headers := headerFromMap(d.cfg.Headers)
	switch d.cfg.Transport {
	case config.TransportTCP:
		dialCtx, cancel := context.WithTimeout(ctx, d.cfg.DialTimeoutDuration())
		defer cancel()
		return tcp.Dial(dialCtx, d.cfg.Address)
	case config.TransportTLS:
		dialCtx, cancel := context.WithTimeout(ctx, d.cfg.DialTimeoutDuration())
		defer cancel()
		return ktls.Dial(dialCtx, d.cfg.Address, tlsConfig)
	case config.TransportHTTPUpgrade, config.TransportHTTPUpdate:
		dialCtx, cancel := context.WithTimeout(ctx, d.cfg.DialTimeoutDuration())
		defer cancel()
		return httpupgrade.Dial(dialCtx, d.cfg.URL, headers, tlsConfig)
	case config.TransportWebSocket:
		dialCtx, cancel := context.WithTimeout(ctx, d.cfg.DialTimeoutDuration())
		defer cancel()
		return websocket.Dial(dialCtx, d.cfg.URL, headers, tlsConfig)
	case config.TransportHTTPStream:
		return httpstream.Dial(ctx, httpClient(tlsConfig), d.cfg.URL, headers)
	case config.TransportGRPC:
		return grpc.Dial(ctx, httpClient(tlsConfig), d.cfg.URL, headers)
	case config.TransportXHTTP:
		return xhttp.Dial(ctx, httpClient(tlsConfig), d.cfg.URL, headers)
	case config.TransportHTTP3:
		return nil, kless.ErrUnsupported
	default:
		return nil, fmt.Errorf("unsupported transport %q", d.cfg.Transport)
	}
}

func (d *UpstreamDialer) tlsConfig() (*tls.Config, error) {
	if d.cfg.Transport == config.TransportTCP {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		ServerName:         d.serverName(),
		InsecureSkipVerify: d.cfg.InsecureSkipVerify,
	}
	if d.cfg.CAFile != "" {
		cert, err := os.ReadFile(d.cfg.CAFile)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cert) {
			return nil, errors.New("ca_file does not contain a valid PEM certificate")
		}
		cfg.RootCAs = roots
	}
	return cfg, nil
}

func (d *UpstreamDialer) serverName() string {
	if d.cfg.ServerName != "" {
		return d.cfg.ServerName
	}
	if d.cfg.Address != "" {
		host, _, err := net.SplitHostPort(d.cfg.Address)
		if err == nil {
			return trimBrackets(host)
		}
		return trimBrackets(d.cfg.Address)
	}
	if d.cfg.URL != "" {
		host := d.cfg.URL
		if parsed, err := http.NewRequest(http.MethodGet, d.cfg.URL, nil); err == nil && parsed.URL.Hostname() != "" {
			host = parsed.URL.Hostname()
		}
		return trimBrackets(host)
	}
	return ""
}

func httpClient(tlsConfig *tls.Config) *http.Client {
	if tlsConfig == nil {
		return http.DefaultClient
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
}

func headerFromMap(values map[string]string) http.Header {
	headers := make(http.Header, len(values))
	for key, value := range values {
		headers.Set(key, value)
	}
	return headers
}

func trimBrackets(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}
