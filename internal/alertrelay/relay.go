package alertrelay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxWebhookBytes = 1024 * 1024

type Config struct {
	CellID         string
	RelayID        string
	Version        string
	CenterEndpoint string
	TLSCertificate string
	TLSPrivateKey  string
	TLSCA          string
	Fallback       *FallbackConfig
}

type FallbackConfig struct {
	Endpoint         string            `json:"endpoint"`
	Headers          map[string]string `json:"headers,omitempty"`
	FailureThreshold int               `json:"failureThreshold,omitempty"`
	StateFile        string            `json:"-"`
}

type Relay struct {
	config         Config
	client         *http.Client
	fallbackClient *http.Client
	signer         crypto.Signer
	now            func() time.Time
	stateMu        sync.Mutex
	forwardSuccess atomic.Uint64
	forwardFailure atomic.Uint64
	fallbackSent   atomic.Uint64
}

type alertmanagerWebhook struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
		Fingerprint string            `json:"fingerprint"`
	} `json:"alerts"`
}

type alertSignal struct {
	SchemaVersion  string            `json:"schemaVersion"`
	EventID        string            `json:"eventId"`
	SourceType     string            `json:"sourceType"`
	SourceInstance string            `json:"sourceInstance"`
	SourceEventID  string            `json:"sourceEventId"`
	ObservedAt     time.Time         `json:"observedAt"`
	State          string            `json:"state"`
	TemplateRef    templateRef       `json:"templateRef"`
	Severity       string            `json:"severity"`
	RawSeverity    string            `json:"rawSeverity,omitempty"`
	ScopeLabels    map[string]string `json:"scopeLabels"`
	Dimensions     map[string]string `json:"dimensions,omitempty"`
	Summary        string            `json:"summary"`
	Evidence       map[string]any    `json:"evidence,omitempty"`
	StartsAt       time.Time         `json:"startsAt"`
	EndsAt         *time.Time        `json:"endsAt,omitempty"`
	Fingerprint    string            `json:"-"`
}

type templateRef struct {
	TemplateKey     string `json:"templateKey"`
	TemplateVersion string `json:"templateVersion"`
	PolicyID        string `json:"policyId"`
	PolicyRevision  int64  `json:"policyRevision"`
}

func New(config Config) (*Relay, error) {
	if strings.TrimSpace(config.CellID) == "" || strings.TrimSpace(config.RelayID) == "" || strings.TrimSpace(config.CenterEndpoint) == "" {
		return nil, errors.New("relay cell, identity and center endpoint are required")
	}
	certificate, err := tls.LoadX509KeyPair(config.TLSCertificate, config.TLSPrivateKey)
	if err != nil {
		return nil, err
	}
	signer, ok := certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("relay TLS private key cannot sign requests")
	}
	caPEM, err := os.ReadFile(config.TLSCA)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("relay center CA is invalid")
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}}
	return &Relay{config: config, client: client, fallbackClient: &http.Client{Timeout: 10 * time.Second}, signer: signer, now: time.Now}, nil
}

func LoadFallbackConfig(path string) (*FallbackConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config FallbackConfig
	if err = decoder.Decode(&config); err != nil {
		return nil, errors.New("fallback config is invalid")
	}
	parsed, parseErr := url.Parse(strings.TrimSpace(config.Endpoint))
	if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("fallback endpoint must use https")
	}
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 3
	}
	if config.FailureThreshold < 2 || config.FailureThreshold > 10 {
		return nil, errors.New("fallback failure threshold is invalid")
	}
	if config.StateFile == "" {
		config.StateFile = "/var/lib/kdb-alert-relay/fallback-state.json"
	}
	for name, value := range config.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "" || lower == "host" || lower == "content-length" || lower == "idempotency-key" || strings.ContainsAny(name+value, "\r\n") {
			return nil, errors.New("fallback header is invalid")
		}
	}
	return &config, nil
}

