package datadog

import (
	"testing"

	json "encoding/json/v2"

	"github.com/google/go-cmp/cmp"
)

func TestRenderMessage(t *testing.T) {
	priority := int64(2)
	stateBlocks := "{{#is_alert}}alert{{/is_alert}}{{#is_warning}}warning{{/is_warning}}{{#is_no_data}}no data{{/is_no_data}}{{#is_recovery}}recovery{{/is_recovery}}"
	nestedBlocks := "{{#is_alert}}a{{#is_warning}}b{{else}}c{{/is_warning}}d{{/is_alert}}{{#is_warning}}e{{#is_alert}}f{{else}}g{{/is_alert}}{{/is_warning}}"
	elseBlocks := "{{#is_alert}}alert{{else}}other{{/is_alert}} {{^is_alert}}other{{else}}alert{{/is_alert}}"

	type testCaseT struct {
		name     string
		message  string
		status   string
		expected string
	}

	testCases := []testCaseT{
		{
			name:     "plain text",
			message:  "CPU usage is high @slack-ops",
			expected: "CPU usage is high @slack-ops",
		},
		{
			name:     "empty message",
			message:  "",
			expected: "",
		},
		{
			name:     "whitespace around the message is trimmed",
			message:  "\n  CPU usage is high \n",
			expected: "CPU usage is high",
		},
		{
			name:     "alert blocks",
			message:  stateBlocks,
			status:   statusAlert,
			expected: "alert",
		},
		{
			name:     "warning blocks",
			message:  stateBlocks,
			status:   statusWarn,
			expected: "warning",
		},
		{
			name:     "no data blocks",
			message:  stateBlocks,
			status:   statusNoData,
			expected: "no data",
		},
		{
			name:     "transition blocks",
			message:  "{{#is_alert_recovery}}a{{/is_alert_recovery}}{{#is_warning_recovery}}b{{/is_warning_recovery}}{{#is_no_data_recovery}}c{{/is_no_data_recovery}}{{#is_alert_to_warning}}d{{/is_alert_to_warning}}{{#is_unknown}}e{{/is_unknown}}{{#is_renotify}}f{{/is_renotify}}done",
			expected: "done",
		},
		{
			name:     "inverted blocks",
			message:  "{{^is_alert}}not alerting{{/is_alert}}{{^is_warning}}not warning{{/is_warning}}",
			expected: "not warning",
		},
		{
			name:     "else when the condition is true",
			message:  elseBlocks,
			expected: "alert alert",
		},
		{
			name:     "else when the condition is false",
			message:  elseBlocks,
			status:   statusWarn,
			expected: "other other",
		},
		{
			name:     "nested blocks",
			message:  nestedBlocks,
			expected: "acd",
		},
		{
			name:     "nested blocks in hidden blocks",
			message:  nestedBlocks,
			status:   statusWarn,
			expected: "eg",
		},
		{
			name:     "is_priority",
			message:  `{{#is_priority 'P2'}}P2{{/is_priority}}{{#is_priority "p2"}} p2{{/is_priority}}{{#is_priority 'P1'}} P1{{/is_priority}}`,
			expected: "P2 p2",
		},
		{
			name:     "is_match",
			message:  `{{#is_match "host.name" "web"}}web{{/is_match}}{{#is_match "host.name" "db" "eb1"}} eb1{{/is_match}}{{#is_match "host.name" "db"}} db{{else}} not db{{/is_match}}{{^is_match "env" "dev"}} not dev{{/is_match}}`,
			expected: "web eb1 not db not dev",
		},
		{
			name:     "is_exact_match",
			message:  `{{#is_exact_match "host.name" "web"}}web{{/is_exact_match}}{{#is_exact_match "host.name" "db" "web1"}}web1{{/is_exact_match}}{{#is_exact_match "count.name" "5.0"}} 5.0{{/is_exact_match}}{{#is_exact_match "[dot.key].name" "dotted"}} dotted{{/is_exact_match}}{{#is_exact_match "env.name" "1"}} 1{{/is_exact_match}}`,
			expected: "web1 5.0 dotted",
		},
		{
			name:     "conditions that can't be evaluated are kept",
			message:  `{{#is_match "host.role.name" "db"}}db on {{host.name}}{{else}}other{{/is_match}} {{#is_exact_match "value" "5"}}five{{/is_exact_match}} {{#is_match "host.name"}}web{{/is_match}} {{#is_priority}}P2{{/is_priority}}`,
			expected: `{{#is_match "host.role.name" "db"}}db on web1{{else}}other{{/is_match}} {{#is_exact_match "value" "5"}}five{{/is_exact_match}} {{#is_match "host.name"}}web{{/is_match}} {{#is_priority}}P2{{/is_priority}}`,
		},
		{
			name:     "unknown blocks are kept",
			message:  "{{#each items}}{{host.name}}{{/each}}",
			expected: "{{#each items}}web1{{/each}}",
		},
		{
			name:     "tag variables",
			message:  "{{host.name}} {{ env.name }} {{[dot.key].name}} {{team.name}} {{{host.name}}}",
			expected: "web1 prod dotted infra,sre web1",
		},
		{
			name:     "variables",
			message:  "{{threshold}} {{warn_threshold}}",
			expected: "90 80",
		},
		{
			name:     "unresolved variables are kept",
			message:  "{{value}} {{host.ip}} {{service.name}} {{env}} {{local_time 'last_triggered_at' 'Europe/Paris'}} {{}}",
			expected: "{{value}} {{host.ip}} {{service.name}} {{env}} {{local_time 'last_triggered_at' 'Europe/Paris'}} {{}}",
		},
		{
			name:     "comments",
			message:  "a{{! comment }}b{{!-- {{#is_alert}} --}}c",
			expected: "abc",
		},
		{
			name:     "raw blocks",
			message:  "{{{{raw}}}}{{host.name}} {{#is_alert}}{{{{/raw}}}}",
			expected: "{{host.name}} {{#is_alert}}",
		},
		{
			name:     "lines with standalone tags are removed",
			message:  "{{#is_alert}}\n  Alert on {{host.name}}\n{{/is_alert}}\n{{#is_recovery}}\nRecovered\n{{/is_recovery}}\n  {{! comment }}  \n\n@slack-ops\n",
			expected: "Alert on web1\n\n@slack-ops",
		},
		{
			name:     "lines with tags of blocks that are kept are not removed",
			message:  "{{#is_match \"host.role.name\" \"db\"}}\ndb\n{{/is_match}}\nend",
			expected: "{{#is_match \"host.role.name\" \"db\"}}\ndb\n{{/is_match}}\nend",
		},
		{
			name:     "windows line endings",
			message:  "{{#is_alert}}\r\nalert\r\n{{/is_alert}}\r\n{{#is_warning}}\r\nwarning\r\n{{/is_warning}}\r\nend",
			expected: "alert\r\nend",
		},
		{
			name:     "lines with other content are not removed",
			message:  "{{#is_alert}}alert{{/is_alert}}{{#is_warning}}warning{{/is_warning}}\n{{host.name}} {{#is_alert}}\nend\n{{/is_alert}}",
			expected: "alert\nweb1 \nend",
		},
		{
			name:     "lines with text after tags are not removed",
			message:  "{{#is_alert}}alert\n{{/is_alert}}end",
			expected: "alert\nend",
		},
		{
			name:     "standalone tags at the end of the message",
			message:  "{{#is_alert}}\nalert\n{{/is_alert}}  ",
			expected: "alert",
		},
		{
			name:     "empty blocks",
			message:  "{{#is_alert}}{{/is_alert}}",
			expected: "",
		},
		{
			name:     "unclosed blocks are kept",
			message:  "{{#is_alert}}a{{else}}b",
			expected: "{{#is_alert}}a{{else}}b",
		},
		{
			name:     "tags outside of blocks are kept",
			message:  "{{else}}a{{/is_alert}}b",
			expected: "{{else}}a{{/is_alert}}b",
		},
		{
			name:     "mismatched closing tags are kept",
			message:  "{{#is_alert}}a{{/is_warning}}b{{/is_alert}}",
			expected: "a{{/is_warning}}b",
		},
		{
			name:     "unterminated tags are kept",
			message:  "{{{host.name}} {{!-- a {{host.name",
			expected: "{{{host.name}} {{!-- a {{host.name",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := &templateData{
				priority:  &priority,
				tags:      map[string]string{"host": "web1", "env": "prod", "dot.key": "dotted", "team": "infra,sre", "count": "5"},
				variables: map[string]string{"threshold": "90", "warn_threshold": "80"},
				status:    tc.status,
			}
			if data.status == "" {
				data.status = statusAlert
			}
			if got := renderMessage(tc.message, data); got != tc.expected {
				t.Errorf("renderMessage(%q) returned %q, expected %q", tc.message, got, tc.expected)
			}
		})
	}
}

