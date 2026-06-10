package config_test

import (
	"testing"

	"github.com/kexue-aihao/Knode/internal/config"
	"kray/pkg/kless"
)

func TestConfigValidateAcceptsKLESSMaterial(t *testing.T) {
	serverPublic, _, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		NodeID: "knode-a",
		Upstreams: []config.UpstreamConfig{
			{
				Name:      "primary",
				Transport: config.TransportTCP,
				Address:   "127.0.0.1:9000",
				KLESS: config.KLESSConfig{
					ClientID:         "knode-a",
					ClientSecret:     kless.EncodeKey(clientSecret),
					ServerSigningKey: kless.EncodeKey(serverPublic),
				},
			},
		},
		Inbounds: []config.InboundConfig{
			{Name: "local", Listen: "127.0.0.1:7000", Upstream: "primary"},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Admin.Address != config.DefaultAdminAddress {
		t.Fatalf("Admin.Address = %q, want %q", cfg.Admin.Address, config.DefaultAdminAddress)
	}
}

func TestConfigValidateRejectsUnknownUpstream(t *testing.T) {
	serverPublic, _, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		NodeID: "knode-a",
		Upstreams: []config.UpstreamConfig{
			{
				Name:      "primary",
				Transport: config.TransportTCP,
				Address:   "127.0.0.1:9000",
				KLESS: config.KLESSConfig{
					ClientID:         "knode-a",
					ClientSecret:     kless.EncodeKey(clientSecret),
					ServerSigningKey: kless.EncodeKey(serverPublic),
				},
			},
		},
		Inbounds: []config.InboundConfig{
			{Name: "local", Listen: "127.0.0.1:7000", Upstream: "missing"},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}
