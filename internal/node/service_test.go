package node

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
	"kray/pkg/kless"
	"kray/pkg/relay"
)

func TestServiceProxiesTCPOverKLESS(t *testing.T) {
	serverPublic, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	upstreamErr := make(chan error, 1)
	go serveKLESSEcho(upstream, serverPrivate, clientSecret, upstreamErr)

	cfg := config.Config{
		NodeID: "knode-a",
		Admin:  config.AdminConfig{Address: "127.0.0.1:0"},
		Upstreams: []config.UpstreamConfig{
			{
				Name:      "primary",
				Transport: config.TransportTCP,
				Address:   upstream.Addr().String(),
				KLESS: config.KLESSConfig{
					ClientID:         "knode-a",
					ClientSecret:     kless.EncodeKey(clientSecret),
					ServerSigningKey: kless.EncodeKey(serverPublic),
				},
			},
		},
		Inbounds: []config.InboundConfig{
			{Name: "local", Listen: "127.0.0.1:0", Upstream: "primary"},
		},
	}

	svc, err := New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- svc.Run(ctx)
	}()

	inboundAddress := waitForAddress(t, func() string { return svc.InboundAddress("local") })
	conn, err := net.DialTimeout("tcp", inboundAddress, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	payload := []byte("hello over kless")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	select {
	case err := <-upstreamErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("upstream error = %v", err)
		}
	default:
	}
}