func TestRenderMessageWithoutPriority(t *testing.T) {
	message := "{{#is_priority 'P1'}}P1{{else}}no priority{{/is_priority}}"
	if got := renderMessage(message, &templateData{status: statusAlert}); got != "no priority" {
		t.Errorf("renderMessage(%q) returned %q, expected %q", message, got, "no priority")
	}
}

func TestNewTemplateData(t *testing.T) {
	var m monitor
	if err := json.Unmarshal([]byte(`{
  "query": "avg(last_5m):avg:system.disk.free{*} by {host,device} < 110000000000",
  "priority": 1,
  "options": {"thresholds": {"critical": 110000000000, "warning": 2.5e11, "critical_recovery": null, "warning_recovery": "1", "ok": 0.93}}
}`), &m); err != nil {
		t.Fatalf("failed to decode monitor: %s", err)
	}

	group := monitorGroup{Status: statusNoData, LastTriggeredTs: 1600000000, LastNodataTs: 1700000000}
	expected := &templateData{
		priority: m.Priority,
		tags:     map[string]string{"host": "web1", "device": "c:", "dot.key": "a,b"},
		variables: map[string]string{
			"threshold":               "110000000000",
			"warn_threshold":          "2.5e11",
			"ok_threshold":            "0.93",
			"comparator":              "<",
			"last_triggered_at":       "2023-11-14 22:13:20 UTC",
			"last_triggered_at_epoch": "1700000000000",
		},
		status: statusNoData,
	}
	data := newTemplateData(&m, group, []string{"host:web1", "device:c:", "dot.key:b", "dot.key:a", "critical"})
	if diff := cmp.Diff(expected, data, cmp.AllowUnexported(templateData{})); diff != "" {
		t.Errorf("Wrong template data (-want +got):\n%s", diff)
	}

	m = monitor{Query: "12345 && 67890"}
	expected = &templateData{tags: map[string]string{}, variables: map[string]string{}, status: statusAlert}
	data = newTemplateData(&m, monitorGroup{Status: statusAlert}, nil)
	if diff := cmp.Diff(expected, data, cmp.AllowUnexported(templateData{})); diff != "" {
		t.Errorf("Wrong template data (-want +got):\n%s", diff)
	}
}

