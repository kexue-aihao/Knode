package node

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
)

const maxKboardErrorBody = 4096

type kboardReporter struct {
	cfg         config.KboardConfig
	nodeID      string
	client      *http.Client
	logger      *log.Logger
	status      func() Status
	hostname    string
	runtimeInfo string
	userStore   *dynamicClientStore
	usersETag   string
}

type KboardUser struct {
	ID           int64  `json:"id"`
	UUID         string `json:"uuid"`
	Token        string `json:"token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	KLESSSecret  string `json:"kless_client_secret,omitempty"`
}

type kboardUsersResponse struct {
	ProtocolVersion string       `json:"protocol_version"`
	NodeID          int64        `json:"node_id"`
	Users           []KboardUser `json:"users"`
	Count           int          `json:"count"`
	PulledAt        int64        `json:"pulled_at"`
}

func newKboardReporter(cfg config.KboardConfig, nodeID string, logger *log.Logger, status func() Status, userStore *dynamicClientStore) *kboardReporter {
	hostname, _ := os.Hostname()
	return &kboardReporter{
		cfg:         cfg,
		nodeID:      nodeID,
		client:      &http.Client{Timeout: 10 * time.Second},
		logger:      logger,
		status:      status,
		hostname:    hostname,
		runtimeInfo: fmt.Sprintf("%s/%s-%s", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		userStore:   userStore,
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
	r.syncUsers(ctx)
	r.reportAlive(ctx)
	r.reportTraffic(ctx)
}

func (r *kboardReporter) syncUsers(ctx context.Context) {
	if r.cfg.UsersEndpoint == "" || r.userStore == nil {
		return
	}
	var users kboardUsersResponse
	etag, notModified, err := r.getJSON(ctx, r.cfg.UsersEndpoint, r.usersETag, &users)
	if err != nil {
		r.logger.Printf("kboard users sync failed: %v", err)
		return
	}
	if etag != "" {
		r.usersETag = etag
	}
	if notModified {
		return
	}
	credentials := make([]ClientCredential, 0, len(users.Users))
	for _, user := range users.Users {
		credential, ok := r.userCredential(user)
		if ok {
			credentials = append(credentials, credential)
		}
	}
	r.userStore.ReplaceDynamic(credentials)
	r.logger.Printf("kboard users sync loaded %d kless clients", len(credentials))
}

func (r *kboardReporter) reportAlive(ctx context.Context) {
	if r.cfg.AliveEndpoint == "" {
		return
	}
	status := r.status()
	payload := map[string]any{
		"online":          onlineCount(status.Metrics.Active),
		"observed_at":     time.Now().Unix(),
		"backend_version": backendVersion(),
		"runtime":         r.runtimeInfo,
		"hostname":        r.hostname,
		"arch":            runtime.GOARCH,
	}
	if err := r.postJSON(ctx, r.cfg.AliveEndpoint, payload); err != nil {
		r.logger.Printf("kboard alive report failed: %v", err)
	}
}

func (r *kboardReporter) reportTraffic(ctx context.Context) {
	if r.cfg.TrafficEndpoint == "" {
		return
	}
	payload := map[string]any{
		"batch_id": fmt.Sprintf("knode-%s-%d", safeBatchNodeID(r.nodeID), time.Now().UnixNano()),
		"items":    []any{},
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
	reqURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	signaturePath := reqURL.EscapedPath()
	if signaturePath == "" {
		signaturePath = "/"
	}
	signature := hmacSignature(r.cfg.NodeSharedSecret, http.MethodPost, signaturePath, reqURL.RawQuery, r.cfg.NodeID, now)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "knode/"+backendVersion())
	req.Header.Set("X-KBoard-Node-ID", r.cfg.NodeID)
	req.Header.Set("X-KBoard-Timestamp", now)
	req.Header.Set("X-KBoard-Signature", signature)

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxKboardErrorBody))
		if len(respBody) > 0 {
			return fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
		}
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func (r *kboardReporter) getJSON(ctx context.Context, endpoint, etag string, out any) (string, bool, error) {
	rawURL := r.endpointURL(endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", false, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if err := r.sign(req, rawURL, http.MethodGet); err != nil {
		return "", false, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return resp.Header.Get("ETag"), true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxKboardErrorBody))
		if len(respBody) > 0 {
			return "", false, fmt.Errorf("unexpected status %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
		}
		return "", false, fmt.Errorf("unexpected status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", false, err
	}
	return resp.Header.Get("ETag"), false, nil
}

func (r *kboardReporter) sign(req *http.Request, rawURL, method string) error {
	now := fmt.Sprintf("%d", time.Now().Unix())
	reqURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	signaturePath := reqURL.EscapedPath()
	if signaturePath == "" {
		signaturePath = "/"
	}
	signature := hmacSignature(r.cfg.NodeSharedSecret, method, signaturePath, reqURL.RawQuery, r.cfg.NodeID, now)
	req.Header.Set("User-Agent", "knode/"+backendVersion())
	req.Header.Set("X-KBoard-Node-ID", r.cfg.NodeID)
	req.Header.Set("X-KBoard-Timestamp", now)
	req.Header.Set("X-KBoard-Signature", signature)
	return nil
}

func (r *kboardReporter) endpointURL(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return strings.TrimRight(r.cfg.PublicURL, "/") + endpoint
}

func (r *kboardReporter) userCredential(user KboardUser) (ClientCredential, bool) {
	clientID := clientIDForUser(user)
	if clientID == "" {
		return ClientCredential{}, false
	}
	secretText := strings.TrimSpace(user.ClientSecret)
	if secretText == "" {
		secretText = strings.TrimSpace(user.KLESSSecret)
	}
	var secret []byte
	if secretText != "" {
		decoded, err := decodeKLESSKey(secretText)
		if err != nil || len(decoded) < 32 {
			r.logger.Printf("kboard user %d client %q has invalid client_secret", user.ID, clientID)
			return ClientCredential{}, false
		}
		secret = decoded
	} else {
		secret = deriveUserClientSecret(r.cfg.NodeSharedSecret, user.UUID, user.Token)
	}
	return ClientCredential{ClientID: clientID, Secret: secret, UserID: user.ID}, true
}

func hmacSignature(secret, method, path, rawQuery, nodeID, timestamp string) string {
	base := strings.Join([]string{
		method,
		path,
		rawQuery,
		nodeID,
		timestamp,
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	return hex.EncodeToString(mac.Sum(nil))
}

func onlineCount(active int64) int {
	if active <= 0 {
		return 0
	}
	maxInt := int64(int(^uint(0) >> 1))
	if active > maxInt {
		return int(maxInt)
	}
	return int(active)
}

func safeBatchNodeID(nodeID string) string {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return "node"
	}
	var b strings.Builder
	for _, r := range nodeID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "node"
	}
	return b.String()
}

func decodeKLESSKey(text string) ([]byte, error) {
	text = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(text))
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
