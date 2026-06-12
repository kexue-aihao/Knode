package node

import (
	"bytes"
	"testing"
	"time"
)

func TestRelayConnectRequestRoundTrip(t *testing.T) {
	targets := []RelayTarget{
		{Host: "example.com", Port: 443},
		{Host: "127.0.0.1", Port: 8080},
		{Host: "::1", Port: 8081},
	}
	for _, target := range targets {
		var buf bytes.Buffer
		if err := writeConnectRequest(&buf, target); err != nil {
			t.Fatal(err)
		}
		got, err := readConnectRequest(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.Host != target.Host || got.Port != target.Port {
			t.Fatalf("got %+v want %+v", got, target)
		}
	}
}

func TestRelayConnectResponseRoundTrip(t *testing.T) {
	var ok bytes.Buffer
	if err := writeConnectResponse(&ok, ""); err != nil {
		t.Fatal(err)
	}
	if err := readConnectResponse(&ok); err != nil {
		t.Fatalf("read ok response: %v", err)
	}

	var rejected bytes.Buffer
	if err := writeConnectResponse(&rejected, "dial failed"); err != nil {
		t.Fatal(err)
	}
	if err := readConnectResponse(&rejected); err == nil {
		t.Fatal("read rejected response error = nil, want error")
	}
}

func TestAccessLogItemUsesUDPProtocol(t *testing.T) {
	item := accessLogItem("user-uuid", 1001, RelayTarget{Host: "1.1.1.1", Port: 53}, "udp", "192.0.2.1:50000", time.Unix(1800000000, 0))
	if item.Protocol != "udp" {
		t.Fatalf("protocol = %q, want udp", item.Protocol)
	}
	if item.URI != "udp://1.1.1.1:53" {
		t.Fatalf("uri = %q, want udp://1.1.1.1:53", item.URI)
	}
	if item.UserID == nil || *item.UserID != 1001 {
		t.Fatalf("user id = %#v, want 1001", item.UserID)
	}
}
