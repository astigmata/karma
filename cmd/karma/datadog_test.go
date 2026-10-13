package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"

	"github.com/prymitive/karma/internal/alertmanager"
	"github.com/prymitive/karma/internal/config"
	"github.com/prymitive/karma/internal/models"
)

func TestDatadogUpstream(t *testing.T) {
	mockConfig(t.Setenv)
	mockCache()

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	uri := "https://api.datadog-karma.test"
	httpmock.RegisterMatcherResponder("GET", uri+"/api/v1/monitor",
		httpmock.HeaderIs("DD-API-KEY", "api-secret").And(httpmock.HeaderIs("DD-APPLICATION-KEY", "app-secret")),
		httpmock.NewStringResponder(200, `[
  {
    "id": 1,
    "name": "CPU usage is high",
    "overall_state": "Alert",
    "state": {"groups": {"host:a": {"name": "host:a", "status": "Alert", "last_triggered_ts": 1700000000}}}
  }
]`))

	am, err := alertmanager.NewAlertmanager(
		"",
		"datadog",
		uri,
		alertmanager.WithType(config.UpstreamTypeDatadog),
		alertmanager.WithReadOnly(true),
		alertmanager.WithHTTPHeaders(map[string]string{"DD-API-KEY": "api-secret", "DD-APPLICATION-KEY": "app-secret"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// replace all upstreams with our Datadog instance, restore them when done
	upstreams := alertmanager.GetAlertmanagers()
	alertmanager.UnregisterAll()
	defer func() {
		alertmanager.UnregisterAll()
		for _, upstream := range upstreams {
			_ = alertmanager.RegisterAlertmanager(upstream)
		}
	}()
	if err = alertmanager.RegisterAlertmanager(am); err != nil {
		t.Fatal(err)
	}

	pullFromAlertmanager()
	if am.Error() != "" {
		t.Fatalf("Datadog upstream returned an error: %s", am.Error())
	}

	hp := newHistoryPoller(1, time.Second*5)
	r := testRouter()
	setupRouter(r, hp)
	go hp.run(1)
	defer hp.stop()

	payload, err := json.Marshal(models.AlertsRequest{
		Filters:           []string{},
		GridLimits:        map[string]int{},
		DefaultGroupLimit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/alerts.json", bytes.NewReader(payload))
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /alerts.json returned status %d", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "secret") {
		t.Errorf("Datadog API keys leaked in /alerts.json response: %s", resp.Body.String())
	}

	ur := models.AlertsResponse{}
	if err = json.Unmarshal(resp.Body.Bytes(), &ur); err != nil {
		t.Fatalf("Failed to unmarshal response: %s", err)
	}
	if ur.TotalAlerts != 1 {
		t.Errorf("Expected 1 alert, got %d", ur.TotalAlerts)
	}
	if len(ur.Upstreams.Instances) != 1 {
		t.Fatalf("Expected 1 upstream, got %d", len(ur.Upstreams.Instances))
	}
	upstream := ur.Upstreams.Instances[0]
	if upstream.Type != config.UpstreamTypeDatadog {
		t.Errorf("Expected upstream type %q, got %q", config.UpstreamTypeDatadog, upstream.Type)
	}
	if len(upstream.Headers) != 0 {
		t.Errorf("Expected no headers for Datadog upstream, got %v", upstream.Headers)
	}
	if !upstream.ReadOnly {
		t.Error("Expected Datadog upstream to be read-only")
	}
	if upstream.PublicURI != "https://app.datadog-karma.test" {
		t.Errorf("Expected public URI https://app.datadog-karma.test, got %s", upstream.PublicURI)
	}

	// history for Datadog alerts is skipped without sending any request
	calls := httpmock.GetTotalCallCount()
	payload, err = json.Marshal(AlertHistoryPayload{
		Sources: []string{"https://app.datadog-karma.test/monitors/1?group=host%3Aa"},
		Labels:  map[string]string{"alertname": "CPU usage is high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("POST", "/history.json", bytes.NewReader(payload))
	resp = httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /history.json returned status %d", resp.Code)
	}
	hr := AlertHistoryResponse{}
	if err = json.Unmarshal(resp.Body.Bytes(), &hr); err != nil {
		t.Fatalf("Failed to unmarshal response: %s", err)
	}
	if hr.Error != "" {
		t.Errorf("Unexpected history error: %s", hr.Error)
	}
	if len(hr.Samples) != 24 {
		t.Errorf("Expected 24 history samples, got %d", len(hr.Samples))
	}
	if httpmock.GetTotalCallCount() != calls {
		t.Errorf("History request for a Datadog source sent %d request(s)", httpmock.GetTotalCallCount()-calls)
	}
}
