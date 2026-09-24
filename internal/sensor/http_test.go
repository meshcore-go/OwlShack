package sensor

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func httpSpec(url string, extra map[string]string) Spec {
	o := map[string]string{
		optURL: url, optFormat: "json", optInterval: "1m", optStaleAfter: "5m", optAuth: "none",
		optValues: "temperature, °C, current.temperature_2m\nwind, km/h, current.wind.0",
	}
	for k, v := range extra {
		o[k] = v
	}
	return Spec{ID: 1, Provider: "http", Kind: KindHTTP, Name: "weather", Options: o}
}

func fetch(t *testing.T, spec Spec) ([]Reading, error) {
	t.Helper()
	src, err := parseHTTP(spec)
	if err != nil {
		t.Fatalf("parseHTTP: %v", err)
	}
	return src.Read(t.Context())
}

func TestHTTP_ReadsEachJSONValueAtItsPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"current":{"temperature_2m":14.2,"wind":["7.5",1]}}`)
	}))
	defer srv.Close()

	got, err := fetch(t, httpSpec(srv.URL, nil))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []Reading{{Metric: "temperature", Value: 14.2, Unit: "°C"}, {Metric: "wind", Value: 7.5, Unit: "km/h"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("read %+v, want %+v", got, want)
	}
}

func TestHTTP_ReadsATextValueByItsPattern(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Temp: 21.5 C, humidity 40, pressure 1013")
	}))
	defer srv.Close()

	got, err := fetch(t, httpSpec(srv.URL, map[string]string{
		optFormat: "text", optValues: `humidity, %, humidity (\d+)` + "\n" + `temperature, °C, Temp: (-?[\d.]+) C`,
	}))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 || got[0].Value != 40 || got[1].Value != 21.5 {
		t.Errorf("read %+v, want humidity 40 and temperature 21.5", got)
	}
}

// A wrong number that looks right is worse than none, so every shape but a number is refused, and says where.
func TestHTTP_RefusesWhatIsNotANumber(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"missing", `{"current":{}}`, "has no current.temperature_2m"},
		{"text", `{"current":{"temperature_2m":"warm","wind":[1]}}`, `is "warm", not a number`},
		{"an object", `{"current":{"temperature_2m":{"v":1},"wind":[1]}}`, "holds more values"},
		{"empty", `{"current":{"temperature_2m":null,"wind":[1]}}`, "is empty"},
		{"past a list", `{"current":{"temperature_2m":1,"wind":[]}}`, "a list of 0"},
		{"not json", `<html>`, "is not JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
			defer srv.Close()
			if _, err := fetch(t, httpSpec(srv.URL, nil)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one saying %q", err, tc.want)
			}
		})
	}
}

func TestHTTP_SendsEachAuthMethodAndTheHeaders(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		io.WriteString(w, `{"current":{"temperature_2m":1,"wind":[1]}}`)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name  string
		extra map[string]string
		check func(r *http.Request) bool
	}{
		{"basic", map[string]string{optAuth: "basic", optUsername: "owl", optPassword: "hoot"},
			func(r *http.Request) bool { u, p, ok := r.BasicAuth(); return ok && u == "owl" && p == "hoot" }},
		{"bearer", map[string]string{optAuth: "bearer", optToken: "t0k"},
			func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer t0k" }},
		{"header", map[string]string{optAuth: "header", optKeyName: "X-Api-Key", optToken: "k3y"},
			func(r *http.Request) bool { return r.Header.Get("X-Api-Key") == "k3y" }},
		{"query", map[string]string{optAuth: "query", optKeyName: "appid", optToken: "k3y"},
			func(r *http.Request) bool { return r.URL.Query().Get("appid") == "k3y" }},
		{"headers", map[string]string{optHeaders: "Accept: application/json\nX-Units: metric"},
			func(r *http.Request) bool {
				return r.Header.Get("Accept") == "application/json" && r.Header.Get("X-Units") == "metric" && r.Header.Get("User-Agent") == "OwlShack"
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = nil
			if _, err := fetch(t, httpSpec(srv.URL, tc.extra)); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got == nil || !tc.check(got) {
				t.Errorf("the server was not sent the %s credentials: %v %v", tc.name, got.Header, got.URL)
			}
		})
	}
}

// The address in an error is shown on the page and logged, and a query key would ride along in it.
func TestHTTP_AnErrorNamesTheHostButNeverTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	url := srv.URL
	spec := httpSpec(url, map[string]string{optAuth: "query", optKeyName: "appid", optToken: "s3cret"})
	_, err := fetch(t, spec)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error %v, want the 401", err)
	}
	srv.Close()
	_, err = fetch(t, spec)
	if err == nil {
		t.Fatal("a fetch from a closed server worked")
	}
	if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "appid") {
		t.Errorf("the error carries the key: %v", err)
	}
	if !strings.Contains(err.Error(), strings.TrimPrefix(url, "http://")) {
		t.Errorf("the error does not name the host: %v", err)
	}
}

func TestHTTP_RefusesAReplyOverTheCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat(" ", maxReplyBytes+1))
	}))
	defer srv.Close()
	if _, err := fetch(t, httpSpec(srv.URL, nil)); err == nil || !strings.Contains(err.Error(), "KiB") {
		t.Errorf("error %v, want the reply refused as too big", err)
	}
}

// Each is refused at save, so it fails once in the form rather than on every fetch.
func TestHTTP_ValidateRefusesWhatWouldNeverFetch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]string
	}{
		{"no scheme", map[string]string{optURL: "api.example.com/x"}},
		{"ftp", map[string]string{optURL: "ftp://example.com/x"}},
		{"credentials in the address", map[string]string{optURL: "https://owl:hoot@example.com/x"}},
		{"no values", map[string]string{optValues: " \n "}},
		{"a line short", map[string]string{optValues: "temperature, °C"}},
		{"a bad name", map[string]string{optValues: "2hot, °C, a.b"}},
		{"a name twice", map[string]string{optValues: "t, °C, a\nt, °C, b"}},
		{"an empty step", map[string]string{optValues: "t, °C, a..b"}},
		{"a pattern with no group", map[string]string{optFormat: "text", optValues: `t, °C, \d+`}},
		{"a broken pattern", map[string]string{optFormat: "text", optValues: `t, °C, (\d+`}},
		{"too often", map[string]string{optInterval: "30s"}},
		{"not a time", map[string]string{optInterval: "often"}},
		{"stale before the next fetch", map[string]string{optInterval: "10m", optStaleAfter: "5m"}},
		{"a bad header", map[string]string{optHeaders: "Not a header"}},
		{"a bad key header", map[string]string{optAuth: "header", optKeyName: "X Api", optToken: "k"}},
		{"a bad query name", map[string]string{optAuth: "query", optKeyName: "a=b", optToken: "k"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (HTTPProvider{}).Validate(httpSpec("https://example.com/w", tc.extra)); err == nil {
				t.Errorf("accepted %s", tc.name)
			}
		})
	}
	if err := (HTTPProvider{}).Validate(httpSpec("https://example.com/w", nil)); err != nil {
		t.Errorf("a sound spec was refused: %v", err)
	}
}

// Switching the auth method must not leave the old password stored, where nothing shows it and a backup keeps it.
func TestPrepare_DropsAFieldItsConditionHides(t *testing.T) {
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPProvider{})
	spec := httpSpec("https://example.com/w", map[string]string{optAuth: "bearer", optToken: "t", optPassword: "left over", optUsername: "owl"})
	got, err := h.Prepare(spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, k := range []string{optPassword, optUsername, optKeyName} {
		if _, ok := got.Options[k]; ok {
			t.Errorf("%s was kept under bearer auth, which does not use it", k)
		}
	}
	if got.Options[optToken] != "t" {
		t.Error("the token bearer auth uses was dropped")
	}
	// Required only while shown: basic needs a username, bearer does not.
	if _, err := h.Prepare(httpSpec("https://example.com/w", map[string]string{optAuth: "basic"})); err == nil {
		t.Error("basic auth with no username was accepted")
	}
}

func TestHTTP_ItsFieldsOnlyDependOnEarlierOnes(t *testing.T) {
	fields := HTTPProvider{}.Kinds()[0].Fields
	for i, f := range fields {
		if f.When == nil {
			continue
		}
		earlier := false
		for _, g := range fields[:i] {
			earlier = earlier || g.Key == f.When.Key
		}
		if !earlier {
			t.Errorf("%s depends on %s, which is not an earlier field, so Prepare would test it before its default", f.Key, f.When.Key)
		}
	}
}

func TestHTTP_StaleAfterIsTheSensorsOwn(t *testing.T) {
	d, ok := HTTPProvider{}.StaleAfter(httpSpec("https://example.com/w", map[string]string{optStaleAfter: "45m"}))
	if !ok || d != 45*time.Minute {
		t.Errorf("stale after %s %v, want 45m", d, ok)
	}
}

// A slow server must not hold up the pass that opened the sensor.
func TestHTTP_OpenReturnsBeforeTheFirstFetchLands(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		io.WriteString(w, `{"current":{"temperature_2m":1,"wind":[1]}}`)
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	s, err := HTTPProvider{}.Open(httpSpec(srv.URL, nil))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.(io.Closer).Close()
	if time.Since(start) > time.Second {
		t.Errorf("Open took %s, waiting on the fetch", time.Since(start))
	}
	if got, err := s.Read(t.Context()); err != nil || got != nil {
		t.Errorf("before the first fetch Read gave %v, %v; want nothing yet", got, err)
	}
	if at := s.(sampler).SampledAt(); !at.IsZero() {
		t.Errorf("sampled at %s before any fetch", at)
	}
}

// Stamped with the read's own time, a ten-minute-old fetch would look fresh on every pass and never go stale.
func TestHub_AgesASampledSensorBySample(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"current":{"temperature_2m":1,"wind":[1]}}`)
	}))
	defer srv.Close()
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPProvider{})
	defer h.Close()
	h.Set([]Spec{httpSpec(srv.URL, nil)})

	h.pass(t.Context(), false)
	h.mu.RLock()
	s := h.entries[1].sensor
	h.mu.RUnlock()
	for deadline := time.Now().Add(2 * time.Second); s.(sampler).SampledAt().IsZero(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no sample within 2s")
		}
	}
	sampled := s.(sampler).SampledAt()
	time.Sleep(50 * time.Millisecond)
	h.pass(t.Context(), false)

	st := h.Snapshot()[0]
	if !st.At.Equal(sampled) {
		t.Errorf("status stamped %s, want the sample's own %s", st.At.Format(time.StampMicro), sampled.Format(time.StampMicro))
	}
	if st.StaleAfter != 5*time.Minute {
		t.Errorf("stale after %s, want the sensor's 5m", st.StaleAfter)
	}
}

// Before its first sample a sensor is waiting, not read at the moment of the pass with nothing in it.
func TestHub_ASampledSensorWaitsForItsFirstSample(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	h := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPProvider{})
	defer h.Close()
	h.Set([]Spec{httpSpec(srv.URL, nil)})
	h.pass(t.Context(), false)
	if st := h.Snapshot()[0]; !st.At.IsZero() || st.Err != "" {
		t.Errorf("before its first sample it reads at %s with error %q, want never read", st.At, st.Err)
	}
}
