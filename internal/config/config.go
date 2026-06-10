package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"kray/pkg/kless"
)

const (
	DefaultAdminAddress     = "127.0.0.1:8080"
	DefaultDialTimeout      = 10 * time.Second
	DefaultHandshakeTimeout = 10 * time.Second
	DefaultShutdownGrace    = 10 * time.Second
	DefaultMaxFramePayload  = kless.DefaultMaxFramePayload
	defaultConfigFileMode   = 0o600
	defaultConfigDirectory  = 0o755
	TransportTCP            = "tcp"
	TransportTLS            = "tls"
	TransportHTTPUpgrade    = "httpupgrade"
	TransportHTTPUpdate     = "httpupdate"
	TransportWebSocket      = "websocket"
	TransportHTTPStream     = "httpstream"
	TransportGRPC           = "grpc"
	TransportXHTTP          = "xhttp"
	TransportHTTP3          = "http3"
)

type Config struct {
	NodeID        string           `json:"node_id"`
	Admin         AdminConfig      `json:"admin"`
	ShutdownGrace string           `json:"shutdown_grace,omitempty"`
	Upstreams     []UpstreamConfig `json:"upstreams"`
	Inbounds      []InboundConfig  `json:"inbounds"`
}

type AdminConfig struct {
	Address string `json:"address"`
}

