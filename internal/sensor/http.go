package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// KindHTTP reads values out of one web address's reply.
const KindHTTP = "http"

const (
	optURL        = "url"
	optFormat     = "format"
	optValues     = "values"
	optInterval   = "interval"
	optStaleAfter = "stale_after"
	optHeaders    = "headers"
	optAuth       = "auth"
	optUsername   = "username"
	optPassword   = "password"
	optToken      = "token"
	optKeyName    = "key_name"

	fetchTimeout  = 10 * time.Second
	maxReplyBytes = 1 << 20
	minInterval   = time.Minute
	maxInterval   = 24 * time.Hour
	maxStaleAfter = 7 * 24 * time.Hour
)

// headerName is RFC 9110's token, so a header the transport would refuse is refused at save instead.
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// HTTPProvider reads values out of a web address's reply, such as a weather report or a device on the network.
type HTTPProvider struct{}

func (HTTPProvider) ID() string    { return "http" }
func (HTTPProvider) Label() string { return "Web" }

func (HTTPProvider) Available(context.Context) (bool, string) { return true, "" }

// Discover returns nothing: a web sensor is defined, never found.
func (HTTPProvider) Discover(context.Context) ([]Candidate, error) { return nil, nil }

func (HTTPProvider) Kinds() []KindInfo {
	basic := &When{Key: optAuth, Values: []string{"basic"}}
	keyed := &When{Key: optAuth, Values: []string{"header", "query"}}
	return []KindInfo{{
		Kind: KindHTTP, Label: "HTTP",
		Description: "Values read from a web address, such as a weather report or a device on the network",
		Category:    "Online",
		ReportsUnder: func(o map[string]string) []Metric {
			vs, err := parseValues(o[optValues], o[optFormat])
			if err != nil {
				return nil
			}
			out := make([]Metric, 0, len(vs))
			for _, v := range vs {
				out = append(out, v.metric)
			}
			return out
		},
		Fields: []Field{
			{Key: optURL, Label: "Address", Required: true, Identifies: true, Help: "The address to fetch, starting http:// or https://"},
			{Key: optFormat, Label: "Reply format", Choices: []string{"json", "text"}, Default: "json",
				Help: "json reads each value at a path in the reply; text finds each with a pattern"},
			{Key: optValues, Label: "Values", Required: true, Multiline: true,
				Help: "One per line: name, unit, where. For json, where is a path such as current.temperature_2m; for text, a pattern whose first bracketed group is the number. The unit may be empty"},
			{Key: optInterval, Label: "Fetch every", Default: "10m", Help: "From 1m to 24h, such as 10m; many services refuse to be asked more often"},
			{Key: optStaleAfter, Label: "Out of date after", Default: "30m", Help: "How old a value may get before the page and the mesh treat it as out of date; at least the fetch interval"},
			{Key: optHeaders, Label: "Headers", Multiline: true, Help: "Extra request headers, one Name: value per line. Put keys under Authentication, which is kept hidden"},
			{Key: optAuth, Label: "Authentication", Choices: []string{"none", "basic", "bearer", "header", "query"}, Default: "none",
				Help: "basic sends a username and password, bearer a token, header or query a key under a name you give"},
			{Key: optUsername, Label: "Username", Required: true, When: basic},
			{Key: optPassword, Label: "Password", Secret: true, When: basic},
			{Key: optKeyName, Label: "Key name", Required: true, When: keyed, Help: "The header, such as X-Api-Key, or the query parameter, such as appid"},
			{Key: optToken, Label: "Token or key", Required: true, Secret: true, When: &When{Key: optAuth, Values: []string{"bearer", "header", "query"}}},
		},
	}}
}

// Validate parses everything Open will, so a typo is refused once at save rather than every fetch.
func (HTTPProvider) Validate(spec Spec) error {
	if spec.Kind != KindHTTP {
		return fmt.Errorf("unknown web sensor kind %q", spec.Kind)
	}
	_, err := parseHTTP(spec)
	return err
}

