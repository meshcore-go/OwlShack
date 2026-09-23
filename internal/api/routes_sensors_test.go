package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type discoverBackend struct {
	Backend
	provider string
}

func (d *discoverBackend) DiscoverSensors(_ context.Context, provider string) (SensorScan, error) {
	d.provider = provider
	return SensorScan{}, nil
}

// A chunked request has no ContentLength, so a length test scans every provider instead of the one asked for.
func TestSensorDiscover_ReadsTheBodyWithoutAContentLength(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		chunked, want bool
		provider      string
	}{
		{name: "chunked", body: `{"provider":"i2c"}`, chunked: true, provider: "i2c"},
		{name: "measured", body: `{"provider":"i2c"}`, provider: "i2c"},
		{name: "empty", body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &discoverBackend{}
			s := &Server{mux: http.NewServeMux()}
			s.routes()
			s.SetBackend(b)

			req := httptest.NewRequest(http.MethodPost, "/api/sensors/discover", strings.NewReader(tc.body))
			if tc.chunked {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if b.provider != tc.provider {
				t.Errorf("scanned provider %q, want %q", b.provider, tc.provider)
			}
		})
	}
}

type createBackend struct {
	Backend
	created int
}

func (c *createBackend) CreateSensor(context.Context, SensorInput) (int64, error) {
	c.created++
	return 1, nil
}

// A body far past any real request is refused before the backend sees it, or a 20 MB name is saved, logged every poll and pushed to every client.
func TestReadJSON_RefusesAnOversizedBody(t *testing.T) {
	b := &createBackend{}
	s := &Server{mux: http.NewServeMux()}
	s.routes()
	s.SetBackend(b)
	post := func(name string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/sensors", strings.NewReader(`{"provider":"i2c","kind":"shtc3","name":"`+name+`"}`))
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post(strings.Repeat("x", 2<<20)); code != http.StatusBadRequest || b.created != 0 {
		t.Fatalf("a 2 MB body got %d and %d creates, want 400 and none", code, b.created)
	}
	if code := post("air"); code != http.StatusOK || b.created != 1 {
		t.Fatalf("an ordinary body got %d and %d creates, so the refusal proves nothing", code, b.created)
	}
}

type mapBackend struct {
	Backend
	sets int
}

func (m *mapBackend) SetTelemetryMap(context.Context, TelemetryNode, []TelemetryMapEntry) error {
	m.sets++
	return nil
}

func (m *mapBackend) TelemetryMap(context.Context) (TelemetryMap, error) { return TelemetryMap{}, nil }

// A body with no entries is a mistake, not a request to clear the map; clearing one says so with [].
func TestSetTelemetryMap_RequiresEntries(t *testing.T) {
	for body, want := range map[string]int{
		`{"node":{"kind":"repeater","id":1}}`:                http.StatusBadRequest,
		`{"node":{"kind":"repeater","id":1},"entries":null}`: http.StatusBadRequest,
		`{"node":{"kind":"repeater","id":1},"entries":[]}`:   http.StatusOK,
	} {
		b := &mapBackend{}
		s := &Server{mux: http.NewServeMux()}
		s.routes()
		s.SetBackend(b)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/sensors/telemetry-map", strings.NewReader(body)))
		if rec.Code != want || (want != http.StatusOK && b.sets != 0) {
			t.Errorf("%s: %d with %d saves, want %d", body, rec.Code, b.sets, want)
		}
	}
}
