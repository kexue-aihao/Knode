package node

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
)

type kboardReporter struct {
	cfg    config.KboardConfig
	nodeID string
	client *http.Client
	logger *log.Logger
	status func() Status
}

func newKboardReporter(cfg config.KboardConfig, nodeID string, logger *log.Logger, status func() Status) *kboardReporter {
	return &kboardReporter{
		cfg:    cfg,
		nodeID: nodeID,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger,
		status: status,
	}
}

func (r *kboardReporter) run(ctx context.Context) {
	interval := r.cfg.ReportIntervalDuration()
	r.report(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.report(ctx)
		}
	}
}

func (r *kboardReporter) report(ctx context.Context) {
	r.reportAlive(ctx)
	r.reportTraffic(ctx)
}

func (r *kboardReporter) reportAlive(ctx context.Context) {
	if r.cfg.AliveEndpoint == "" {
		return
	}
	status := r.status()
	payload := map[string]any{
		"node_id":        r.nodeID,
		"kboard_node_id": r.cfg.NodeID,
		"status":         "alive",
		"timestamp":      time.Now().Unix(),
		"admin":          status.Admin,
		"inbounds":       status.Inbounds,
		"metrics":        status.Metrics,
	}
	if err := r.postJSON(ctx, r.cfg.AliveEndpoint, payload); err != nil {
		r.logger.Printf("kboard alive report failed: %v", err)
	}
}

func (r *kboardReporter) reportTraffic(ctx context.Context) {
	if r.cfg.TrafficEndpoint == "" {
		return
	}
	status := r.status()
	payload := map[string]any{
		"node_id":          r.nodeID,
		"kboard_node_id":   r.cfg.NodeID,
		"timestamp":        time.Now().Unix(),
		"upstream_bytes":   status.Metrics.UpstreamBytes,
		"downstream_bytes": status.Metrics.DownstreamBytes,
		"active":           status.Metrics.Active,
		"total":            status.Metrics.Total,
	}
	if err := r.postJSON(ctx, r.cfg.TrafficEndpoint, payload); err != nil {
		r.logger.Printf("kboard traffic report failed: %v", err)
	}
}

func (r *kboardReporter) postJSON(ctx context.Context, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	rawURL := r.endpointURL(endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	now := fmt.Sprintf("%d", time.Now().Unix())
	signature := hmacSignature(r.cfg.NodeSharedSecret, http.MethodPost, endpoint, now, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.NodeSharedSecret)
	req.Header.Set("X-Kboard-Node-ID", r.cfg.NodeID)
	req.Header.Set("X-Kboard-Node-Secret", r.cfg.NodeSharedSecret)
	req.Header.Set("X-Knode-Node-ID", r.nodeID)
	req.Header.Set("X-Knode-Timestamp", now)
	req.Header.Set("X-Knode-Signature", signature)

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func (r *kboardReporter) endpointURL(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return strings.TrimRight(r.cfg.PublicURL, "/") + endpoint
}

func hmacSignature(secret, method, endpoint, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(endpoint))
	mac.Write([]byte("\n"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