func (HTTPProvider) StaleAfter(spec Spec) (time.Duration, bool) {
	cfg, err := parseHTTP(spec)
	if err != nil {
		return 0, false
	}
	return cfg.staleAfter, true
}

func (HTTPProvider) Open(spec Spec) (Sensor, error) {
	cfg, err := parseHTTP(spec)
	if err != nil {
		return nil, err
	}
	return sample(cfg, cfg.interval), nil
}

// httpValue is one line of the values option: what to call a number, its unit, and where in the reply it is.
type httpValue struct {
	metric  Metric
	unit    string
	path    []string
	pattern *regexp.Regexp
}

// httpSource is a parsed web sensor; it is the part a sampledSensor fetches on its clock.
type httpSource struct {
	url        *url.URL
	host       string
	json       bool
	values     []httpValue
	interval   time.Duration
	staleAfter time.Duration
	headers    http.Header
	client     *http.Client
}

func parseHTTP(spec Spec) (*httpSource, error) {
	o := spec.Options
	u, err := url.Parse(strings.TrimSpace(o[optURL]))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the address has to be a full http:// or https:// address")
	}
	// The address is shown on every read of the sensor, and credentials there would be too.
	if u.User != nil {
		return nil, errors.New("take the username and password out of the address and put them under Authentication, which is kept hidden")
	}
	format := o[optFormat]
	values, err := parseValues(o[optValues], format)
	if err != nil {
		return nil, err
	}
	interval, err := parseSpan(o[optInterval], "fetch interval", minInterval, maxInterval)
	if err != nil {
		return nil, err
	}
	staleAfter, err := parseSpan(o[optStaleAfter], "out-of-date time", interval, maxStaleAfter)
	if err != nil {
		return nil, err
	}
	headers, err := parseHeaders(o[optHeaders])
	if err != nil {
		return nil, err
	}

	s := &httpSource{url: u, host: u.Host, json: format != "text", values: values, interval: interval, staleAfter: staleAfter, headers: headers}
	var keyHeader string
	switch o[optAuth] {
	case "", "none":
	case "basic":
		req := http.Request{Header: http.Header{}}
		req.SetBasicAuth(o[optUsername], o[optPassword])
		s.headers.Set("Authorization", req.Header.Get("Authorization"))
	case "bearer":
		s.headers.Set("Authorization", "Bearer "+o[optToken])
	case "header":
		if !headerName.MatchString(o[optKeyName]) {
			return nil, fmt.Errorf("%q is not a header name", o[optKeyName])
		}
		keyHeader = o[optKeyName]
		s.headers.Set(keyHeader, o[optToken])
	case "query":
		name := strings.TrimSpace(o[optKeyName])
		if name == "" || strings.ContainsAny(name, " &=#") {
			return nil, fmt.Errorf("%q is not a query parameter name", o[optKeyName])
		}
		q := u.Query()
		q.Set(name, o[optToken])
		withKey := *u
		withKey.RawQuery = q.Encode()
		s.url = &withKey
	default:
		return nil, fmt.Errorf("%q is not an authentication method", o[optAuth])
	}
	if s.headers.Get("User-Agent") == "" {
		s.headers.Set("User-Agent", "OwlShack")
	}
	s.client = &http.Client{
		Timeout: fetchTimeout,
		// Go drops Authorization on a redirect to another host, but not a key under a header of the operator's naming.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if keyHeader != "" && req.URL.Host != via[0].URL.Host {
				req.Header.Del(keyHeader)
			}
			return nil
		},
	}
	return s, nil
}

