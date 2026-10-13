package datadog

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Monitor messages are notification templates rendered by Datadog for every
// notification it sends, see https://docs.datadoghq.com/monitors/notify/variables/
// Alerts get the message rendered for the current state of their monitor
// group. Conditions and variables that can't be resolved with what the
// monitors API returns, like {{value}}, are kept as they are.

const (
	rawBlockStart = "{{{{raw}}}}"
	rawBlockEnd   = "{{{{/raw}}}}"
)

// template variables set from monitor thresholds, by threshold name
var thresholdVariables = map[string]string{
	"critical":          "threshold",
	"warning":           "warn_threshold",
	"critical_recovery": "alert_recovery_threshold",
	"warning_recovery":  "warn_recovery_threshold",
	"ok":                "ok_threshold",
}

// matches the comparator used in alert conditions of monitor queries, like
// "avg(last_5m):avg:system.cpu.user{*} > 90"
var comparatorRegex = regexp.MustCompile(`(>=|<=|==|!=|>|<)\s*[-+]?[0-9.]+(?:[eE][-+]?[0-9]+)?\s*$`)

type tokenType int

const (
	textToken     tokenType = iota
	rawBlockToken           // {{{{raw}}}}text{{{{/raw}}}}
	commentToken            // {{! comment }} or {{!-- comment --}}
	variableToken           // {{variable}} or {{{variable}}}
	openToken               // {{#condition args}} or {{^condition args}}
	elseToken               // {{else}}
	closeToken              // {{/condition}}
)

type token struct {
	text     string   // template text, or the content of a raw block
	name     string   // variable or condition name
	args     []string // condition arguments
	typ      tokenType
	open     int  // index of the opening tag, for {{else}} and closing tags
	inverted bool // true for {{^condition}}
	closed   bool // true for opening tags of blocks with a closing tag
	evaluate bool // true for tags of blocks with a condition that can be evaluated
	result   bool // value of the condition, for opening tags
	strip    bool // true if the line of this tag is removed from the output
}

// templateData holds everything known about an alert that is used to render
// the message of its monitor
type templateData struct {
	priority  *int64
	tags      map[string]string // tags of the monitor group
	variables map[string]string // other template variables, like threshold
	status    string            // status of the monitor group
}

// newTemplateData returns data used to render the message of a monitor for
// one of its groups
func newTemplateData(m *monitor, group monitorGroup, groupTags []string) *templateData {
	data := &templateData{
		priority:  m.Priority,
		tags:      tagValues(groupTags, func(key string) string { return key }),
		variables: map[string]string{},
		status:    group.Status,
	}
	for threshold, name := range thresholdVariables {
		// thresholds are rendered as returned by the API
		if value := m.Options.Thresholds[threshold]; value.Kind() == '0' {
			data.variables[name] = string(value)
		}
	}
	if match := comparatorRegex.FindStringSubmatch(m.Query); match != nil {
		data.variables["comparator"] = match[1]
	}
	if ts := group.triggeredAt(); ts > 0 {
		data.variables["last_triggered_at"] = unixTime(ts).Format("2006-01-02 15:04:05 UTC")
		data.variables["last_triggered_at_epoch"] = strconv.FormatInt(ts*1000, 10)
	}
	return data
}

// renderMessage renders a monitor message template
func renderMessage(message string, data *templateData) string {
	tokens := tokenize(message)
	pairTags(tokens)
	for i := range tokens {
		t := &tokens[i]
		switch {
		case t.typ == openToken && t.closed:
			t.result, t.evaluate = data.condition(t.name, t.args)
		case t.typ == elseToken, t.typ == closeToken:
			t.evaluate = tokens[t.open].evaluate
		}
	}
	// like in Handlebars, lines with nothing else than a block tag or
	// a comment are removed from the output
	for i := range tokens {
		t := &tokens[i]
		t.strip = (t.evaluate || t.typ == commentToken) && isStandalone(tokens, i)
	}

	var b strings.Builder
	// visibility of nested blocks, the last one is the innermost block
	visible := []bool{true}
	for i, t := range tokens {
		switch {
		case t.evaluate && t.typ == openToken:
			visible = append(visible, visible[len(visible)-1] && t.result != t.inverted)
		case t.evaluate && t.typ == elseToken:
			open := tokens[t.open]
			visible[len(visible)-1] = visible[len(visible)-2] && open.result == open.inverted
		case t.evaluate && t.typ == closeToken:
			visible = visible[:len(visible)-1]
		case !visible[len(visible)-1], t.typ == commentToken:
			// nothing to render
		case t.typ == textToken:
			b.WriteString(trimStandalone(tokens, i))
		case t.typ == rawBlockToken:
			b.WriteString(t.text)
		case t.typ == variableToken:
			if value, ok := data.variable(t.name); ok {
				b.WriteString(value)
			} else {
				b.WriteString(t.text)
			}
		default:
			// tags of blocks with a condition that can't be evaluated
			b.WriteString(t.text)
		}
	}
	return strings.TrimSpace(b.String())
}

