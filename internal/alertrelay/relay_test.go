package alertrelay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadFallbackConfigRequiresHTTPSAndFixedStatePath(t *testing.T) {
	path := t.TempDir() + "/fallback.json"
	if err := os.WriteFile(path, []byte(`{"endpoint":"http://receiver.example.test","failureThreshold":3}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFallbackConfig(path); err == nil {
		t.Fatal("HTTP fallback endpoint accepted")
	}
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://receiver.example.test","failureThreshold":3,"stateFile":"/tmp/escape"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFallbackConfig(path); err == nil {
		t.Fatal("secret-controlled state path accepted")
	}
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://receiver.example.test","failureThreshold":3,"headers":{"Authorization":"Bearer ref"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadFallbackConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.StateFile != "/var/lib/kdb-alert-relay/fallback-state.json" {
		t.Fatalf("state file=%q", config.StateFile)
	}
}

func TestP0FallbackRequiresThreeFailuresAndDeduplicatesAcrossRestart(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	_ = publicKey
	if err != nil {
		t.Fatal(err)
	}
	var fallbackCalls atomic.Int32
	var centerFailing atomic.Bool
	centerFailing.Store(true)
	var recoveredEvidence atomic.Bool
	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Idempotency-Key") == "" {
			t.Error("missing fallback idempotency key")
		}
		fallbackCalls.Add(1)
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer fallback.Close()
	center := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if centerFailing.Load() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var signal alertSignal
		if json.NewDecoder(request.Body).Decode(&signal) == nil && signal.Evidence["fallback"] == true && signal.Evidence["centerFailureReason"] == "center_unreachable" {
			recoveredEvidence.Store(true)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer center.Close()
	statePath := t.TempDir() + "/fallback-state.json"
	config := Config{CellID: "cell-a", RelayID: "relay-a", CenterEndpoint: center.URL, Fallback: &FallbackConfig{Endpoint: fallback.URL, FailureThreshold: 3, StateFile: statePath}}
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"Down","kdb_template_key":"mysql.availability.down","kdb_template_version":"1.0.0","kdb_policy_id":"apol-1","kdb_policy_revision":"1","kdb_severity":"P0","tenant_id":"default","project_id":"pay","environment_id":"prod","region_id":"v-hash","instance_id":"mysql-orders","cloud_cluster_id":"cell-a"},"annotations":{"summary":"down"},"startsAt":"2026-08-26T00:00:00Z","fingerprint":"am-p0"}]}`
	relay := &Relay{config: config, client: center.Client(), fallbackClient: fallback.Client(), signer: privateKey, now: time.Now}
	for attempt := 1; attempt <= 3; attempt++ {
		recorder := httptest.NewRecorder()
		relay.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/alertmanager", strings.NewReader(body)))
		if recorder.Code != http.StatusBadGateway {
			t.Fatalf("attempt %d status=%d", attempt, recorder.Code)
		}
	}
	if got := fallbackCalls.Load(); got != 1 {
		t.Fatalf("fallback calls=%d want=1", got)
	}
	restarted := &Relay{config: config, client: center.Client(), fallbackClient: fallback.Client(), signer: privateKey, now: time.Now}
	recorder := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/alertmanager", strings.NewReader(body)))
	if got := fallbackCalls.Load(); got != 1 {
		t.Fatalf("restart duplicated fallback calls=%d", got)
	}
	centerFailing.Store(false)
	recovery := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(recovery, httptest.NewRequest(http.MethodPost, "/v1/alertmanager", strings.NewReader(body)))
	if recovery.Code != http.StatusOK || !recoveredEvidence.Load() {
		t.Fatalf("recovery=%d evidence=%v", recovery.Code, recoveredEvidence.Load())
	}
	if err := restarted.SendHeartbeat(context.Background()); err != nil {
		t.Fatalf("heartbeat after recovery: %v", err)
	}
}

func TestP1NeverUsesCellFallback(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	var fallbackCalls atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer fallback.Close()
	center := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusServiceUnavailable) }))
	defer center.Close()
	relay := &Relay{config: Config{CellID: "cell-a", RelayID: "relay-a", CenterEndpoint: center.URL, Fallback: &FallbackConfig{Endpoint: fallback.URL, FailureThreshold: 1, StateFile: t.TempDir() + "/state.json"}}, client: center.Client(), fallbackClient: fallback.Client(), signer: privateKey, now: time.Now}
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"Lag","kdb_template_key":"mysql.replication.lag-high","kdb_template_version":"1.0.0","kdb_policy_id":"apol-1","kdb_policy_revision":"1","kdb_severity":"P1","tenant_id":"default","project_id":"pay","environment_id":"prod","region_id":"v-hash","instance_id":"mysql-orders","cloud_cluster_id":"cell-a"},"annotations":{"summary":"lag"},"startsAt":"2026-08-26T00:00:00Z","fingerprint":"am-p1"}]}`
	for attempt := 0; attempt < 3; attempt++ {
		relay.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/alertmanager", strings.NewReader(body)))
	}
	if got := fallbackCalls.Load(); got != 0 {
		t.Fatalf("P1 fallback calls=%d", got)
	}
}

func TestTranslateWebhookProducesDeterministicManagedSignal(t *testing.T) {
	body := []byte(`{"receiver":"kdb-relay","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"KDBMySQLReplicationLagHigh","kdb_template_key":"mysql.replication.lag-high","kdb_template_version":"1.2.0","kdb_policy_id":"apol-1","kdb_policy_revision":"7","kdb_severity":"P1","severity":"warning","tenant_id":"default","project_id":"pay","environment_id":"prod","region_id":"v-hash","instance_id":"mysql-orders","cloud_cluster_id":"cell-a","pod":"mysql-orders-0"},"annotations":{"summary":"lag high","currentValue":"128"},"startsAt":"2026-08-25T13:59:00Z","endsAt":"0001-01-01T00:00:00Z","generatorURL":"http://prometheus/graph","fingerprint":"am-fp"}],"groupLabels":{},"commonLabels":{},"commonAnnotations":{},"externalURL":"http://alertmanager","version":"4","groupKey":"{}:{}"}`)
	first, err := translateWebhook("cell-a", body)
	if err != nil || len(first) != 1 {
		t.Fatalf("signals=%#v err=%v", first, err)
	}
	second, err := translateWebhook("cell-a", body)
	if err != nil || second[0].EventID != first[0].EventID {
		t.Fatalf("retry event id changed: %#v %#v err=%v", first, second, err)
	}
	signal := first[0]
	if signal.SourceInstance != "cell-a" || signal.TemplateRef.PolicyRevision != 7 || signal.ScopeLabels["region_id"] != "v-hash" || signal.Dimensions["pod"] != "mysql-orders-0" || signal.Evidence["currentValue"] != "128" {
		t.Fatalf("signal=%#v", signal)
	}
	wrongCell := strings.Replace(string(body), `"cloud_cluster_id":"cell-a"`, `"cloud_cluster_id":"cell-b"`, 1)
	if _, err = translateWebhook("cell-a", []byte(wrongCell)); err == nil {
		t.Fatal("relay accepted webhook for another Cell")
	}
}

