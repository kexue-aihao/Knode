package node

import (
	"bytes"
	"testing"
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