// tokenize splits a template into text and tags
func tokenize(tmpl string) []token {
	var tokens []token
	for tmpl != "" {
		t, size := token{typ: textToken, text: tmpl}, len(tmpl)
		if i := strings.Index(tmpl, "{{"); i == 0 {
			t, size = readTag(tmpl)
		} else if i > 0 {
			t, size = token{typ: textToken, text: tmpl[:i]}, i
		}
		if n := len(tokens); n > 0 && t.typ == textToken && tokens[n-1].typ == textToken {
			tokens[n-1].text += t.text
		} else {
			tokens = append(tokens, t)
		}
		tmpl = tmpl[size:]
	}
	return tokens
}

// readTag reads the tag at the start of a template and returns it with its
// length, unterminated tags are returned as text
func readTag(tmpl string) (token, int) {
	switch {
	case strings.HasPrefix(tmpl, rawBlockStart):
		if content, _, ok := strings.Cut(tmpl[len(rawBlockStart):], rawBlockEnd); ok {
			return token{typ: rawBlockToken, text: content}, len(rawBlockStart) + len(content) + len(rawBlockEnd)
		}
	case strings.HasPrefix(tmpl, "{{!--"):
		if i := strings.Index(tmpl, "--}}"); i >= 0 {
			return token{typ: commentToken, text: tmpl[:i+4]}, i + 4
		}
	case strings.HasPrefix(tmpl, "{{{"):
		if i := strings.Index(tmpl, "}}}"); i >= 0 {
			return token{typ: variableToken, text: tmpl[:i+3], name: strings.TrimSpace(tmpl[3:i])}, i + 3
		}
	}

	i := strings.Index(tmpl, "}}")
	if i < 0 {
		return token{typ: textToken, text: tmpl}, len(tmpl)
	}
	t := token{text: tmpl[:i+2]}
	content := strings.TrimSpace(tmpl[2:i])
	switch {
	case strings.HasPrefix(content, "!"):
		t.typ = commentToken
	case strings.HasPrefix(content, "#"), strings.HasPrefix(content, "^"):
		t.typ = openToken
		t.inverted = content[0] == '^'
		if args := splitArgs(content[1:]); len(args) > 0 {
			t.name, t.args = args[0], args[1:]
		}
	case strings.HasPrefix(content, "/"):
		t.typ = closeToken
		t.name = strings.TrimSpace(content[1:])
	case content == "else":
		t.typ = elseToken
	default:
		t.typ = variableToken
		t.name = content
	}
	return t, i + 2
}

// splitArgs splits space separated condition arguments, arguments can be
// quoted with single or double quotes
func splitArgs(s string) []string {
	var args []string
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return args
		}
		if quote := s[0]; quote == '"' || quote == '\'' {
			if end := strings.IndexByte(s[1:], quote); end >= 0 {
				args = append(args, s[1:end+1])
				s = s[end+2:]
				continue
			}
		}
		end := strings.IndexAny(s, " \t\r\n")
		if end < 0 {
			end = len(s)
		}
		args = append(args, s[:end])
		s = s[end:]
	}
}

