package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
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

func queryClickHouseSidecarStatus(ctx context.Context, podIP string) (sidecarStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/v1/clickhouse/status", net.JoinHostPort(podIP, "8080")), nil)
	if err != nil {
		return sidecarStatus{}, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
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
