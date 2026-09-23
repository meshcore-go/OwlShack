package sensor

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meshcore-go/OwlShack/internal/sensor/pisugar"
)

// fakePiSugar answers like pisugar-server; the protocol itself is exercised in the driver's own tests.
func fakePiSugar(t *testing.T, answers map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pisugar.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on %s: %v", path, err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					reply, ok := answers[strings.TrimSpace(line)]
					if !ok {
						reply = "Invalid request."
					}
					if _, err := c.Write([]byte(reply + "\n")); err != nil {
						return
					}
				}
			}()
		}
	}()
	return path
}

// The mapping is the provider's whole job: a command read then dropped, or named as the wrong metric, publishes a battery as something else or not at all.
func TestPiSugarProvider_NamesEveryReadingItPublishes(t *testing.T) {
	addr := fakePiSugar(t, map[string]string{
		"get model":                 "model: PiSugar 3",
		"get battery_v":             "battery_v: 3.84",
		"get battery":               "battery: 36",
		"get battery_i":             "battery_i: -0.23",
		"get temperature":           "temperature: 31",
		"get battery_charging":      "battery_charging: false",
		"get battery_power_plugged": "battery_power_plugged: true",
	})
	s, err := PiSugarProvider{}.Open(Spec{Options: map[string]string{"address": addr}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	want := []Reading{
		{Metric: Voltage, Value: 3.84, Unit: "V"},
		{Metric: Percentage, Value: 36, Unit: "%"},
		{Metric: Current, Value: -0.23, Unit: "A"},
		{Metric: Temperature, Value: 31, Unit: "°C"},
		{Metric: Charging, Value: 0},
		{Metric: Plugged, Value: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("read %+v, want %d readings", got, len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("reading %d is %+v, want %+v", i, got[i], w)
		}
	}

	// Every metric the picker promises has to be one the provider actually publishes.
	promised := PiSugarProvider{}.Kinds()[0].Metrics
	for _, m := range promised {
		if !hasMetric(got, m) {
			t.Errorf("the catalogue lists %s, which this reading does not carry", m)
		}
	}
}

func hasMetric(rs []Reading, m Metric) bool {
	for _, r := range rs {
		if r.Metric == m {
			return true
		}
	}
	return false
}

func TestPiSugarProvider_RefusesAnAddressItCannotUse(t *testing.T) {
	if err := (PiSugarProvider{}).Validate(Spec{Options: map[string]string{"address": ""}}); err == nil {
		t.Fatal("accepted a spec with no address")
	}
	if err := (PiSugarProvider{}).Validate(Spec{Options: map[string]string{"address": pisugar.DefaultSocket}}); err != nil {
		t.Fatalf("refused the default socket: %v", err)
	}
}

// A host with no PiSugar is not a failed scan, or the picker warns about every part nobody has attached.
func TestPiSugarProvider_AnAbsentHatIsNotAScanFailure(t *testing.T) {
	ok, reason := PiSugarProvider{}.Available(context.Background())
	if !ok {
		t.Fatalf("reported a problem with nothing attached: %s", reason)
	}
	if reason != "" {
		t.Errorf("reason %q, want nothing to report", reason)
	}
}

// A socket we are refused is the other case: we could not look, and that has to stay distinguishable.
func TestPiSugarProvider_ASocketItCannotOpenIsAProblem(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens anything, so this cannot be provoked here")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o000); err != nil {
		t.Fatalf("making an unreadable directory: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	defer func(prev []string) { piSugarAddrs = prev }(piSugarAddrs)
	piSugarAddrs = []string{filepath.Join(dir, "pisugar.sock")}
	if ok, reason := (PiSugarProvider{}).Available(context.Background()); ok || !strings.Contains(reason, "will not accept") {
		t.Fatalf("Available = %v, %q; want a refused socket reported as a problem", ok, reason)
	}

	// Provokes the negative: nothing listening is a scan finding nothing, not a problem.
	piSugarAddrs = []string{filepath.Join(t.TempDir(), "absent.sock")}
	if ok, reason := (PiSugarProvider{}).Available(context.Background()); !ok {
		t.Fatalf("an absent socket made the provider unavailable: %q", reason)
	}
}