// parseValues reads "name, unit, where" lines; where runs to the end of the line, so a pattern may hold commas.
func parseValues(raw, format string) ([]httpValue, error) {
	var out []httpValue
	seen := map[Metric]bool{}
	for n, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 3)
		if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("values line %d needs a name, a unit and where to find it, separated by commas", n+1)
		}
		name := strings.TrimSpace(parts[0])
		if !bindingName.MatchString(name) {
			return nil, fmt.Errorf("values line %d: %q is not a usable name; use letters, digits and underscores, starting with a letter", n+1, name)
		}
		m := Metric(name)
		if seen[m] {
			return nil, fmt.Errorf("two values are both called %s", name)
		}
		seen[m] = true
		v := httpValue{metric: m, unit: strings.TrimSpace(parts[1])}
		where := strings.TrimSpace(parts[2])
		if format == "text" {
			re, err := regexp.Compile(where)
			if err != nil {
				return nil, fmt.Errorf("values line %d: the pattern does not compile: %w", n+1, err)
			}
			if re.NumSubexp() < 1 {
				return nil, fmt.Errorf("values line %d: the pattern needs a bracketed group around the number", n+1)
			}
			v.pattern = re
		} else {
			v.path = strings.Split(where, ".")
			if slices.Contains(v.path, "") {
				return nil, fmt.Errorf("values line %d: %q has an empty step in it", n+1, where)
			}
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errors.New("give at least one value to read")
	}
	return out, nil
}

func parseSpan(raw, what string, least, most time.Duration) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("the %s %q is not a time such as 10m or 1h", what, raw)
	}
	if d < least || d > most {
		return 0, fmt.Errorf("the %s has to be from %s to %s", what, least, most)
	}
	return d, nil
}

func parseHeaders(raw string) (http.Header, error) {
	h := http.Header{}
	for n, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || !headerName.MatchString(name) {
			return nil, fmt.Errorf("headers line %d is not Name: value", n+1)
		}
		h.Add(name, strings.TrimSpace(value))
	}
	return h, nil
}

// Read is one fetch; an error names the host, never the address, which may carry a key in its query.
func (s *httpSource) Read(ctx context.Context) ([]Reading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building the request to %s: %w", s.host, err)
	}
	req.Header = s.headers.Clone()
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("fetching from %s: %w", s.host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s answered %s", s.host, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the reply from %s: %w", s.host, err)
	}
	if len(body) > maxReplyBytes {
		return nil, fmt.Errorf("the reply from %s is over %d KiB, the most this reads", s.host, maxReplyBytes>>10)
	}

	var doc any
	if s.json {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("the reply from %s is not JSON: %w", s.host, err)
		}
	}
	out := make([]Reading, 0, len(s.values))
	for _, v := range s.values {
		var x float64
		var err error
		if s.json {
			x, err = atPath(doc, v.path)
		} else {
			x, err = matchNumber(body, v.pattern)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", v.metric, err)
		}
		out = append(out, Reading{Metric: v.metric, Value: x, Unit: v.unit})
	}
	return out, nil
}

func (s *httpSource) Close() error { return nil }

// atPath walks object keys and array indexes; a number sent as text is taken, anything else is refused rather than guessed.
func atPath(doc any, path []string) (float64, error) {
	cur := doc
	for i, step := range path {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[step]
			if !ok {
				return 0, fmt.Errorf("the reply has no %s", strings.Join(path[:i+1], "."))
			}
			cur = next
		case []any:
			n, err := strconv.Atoi(step)
			if err != nil || n < 0 || n >= len(node) {
				return 0, fmt.Errorf("%s is a list of %d, with no item %q", strings.Join(path[:i], "."), len(node), step)
			}
			cur = node[n]
		default:
			return 0, fmt.Errorf("%s is a single value, with nothing inside it", strings.Join(path[:i], "."))
		}
	}
	where := strings.Join(path, ".")
	switch v := cur.(type) {
	case json.Number:
		return v.Float64()
	case string:
		x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%s is %q, not a number", where, v)
		}
		return x, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case nil:
		return 0, fmt.Errorf("%s is empty", where)
	}
	return 0, fmt.Errorf("%s holds more values, not one number", where)
}

func matchNumber(body []byte, re *regexp.Regexp) (float64, error) {
	m := re.FindSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("the pattern %s matches nothing in the reply", re)
	}
	x, err := strconv.ParseFloat(strings.TrimSpace(string(m[1])), 64)
	if err != nil {
		return 0, fmt.Errorf("the pattern matched %q, not a number", m[1])
	}
	return x, nil
}

var (
	_ Provider  = HTTPProvider{}
	_ Validator = HTTPProvider{}
	_ Staler    = HTTPProvider{}
)
