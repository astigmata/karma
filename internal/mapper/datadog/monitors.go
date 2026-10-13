package datadog

import (
	"cmp"
	"encoding/json/jsontext"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/prymitive/karma/internal/models"
)

// Receiver is the receiver name set on all alerts collected from Datadog
const Receiver = "datadog"

const (
	statusAlert  = "Alert"
	statusWarn   = "Warn"
	statusNoData = "No Data"
)

// monitor group states that are mapped to alerts, with the value of the
// severity label set on those alerts, other states (OK, Ignored, Skipped and
// Unknown) are not alerting
var severities = map[string]string{
	statusAlert:  "critical",
	statusWarn:   "warning",
	statusNoData: "nodata",
}

// silences created from downtimes without an end time never expire
var neverEnds = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

type monitorGroup struct {
	Status          string `json:"status"`
	LastTriggeredTs int64  `json:"last_triggered_ts"`
	LastNodataTs    int64  `json:"last_nodata_ts"`
}

type monitorState struct {
	Groups map[string]monitorGroup `json:"groups"`
}

type downtime struct {
	Start *int64   `json:"start"`
	End   *int64   `json:"end"`
	Scope []string `json:"scope"`
	ID    int64    `json:"id"`
}

type monitorOptions struct {
	Thresholds map[string]jsontext.Value `json:"thresholds"`
}

type monitor struct {
	State             monitorState   `json:"state"`
	Options           monitorOptions `json:"options"`
	Priority          *int64         `json:"priority"`
	Name              string         `json:"name"`
	Message           string         `json:"message"`
	Query             string         `json:"query"`
	OverallState      string         `json:"overall_state"`
	Tags              []string       `json:"tags"`
	MatchingDowntimes []downtime     `json:"matching_downtimes"`
	ID                int64          `json:"id"`
}

// mutingDowntime is a downtime muting at least one alert, with IDs of all
// monitors those alerts belong to
type mutingDowntime struct {
	monitors map[string]struct{}
	downtime downtime
}

func mapMonitors(monitors []monitor, webURI string, now time.Time) ([]models.AlertGroup, []models.Silence) {
	groups := []models.AlertGroup{}
	muting := map[int64]*mutingDowntime{}

	for i := range monitors {
		m := &monitors[i]
		alerts := m.alerts(webURI, now, muting)
		if len(alerts) == 0 {
			continue
		}
		groups = append(groups, models.AlertGroup{
			Receiver: Receiver,
			Labels:   labels.FromStrings("alertname", m.Name, "monitor_id", m.id()),
			Alerts:   alerts,
		})
	}

	silences := make([]models.Silence, 0, len(muting))
	for _, md := range muting {
		silences = append(silences, md.silence())
	}
	slices.SortFunc(silences, func(a, b models.Silence) int {
		return cmp.Compare(a.ID, b.ID)
	})

	return groups, silences
}

func (m *monitor) id() string {
	return strconv.FormatInt(m.ID, 10)
}

func (m *monitor) alerts(webURI string, now time.Time, muting map[int64]*mutingDowntime) models.AlertList {
	// monitors that are not broken down into groups might not report any
	// group, use the overall state for those
	if len(m.State.Groups) == 0 {
		if _, ok := severities[m.OverallState]; ok {
			return models.AlertList{m.alert("", monitorGroup{Status: m.OverallState}, webURI, now, muting)}
		}
		return nil
	}

	alerts := models.AlertList{}
	for _, name := range slices.Sorted(maps.Keys(m.State.Groups)) {
		group := m.State.Groups[name]
		if _, ok := severities[group.Status]; ok {
			alerts = append(alerts, m.alert(name, group, webURI, now, muting))
		}
	}
	return alerts
}

func (m *monitor) alert(groupName string, group monitorGroup, webURI string, now time.Time, muting map[int64]*mutingDowntime) models.Alert {
	monitorID := m.id()
	groupTags := splitTags(groupName)

	// group tags are more specific than monitor tags so they take precedence,
	// labels describing the monitor itself can't be overridden by any tag
	ls := tagsToLabels(m.Tags)
	maps.Copy(ls, tagsToLabels(groupTags))
	ls["alertname"] = m.Name
	ls["monitor_id"] = monitorID
	ls["severity"] = severities[group.Status]
	if m.Priority != nil {
		ls["priority"] = "P" + strconv.FormatInt(*m.Priority, 10)
	}

	alert := models.Alert{
		Fingerprint:  strconv.FormatUint(xxhash.Sum64String(monitorID+"/"+groupName), 16),
		Receiver:     Receiver,
		Annotations:  m.annotations(group, groupTags),
		Labels:       labels.FromMap(ls),
		StartsAt:     group.startsAt(now),
		GeneratorURL: monitorURL(webURI, monitorID, groupTags),
		State:        models.AlertStateActive,
	}

	for i := range m.MatchingDowntimes {
		d := &m.MatchingDowntimes[i]
		if !d.isActive(now) || !d.mutes(groupTags) {
			continue
		}
		alert.SilencedBy = append(alert.SilencedBy, d.silenceID())
		md, ok := muting[d.ID]
		if !ok {
			md = &mutingDowntime{downtime: *d, monitors: map[string]struct{}{}}
			muting[d.ID] = md
		}
		md.monitors[monitorID] = struct{}{}
	}
	if len(alert.SilencedBy) > 0 {
		alert.State = models.AlertStateSuppressed
		slices.Sort(alert.SilencedBy)
		alert.SilencedBy = slices.Compact(alert.SilencedBy)
	}

	alert.UpdateFingerprints()
	return alert
}

