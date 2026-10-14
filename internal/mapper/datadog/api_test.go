package datadog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitor" {
			t.Errorf("Request sent to %s, expected /api/v1/monitor", r.URL.Path)
		}
		expectedQuery := map[string]string{
			"group_states":   "alert,warn,no data",
			"with_downtimes": "true",
			"page":           "0",
			"page_size":      "1000",
			"monitor_tags":   "team:sre",
		}
		for k, v := range expectedQuery {
			if got := r.URL.Query().Get(k); got != v {
				t.Errorf("Query parameter %s=%q, expected %q", k, got, v)
			}
		}
		if got := r.Header.Get("DD-API-KEY"); got != "api-key" {
			t.Errorf("DD-API-KEY header is %q, expected %q", got, "api-key")
		}
		if got := r.Header.Get("DD-APPLICATION-KEY"); got != "app-key" {
			t.Errorf("DD-APPLICATION-KEY header is %q, expected %q", got, "app-key")
		}
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("Unexpected basic auth credentials in the request")
		}
		_, _ = w.Write([]byte(testMonitors))
	}))
	defer server.Close()

	headers := map[string]string{"DD-API-KEY": "api-key", "DD-APPLICATION-KEY": "app-key"}
	groups, silences, err := Collect(server.URL+"/?monitor_tags=team:sre", "https://app.datadoghq.com", nil, headers, time.Second*5, nil)
	if err != nil {
		t.Fatalf("Collect() returned an error: %s", err)
	}
	if len(groups) != 5 {
		t.Errorf("Expected 5 alert groups, got %d", len(groups))
	}
	// only check downtimes that never end, others depend on the current time
	ids := map[string]bool{}
	for _, s := range silences {
		ids[s.ID] = true
	}
	for _, id := range []string{"100", "300"} {
		if !ids[id] {
			t.Errorf("Missing silence %s in %v", id, ids)
		}
	}
}

func TestCollectExcludeTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id": 1, "name": "composite", "overall_state": "Alert", "tags": ["team:sre"]},
			{"id": 2, "name": "child", "overall_state": "Alert", "tags": ["team:sre", "roles:composite-child"]}
		]`))
	}))
	defer server.Close()

	groups, _, err := Collect(server.URL, server.URL, []string{"roles:composite-child"}, nil, time.Second*5, nil)
	if err != nil {
		t.Fatalf("Collect() returned an error: %s", err)
	}
	if len(groups) != 1 {
		t.Fatalf("Expected 1 alert group, got %d", len(groups))
	}
	if name := groups[0].Labels.Get("alertname"); name != "composite" {
		t.Errorf("Expected alert group for monitor 'composite', got %q", name)
	}
}

func TestCollectBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "user" || password != "pass" {
			t.Errorf("Expected basic auth with user:pass, got %q:%q", username, password)
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	uri := strings.Replace(server.URL, "http://", "http://user:pass@", 1)
	_, _, err := Collect(uri, server.URL, nil, nil, time.Second*5, http.DefaultTransport)
	if err != nil {
		t.Fatalf("Collect() returned an error: %s", err)
	}
}

func TestCollectPagination(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		// first page is full of monitors that are OK, second one has a single
		// monitor in alert state
		monitors := []string{}
		switch page {
		case 0:
			for i := range pageSize {
				monitors = append(monitors, fmt.Sprintf(`{"id": %d, "name": "ok", "overall_state": "OK"}`, i))
			}
		case 1:
			monitors = append(monitors, `{"id": 9999, "name": "alert", "overall_state": "Alert"}`)
		default:
			t.Errorf("Unexpected request for page %d", page)
		}
		_, _ = w.Write([]byte("[" + strings.Join(monitors, ",") + "]"))
	}))
	defer server.Close()

	groups, _, err := Collect(server.URL, server.URL, nil, nil, time.Second*5, nil)
	if err != nil {
		t.Fatalf("Collect() returned an error: %s", err)
	}
	if requests.Load() != 2 {
		t.Errorf("Expected 2 requests, got %d", requests.Load())
	}
	if len(groups) != 1 {
		t.Fatalf("Expected 1 alert group, got %d", len(groups))
	}
	if name := groups[0].Labels.Get("alertname"); name != "alert" {
		t.Errorf("Expected alert group for monitor 'alert', got %q", name)
	}
}

func TestCollectPaginationIgnored(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		// return the same list of monitors, bigger than a single page, on
		// every request
		monitors := make([]string, 0, pageSize+1)
		for i := range pageSize + 1 {
			monitors = append(monitors, fmt.Sprintf(`{"id": %d, "name": "ok", "overall_state": "OK"}`, i))
		}
		_, _ = w.Write([]byte("[" + strings.Join(monitors, ",") + "]"))
	}))
	defer server.Close()

	_, _, err := Collect(server.URL, server.URL, nil, nil, time.Second*5, nil)
	if err != nil {
		t.Fatalf("Collect() returned an error: %s", err)
	}
	if requests.Load() != 1 {
		t.Errorf("Expected 1 request, got %d", requests.Load())
	}
}

func TestCollectErrors(t *testing.T) {
	type testCaseT struct {
		handler http.HandlerFunc
		name    string
		err     string
	}

	testCases := []testCaseT{
		{
			name: "non-200 response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"errors": ["Forbidden"]}`))
			},
			err: "returned status 403",
		},
		{
			name: "invalid JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"errors": `))
			},
			err: "failed to decode monitors",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			groups, silences, err := Collect(server.URL, server.URL, nil, nil, time.Second*5, nil)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("Expected error containing %q, got %v", tc.err, err)
			}
			if groups != nil || silences != nil {
				t.Errorf("Expected no data on error, got %d group(s) and %d silence(s)", len(groups), len(silences))
			}
		})
	}
}

func TestCollectRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	uri := server.URL
	server.Close()

	_, _, err := Collect(uri, uri, nil, nil, time.Second*5, nil)
	if err == nil {
		t.Error("Expected an error when the server is down, got nil")
	}
}

func TestCollectInvalidURI(t *testing.T) {
	_, _, err := Collect("http://[::1", "", nil, nil, time.Second*5, nil)
	if err == nil {
		t.Error("Expected an error for an invalid URI, got nil")
	}
}

func TestWebURI(t *testing.T) {
	type testCaseT struct {
		apiURI string
		webURI string
	}

	testCases := []testCaseT{
		{apiURI: "https://api.datadoghq.com", webURI: "https://app.datadoghq.com"},
		{apiURI: "https://api.datadoghq.com/", webURI: "https://app.datadoghq.com"},
		{apiURI: "https://api.datadoghq.eu?monitor_tags=team:sre", webURI: "https://app.datadoghq.eu"},
		{apiURI: "https://api.ddog-gov.com", webURI: "https://app.ddog-gov.com"},
		{apiURI: "https://api.us3.datadoghq.com", webURI: "https://us3.datadoghq.com"},
		{apiURI: "https://api.us5.datadoghq.com/", webURI: "https://us5.datadoghq.com"},
		{apiURI: "https://api.ap1.datadoghq.com", webURI: "https://ap1.datadoghq.com"},
		{apiURI: "https://app.datadoghq.com/api", webURI: "https://app.datadoghq.com"},
		{apiURI: "http://127.0.0.1:8080/datadog", webURI: "http://127.0.0.1:8080"},
		{apiURI: "http://[::1", webURI: "http://[::1"},
		{apiURI: "not a uri", webURI: "not a uri"},
	}

	for _, tc := range testCases {
		if got := WebURI(tc.apiURI); got != tc.webURI {
			t.Errorf("WebURI(%q) returned %q, expected %q", tc.apiURI, got, tc.webURI)
		}
	}
}

func TestIsMonitorURI(t *testing.T) {
	type testCaseT struct {
		webURI    string
		uri       string
		isMonitor bool
	}

	testCases := []testCaseT{
		{webURI: "https://app.datadoghq.com", uri: "https://app.datadoghq.com/monitors/1", isMonitor: true},
		{webURI: "https://app.datadoghq.com/", uri: "https://app.datadoghq.com/monitors/1?group=host%3Aa", isMonitor: true},
		{webURI: "https://app.datadoghq.com", uri: "https://app.datadoghq.eu/monitors/1", isMonitor: false},
		{webURI: "https://app.datadoghq.com", uri: "https://app.datadoghq.com/dashboard/1", isMonitor: false},
		{webURI: "https://app.datadoghq.com", uri: "http://prometheus.example.com/graph", isMonitor: false},
	}

	for _, tc := range testCases {
		if got := IsMonitorURI(tc.webURI, tc.uri); got != tc.isMonitor {
			t.Errorf("IsMonitorURI(%q, %q) returned %v, expected %v", tc.webURI, tc.uri, got, tc.isMonitor)
		}
	}
}
