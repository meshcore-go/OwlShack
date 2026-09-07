package api

import (
	"net/http"
	"testing"
)

func TestSameHostOrigin(t *testing.T) {
	for _, c := range []struct {
		origin, host string
		want         bool
	}{
		{"", "owl.local:8080", true}, // non-browser client
		{"http://owl.local:8080", "owl.local:8080", true},
		{"http://OWL.local:8080", "owl.local:8080", true},
		{"http://192.168.1.5:8080", "192.168.1.5:8080", true},
		{"http://evil.example", "owl.local:8080", false},
		{"http://owl.local:9999", "owl.local:8080", false},
		{"null", "owl.local:8080", false},
	} {
		r := &http.Request{Host: c.host, Header: http.Header{}}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := sameHostOrigin(r); got != c.want {
			t.Errorf("origin %q host %q = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}