func TestComparatorRegex(t *testing.T) {
	for query, expected := range map[string]string{
		"avg(last_5m):avg:system.cpu.user{*} by {host} > 90":                                    ">",
		"avg(last_5m):avg:system.cpu.user{*} >= 90.5":                                           ">=",
		"max(last_15m):avg:sqlserver.buffer.page_life_expectancy{env:(a OR b)} by {host} < 300": "<",
		"avg(last_4h):anomalies(avg:system.cpu.user{*}, 'basic', 2) <= -1e-3":                   "<=",
		"sum(last_5m):sum:app.errors{*} == 0":                                                   "==",
		"sum(last_5m):sum:app.errors{*} != 0 \n":                                                "!=",
		`logs("status:error").index("*").rollup("count").by("service").last("5m") > 100`:        ">",
		"12345 && 67890": "",
		`"datadog.agent.up".over("*").by("host").last(2).count_by_status()`:                     "",
		"avg(last_5m):avg:system.cpu.user{*} by {host} > avg(last_5m):avg:system.cpu.system{*}": "",
	} {
		got := ""
		if match := comparatorRegex.FindStringSubmatch(query); match != nil {
			got = match[1]
		}
		if got != expected {
			t.Errorf("Wrong comparator for %q, expected %q, got %q", query, expected, got)
		}
	}
}
