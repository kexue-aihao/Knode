package node

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	maxAccessLogQueueSize = 10000
	maxAccessLogBatchSize = 5000
	accessLogFlushPeriod  = 10 * time.Second
)

type accessLogBuffer struct {
	mu    sync.Mutex
	items []kboardAccessLogItem
}

type kboardAccessLogItem struct {
	UserID        *int64 `json:"user_id,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	KLESSClientID string `json:"kless_client_id,omitempty"`
	Domain        string `json:"domain"`
	Host          string `json:"host"`
	URI           string `json:"uri,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	RemoteAddr    string `json:"remote_addr,omitempty"`
	AccessedAt    int64  `json:"accessed_at,omitempty"`
}

func newAccessLogBuffer() *accessLogBuffer {
	return &accessLogBuffer{}
}

func (b *accessLogBuffer) Record(item kboardAccessLogItem) bool {
	if b == nil || strings.TrimSpace(item.Domain) == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) >= maxAccessLogQueueSize {
		return false
	}
	b.items = append(b.items, item)
	return true
}

func (b *accessLogBuffer) Drain(max int) []kboardAccessLogItem {
	if b == nil || max <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) == 0 {
		return nil
	}
	if len(b.items) <= max {
		out := b.items
		b.items = nil
		return out
	}
	out := append([]kboardAccessLogItem(nil), b.items[:max]...)
	copy(b.items, b.items[max:])
	b.items = b.items[:len(b.items)-max]
	return out
}

func (b *accessLogBuffer) Restore(items []kboardAccessLogItem) {
	if b == nil || len(items) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	restored := make([]kboardAccessLogItem, 0, len(items)+len(b.items))
	restored = append(restored, items...)
	restored = append(restored, b.items...)
	if len(restored) > maxAccessLogQueueSize {
		restored = restored[:maxAccessLogQueueSize]
	}
	b.items = restored
}

func accessLogItem(clientID string, userID int64, target RelayTarget, remoteAddr string, now time.Time) kboardAccessLogItem {
	host := strings.TrimSpace(target.Host)
	protocol := protocolForTarget(target.Port)
	item := kboardAccessLogItem{
		ClientID:      strings.TrimSpace(clientID),
		KLESSClientID: strings.TrimSpace(clientID),
		Domain:        strings.ToLower(host),
		Host:          strings.ToLower(host),
		URI:           targetURI(protocol, target),
		Protocol:      protocol,
		RemoteAddr:    strings.TrimSpace(remoteAddr),
		AccessedAt:    now.Unix(),
	}
	if userID > 0 {
		item.UserID = &userID
	}
	return item
}

func protocolForTarget(port uint16) string {
	switch port {
	case 80:
		return "http"
	case 443:
		return "https"
	default:
		return "tcp"
	}
}

func targetURI(protocol string, target RelayTarget) string {
	hostPort := net.JoinHostPort(strings.TrimSpace(target.Host), fmt.Sprintf("%d", target.Port))
	if protocol == "" {
		protocol = "tcp"
	}
	return protocol + "://" + hostPort
}
