package datadog

import (
	"slices"
	"strconv"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/cespare/xxhash/v2"
	"github.com/google/go-cmp/cmp"

	"github.com/prymitive/karma/internal/models"
)

type testAlert struct {
	StartsAt     time.Time
	Labels       map[string]string
	Annotations  map[string]string
	Fingerprint  string
	GeneratorURL string
	Receiver     string
	SilencedBy   []string
	State        models.AlertState
}

type testGroup struct {
	Labels   map[string]string
	Receiver string
	Alerts   []testAlert
}

type testSilence struct {
	StartsAt  time.Time
	EndsAt    time.Time
	ID        string
	CreatedBy string
	Comment   string
	Matchers  []string
}

func toTestGroups(groups []models.AlertGroup) []testGroup {
	tgs := make([]testGroup, 0, len(groups))
	for _, g := range groups {
		tg := testGroup{
			Labels:   g.Labels.Map(),
			Receiver: g.Receiver,
			Alerts:   make([]testAlert, 0, len(g.Alerts)),
		}
		for _, a := range g.Alerts {
			annotations := map[string]string{}
			for _, annotation := range a.Annotations {
				annotations[annotation.Name] = annotation.Value
			}
			tg.Alerts = append(tg.Alerts, testAlert{
				StartsAt:     a.StartsAt,
				Labels:       a.Labels.Map(),
				Annotations:  annotations,
				Fingerprint:  a.Fingerprint,
				GeneratorURL: a.GeneratorURL,
				Receiver:     a.Receiver,
				SilencedBy:   a.SilencedBy,
				State:        a.State,
			})
		}
		tgs = append(tgs, tg)
	}
	return tgs
}

func toTestSilences(silences []models.Silence) []testSilence {
	tss := make([]testSilence, 0, len(silences))
	for _, s := range silences {
		ts := testSilence{
			StartsAt:  s.StartsAt,
			EndsAt:    s.EndsAt,
			ID:        s.ID,
			CreatedBy: s.CreatedBy,
			Comment:   s.Comment,
		}
		for _, m := range s.Matchers {
			op := "="
			if m.IsRegex {
				op = "=~"
			}
			ts.Matchers = append(ts.Matchers, m.Name+op+m.Value)
		}
		tss = append(tss, ts)
	}
	return tss
}

func fingerprint(s string) string {
	return strconv.FormatUint(xxhash.Sum64String(s), 16)
}

