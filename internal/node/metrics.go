package node

import (
	"sync/atomic"
	"time"
)

type Metrics struct {
	startedAt     time.Time
	active        atomic.Int64
	total         atomic.Uint64
	rejected      atomic.Uint64
	transportErrs atomic.Uint64
	handshakeErrs atomic.Uint64
	proxyErrs     atomic.Uint64
	upBytes       atomic.Uint64
	downBytes     atomic.Uint64
}

type MetricsSnapshot struct {
	StartedAt       time.Time `json:"started_at"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	Active          int64     `json:"active_connections"`
	Total           uint64    `json:"total_connections"`
	Rejected        uint64    `json:"rejected_connections"`
	TransportErrors uint64    `json:"transport_errors"`
	HandshakeErrors uint64    `json:"handshake_errors"`
	ProxyErrors     uint64    `json:"proxy_errors"`
	UpstreamBytes   uint64    `json:"upstream_bytes"`
	DownstreamBytes uint64    `json:"downstream_bytes"`
}

func NewMetrics() *Metrics {
	return &Metrics{startedAt: time.Now().UTC()}
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	now := time.Now().UTC()
	return MetricsSnapshot{
		StartedAt:       m.startedAt,
		UptimeSeconds:   int64(now.Sub(m.startedAt).Seconds()),
		Active:          m.active.Load(),
		Total:           m.total.Load(),
		Rejected:        m.rejected.Load(),
		TransportErrors: m.transportErrs.Load(),
		HandshakeErrors: m.handshakeErrs.Load(),
		ProxyErrors:     m.proxyErrs.Load(),
		UpstreamBytes:   m.upBytes.Load(),
		DownstreamBytes: m.downBytes.Load(),
	}
}

func (m *Metrics) beginConnection() {
	m.active.Add(1)
	m.total.Add(1)
}

func (m *Metrics) endConnection() {
	m.active.Add(-1)
}

func (m *Metrics) addRejected() {
	m.rejected.Add(1)
}

func (m *Metrics) addTransportError() {
	m.transportErrs.Add(1)
}

func (m *Metrics) addHandshakeError() {
	m.handshakeErrs.Add(1)
}

func (m *Metrics) addProxyError() {
	m.proxyErrs.Add(1)
}

func (m *Metrics) addUpBytes(n uint64) {
	m.upBytes.Add(n)
}

func (m *Metrics) addDownBytes(n uint64) {
	m.downBytes.Add(n)
}
