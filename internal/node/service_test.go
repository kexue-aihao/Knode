package node_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
	"github.com/kexue-aihao/Knode/internal/node"
	"kray/pkg/kless"
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

	svc, err := node.New(cfg, log.New(io.Discard, "", 0))
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