func TestRelayForwardsPersistedContractWithCertificateKeySignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	center := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Error(readErr)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		payloadSum := sha256.Sum256(body)
		payloadHash := fmt.Sprintf("sha256:%x", payloadSum[:])
		canonical := []byte(request.Method + "\n" + request.URL.Path + "\n" + request.Header.Get("X-KDB-Alert-Timestamp") + "\n" + request.Header.Get("X-KDB-Alert-Nonce") + "\n" + payloadHash)
		signature, decodeErr := base64.StdEncoding.DecodeString(request.Header.Get("X-KDB-Alert-Signature"))
		if decodeErr != nil || request.Header.Get("X-KDB-Alert-Signature-Algorithm") != "ed25519" || !ed25519.Verify(publicKey, canonical, signature) {
			t.Errorf("invalid relay signature: %v", decodeErr)
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		var signal alertSignal
		if json.Unmarshal(body, &signal) != nil || signal.EventID != request.Header.Get("X-KDB-Alert-Nonce") {
			t.Errorf("nonce/body mismatch body=%s", body)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer center.Close()
	now := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	relay := &Relay{config: Config{CellID: "cell-a", RelayID: "relay-a", CenterEndpoint: center.URL}, client: center.Client(), signer: privateKey, now: func() time.Time { return now }}
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"Lag","kdb_template_key":"mysql.replication.lag-high","kdb_template_version":"1.2.0","kdb_policy_id":"apol-1","kdb_policy_revision":"7","kdb_severity":"P1","severity":"warning","tenant_id":"default","project_id":"pay","environment_id":"prod","region_id":"v-hash","instance_id":"mysql-orders","cloud_cluster_id":"cell-a"},"annotations":{"summary":"lag"},"startsAt":"2026-08-25T13:59:00Z","fingerprint":"am-fp"}]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/alertmanager", strings.NewReader(body))
	relay.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
