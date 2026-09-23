package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