// pairTags links {{else}} and closing tags to the tag opening their block,
// tags that don't belong to any block are treated as text
func pairTags(tokens []token) {
	var opened []int
	for i := range tokens {
		t := &tokens[i]
		switch {
		case t.typ == openToken:
			opened = append(opened, i)
		case t.typ != elseToken && t.typ != closeToken:
			// text, comments and variables
		case len(opened) == 0, t.typ == closeToken && t.name != tokens[opened[len(opened)-1]].name:
			t.typ = textToken
		default:
			t.open = opened[len(opened)-1]
			if t.typ == closeToken {
				tokens[t.open].closed = true
				opened = opened[:len(opened)-1]
			}
		}
	}
}

// isStandalone returns true if the i-th token is alone on its line, with
// nothing else than whitespace around it
func isStandalone(tokens []token, i int) bool {
	if i > 0 {
		prev := tokens[i-1]
		nl := strings.LastIndexByte(prev.text, '\n')
		if prev.typ != textToken || (nl < 0 && i > 1) || !isBlank(prev.text[nl+1:]) {
			return false
		}
	}
	if i < len(tokens)-1 {
		next := tokens[i+1]
		nl := strings.IndexByte(next.text, '\n')
		if nl < 0 && i < len(tokens)-2 {
			return false
		}
		if nl < 0 {
			nl = len(next.text)
		}
		if next.typ != textToken || !isBlank(next.text[:nl]) {
			return false
		}
	}
	return true
}

func isBlank(s string) bool {
	return strings.Trim(s, " \t\r") == ""
}

// trimStandalone returns the text of the i-th token without the whitespace
// on lines of standalone tags removed from the output
func trimStandalone(tokens []token, i int) string {
	text := tokens[i].text
	start, end := 0, len(text)
	if i > 0 && tokens[i-1].strip {
		start = strings.IndexByte(text, '\n') + 1
		if start == 0 {
			start = len(text)
		}
	}
	if i < len(tokens)-1 && tokens[i+1].strip {
		end = strings.LastIndexByte(text, '\n') + 1
	}
	if start >= end {
		return ""
	}
	return text[start:end]
}

// condition returns the value of a block condition, ok is false if it can't
// be evaluated
func (data *templateData) condition(name string, args []string) (result, ok bool) {
	switch name {
	case "is_alert":
		return data.status == statusAlert, true
	case "is_warning":
		return data.status == statusWarn, true
	case "is_no_data":
		return data.status == statusNoData, true
	case "is_recovery", "is_alert_recovery", "is_warning_recovery", "is_no_data_recovery",
		"is_alert_to_warning", "is_unknown", "is_renotify":
		// alerts are only created for monitor groups in Alert, Warn or
		// No Data state, previous states are unknown
		return false, true
	case "is_priority":
		if len(args) != 1 {
			return false, false
		}
		return data.priority != nil && strings.EqualFold(args[0], "P"+strconv.FormatInt(*data.priority, 10)), true
	case "is_match", "is_exact_match":
		if len(args) < 2 {
			return false, false
		}
		value, found := data.tagValue(args[0])
		if !found {
			return false, false
		}
		match := strings.Contains
		if name == "is_exact_match" {
			match = exactMatch
		}
		return slices.ContainsFunc(args[1:], func(s string) bool { return match(value, s) }), true
	}
	return false, false
}

// variable returns the value of a template variable
func (data *templateData) variable(name string) (string, bool) {
	if value, ok := data.variables[name]; ok {
		return value, true
	}
	if strings.HasSuffix(name, ".name") {
		return data.tagValue(name)
	}
	return "", false
}

// tagValue returns the value of a monitor group tag referenced by a tag
// variable like env.name, keys with periods are wrapped in brackets:
// [kube.namespace].name
func (data *templateData) tagValue(name string) (string, bool) {
	key := strings.TrimSuffix(name, ".name")
	if strings.HasPrefix(key, "[") && strings.HasSuffix(key, "]") {
		key = key[1 : len(key)-1]
	}
	value, ok := data.tags[key]
	return value, ok
}

// exactMatch compares values like is_exact_match, numbers are compared by
// value so "5" matches "5.0"
func exactMatch(value, s string) bool {
	if value == s {
		return true
	}
	a, errA := strconv.ParseFloat(value, 64)
	b, errB := strconv.ParseFloat(s, 64)
	return errA == nil && errB == nil && a == b
}