const testMonitors = `[
  {
    "id": 1,
    "name": "CPU usage is high on {{host.name}}",
    "message": "{{#is_alert}}CPU usage is above {{threshold}} on {{host.name}}{{/is_alert}}{{#is_warning}}CPU usage is above {{warn_threshold}} on {{host.name}}{{/is_warning}}{{#is_no_data}}No data from {{host.name}}{{/is_no_data}} @slack-ops",
    "query": "avg(last_5m):avg:system.cpu.user{*} by {env,host} > 90",
    "options": {"thresholds": {"critical": 90, "warning": 80}, "notify_no_data": true},
    "type": "metric alert",
    "overall_state": "Alert",
    "priority": 2,
    "tags": ["team:sre", "env:all", "critical", "team:infra", "kube-namespace:monitoring", "severity:high", "1st:yes"],
    "state": {
      "groups": {
        "env:prod,host:web1": {"name": "env:prod,host:web1", "status": "Alert", "last_triggered_ts": 1700000000, "last_nodata_ts": null},
        "env:prod,host:web2": {"name": "env:prod,host:web2", "status": "Warn", "last_triggered_ts": 1700000100},
        "env:dev,host:web3": {"name": "env:dev,host:web3", "status": "No Data", "last_triggered_ts": 1600000000, "last_nodata_ts": 1700000200},
        "env:dev,host:web4": {"name": "env:dev,host:web4", "status": "OK", "last_triggered_ts": 1700000300}
      }
    },
    "matching_downtimes": [
      {"id": 100, "start": 1690000000, "end": null, "scope": ["host:web2"]},
      {"id": 101, "start": 1800000000, "end": 1900000000, "scope": ["*"]},
      {"id": 102, "start": 1600000000, "end": 1650000000, "scope": ["*"]}
    ]
  },
  {
    "id": 2,
    "name": "Disk is full",
    "message": "",
    "query": "avg(last_5m):avg:system.disk.in_use{*} > 0.9",
    "overall_state": "Alert",
    "overall_state_modified": "2023-11-14T22:13:20+00:00",
    "priority": null,
    "tags": [],
    "state": {"groups": {}},
    "matching_downtimes": [
      {"id": 200, "start": 1690000000, "end": 1750000000, "scope": ["env:prod"]}
    ]
  },
  {
    "id": 3,
    "name": "Everything is fine",
    "overall_state": "OK",
    "state": {}
  },
  {
    "id": 4,
    "name": "Maintenance A",
    "message": "{{#is_recovery}}Back to normal{{/is_recovery}}",
    "overall_state": "Warn",
    "state": {"groups": {"*": {"name": "*", "status": "Warn", "last_triggered_ts": 1700000400}}},
    "matching_downtimes": [
      {"id": 300, "start": 1690000000, "scope": ["*"]}
    ]
  },
  {
    "id": 5,
    "name": "Maintenance B",
    "overall_state": "Alert",
    "state": {"groups": {"host:db1": {"name": "host:db1", "status": "Alert", "last_triggered_ts": 1700000500}}},
    "matching_downtimes": [
      {"id": 300, "start": 1690000000, "scope": ["*"]}
    ]
  },
  {
    "id": 6,
    "name": "Queue is stuck",
    "query": "logs(\"service:queue\").index(\"*\").rollup(\"count\").last(\"10m\") < 1",
    "options": {"on_missing_data": "show_and_notify_no_data"},
    "overall_state": "No Data",
    "overall_state_modified": "not a date",
    "state": {"groups": {}}
  },
  {
    "id": 7,
    "name": "Error rate is high",
    "query": "sum(last_10m):sum:trace.http.request.errors{*} by {service}.as_count() > 5",
    "options": {"on_missing_data": "show_no_data"},
    "overall_state": "No Data",
    "state": {"groups": {"service:api": {"name": "service:api", "status": "No Data", "last_nodata_ts": 1700000600}}}
  },
  {
    "id": 8,
    "name": "API has no traffic",
    "query": "sum(last_10m):sum:trace.http.request.hits{*}.as_count() < 1",
    "options": {"notify_no_data": false},
    "overall_state": "No Data",
    "overall_state_modified": "2023-11-14T22:13:20+00:00",
    "state": {"groups": {}}
  }
]`

