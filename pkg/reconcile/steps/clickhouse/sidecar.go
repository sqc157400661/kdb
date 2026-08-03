package clickhouse

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/sqc157400661/kdb/internal/naming"
	reconcilecontext "github.com/sqc157400661/kdb/pkg/reconcile/context"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type sidecarStatus struct {
	Healthy                 bool
	Readonly                bool
	ReplicationDelaySeconds int64
}

type sidecarStatusResponse struct {
	Status string `json:"status"`
	Data   struct {
		Readonly    bool `json:"readonly"`
		Replication struct {
			AbsoluteDelaySeconds int64 `json:"absoluteDelaySeconds"`
		} `json:"replication"`
	} `json:"data"`
}

func queryClickHouseSidecarStatus(rc *reconcilecontext.InstanceContext, podIP string) (sidecarStatus, error) {
	tlsSecret := &corev1.Secret{}
	instance := rc.GetInstance()
	if err := rc.Client().Get(rc.Context(), types.NamespacedName{Namespace: instance.Namespace, Name: naming.ClickHouseSecretName(instance.Name)}, tlsSecret); err != nil {
		return sidecarStatus{}, err
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(tlsSecret.Data[naming.ClickHouseTLSCAKey]) {
		return sidecarStatus{}, fmt.Errorf("invalid ClickHouse sidecar TLS CA")
	}
	certificate, err := tls.X509KeyPair(tlsSecret.Data[naming.ClickHouseTLSClientCertKey], tlsSecret.Data[naming.ClickHouseTLSClientPrivateKey])
	if err != nil {
		return sidecarStatus{}, fmt.Errorf("invalid ClickHouse sidecar client certificate: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      ca,
		Certificates: []tls.Certificate{certificate},
		ServerName:   "localhost",
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	req, err := http.NewRequestWithContext(rc.Context(), http.MethodGet, fmt.Sprintf("https://%s/v1/clickhouse/status", net.JoinHostPort(podIP, "8080")), nil)
	if err != nil {
		return sidecarStatus{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return sidecarStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return sidecarStatus{}, fmt.Errorf("sidecar status http %d", resp.StatusCode)
	}
	var payload sidecarStatusResponse
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return sidecarStatus{}, err
	}
	return sidecarStatus{
		Healthy:                 payload.Status == "success",
		Readonly:                payload.Data.Readonly,
		ReplicationDelaySeconds: payload.Data.Replication.AbsoluteDelaySeconds,
	}, nil
}

func replicaRoutable(status sidecarStatus, maxDelaySeconds int64) bool {
	return status.Healthy && !status.Readonly && status.ReplicationDelaySeconds <= maxDelaySeconds
}