func (m *monitor) annotations(group monitorGroup, groupTags []string) models.Annotations {
	annotations := models.Annotations{}
	if message := renderMessage(m.Message, newTemplateData(m, group, groupTags)); message != "" {
		annotations = append(annotations, models.NewAnnotation("message", message))
	}
	if m.Query != "" {
		annotations = append(annotations, models.NewAnnotation("query", m.Query))
	}
	models.SortAnnotations(annotations)
	return annotations
}

// triggeredAt returns the timestamp of the last transition of the group to its
// current state, 0 if it's unknown
func (g monitorGroup) triggeredAt() int64 {
	if g.Status == statusNoData && g.LastNodataTs > 0 {
		return g.LastNodataTs
	}
	return g.LastTriggeredTs
}

func (g monitorGroup) startsAt(now time.Time) time.Time {
	if ts := g.triggeredAt(); ts > 0 {
		return unixTime(ts)
	}
	// no timestamp reported for this group, use the collection time
	return now
}

func (d *downtime) silenceID() string {
	return strconv.FormatInt(d.ID, 10)
}

// isActive returns true if downtime is muting alerts at given time
func (d *downtime) isActive(now time.Time) bool {
	if d.Start != nil && unixTime(*d.Start).After(now) {
		return false
	}
	return d.End == nil || unixTime(*d.End).After(now)
}

// scopes returns the list of tags this downtime is scoped to, an empty list
// means that it's not limited to any scope
func (d *downtime) scopes() []string {
	scopes := make([]string, 0, len(d.Scope))
	for _, scope := range d.Scope {
		scopes = append(scopes, splitTags(scope)...)
	}
	return scopes
}

// mutes returns true if this downtime applies to a monitor group with given
// tags. Datadog only returns downtimes matching the monitor, but those can be
// scoped to some of its groups, so a downtime only mutes groups having all the
// tags it's scoped to. Monitors not broken down into groups have no tags and
// are muted by every matching downtime.
func (d *downtime) mutes(groupTags []string) bool {
	if len(groupTags) == 0 {
		return true
	}
	for _, scope := range d.scopes() {
		if !slices.Contains(groupTags, scope) {
			return false
		}
	}
	return true
}

func (md *mutingDowntime) silence() models.Silence {
	s := models.Silence{
		ID:        md.downtime.silenceID(),
		EndsAt:    neverEnds,
		CreatedBy: Receiver,
		Comment:   "Datadog downtime",
	}
	if md.downtime.Start != nil {
		s.StartsAt = unixTime(*md.downtime.Start)
	}
	if md.downtime.End != nil {
		s.EndsAt = unixTime(*md.downtime.End)
	}

	monitorIDs := slices.Sorted(maps.Keys(md.monitors))
	if len(monitorIDs) == 1 {
		s.Matchers = append(s.Matchers, models.NewSilenceMatcher("monitor_id", monitorIDs[0], false, true))
	} else {
		s.Matchers = append(s.Matchers, models.NewSilenceMatcher("monitor_id", strings.Join(monitorIDs, "|"), true, true))
	}
	for _, scope := range md.downtime.scopes() {
		if key, value, ok := strings.Cut(scope, ":"); ok && key != "" && value != "" {
			s.Matchers = append(s.Matchers, models.NewSilenceMatcher(labelName(key), value, false, true))
		}
	}

	return s
}

// monitorURL returns a link to the monitor status page, focused on given group
func monitorURL(webURI, monitorID string, groupTags []string) string {
	uri := monitorsURI(webURI) + monitorID
	if len(groupTags) > 0 {
		uri += "?" + url.Values{"group": {strings.Join(groupTags, ",")}}.Encode()
	}
	return uri
}

// splitTags splits comma separated tags, like monitor group names
// ("host:foo,env:prod"), "*" is skipped since it's used for monitors that
// are not broken down into groups and for downtimes that are not scoped
func splitTags(s string) []string {
	tags := make([]string, 0, strings.Count(s, ",")+1)
	for tag := range strings.SplitSeq(s, ",") {
		if tag = strings.TrimSpace(tag); tag != "" && tag != "*" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// tagsToLabels converts key:value tags to labels
func tagsToLabels(tags []string) map[string]string {
	return tagValues(tags, labelName)
}

// tagValues returns values of key:value tags by key, converted with keyFunc.
// Tags without a value are skipped and values of all tags with the same key
// are sorted and joined with ",".
func tagValues(tags []string, keyFunc func(string) string) map[string]string {
	values := map[string][]string{}
	for _, tag := range tags {
		key, value, ok := strings.Cut(tag, ":")
		if !ok || key == "" || value == "" {
			continue
		}
		key = keyFunc(key)
		if !slices.Contains(values[key], value) {
			values[key] = append(values[key], value)
		}
	}

	joined := make(map[string]string, len(values))
	for key, vals := range values {
		slices.Sort(vals)
		joined[key] = strings.Join(vals, ",")
	}
	return joined
}

// labelName converts tag key to a label name that can be used in karma
// filters, characters that are not allowed are replaced with _
func labelName(key string) string {
	name := strings.Map(func(r rune) rune {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, key)
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name
}

// unixTime converts POSIX timestamp returned by Datadog API to time.Time
func unixTime(ts int64) time.Time {
	return time.Unix(ts, 0).UTC()
}