func TestMapMonitors(t *testing.T) {
	var monitors []monitor
	if err := json.Unmarshal([]byte(testMonitors), &monitors); err != nil {
		t.Fatalf("failed to decode monitors: %s", err)
	}

	now := time.Unix(1700001000, 0).UTC()
	groups, silences := mapMonitors(monitors, "https://app.datadoghq.com/", now)

	cpuAnnotations := func(message string) map[string]string {
		return map[string]string{
			"message": "<p>" + message + "</p>",
			"query":   "avg(last_5m):avg:system.cpu.user{*} by {env,host} > 90",
		}
	}
	cpuLabels := func(env, host, severity string) map[string]string {
		return map[string]string{
			"alertname":      "CPU usage is high on {{host.name}}",
			"monitor_id":     "1",
			"severity":       severity,
			"priority":       "P2",
			"team":           "infra,sre",
			"kube_namespace": "monitoring",
			"_1st":           "yes",
			"env":            env,
			"host":           host,
		}
	}

	expectedGroups := []testGroup{
		{
			Labels:   map[string]string{"alertname": "CPU usage is high on {{host.name}}", "monitor_id": "1"},
			Receiver: "datadog",
			Alerts: []testAlert{
				{
					StartsAt:     time.Unix(1700000200, 0).UTC(),
					Labels:       cpuLabels("dev", "web3", "nodata"),
					Annotations:  cpuAnnotations("No data from web3 @slack-ops"),
					Fingerprint:  fingerprint("1/env:dev,host:web3"),
					GeneratorURL: "https://app.datadoghq.com/monitors/1?group=env%3Adev%2Chost%3Aweb3",
					Receiver:     "datadog",
					State:        models.AlertStateActive,
				},
				{
					StartsAt:     time.Unix(1700000000, 0).UTC(),
					Labels:       cpuLabels("prod", "web1", "critical"),
					Annotations:  cpuAnnotations("CPU usage is above 90 on web1 @slack-ops"),
					Fingerprint:  fingerprint("1/env:prod,host:web1"),
					GeneratorURL: "https://app.datadoghq.com/monitors/1?group=env%3Aprod%2Chost%3Aweb1",
					Receiver:     "datadog",
					State:        models.AlertStateActive,
				},
				{
					StartsAt:     time.Unix(1700000100, 0).UTC(),
					Labels:       cpuLabels("prod", "web2", "warning"),
					Annotations:  cpuAnnotations("CPU usage is above 80 on web2 @slack-ops"),
					Fingerprint:  fingerprint("1/env:prod,host:web2"),
					GeneratorURL: "https://app.datadoghq.com/monitors/1?group=env%3Aprod%2Chost%3Aweb2",
					Receiver:     "datadog",
					SilencedBy:   []string{"100"},
					State:        models.AlertStateSuppressed,
				},
			},
		},
		{
			Labels:   map[string]string{"alertname": "Disk is full", "monitor_id": "2"},
			Receiver: "datadog",
			Alerts: []testAlert{
				{
					StartsAt:     time.Unix(1700000000, 0).UTC(),
					Labels:       map[string]string{"alertname": "Disk is full", "monitor_id": "2", "severity": "critical"},
					Annotations:  map[string]string{"query": "avg(last_5m):avg:system.disk.in_use{*} > 0.9"},
					Fingerprint:  fingerprint("2/"),
					GeneratorURL: "https://app.datadoghq.com/monitors/2",
					Receiver:     "datadog",
					SilencedBy:   []string{"200"},
					State:        models.AlertStateSuppressed,
				},
			},
		},
		{
			Labels:   map[string]string{"alertname": "Maintenance A", "monitor_id": "4"},
			Receiver: "datadog",
			Alerts: []testAlert{
				{
					StartsAt:     time.Unix(1700000400, 0).UTC(),
					Labels:       map[string]string{"alertname": "Maintenance A", "monitor_id": "4", "severity": "warning"},
					Annotations:  map[string]string{},
					Fingerprint:  fingerprint("4/*"),
					GeneratorURL: "https://app.datadoghq.com/monitors/4",
					Receiver:     "datadog",
					SilencedBy:   []string{"300"},
					State:        models.AlertStateSuppressed,
				},
			},
		},
		{
			Labels:   map[string]string{"alertname": "Maintenance B", "monitor_id": "5"},
			Receiver: "datadog",
			Alerts: []testAlert{
				{
					StartsAt:     time.Unix(1700000500, 0).UTC(),
					Labels:       map[string]string{"alertname": "Maintenance B", "monitor_id": "5", "severity": "critical", "host": "db1"},
					Annotations:  map[string]string{},
					Fingerprint:  fingerprint("5/host:db1"),
					GeneratorURL: "https://app.datadoghq.com/monitors/5?group=host%3Adb1",
					Receiver:     "datadog",
					SilencedBy:   []string{"300"},
					State:        models.AlertStateSuppressed,
				},
			},
		},
		{
			Labels:   map[string]string{"alertname": "Queue is stuck", "monitor_id": "6"},
			Receiver: "datadog",
			Alerts: []testAlert{
				{
					StartsAt:     now,
					Labels:       map[string]string{"alertname": "Queue is stuck", "monitor_id": "6", "severity": "nodata"},
					Annotations:  map[string]string{"query": `logs("service:queue").index("*").rollup("count").last("10m") < 1`},
					Fingerprint:  fingerprint("6/"),
					GeneratorURL: "https://app.datadoghq.com/monitors/6",
					Receiver:     "datadog",
					State:        models.AlertStateActive,
				},
			},
		},
	}
	if diff := cmp.Diff(expectedGroups, toTestGroups(groups)); diff != "" {
		t.Errorf("Wrong alert groups returned (-want +got):\n%s", diff)
	}

	expectedSilences := []testSilence{
		{
			StartsAt:  time.Unix(1690000000, 0).UTC(),
			EndsAt:    neverEnds,
			ID:        "100",
			CreatedBy: "datadog",
			Comment:   "Datadog downtime",
			Matchers:  []string{"monitor_id=1", "host=web2"},
		},
		{
			StartsAt:  time.Unix(1690000000, 0).UTC(),
			EndsAt:    time.Unix(1750000000, 0).UTC(),
			ID:        "200",
			CreatedBy: "datadog",
			Comment:   "Datadog downtime",
			Matchers:  []string{"monitor_id=2", "env=prod"},
		},
		{
			StartsAt:  time.Unix(1690000000, 0).UTC(),
			EndsAt:    neverEnds,
			ID:        "300",
			CreatedBy: "datadog",
			Comment:   "Datadog downtime",
			Matchers:  []string{"monitor_id=~4|5"},
		},
	}
	if diff := cmp.Diff(expectedSilences, toTestSilences(silences)); diff != "" {
		t.Errorf("Wrong silences returned (-want +got):\n%s", diff)
	}

	for _, g := range groups {
		for _, a := range g.Alerts {
			if a.ContentFingerprint() == "" {
				t.Errorf("Alert %s has no content fingerprint", a.Labels.String())
			}
		}
	}
}

