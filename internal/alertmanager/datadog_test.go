package alertmanager

import (
	"slices"
	"strings"
	"testing"

	"github.com/jarcoal/httpmock"

	"github.com/prymitive/karma/internal/config"
	"github.com/prymitive/karma/internal/models"
)

const datadogMonitors = `[
  {
    "id": 1,
    "name": "CPU usage is high",
    "overall_state": "Alert",
    "tags": ["team:sre"],
    "state": {
      "groups": {
        "host:a": {"name": "host:a", "status": "Alert", "last_triggered_ts": 1700000000},
        "host:b": {"name": "host:b", "status": "Warn", "last_triggered_ts": 1700000100}
      }
    },
    "matching_downtimes": [{"id": 10, "start": 1690000000, "end": null, "scope": ["host:b"]}]
  }
]`

func TestDatadogExternalURI(t *testing.T) {
	type testCaseT struct {
		uri         string
		externalURI string
		upstream    string
		publicURI   string
	}

	testCases := []testCaseT{
		{
			uri:       "https://api.datadoghq.eu",
			upstream:  config.UpstreamTypeDatadog,
			publicURI: "https://app.datadoghq.eu",
		},
		{
			uri:         "https://api.datadoghq.eu",
			externalURI: "https://datadog.example.com",
			upstream:    config.UpstreamTypeDatadog,
			publicURI:   "https://datadog.example.com",
		},
		{
			uri:       "https://api.datadoghq.eu",
			upstream:  config.UpstreamTypeAlertmanager,
			publicURI: "https://api.datadoghq.eu",
		},
	}

	for _, tc := range testCases {
		am, err := NewAlertmanager("", "dd", tc.uri, WithType(tc.upstream), WithExternalURI(tc.externalURI))
		if err != nil {
			t.Fatalf("NewAlertmanager() returned an error: %s", err)
		}
		if am.Type != tc.upstream {
			t.Errorf("Expected type %q, got %q", tc.upstream, am.Type)
		}
		if am.PublicURI() != tc.publicURI {
			t.Errorf("Expected public URI %q for type %q, got %q", tc.publicURI, tc.upstream, am.PublicURI())
		}
	}
}

func TestDatadogDefaultType(t *testing.T) {
	am, err := NewAlertmanager("", "am", "http://localhost")
	if err != nil {
		t.Fatalf("NewAlertmanager() returned an error: %s", err)
	}
	if am.Type != config.UpstreamTypeAlertmanager {
		t.Errorf("Expected type %q, got %q", config.UpstreamTypeAlertmanager, am.Type)
	}
}

func TestDatadogPull(t *testing.T) {
	uri := "https://api.datadog-pull.test"
	httpmock.Activate()
	httpmock.RegisterMatcherResponder("GET", uri+"/api/v1/monitor",
		httpmock.HeaderIs("DD-API-KEY", "secret"),
		httpmock.NewStringResponder(200, datadogMonitors))

	am, err := NewAlertmanager(
		"",
		"datadog-pull",
		uri,
		WithType(config.UpstreamTypeDatadog),
		WithHTTPHeaders(map[string]string{"DD-API-KEY": "secret"}),
	)
	if err != nil {
		t.Fatalf("NewAlertmanager() returned an error: %s", err)
	}
	// other tests in this package depend on registered upstreams, so only
	// remove this one when done
	if err = RegisterAlertmanager(am); err != nil {
		t.Fatalf("RegisterAlertmanager() returned an error: %s", err)
	}
	defer delete(upstreams, am.Name)

	if err = am.Pull(); err != nil {
		t.Fatalf("Pull() returned an error: %s", err)
	}
	if am.Error() != "" {
		t.Errorf("Unexpected error: %s", am.Error())
	}

	groups := am.Alerts()
	if len(groups) != 1 {
		t.Fatalf("Expected 1 alert group, got %d", len(groups))
	}
	if len(groups[0].Alerts) != 2 {
		t.Fatalf("Expected 2 alerts, got %d", len(groups[0].Alerts))
	}
	states := map[string]models.AlertState{}
	for _, alert := range groups[0].Alerts {
		host := alert.Labels.Get("host")
		states[host] = alert.State
		if len(alert.Alertmanager) != 1 {
			t.Fatalf("Expected 1 alertmanager instance, got %d", len(alert.Alertmanager))
		}
		source := alert.Alertmanager[0].Source
		if !strings.HasPrefix(source, "https://app.datadog-pull.test/monitors/1?group=host%3A") {
			t.Errorf("Unexpected source link %q", source)
		}
		if !IsDatadogSource(source) {
			t.Errorf("IsDatadogSource(%q) returned false", source)
		}
		if alert.Receiver != "datadog" {
			t.Errorf("Expected receiver datadog, got %q", alert.Receiver)
		}
	}
	if states["a"] != models.AlertStateActive {
		t.Errorf("Expected alert for host a to be active, got %s", states["a"])
	}
	if states["b"] != models.AlertStateSuppressed {
		t.Errorf("Expected alert for host b to be suppressed, got %s", states["b"])
	}

	silences := am.Silences()
	if _, ok := silences["10"]; !ok || len(silences) != 1 {
		t.Errorf("Expected silence 10, got %v", silences)
	}
	if !slices.Contains(am.KnownLabels(), "team") {
		t.Errorf("Expected team label in known labels, got %v", am.KnownLabels())
	}

	if IsDatadogSource("http://prometheus.example.com/graph?g0.expr=up") {
		t.Error("IsDatadogSource() returned true for a Prometheus link")
	}
}

func TestDatadogPullError(t *testing.T) {
	uri := "https://api.datadog-error.test"
	httpmock.Activate()
	httpmock.RegisterResponder("GET", uri+"/api/v1/monitor", httpmock.NewStringResponder(403, `{"errors": ["Forbidden"]}`))

	am, err := NewAlertmanager("", "datadog-error", uri, WithType(config.UpstreamTypeDatadog))
	if err != nil {
		t.Fatalf("NewAlertmanager() returned an error: %s", err)
	}

	if err = am.Pull(); err == nil {
		t.Fatal("Pull() didn't return any error")
	}
	if !strings.Contains(am.Error(), "returned status 403") {
		t.Errorf("Unexpected error: %q", am.Error())
	}
	if am.Metrics.Errors[labelValueErrorsAlerts] != 1 {
		t.Errorf("Expected 1 alerts error, got %v", am.Metrics.Errors)
	}
	if len(am.Alerts()) != 0 {
		t.Errorf("Expected no alerts, got %d", len(am.Alerts()))
	}
}
