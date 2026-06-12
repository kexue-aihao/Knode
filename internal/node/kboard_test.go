package node

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kexue-aihao/Knode/internal/config"
)

func TestKboardSignatureMatchesKboardScript(t *testing.T) {
	got := hmacSignature(
		"change-this-node-secret",
		http.MethodPost,
		"/kb-8f3d7a2c9e4b/knode/control/alive",
		"",
		"1",
		"1800000000",
	)
	want := "b37607440d650d083f058923d0d0455195eb3304c578fbbe67b562f32dcf9044"
	if got != want {
		t.Fatalf("hmacSignature() = %s, want %s", got, want)
	}
}

func TestKboardReporterAliveUsesKboardProtocol(t *testing.T) {
	const (
		nodeID = "2"
		secret = "test-node-shared-secret"
	)

	payloadCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/kb-prefix/knode/control/alive" {
			errCh <- fmt.Errorf("path = %q", r.URL.EscapedPath())
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if r.URL.RawQuery != "source=test" {
			errCh <- fmt.Errorf("raw query = %q", r.URL.RawQuery)
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		if err := verifyKboardRequest(r, secret, nodeID); err != nil {
			errCh <- err
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			errCh <- err
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		payloadCh <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	SetBuildInfo("v-test", "", "")
	defer SetBuildInfo("dev", "", "")

	reporter := newKboardReporter(config.KboardConfig{
		PublicURL:        server.URL,
		NodeID:           nodeID,
		NodeSharedSecret: secret,
		AliveEndpoint:    "/kb-prefix/knode/control/alive?source=test",
	}, "kboard-node-2", log.New(io.Discard, "", 0), func() Status {
		return Status{Metrics: MetricsSnapshot{Active: 7}}
	}, nil, nil)

	reporter.reportAlive(context.Background())

	select {
	case err := <-errCh:
		t.Fatal(err)
	case payload := <-payloadCh:
		if got := payload["online"]; got != float64(7) {
			t.Fatalf("online = %#v, want 7", got)
		}
		if got := payload["backend_version"]; got != "knode-v-test" {
			t.Fatalf("backend_version = %#v, want knode-v-test", got)
		}
		if got, ok := payload["runtime"].(string); !ok || got == "" {
			t.Fatalf("runtime = %#v, want non-empty string", payload["runtime"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("alive report was not received")
	}
}

func TestKboardReporterTrafficUsesEmptyBatch(t *testing.T) {
	const (
		nodeID = "2"
		secret = "test-node-shared-secret"
	)

	payloadCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := verifyKboardRequest(r, secret, nodeID); err != nil {
			errCh <- err
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			errCh <- err
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		payloadCh <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	reporter := newKboardReporter(config.KboardConfig{
		PublicURL:        server.URL,
		NodeID:           nodeID,
		NodeSharedSecret: secret,
		TrafficEndpoint:  "/kb-prefix/knode/control/traffic",
	}, "kboard-node-2", log.New(io.Discard, "", 0), func() Status {
		return Status{}
	}, nil, nil)

	reporter.reportTraffic(context.Background())

	select {
	case err := <-errCh:
		t.Fatal(err)
	case payload := <-payloadCh:
		if got, ok := payload["batch_id"].(string); !ok || got == "" {
			t.Fatalf("batch_id = %#v, want non-empty string", payload["batch_id"])
		}
		items, ok := payload["items"].([]any)
		if !ok {
			t.Fatalf("items = %#v, want array", payload["items"])
		}
		if len(items) != 0 {
			t.Fatalf("len(items) = %d, want 0", len(items))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("traffic report was not received")
	}
}

func TestKboardReporterAccessLogsReportsBufferedItems(t *testing.T) {
	const (
		nodeID = "5"
		secret = "test-node-shared-secret"
	)

	payloadCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/kb-prefix/knode/control/access-logs" {
			errCh <- fmt.Errorf("path = %q", r.URL.EscapedPath())
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if err := verifyKboardRequest(r, secret, nodeID); err != nil {
			errCh <- err
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			errCh <- err
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		payloadCh <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	buffer := newAccessLogBuffer()
	if ok := buffer.Record(kboardAccessLogItem{
		ClientID:      "user-uuid",
		KLESSClientID: "user-uuid",
		Domain:        "www.google.com",
		Host:          "www.google.com",
		URI:           "https://www.google.com",
		Protocol:      "https",
		RemoteAddr:    "192.0.2.1:50000",
		AccessedAt:    1800000000,
	}); !ok {
		t.Fatal("access log item was not buffered")
	}
	reporter := newKboardReporter(config.KboardConfig{
		PublicURL:          server.URL,
		NodeID:             nodeID,
		NodeSharedSecret:   secret,
		AccessLogsEndpoint: "/kb-prefix/knode/control/access-logs",
	}, "kboard-node-5", log.New(io.Discard, "", 0), func() Status {
		return Status{}
	}, nil, buffer)

	reporter.reportAccessLogs(context.Background())

	select {
	case err := <-errCh:
		t.Fatal(err)
	case payload := <-payloadCh:
		if got, ok := payload["batch_id"].(string); !ok || !strings.HasPrefix(got, "knode-access-kboard-node-5-") {
			t.Fatalf("batch_id = %#v", payload["batch_id"])
		}
		items, ok := payload["items"].([]any)
		if !ok {
			t.Fatalf("items = %#v, want array", payload["items"])
		}
		if len(items) != 1 {
			t.Fatalf("len(items) = %d, want 1", len(items))
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("item = %#v", items[0])
		}
		if item["kless_client_id"] != "user-uuid" || item["domain"] != "www.google.com" || item["protocol"] != "https" {
			t.Fatalf("item = %#v", item)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("access log report was not received")
	}
	if drained := buffer.Drain(1); len(drained) != 0 {
		t.Fatalf("buffer still has %d items", len(drained))
	}
}

func TestKboardReporterSyncUsersUpdatesDynamicClientStore(t *testing.T) {
	const (
		nodeID = "2"
		secret = "test-node-shared-secret"
	)

	legacyClientSecret := []byte("legacy-client-secret-000000000001")
	clientSecret := []byte("kless-client-secret-0000000000001")
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errCh <- fmt.Errorf("method = %s", r.Method)
			http.Error(w, "bad method", http.StatusBadRequest)
			return
		}
		if err := verifyKboardRequest(r, secret, nodeID); err != nil {
			errCh <- err
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocol_version": "kboard-knode-control/1",
			"node_id":          2,
			"users": []map[string]any{
				{
					"id":                  1001,
					"uuid":                "user-uuid",
					"client_secret":       encodeClientSecret(legacyClientSecret),
					"kless_client_secret": encodeClientSecret(clientSecret),
				},
			},
			"count": 1,
		})
	}))
	defer server.Close()

	store := newDynamicClientStore()
	reporter := newKboardReporter(config.KboardConfig{
		PublicURL:        server.URL,
		NodeID:           nodeID,
		NodeSharedSecret: secret,
		UsersEndpoint:    "/kb-prefix/knode/control/users",
	}, "kboard-node-2", log.New(io.Discard, "", 0), func() Status {
		return Status{}
	}, store, nil)

	reporter.syncUsers(context.Background())

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
	got, ok := store.LookupClientSecret("user-uuid")
	if !ok {
		t.Fatal("client secret not loaded")
	}
	if string(got) != string(clientSecret) {
		t.Fatalf("client secret = %q, want %q", got, clientSecret)
	}
	if userID := store.UserID("user-uuid"); userID != 1001 {
		t.Fatalf("user id = %d, want 1001", userID)
	}
}

func TestKboardReporterSyncUsersSupportsWrappedDataResponse(t *testing.T) {
	const (
		nodeID = "4"
		secret = "test-node-shared-secret"
	)

	clientSecret := []byte("wrapped-kless-client-secret-0001")
	errCh := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errCh <- fmt.Errorf("method = %s", r.Method)
			http.Error(w, "bad method", http.StatusBadRequest)
			return
		}
		if err := verifyKboardRequest(r, secret, nodeID); err != nil {
			errCh <- err
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"protocol_version": "kboard-knode-control/1",
				"node_id":          4,
				"users": []map[string]any{
					{
						"id":                  2,
						"uuid":                "legacy-user-id",
						"client_id":           "legacy-client-id",
						"client_secret":       encodeClientSecret([]byte("legacy-client-secret-000000000001")),
						"kless_client_id":     "kless-client-id",
						"kless_client_secret": encodeClientSecret(clientSecret),
					},
				},
				"count": 1,
			},
		})
	}))
	defer server.Close()

	store := newDynamicClientStore()
	reporter := newKboardReporter(config.KboardConfig{
		PublicURL:        server.URL,
		NodeID:           nodeID,
		NodeSharedSecret: secret,
		UsersEndpoint:    "/kb-prefix/knode/control/users",
	}, "kboard-node-4", log.New(io.Discard, "", 0), func() Status {
		return Status{}
	}, store, nil)

	reporter.syncUsers(context.Background())

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
	got, ok := store.LookupClientSecret("kless-client-id")
	if !ok {
		t.Fatal("wrapped kless client secret not loaded")
	}
	if string(got) != string(clientSecret) {
		t.Fatalf("client secret = %q, want %q", got, clientSecret)
	}
	if _, ok := store.LookupClientSecret("legacy-client-id"); ok {
		t.Fatal("legacy client id should not be preferred when kless_client_id is present")
	}
	if userID := store.UserID("kless-client-id"); userID != 2 {
		t.Fatalf("user id = %d, want 2", userID)
	}
}

func verifyKboardRequest(r *http.Request, secret, nodeID string) error {
	if got := r.Header.Get("X-KBoard-Node-ID"); got != nodeID {
		return fmt.Errorf("node header = %q", got)
	}
	timestamp := r.Header.Get("X-KBoard-Timestamp")
	if timestamp == "" {
		return fmt.Errorf("missing timestamp header")
	}
	signature := r.Header.Get("X-KBoard-Signature")
	if signature == "" {
		return fmt.Errorf("missing signature header")
	}
	base := strings.Join([]string{
		r.Method,
		r.URL.EscapedPath(),
		r.URL.RawQuery,
		nodeID,
		timestamp,
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(strings.ToLower(signature))) {
		return fmt.Errorf("signature = %q, want %q", signature, expected)
	}
	return nil
}