func TestServiceAcceptsKrayNRelayOverKLESS(t *testing.T) {
	serverPublic, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetErr := make(chan error, 1)
	go servePlainEcho(target, targetErr)

	cfg := config.Config{
		NodeID: "knode-relay",
		Admin:  config.AdminConfig{Address: "127.0.0.1:0"},
		Inbounds: []config.InboundConfig{
			{
				Name:   "public-kless",
				Listen: "127.0.0.1:0",
				Mode:   config.InboundModeKLESSServer,
				KLESS: config.ServerKLESSConfig{
					ClientID:             "test-client",
					ClientSecret:         kless.EncodeKey(clientSecret),
					ServerSigningKey:     kless.EncodeKey(serverPublic),
					ServerSigningPrivate: kless.EncodeKey(serverPrivate),
				},
			},
		},
	}

	svc, err := New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- svc.Run(ctx)
	}()

	inboundAddress := waitForAddress(t, func() string { return svc.InboundAddress("public-kless") })
	raw, err := net.DialTimeout("tcp", inboundAddress, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	secure, err := kless.ClientHandshake(raw, kless.ClientConfig{
		ClientID:         "test-client",
		ClientSecret:     clientSecret,
		ServerSigningKey: ed25519.PublicKey(serverPublic),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer secure.Close()
	host, portText, err := net.SplitHostPort(target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	targetPort, err := parseUint16(portText)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.WriteRequest(secure, relay.TCPConnect(host, targetPort)); err != nil {
		t.Fatal(err)
	}
	resp, err := relay.ReadResponse(secure)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != relay.StatusOK {
		t.Fatalf("relay response = %+v, want ok", resp)
	}
	payload := []byte("hello direct krayn relay")
	if _, err := secure.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(secure, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q, want %q", got, payload)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	select {
	case err := <-targetErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("target error = %v", err)
		}
	default:
	}
}

func TestServiceAcceptsKrayNUDPAssociateOverKLESS(t *testing.T) {
	serverPublic, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	target, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetErr := make(chan error, 1)
	go serveUDPEcho(target, targetErr)

	cfg := config.Config{
		NodeID: "knode-relay",
		Admin:  config.AdminConfig{Address: "127.0.0.1:0"},
		Inbounds: []config.InboundConfig{
			{
				Name:   "public-kless",
				Listen: "127.0.0.1:0",
				Mode:   config.InboundModeKLESSServer,
				KLESS: config.ServerKLESSConfig{
					ClientID:             "test-client",
					ClientSecret:         kless.EncodeKey(clientSecret),
					ServerSigningKey:     kless.EncodeKey(serverPublic),
					ServerSigningPrivate: kless.EncodeKey(serverPrivate),
				},
			},
		},
	}

	svc, err := New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- svc.Run(ctx)
	}()

	inboundAddress := waitForAddress(t, func() string { return svc.InboundAddress("public-kless") })
	raw, err := net.DialTimeout("tcp", inboundAddress, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	secure, err := kless.ClientHandshake(raw, kless.ClientConfig{
		ClientID:         "test-client",
		ClientSecret:     clientSecret,
		ServerSigningKey: ed25519.PublicKey(serverPublic),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer secure.Close()
	if err := relay.WriteRequest(secure, relay.UDPAssociate("0.0.0.0", 0)); err != nil {
		t.Fatal(err)
	}
	resp, err := relay.ReadResponse(secure)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != relay.StatusOK {
		t.Fatalf("relay response = %+v, want ok", resp)
	}
	host, portText, err := net.SplitHostPort(target.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	targetPort, err := parseUint16(portText)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.WriteDatagram(secure, relay.Datagram{
		Address: relay.Address{Host: host, Port: targetPort},
		Payload: []byte("udp query"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := relay.ReadDatagram(secure)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address.Port != targetPort || string(got.Payload) != "udp query" {
		t.Fatalf("udp response = %+v payload %q", got.Address, got.Payload)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	select {
	case err := <-targetErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("target error = %v", err)
		}
	default:
	}
}

func TestServiceReportsKrayNRelayAccessLog(t *testing.T) {
	const (
		nodeID = "5"
		secret = "test-node-shared-secret"
	)
	serverPublic, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	payloadCh := make(chan map[string]any, 1)
	kboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/kb-prefix/knode/control/access-logs" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if err := verifyServiceKboardRequest(r, secret, nodeID); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		payloadCh <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer kboard.Close()

	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetErr := make(chan error, 1)
	go servePlainEcho(target, targetErr)

	cfg := config.Config{
		NodeID: "kboard-node-5",
		Admin:  config.AdminConfig{Address: "127.0.0.1:0"},
		Kboard: &config.KboardConfig{
			PublicURL:          kboard.URL,
			NodeID:             nodeID,
			NodeSharedSecret:   secret,
			AliveEndpoint:      "/kb-prefix/knode/control/alive",
			AccessLogsEndpoint: "/kb-prefix/knode/control/access-logs",
			ReportInterval:     "30s",
		},
		Inbounds: []config.InboundConfig{
			{
				Name:   "public-kless",
				Listen: "127.0.0.1:0",
				Mode:   config.InboundModeKLESSServer,
				KLESS: config.ServerKLESSConfig{
					ClientID:             "user-uuid",
					ClientSecret:         kless.EncodeKey(clientSecret),
					ServerSigningKey:     kless.EncodeKey(serverPublic),
					ServerSigningPrivate: kless.EncodeKey(serverPrivate),
				},
			},
		},
	}

	svc, err := New(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- svc.Run(ctx)
	}()

	inboundAddress := waitForAddress(t, func() string { return svc.InboundAddress("public-kless") })
	raw, err := net.DialTimeout("tcp", inboundAddress, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	secure, err := kless.ClientHandshake(raw, kless.ClientConfig{
		ClientID:         "user-uuid",
		ClientSecret:     clientSecret,
		ServerSigningKey: ed25519.PublicKey(serverPublic),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer secure.Close()
	targetHost, targetPortText, err := net.SplitHostPort(target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	targetPort, err := parseUint16(targetPortText)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeConnectRequest(secure, RelayTarget{Host: targetHost, Port: targetPort}); err != nil {
		t.Fatal(err)
	}

	select {
	case payload := <-payloadCh:
		items, ok := payload["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("items = %#v", payload["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("item = %#v", items[0])
		}
		if item["kless_client_id"] != "user-uuid" || item["domain"] != targetHost || item["protocol"] != "tcp" {
			t.Fatalf("access log item = %#v", item)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("access log was not reported")
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service did not stop")
	}
	select {
	case err := <-targetErr:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("target error = %v", err)
		}
	default:
	}
}

func serveKLESSEcho(listener net.Listener, serverPrivate, clientSecret []byte, errCh chan<- error) {
	raw, err := listener.Accept()
	if err != nil {
		errCh <- err
		return
	}
	defer raw.Close()
	secure, _, err := kless.ServerHandshake(raw, kless.ServerConfig{
		SigningKey:  serverPrivate,
		ClientStore: kless.StaticClientStore{"knode-a": clientSecret},
	})
	if err != nil {
		errCh <- err
		return
	}
	defer secure.Close()
	_, err = io.Copy(secure, secure)
	errCh <- err
}

func servePlainEcho(listener net.Listener, errCh chan<- error) {
	raw, err := listener.Accept()
	if err != nil {
		errCh <- err
		return
	}
	defer raw.Close()
	_, err = io.Copy(raw, raw)
	errCh <- err
}

func serveUDPEcho(conn net.PacketConn, errCh chan<- error) {
	buf := make([]byte, 2048)
	n, addr, err := conn.ReadFrom(buf)
	if err != nil {
		errCh <- err
		return
	}
	_, err = conn.WriteTo(buf[:n], addr)
	errCh <- err
}

func verifyServiceKboardRequest(r *http.Request, secret, nodeID string) error {
	if got := r.Header.Get("X-KBoard-Node-ID"); got != nodeID {
		return fmt.Errorf("node header = %q", got)
	}
	timestamp := r.Header.Get("X-KBoard-Timestamp")
	if timestamp == "" {
		return errors.New("missing timestamp header")
	}
	signature := r.Header.Get("X-KBoard-Signature")
	if signature == "" {
		return errors.New("missing signature header")
	}
	base := strings.Join([]string{
		r.Method,
		r.URL.EscapedPath(),
		r.URL.RawQuery,
		nodeID,
		timestamp,
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(strings.ToLower(signature))) {
		return fmt.Errorf("signature = %q, want %q", signature, expected)
	}
	return nil
}

func parseUint16(text string) (uint16, error) {
	var value uint64
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid port")
		}
		value = value*10 + uint64(ch-'0')
	}
	if value == 0 || value > 65535 {
		return 0, errors.New("invalid port")
	}
	return uint16(value), nil
}

func waitForAddress(t *testing.T, get func() string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		address := get()
		if address != "" {
			return address
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("address was not set")
	return ""
}
