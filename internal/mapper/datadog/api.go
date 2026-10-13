// Package datadog collects triggered monitors from the Datadog API and maps
// them to karma alert groups, downtimes muting those monitors are mapped to
// silences.
package datadog

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	json "encoding/json/v2"

	"github.com/prymitive/karma/internal/mapper"
	"github.com/prymitive/karma/internal/models"
)

// number of monitors requested per page
const pageSize = 1000

// Collect fetches all monitors from the Datadog API and maps every monitor
// group in Alert, Warn or No Data state to an alert. Active downtimes muting
// any of those alerts are returned as silences.
// webURI is the base URI of the Datadog web UI, used for links to monitors.
func Collect(uri, webURI string, headers map[string]string, timeout time.Duration, httpTransport http.RoundTripper) ([]models.AlertGroup, []models.Silence, error) {
	c, u, err := newHTTPClient(uri, headers, httpTransport)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	monitors, err := listMonitors(ctx, c, u)
	if err != nil {
		return nil, nil, err
	}

	groups, silences := mapMonitors(monitors, webURI, time.Now())
	return groups, silences, nil
}

// WebURI returns the base URI of the Datadog web UI for given API URI.
// Datadog sites serve the API from api.<site> and the web UI either from
// app.<site> (datadoghq.com, datadoghq.eu, ddog-gov.com) or from <site>
// (us3.datadoghq.com, us5.datadoghq.com, ap1.datadoghq.com).
// For any other host only the path of given URI is removed.
func WebURI(apiURI string) string {
	u, err := url.Parse(apiURI)
	if err != nil || u.Host == "" {
		return apiURI
	}

	host := u.Host
	if site, ok := strings.CutPrefix(host, "api."); ok {
		if strings.Count(site, ".") == 1 {
			host = "app." + site
		} else {
			host = site
		}
	}

	return (&url.URL{Scheme: u.Scheme, Host: host}).String()
}

// IsMonitorURI returns true if uri is a link to a monitor generated for
// alerts collected from Datadog with given web UI base URI.
func IsMonitorURI(webURI, uri string) bool {
	return strings.HasPrefix(uri, monitorsURI(webURI))
}

func monitorsURI(webURI string) string {
	return strings.TrimSuffix(webURI, "/") + "/monitors/"
}

func newHTTPClient(uri string, headers map[string]string, httpTransport http.RoundTripper) (*http.Client, *url.URL, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, nil, err
	}

	transport := http.DefaultTransport
	if httpTransport != nil {
		transport = httpTransport
	}
	transport = mapper.SetHeaders(transport, headers)

	if u.User.Username() != "" {
		username := u.User.Username()
		password, _ := u.User.Password()
		transport = mapper.SetAuth(transport, username, password)
	}

	return &http.Client{Transport: transport}, u, nil
}

func listMonitors(ctx context.Context, client *http.Client, baseURL *url.URL) ([]monitor, error) {
	var monitors []monitor
	for page := 0; ; page++ {
		batch, err := getMonitors(ctx, client, monitorsURL(baseURL, page))
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, batch...)
		// a partial page means that there's nothing more to fetch, getting
		// more monitors than requested means that pagination was ignored
		if len(batch) != pageSize {
			return monitors, nil
		}
	}
}

func monitorsURL(baseURL *url.URL, page int) string {
	u := *baseURL
	u.Path = path.Join(u.Path, "api/v1/monitor")
	u.User = nil

	// keep query parameters set on the configured URI, they can be used to
	// filter monitors, for example with ?monitor_tags=team:sre
	q := u.Query()
	q.Set("group_states", "alert,warn,no data")
	q.Set("with_downtimes", "true")
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	u.RawQuery = q.Encode()

	return u.String()
}

func getMonitors(ctx context.Context, client *http.Client, uri string) ([]monitor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", uri, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request to %s returned status %d", uri, resp.StatusCode)
	}

	var monitors []monitor
	if err := json.UnmarshalRead(resp.Body, &monitors); err != nil {
		return nil, fmt.Errorf("failed to decode monitors: %w", err)
	}
	return monitors, nil
}
