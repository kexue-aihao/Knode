package config_test

import (
	"encoding/base64"
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

func TestConfigValidateAcceptsBase64URLKLESSMaterial(t *testing.T) {
	serverPublic, _, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		NodeID: "kboard-node-2",
		Upstreams: []config.UpstreamConfig{
			{
				Name:      "primary",
				Transport: config.TransportTCP,
				Address:   "127.0.0.1:9000",
				KLESS: config.KLESSConfig{
					ClientID:         "kboard-node-2",
					ClientSecret:     base64.RawURLEncoding.EncodeToString(clientSecret),
					ServerSigningKey: base64.RawURLEncoding.EncodeToString(serverPublic),
				},
			},
		},
		Inbounds: []config.InboundConfig{
			{Name: "local", Listen: "0.0.0.0:36514", Upstream: "primary"},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateAcceptsKboardControlConfig(t *testing.T) {
	serverPublic, _, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		NodeID: "kboard-node-2",
		Kboard: &config.KboardConfig{
			PublicURL:          "https://kboard.example.com",
			NodeID:             "2",
			NodeSharedSecret:   "secret",
			AliveEndpoint:      "/kb-prefix/knode/control/alive",
			TrafficEndpoint:    "/kb-prefix/knode/control/traffic",
			AccessLogsEndpoint: "/kb-prefix/knode/control/access-logs",
		},
		Upstreams: []config.UpstreamConfig{
			{
				Name:      "primary",
				Transport: config.TransportTCP,
				Address:   "127.0.0.1:9000",
				KLESS: config.KLESSConfig{
					ClientID:         "kboard-node-2",
					ClientSecret:     kless.EncodeKey(clientSecret),
					ServerSigningKey: kless.EncodeKey(serverPublic),
				},
			},
		},
		Inbounds: []config.InboundConfig{
			{Name: "local", Listen: "0.0.0.0:36514", Upstream: "primary"},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Kboard.ReportInterval != "30s" {
		t.Fatalf("ReportInterval = %q, want 30s", cfg.Kboard.ReportInterval)
	}
}

func TestConfigValidateAcceptsKLESSServerInbound(t *testing.T) {
	serverPublic, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		NodeID: "knode-relay",
		Inbounds: []config.InboundConfig{
			{
				Name:   "public-kless",
				Listen: "0.0.0.0:443",
				Mode:   config.InboundModeKLESSServer,
				KLESS: config.ServerKLESSConfig{
					ClientID:             "client-a",
					ClientSecret:         base64.RawURLEncoding.EncodeToString(clientSecret),
					ServerSigningKey:     base64.RawURLEncoding.EncodeToString(serverPublic),
					ServerSigningPrivate: base64.RawURLEncoding.EncodeToString(serverPrivate),
				},
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigValidateRejectsMixedKLESSSecretMaterial(t *testing.T) {
	serverPublic, _, err := kless.GenerateServerIdentity()
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
					ClientSecret:     kless.EncodeKey(serverPublic),
					ServerSigningKey: kless.EncodeKey(serverPublic),
				},
			},
		},
		Inbounds: []config.InboundConfig{
			{Name: "local", Listen: "127.0.0.1:7000", Upstream: "primary"},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestConfigValidateRejectsMismatchedKLESSServerPrivateKey(t *testing.T) {
	serverPublic, _, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	_, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		NodeID: "knode-relay",
		Inbounds: []config.InboundConfig{
			{
				Name:   "public-kless",
				Listen: "0.0.0.0:443",
				Mode:   config.InboundModeKLESSServer,
				KLESS: config.ServerKLESSConfig{
					ClientID:             "client-a",
					ClientSecret:         kless.EncodeKey(clientSecret),
					ServerSigningKey:     kless.EncodeKey(serverPublic),
					ServerSigningPrivate: kless.EncodeKey(serverPrivate),
				},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
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