func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/metrics", r.handleMetrics)
	mux.HandleFunc("/v1/alertmanager", r.handleAlertmanager)
	return mux
}

func (r *Relay) handleAlertmanager(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxWebhookBytes))
	if err != nil {
		http.Error(writer, "webhook exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}
	signals, err := translateWebhook(r.config.CellID, body)
	if err != nil || len(signals) == 0 {
		http.Error(writer, "webhook contract is invalid", http.StatusBadRequest)
		return
	}
	for _, signal := range signals {
		stateKey := fallbackStateKey(signal)
		if state, ok := r.fallbackState(stateKey); ok && state.Sent {
			if signal.Evidence == nil {
				signal.Evidence = map[string]any{}
			}
			signal.Evidence["fallback"] = true
			signal.Evidence["fallbackCell"] = r.config.CellID
			signal.Evidence["originalFingerprint"] = signal.Fingerprint
			signal.Evidence["centerFailureReason"] = "center_unreachable"
		}
		payload, marshalErr := json.Marshal(signal)
		if marshalErr != nil {
			http.Error(writer, "signal encoding failed", http.StatusInternalServerError)
			return
		}
		if err = r.forward(request.Context(), "/internal/v1/kdb/alert-signals/alertmanager", signal.EventID, payload); err != nil {
			r.forwardFailure.Add(1)
			_ = r.recordForwardFailure(request.Context(), signal)
			http.Error(writer, "center ingest failed", http.StatusBadGateway)
			return
		}
		r.forwardSuccess.Add(1)
		if signal.State == "Resolved" {
			_ = r.clearFallbackState(stateKey)
		}
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"status":"forwarded"}`))
}

func (r *Relay) SendHeartbeat(ctx context.Context) error {
	now := r.now().UTC().Truncate(time.Second)
	eventID := fmt.Sprintf("%s/heartbeat/%s", r.config.RelayID, now.Format(time.RFC3339))
	payload, err := json.Marshal(map[string]any{"schemaVersion": "v1", "eventId": eventID, "relayId": r.config.RelayID, "cloudClusterId": r.config.CellID, "version": r.config.Version, "status": "Ready", "observedAt": now, "metadata": map[string]string{"fallbackActive": strconv.Itoa(r.activeFallbackCount())}})
	if err != nil {
		return err
	}
	return r.forward(ctx, "/internal/v1/kdb/alert-signals/relay-heartbeat", eventID, payload)
}

type fallbackRecord struct {
	Failures  int       `json:"failures"`
	Sent      bool      `json:"sent"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func fallbackStateKey(signal alertSignal) string {
	sum := sha256.Sum256([]byte(signal.Fingerprint + "\x00" + signal.StartsAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}

func (r *Relay) recordForwardFailure(ctx context.Context, signal alertSignal) error {
	if r.config.Fallback == nil || signal.Severity != "P0" || signal.State != "Firing" {
		return nil
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	states, err := r.loadFallbackStates()
	if err != nil {
		return err
	}
	key := fallbackStateKey(signal)
	record := states[key]
	if record.Sent {
		return nil
	}
	record.Failures++
	record.UpdatedAt = r.now().UTC()
	threshold := r.config.Fallback.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if record.Failures >= threshold {
		if err = r.sendFallback(ctx, key, signal); err != nil {
			states[key] = record
			_ = r.saveFallbackStates(states)
			return err
		}
		record.Sent = true
		r.fallbackSent.Add(1)
	}
	states[key] = record
	return r.saveFallbackStates(states)
}

func (r *Relay) sendFallback(ctx context.Context, key string, signal alertSignal) error {
	payload, err := json.Marshal(map[string]any{"schemaVersion": "v1", "fallback": true, "cellId": r.config.CellID, "relayId": r.config.RelayID, "idempotencyKey": key, "originalFingerprint": signal.Fingerprint, "templateKey": signal.TemplateRef.TemplateKey, "severity": "P0", "summary": signal.Summary, "startsAt": signal.StartsAt, "scopeLabels": signal.ScopeLabels, "centerFailureReason": "center_unreachable"})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.Fallback.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	for name, value := range r.config.Fallback.Headers {
		req.Header.Set(name, value)
	}
	client := r.fallbackClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("fallback receiver rejected request")
	}
	return nil
}

func (r *Relay) fallbackState(key string) (fallbackRecord, bool) {
	if r.config.Fallback == nil {
		return fallbackRecord{}, false
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	states, err := r.loadFallbackStates()
	if err != nil {
		return fallbackRecord{}, false
	}
	record, ok := states[key]
	return record, ok
}

func (r *Relay) clearFallbackState(key string) error {
	if r.config.Fallback == nil {
		return nil
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	states, err := r.loadFallbackStates()
	if err != nil {
		return err
	}
	delete(states, key)
	return r.saveFallbackStates(states)
}

func (r *Relay) activeFallbackCount() int {
	if r.config.Fallback == nil {
		return 0
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	states, err := r.loadFallbackStates()
	if err != nil {
		return 0
	}
	count := 0
	for _, record := range states {
		if record.Sent {
			count++
		}
	}
	return count
}

func (r *Relay) loadFallbackStates() (map[string]fallbackRecord, error) {
	states := map[string]fallbackRecord{}
	path := strings.TrimSpace(r.config.Fallback.StateFile)
	if path == "" {
		return states, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return states, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &states); err != nil {
		return nil, errors.New("fallback state is invalid")
	}
	cutoff := r.now().UTC().Add(-7 * 24 * time.Hour)
	for key, record := range states {
		if record.UpdatedAt.Before(cutoff) {
			delete(states, key)
		}
	}
	for len(states) > 10000 {
		for key := range states {
			delete(states, key)
			break
		}
	}
	return states, nil
}

func (r *Relay) saveFallbackStates(states map[string]fallbackRecord) error {
	path := strings.TrimSpace(r.config.Fallback.StateFile)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(states)
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (r *Relay) handleMetrics(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(writer, "kdb_alert_relay_forward_total{cell_id=%q,result=\"success\"} %d\n", r.config.CellID, r.forwardSuccess.Load())
	_, _ = fmt.Fprintf(writer, "kdb_alert_relay_forward_total{cell_id=%q,result=\"failure\"} %d\n", r.config.CellID, r.forwardFailure.Load())
	_, _ = fmt.Fprintf(writer, "kdb_alert_relay_fallback_total{cell_id=%q,result=\"sent\"} %d\n", r.config.CellID, r.fallbackSent.Load())
	_, _ = fmt.Fprintf(writer, "kdb_alert_relay_fallback_active{cell_id=%q} %d\n", r.config.CellID, r.activeFallbackCount())
}

func (r *Relay) forward(ctx context.Context, path, nonce string, body []byte) error {
	endpoint := strings.TrimRight(r.config.CenterEndpoint, "/") + path
	timestamp := r.now().UTC().Format(time.RFC3339Nano)
	payloadSum := sha256.Sum256(body)
	payloadHash := "sha256:" + hex.EncodeToString(payloadSum[:])
	canonical := []byte(http.MethodPost + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + payloadHash)
	algorithm, signature, err := sign(r.signer, canonical)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-KDB-Alert-Timestamp", timestamp)
	req.Header.Set("X-KDB-Alert-Nonce", nonce)
	req.Header.Set("X-KDB-Alert-Signature-Algorithm", algorithm)
	req.Header.Set("X-KDB-Alert-Signature", base64.StdEncoding.EncodeToString(signature))
	response, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("center returned %d", response.StatusCode)
	}
	return nil
}

func sign(signer crypto.Signer, message []byte) (string, []byte, error) {
	digest := sha256.Sum256(message)
	switch key := signer.(type) {
	case ed25519.PrivateKey:
		return "ed25519", ed25519.Sign(key, message), nil
	case *ecdsa.PrivateKey:
		signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
		return "ecdsa-sha256", signature, err
	case *rsa.PrivateKey:
		signature, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], nil)
		return "rsa-pss-sha256", signature, err
	default:
		return "", nil, errors.New("unsupported relay TLS key type")
	}
}

func translateWebhook(cellID string, body []byte) ([]alertSignal, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var webhook alertmanagerWebhook
	if err := decoder.Decode(&webhook); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("webhook has trailing JSON")
	}
	bodySum := sha256.Sum256(body)
	out := make([]alertSignal, 0, len(webhook.Alerts))
	for index, alert := range webhook.Alerts {
		if !strings.EqualFold(alert.Status, "firing") && !strings.EqualFold(alert.Status, "resolved") {
			return nil, errors.New("alert status is invalid")
		}
		state := "Firing"
		observed := alert.StartsAt.UTC()
		var endsAt *time.Time
		if strings.EqualFold(alert.Status, "resolved") {
			state = "Resolved"
			resolved := alert.EndsAt.UTC()
			if resolved.IsZero() {
				return nil, errors.New("resolved alert is missing endsAt")
			}
			observed, endsAt = resolved, &resolved
		}
		if alert.StartsAt.IsZero() || strings.TrimSpace(alert.Fingerprint) == "" {
			return nil, errors.New("alert identity is incomplete")
		}
		revision, err := strconv.ParseInt(alert.Labels["kdb_policy_revision"], 10, 64)
		if err != nil || revision <= 0 {
			return nil, errors.New("alert policy revision is invalid")
		}
		severity := normalizeSeverity(alert.Labels["kdb_severity"])
		if severity == "" || strings.TrimSpace(alert.Labels["kdb_template_key"]) == "" ||
			strings.TrimSpace(alert.Labels["kdb_template_version"]) == "" || strings.TrimSpace(alert.Labels["kdb_policy_id"]) == "" {
			return nil, errors.New("managed alert identity labels are incomplete")
		}
		eventSum := sha256.Sum256([]byte(fmt.Sprintf("%x:%d:%s:%s", bodySum[:], index, alert.Fingerprint, state)))
		eventID := cellID + "/alertmanager/" + hex.EncodeToString(eventSum[:16])
		scope := map[string]string{}
		for _, key := range []string{"tenant_id", "project_id", "environment_id", "region_id", "instance_id", "cloud_cluster_id"} {
			if value := strings.TrimSpace(alert.Labels[key]); value != "" {
				scope[key] = value
			}
		}
		if scope["cloud_cluster_id"] != cellID {
			return nil, errors.New("alert Cell label does not match relay identity")
		}
		dimensions := map[string]string{}
		for _, key := range []string{"pod", "container", "persistentvolumeclaim", "shard", "replica"} {
			if value := strings.TrimSpace(alert.Labels[key]); value != "" {
				dimensions[key] = value
			}
		}
		evidence := map[string]any{}
		for _, key := range []string{"currentValue", "threshold"} {
			if value := strings.TrimSpace(alert.Annotations[key]); value != "" {
				evidence[key] = value
			}
		}
		out = append(out, alertSignal{SchemaVersion: "v1", EventID: eventID, SourceType: "MetricRule", SourceInstance: cellID, SourceEventID: "am-" + hex.EncodeToString(eventSum[:]), ObservedAt: observed, State: state, TemplateRef: templateRef{TemplateKey: alert.Labels["kdb_template_key"], TemplateVersion: alert.Labels["kdb_template_version"], PolicyID: alert.Labels["kdb_policy_id"], PolicyRevision: revision}, Severity: severity, RawSeverity: alert.Labels["severity"], ScopeLabels: scope, Dimensions: dimensions, Summary: bounded(firstNonEmpty(alert.Annotations["summary"], alert.Labels["alertname"]), 1000), Evidence: evidence, StartsAt: alert.StartsAt.UTC(), EndsAt: endsAt, Fingerprint: alert.Fingerprint})
	}
	return out, nil
}

func normalizeSeverity(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "P0", "P1", "P2":
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func bounded(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