func TestMapMonitorsEmpty(t *testing.T) {
	groups, silences := mapMonitors(nil, "https://app.datadoghq.com", time.Now())
	if len(groups) != 0 {
		t.Errorf("Expected no alert groups, got %d", len(groups))
	}
	if len(silences) != 0 {
		t.Errorf("Expected no silences, got %d", len(silences))
	}
}

func TestExcludeMonitors(t *testing.T) {
	type testCaseT struct {
		name     string
		tags     []string
		expected []int64
	}

	monitors := []monitor{
		{ID: 1, Tags: []string{"team:sre", "roles:composite-child"}},
		{ID: 2, Tags: []string{"team:sre"}},
		{ID: 3},
		{ID: 4, Tags: []string{"roles:composite-child", "roles:db"}},
	}

	testCases := []testCaseT{
		{
			name:     "no tags excluded",
			expected: []int64{1, 2, 3, 4},
		},
		{
			name:     "monitors with excluded tag",
			tags:     []string{"roles:composite-child"},
			expected: []int64{2, 3},
		},
		{
			name:     "tags are case insensitive",
			tags:     []string{"Roles:Composite-Child"},
			expected: []int64{2, 3},
		},
		{
			name:     "monitors with any excluded tag",
			tags:     []string{"roles:db", "team:sre"},
			expected: []int64{3},
		},
		{
			name:     "tag key alone doesn't match",
			tags:     []string{"roles"},
			expected: []int64{1, 2, 3, 4},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := []int64{}
			for _, m := range excludeMonitors(slices.Clone(monitors), tc.tags) {
				got = append(got, m.ID)
			}
			if diff := cmp.Diff(tc.expected, got); diff != "" {
				t.Errorf("Wrong monitors (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMonitorAnnotations(t *testing.T) {
	m := monitor{
		Message: "{{#is_alert}}**CPU usage** is high on {{host.name}}{{/is_alert}}",
		Query:   "avg(last_5m):avg:system.cpu.user{*} by {host} > 90",
	}
	expected := models.Annotations{
		{Name: "message", Value: "<p><strong>CPU usage</strong> is high on web1</p>", Visible: true, IsHTML: true},
		{Name: "query", Value: "avg(last_5m):avg:system.cpu.user{*} by {host} > 90", Visible: true},
	}
	got := m.annotations(monitorGroup{Status: statusAlert}, []string{"host:web1"})
	if diff := cmp.Diff(expected, got); diff != "" {
		t.Errorf("Incorrect annotations (-want +got):\n%s", diff)
	}
}

func TestDowntimeIsActive(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := func(v int64) *int64 { return &v }

	type testCaseT struct {
		downtime downtime
		isActive bool
	}

	testCases := []testCaseT{
		{downtime: downtime{}, isActive: true},
		{downtime: downtime{Start: ts(1600000000)}, isActive: true},
		{downtime: downtime{Start: ts(1800000000)}, isActive: false},
		{downtime: downtime{End: ts(1800000000)}, isActive: true},
		{downtime: downtime{End: ts(1600000000)}, isActive: false},
		{downtime: downtime{End: ts(1700000000)}, isActive: false},
		{downtime: downtime{Start: ts(1700000000), End: ts(1700000001)}, isActive: true},
	}

	for _, tc := range testCases {
		if got := tc.downtime.isActive(now); got != tc.isActive {
			t.Errorf("isActive() returned %v for start=%v end=%v, expected %v", got, tc.downtime.Start, tc.downtime.End, tc.isActive)
		}
	}
}

func TestDowntimeMutes(t *testing.T) {
	type testCaseT struct {
		scope     []string
		groupTags []string
		mutes     bool
	}

	testCases := []testCaseT{
		{scope: nil, groupTags: nil, mutes: true},
		{scope: []string{"*"}, groupTags: []string{"host:a"}, mutes: true},
		{scope: []string{"env:prod"}, groupTags: nil, mutes: true},
		{scope: []string{"host:a"}, groupTags: []string{"env:prod", "host:a"}, mutes: true},
		{scope: []string{"host:a", "env:prod"}, groupTags: []string{"env:prod", "host:a"}, mutes: true},
		{scope: []string{"host:a,env:prod"}, groupTags: []string{"env:prod", "host:a"}, mutes: true},
		{scope: []string{"host:b"}, groupTags: []string{"env:prod", "host:a"}, mutes: false},
		{scope: []string{"host:a", "env:dev"}, groupTags: []string{"env:prod", "host:a"}, mutes: false},
		{scope: []string{"service:api"}, groupTags: []string{"host:a"}, mutes: false},
	}

	for _, tc := range testCases {
		d := downtime{Scope: tc.scope}
		if got := d.mutes(tc.groupTags); got != tc.mutes {
			t.Errorf("mutes(%v) returned %v for scope %v, expected %v", tc.groupTags, got, tc.scope, tc.mutes)
		}
	}
}

func TestSplitTags(t *testing.T) {
	type testCaseT struct {
		in  string
		out []string
	}

	testCases := []testCaseT{
		{in: "", out: []string{}},
		{in: "*", out: []string{}},
		{in: "host:a", out: []string{"host:a"}},
		{in: "env:prod,host:a", out: []string{"env:prod", "host:a"}},
		{in: " env:prod , ,host:a,", out: []string{"env:prod", "host:a"}},
		{in: "url:http://example.com", out: []string{"url:http://example.com"}},
	}

	for _, tc := range testCases {
		if diff := cmp.Diff(tc.out, splitTags(tc.in)); diff != "" {
			t.Errorf("Wrong tags returned for %q (-want +got):\n%s", tc.in, diff)
		}
	}
}

func TestTagsToLabels(t *testing.T) {
	tags := []string{"team:b", "team:a", "team:b", "valueless", "env:", ":foo", "url:http://example.com", "kube.namespace:default"}
	expected := map[string]string{
		"team":           "a,b",
		"url":            "http://example.com",
		"kube_namespace": "default",
	}
	if diff := cmp.Diff(expected, tagsToLabels(tags)); diff != "" {
		t.Errorf("Wrong labels returned (-want +got):\n%s", diff)
	}
}

func TestLabelName(t *testing.T) {
	type testCaseT struct {
		key  string
		name string
	}

	testCases := []testCaseT{
		{key: "host", name: "host"},
		{key: "kube_namespace", name: "kube_namespace"},
		{key: "availability-zone", name: "availability_zone"},
		{key: "@http.status_code", name: "_http_status_code"},
		{key: "1st", name: "_1st"},
		{key: "zoné", name: "zon_"},
		{key: "", name: ""},
	}

	for _, tc := range testCases {
		if got := labelName(tc.key); got != tc.name {
			t.Errorf("labelName(%q) returned %q, expected %q", tc.key, got, tc.name)
		}
	}
}