type UpstreamConfig struct {
	Name               string            `json:"name"`
	Transport          string            `json:"transport"`
	Address            string            `json:"address,omitempty"`
	URL                string            `json:"url,omitempty"`
	ServerName         string            `json:"server_name,omitempty"`
	CAFile             string            `json:"ca_file,omitempty"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	DialTimeout        string            `json:"dial_timeout,omitempty"`
	KLESS              KLESSConfig       `json:"kless"`
}

type KLESSConfig struct {
	ClientID         string   `json:"client_id"`
	ClientSecret     string   `json:"client_secret"`
	ServerSigningKey string   `json:"server_signing_key"`
	Capabilities     []string `json:"capabilities,omitempty"`
	MaxFramePayload  int      `json:"max_frame_payload,omitempty"`
	PaddingMin       int      `json:"padding_min,omitempty"`
	PaddingMax       int      `json:"padding_max,omitempty"`
	HandshakeTimeout string   `json:"handshake_timeout,omitempty"`
}

type InboundConfig struct {
	Name           string `json:"name"`
	Listen         string `json:"listen"`
	Upstream       string `json:"upstream"`
	MaxConnections int    `json:"max_connections,omitempty"`
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()

	var cfg Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func WriteSample(path string) error {
	if err := os.MkdirAll(parentDir(path), defaultConfigDirectory); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(SampleJSON), defaultConfigFileMode)
}

func (c *Config) Validate() error {
	c.applyDefaults()
	if strings.TrimSpace(c.NodeID) == "" {
		return errors.New("node_id is required")
	}
	if len(c.Upstreams) == 0 {
		return errors.New("at least one upstream is required")
	}
	if len(c.Inbounds) == 0 {
		return errors.New("at least one inbound is required")
	}
	if _, err := parseDuration(c.ShutdownGrace, DefaultShutdownGrace); err != nil {
		return fmt.Errorf("shutdown_grace: %w", err)
	}

	upstreams := make(map[string]struct{}, len(c.Upstreams))
	for i := range c.Upstreams {
		if err := c.Upstreams[i].Validate(); err != nil {
			return fmt.Errorf("upstreams[%d]: %w", i, err)
		}
		name := c.Upstreams[i].Name
		if _, exists := upstreams[name]; exists {
			return fmt.Errorf("upstreams[%d]: duplicate upstream name %q", i, name)
		}
		upstreams[name] = struct{}{}
	}

	inbounds := make(map[string]struct{}, len(c.Inbounds))
	for i := range c.Inbounds {
		if err := c.Inbounds[i].Validate(upstreams); err != nil {
			return fmt.Errorf("inbounds[%d]: %w", i, err)
		}
		name := c.Inbounds[i].Name
		if _, exists := inbounds[name]; exists {
			return fmt.Errorf("inbounds[%d]: duplicate inbound name %q", i, name)
		}
		inbounds[name] = struct{}{}
	}
	return nil
}

func (c *Config) ShutdownGraceDuration() time.Duration {
	d, _ := parseDuration(c.ShutdownGrace, DefaultShutdownGrace)
	return d
}

func (c *Config) UpstreamByName(name string) (UpstreamConfig, bool) {
	for _, upstream := range c.Upstreams {
		if upstream.Name == name {
			return upstream, true
		}
	}
	return UpstreamConfig{}, false
}

func (u *UpstreamConfig) Validate() error {
	u.applyDefaults()
	if u.Name == "" {
		return errors.New("name is required")
	}
	if !isSupportedTransport(u.Transport) {
		return fmt.Errorf("unsupported transport %q", u.Transport)
	}
	if transportUsesAddress(u.Transport) && u.Address == "" {
		return errors.New("address is required for tcp/tls transport")
	}
	if transportUsesURL(u.Transport) {
		if u.URL == "" {
			return fmt.Errorf("url is required for %s transport", u.Transport)
		}
		parsed, err := url.Parse(u.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid url %q", u.URL)
		}
	}
	if _, err := parseDuration(u.DialTimeout, DefaultDialTimeout); err != nil {
		return fmt.Errorf("dial_timeout: %w", err)
	}
	if err := u.KLESS.Validate(u.Transport); err != nil {
		return fmt.Errorf("kless: %w", err)
	}
	if u.CAFile != "" {
		if _, err := os.Stat(u.CAFile); err != nil {
			return fmt.Errorf("ca_file: %w", err)
		}
	}
	return nil
}

func (u UpstreamConfig) DialTimeoutDuration() time.Duration {
	d, _ := parseDuration(u.DialTimeout, DefaultDialTimeout)
	return d
}

func (k *KLESSConfig) Validate(transport string) error {
	if k.ClientID == "" {
		return errors.New("client_id is required")
	}
	secret, err := k.ClientSecretBytes()
	if err != nil {
		return fmt.Errorf("client_secret: %w", err)
	}
	if len(secret) < 32 {
		return errors.New("client_secret must decode to at least 32 bytes")
	}
	signingKey, err := k.ServerSigningKeyBytes()
	if err != nil {
		return fmt.Errorf("server_signing_key: %w", err)
	}
	if len(signingKey) != ed25519.PublicKeySize {
		return fmt.Errorf("server_signing_key must decode to %d bytes", ed25519.PublicKeySize)
	}
	if k.MaxFramePayload < 0 {
		return errors.New("max_frame_payload cannot be negative")
	}
	if k.PaddingMin < 0 || k.PaddingMax < 0 || k.PaddingMin > k.PaddingMax || k.PaddingMax > kless.MaxHandshakePadding {
		return fmt.Errorf("padding must satisfy 0 <= min <= max <= %d", kless.MaxHandshakePadding)
	}
	if _, err := parseDuration(k.HandshakeTimeout, DefaultHandshakeTimeout); err != nil {
		return fmt.Errorf("handshake_timeout: %w", err)
	}
	if _, err := capabilityMask(transport, k.Capabilities); err != nil {
		return err
	}
	return nil
}

func (k KLESSConfig) ClientConfig(transport string) (kless.ClientConfig, error) {
	secret, err := k.ClientSecretBytes()
	if err != nil {
		return kless.ClientConfig{}, err
	}
	signingKey, err := k.ServerSigningKeyBytes()
	if err != nil {
		return kless.ClientConfig{}, err
	}
	caps, err := capabilityMask(transport, k.Capabilities)
	if err != nil {
		return kless.ClientConfig{}, err
	}
	handshakeTimeout, err := parseDuration(k.HandshakeTimeout, DefaultHandshakeTimeout)
	if err != nil {
		return kless.ClientConfig{}, err
	}
	return kless.ClientConfig{
		ClientID:         k.ClientID,
		ClientSecret:     secret,
		ServerSigningKey: signingKey,
		Capabilities:     caps,
		MaxFramePayload:  defaultInt(k.MaxFramePayload, DefaultMaxFramePayload),
		PaddingMin:       k.PaddingMin,
		PaddingMax:       k.PaddingMax,
		HandshakeTimeout: handshakeTimeout,
	}, nil
}

func (k KLESSConfig) ClientSecretBytes() ([]byte, error) {
	return decodeKey(k.ClientSecret)
}

func (k KLESSConfig) ServerSigningKeyBytes() ([]byte, error) {
	return decodeKey(k.ServerSigningKey)
}

func decodeKey(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	encodings := []*base64.Encoding{
		base64.RawStdEncoding,
		base64.StdEncoding,
		base64.RawURLEncoding,
		base64.URLEncoding,
	}
	var firstErr error
	for _, encoding := range encodings {
		out, err := encoding.DecodeString(text)
		if err == nil {
			return out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func (i *InboundConfig) Validate(upstreams map[string]struct{}) error {
	i.Name = strings.TrimSpace(i.Name)
	i.Listen = strings.TrimSpace(i.Listen)
	i.Upstream = strings.TrimSpace(i.Upstream)
	if i.Name == "" {
		return errors.New("name is required")
	}
	if i.Listen == "" {
		return errors.New("listen is required")
	}
	if i.Upstream == "" {
		return errors.New("upstream is required")
	}
	if _, ok := upstreams[i.Upstream]; !ok {
		return fmt.Errorf("unknown upstream %q", i.Upstream)
	}
	if i.MaxConnections < 0 {
		return errors.New("max_connections cannot be negative")
	}
	return nil
}

func (c *Config) applyDefaults() {
	c.NodeID = strings.TrimSpace(c.NodeID)
	if c.Admin.Address == "" {
		c.Admin.Address = DefaultAdminAddress
	}
	if c.ShutdownGrace == "" {
		c.ShutdownGrace = DefaultShutdownGrace.String()
	}
	for i := range c.Upstreams {
		c.Upstreams[i].applyDefaults()
	}
}

func (u *UpstreamConfig) applyDefaults() {
	u.Name = strings.TrimSpace(u.Name)
	u.Transport = strings.ToLower(strings.TrimSpace(u.Transport))
	u.Address = strings.TrimSpace(u.Address)
	u.URL = strings.TrimSpace(u.URL)
	u.ServerName = strings.TrimSpace(u.ServerName)
	u.CAFile = strings.TrimSpace(u.CAFile)
	if u.DialTimeout == "" {
		u.DialTimeout = DefaultDialTimeout.String()
	}
	if u.KLESS.HandshakeTimeout == "" {
		u.KLESS.HandshakeTimeout = DefaultHandshakeTimeout.String()
	}
}

func capabilityMask(transport string, names []string) (kless.Capability, error) {
	if len(names) == 0 {
		return capabilityForTransport(transport)
	}
	var caps kless.Capability
	for _, name := range names {
		capability, err := capabilityForTransport(strings.ToLower(strings.TrimSpace(name)))
		if err != nil {
			return 0, err
		}
		caps |= capability
	}
	return caps, nil
}

func capabilityForTransport(transport string) (kless.Capability, error) {
	switch transport {
	case TransportTCP:
		return kless.CapabilityTCP, nil
	case TransportTLS:
		return kless.CapabilityTLS, nil
	case TransportHTTPUpgrade, TransportHTTPUpdate:
		return kless.CapabilityHTTPUpgrade, nil
	case TransportWebSocket:
		return kless.CapabilityWebSocket, nil
	case TransportHTTPStream:
		return kless.CapabilityHTTP, nil
	case TransportGRPC:
		return kless.CapabilityGRPC, nil
	case TransportXHTTP:
		return kless.CapabilityXHTTP, nil
	case TransportHTTP3:
		return kless.CapabilityHTTP3, nil
	default:
		return 0, fmt.Errorf("unsupported capability %q", transport)
	}
}

func isSupportedTransport(transport string) bool {
	_, err := capabilityForTransport(transport)
	return err == nil
}

func transportUsesAddress(transport string) bool {
	return transport == TransportTCP || transport == TransportTLS
}

func transportUsesURL(transport string) bool {
	return !transportUsesAddress(transport)
}

func parseDuration(text string, fallback time.Duration) (time.Duration, error) {
	if text == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("must be positive")
	}
	return d, nil
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func parentDir(path string) string {
	idx := strings.LastIndexAny(path, `/\`)
	if idx < 0 {
		return "."
	}
	return path[:idx]
}
