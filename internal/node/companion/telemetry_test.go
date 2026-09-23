package companion

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"

	"github.com/meshcore-go/OwlShack/internal/config"
	"github.com/meshcore-go/OwlShack/internal/modem"
	"github.com/meshcore-go/OwlShack/internal/sensor"
	"github.com/meshcore-go/OwlShack/internal/store"
)

func mode(m string) *string { return &m }

// oneRow is a channel map of a single reading, with the sensor behind it already read.
func oneRow(ch, typ byte, metric sensor.Metric, v float64) func() ([]sensor.ChannelEntry, []sensor.Status) {
	return func() ([]sensor.ChannelEntry, []sensor.Status) {
		return []sensor.ChannelEntry{{Channel: ch, Type: typ, SensorID: 1, Metric: metric}},
			[]sensor.Status{{
				Spec:     sensor.Spec{ID: 1},
				Readings: []sensor.Reading{{Metric: metric, Value: v}},
				At:       time.Now(),
			}}
	}
}

func telemetryCompanion(t *testing.T, cfg config.CompanionConfig, hook func() ([]sensor.ChannelEntry, []sensor.Status)) *Companion {
	t.Helper()
	return &Companion{
		cfg:       cfg,
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		stats:     fakeStats{device: modem.DeviceStats{BatteryMV: 4000, HaveBattery: true}},
		telemetry: hook,
	}
}

var directRequest = &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeReq, 0)}

func replyHex(t *testing.T, c *Companion, granted, mask byte) (string, bool) {
	t.Helper()
	body, ok := c.telemetryReply(granted, mask, directRequest)
	return strings.ToUpper(hex.EncodeToString(body)), ok
}

// Every class defaults to deny, so a companion that has never been configured says nothing at all.
func TestTelemetry_SilentUntilAnOperatorAllowsIt(t *testing.T) {
	c := telemetryCompanion(t, config.CompanionConfig{}, oneRow(2, meshcore.LPPTemperature, sensor.Temperature, 22.5))
	if _, ok := replyHex(t, c, sensor.PermAll, 0); ok {
		t.Fatal("a companion with no telemetry modes set answered a request")
	}

	// Provokes the negative above: the same request is answered once base is allowed.
	c.cfg.TelemetryBase = mode(config.TelemetryContacts)
	if _, ok := replyHex(t, c, sensor.PermAll, 0); !ok {
		t.Fatal("allowing the base class did not make the node answer, so the silence proves nothing")
	}
}

// Base gates the whole reply in the firmware, so sensors alone can never be published.
func TestTelemetry_BaseGatesEveryClass(t *testing.T) {
	c := telemetryCompanion(t, config.CompanionConfig{
		TelemetryEnvironment: mode(config.TelemetryContacts),
		TelemetryLocation:    mode(config.TelemetryContacts),
	}, oneRow(2, meshcore.LPPTemperature, sensor.Temperature, 22.5))

	if _, ok := replyHex(t, c, sensor.PermAll, 0); ok {
		t.Fatal("sensors were published to a requester the base class denies")
	}
}

func TestTelemetry_ClassModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     config.CompanionConfig
		granted byte
		mask    byte
		want    string
	}{
		{
			name: "every contact reads what is allowed",
			cfg: config.CompanionConfig{
				TelemetryBase:        mode(config.TelemetryContacts),
				TelemetryEnvironment: mode(config.TelemetryContacts),
			},
			want: "01740190026700E1",
		},
		{
			name: "a selected class needs this contact's grant",
			cfg: config.CompanionConfig{
				TelemetryBase:        mode(config.TelemetryContacts),
				TelemetryEnvironment: mode(config.TelemetrySelected),
			},
			want: "01740190", // the map stays behind
		},
		{
			name: "the grant unlocks the selected class",
			cfg: config.CompanionConfig{
				TelemetryBase:        mode(config.TelemetryContacts),
				TelemetryEnvironment: mode(config.TelemetrySelected),
			},
			granted: sensor.PermEnvironment,
			want:    "01740190026700E1",
		},
		{
			name: "the requester's inverse mask narrows it further",
			cfg: config.CompanionConfig{
				TelemetryBase:        mode(config.TelemetryContacts),
				TelemetryEnvironment: mode(config.TelemetryContacts),
			},
			mask: sensor.PermEnvironment,
			want: "01740190",
		},
		{
			name: "an unrecognised mode denies rather than grants",
			cfg: config.CompanionConfig{
				TelemetryBase: mode("allow-all"),
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := telemetryCompanion(t, tc.cfg, oneRow(2, meshcore.LPPTemperature, sensor.Temperature, 22.5))
			got, ok := replyHex(t, c, tc.granted, tc.mask)
			if tc.want == "" {
				if ok {
					t.Fatalf("answered %s, want silence", got)
				}
				return
			}
			if !ok {
				t.Fatal("the node refused to answer")
			}
			if got != tc.want {
				t.Errorf("reply %s, want %s", got, tc.want)
			}
		})
	}
}

// An oversized reply is refused on the way out, which the requester cannot tell from a node that is off the air.
func TestTelemetry_OversizedMapFallsBackToTheNodesOwnChannel(t *testing.T) {
	c := telemetryCompanion(t, config.CompanionConfig{
		TelemetryBase:        mode(config.TelemetryContacts),
		TelemetryEnvironment: mode(config.TelemetryContacts),
	}, func() ([]sensor.ChannelEntry, []sensor.Status) {
		var entries []sensor.ChannelEntry
		for ch := byte(2); ch < 60; ch++ {
			entries = append(entries, sensor.ChannelEntry{
				Channel: ch, Type: meshcore.LPPTemperature, SensorID: 1, Metric: sensor.Temperature,
			})
		}
		return entries, []sensor.Status{{
			Spec:     sensor.Spec{ID: 1},
			Readings: []sensor.Reading{{Metric: sensor.Temperature, Value: 22.5}},
			At:       time.Now(),
		}}
	})
	got, ok := replyHex(t, c, sensor.PermAll, 0)
	if !ok {
		t.Fatal("the node refused to answer")
	}
	if got != "01740190" {
		t.Fatalf("reply %s, want the node's own channel only", got)
	}
}

// Position is its own class, and it rides on the self channel like the firmware's.
func TestTelemetry_LocationClass(t *testing.T) {
	lat, lon := -41.2865, 174.7762
	cfg := config.CompanionConfig{
		TelemetryBase: mode(config.TelemetryContacts),
		Latitude:      &lat, Longitude: &lon,
	}
	c := telemetryCompanion(t, cfg, nil)

	if got, _ := replyHex(t, c, sensor.PermAll, 0); strings.Contains(got, "0188") {
		t.Fatalf("reply %s carries a position the location class denies", got)
	}

	c.cfg.TelemetryLocation = mode(config.TelemetryContacts)
	body, ok := c.telemetryReply(sensor.PermAll, 0, directRequest)
	if !ok {
		t.Fatal("the node refused to answer")
	}
	readings, err := meshcore.LPPDecode(body)
	if err != nil {
		t.Fatalf("decoding the reply: %v", err)
	}
	var gps *meshcore.LPPGPSValue
	for _, r := range readings {
		if v, isGPS := r.Value.(meshcore.LPPGPSValue); isGPS {
			if r.Channel != sensor.ChannelSelf {
				t.Errorf("position on channel %d, want the self channel", r.Channel)
			}
			gps = &v
		}
	}
	if gps == nil {
		t.Fatal("the reply carries no position")
	}
	// 0.0001 degrees is the type's own resolution.
	if math.Abs(gps.Latitude-lat) > 0.0002 || math.Abs(gps.Longitude-lon) > 0.0002 {
		t.Errorf("position %v, %v, want %v, %v", gps.Latitude, gps.Longitude, lat, lon)
	}
}

// The editor's budget has to hold on the companion's own reply path, position included, or a saved map goes out as channel 1 alone.
func TestTelemetry_AMapAtTheBudgetReachesTheRequester(t *testing.T) {
	lat, lon := -41.2865, 174.7762
	c := telemetryCompanion(t, config.CompanionConfig{
		TelemetryBase:        mode(config.TelemetryContacts),
		TelemetryLocation:    mode(config.TelemetryContacts),
		TelemetryEnvironment: mode(config.TelemetryContacts),
		Latitude:             &lat, Longitude: &lon,
	}, func() ([]sensor.ChannelEntry, []sensor.Status) {
		var entries []sensor.ChannelEntry
		for ch := 2; ch <= 24; ch++ {
			typ := meshcore.LPPGenericSensor
			if ch > 22 {
				typ = meshcore.LPPTemperature
			}
			entries = append(entries, sensor.ChannelEntry{Channel: byte(ch), Type: typ, SensorID: 1, Metric: sensor.Temperature})
		}
		_, sts := oneRow(2, meshcore.LPPTemperature, sensor.Temperature, 22.5)()
		return entries, sts
	})
	c.stats = fakeStats{device: modem.DeviceStats{BatteryMV: 4000, HaveBattery: true, MCUTempC: 21.5, HaveMCUTemp: true}}

	body, ok := c.telemetryReply(sensor.PermAll, 0, directRequest)
	if !ok {
		t.Fatal("the node refused to answer")
	}
	if want := sensor.MaxTelemetryPayload + 19; len(body) != want {
		t.Fatalf("reply is %d bytes, want %d: the map at the budget beside the node's own 19", len(body), want)
	}
}

// recordingRadio is a TxRadio, so node.New sends straight to it and a test reads what went out.
type recordingRadio struct {
	mu   sync.Mutex
	sent [][]byte
}

func (r *recordingRadio) SendData(d []byte) error                             { r.record(d); return nil }
func (r *recordingRadio) SetDataHandler(func(*meshcore.Packet))               {}
func (r *recordingRadio) SetRawDataHandler(func([]byte, float32, int8, bool)) {}
func (r *recordingRadio) AddOutboundHandler(func([]byte))                     {}
func (r *recordingRadio) Close() error                                        { return nil }
func (r *recordingRadio) Enqueue(d []byte, _ uint8, _ time.Duration) bool     { r.record(d); return true }
func (r *recordingRadio) TxQueueLen() int                                     { return 0 }

func (r *recordingRadio) record(d []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, bytes.Clone(d))
}

func (r *recordingRadio) take() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.sent
	r.sent = nil
	return out
}

// handleReq as the air sees it: an encrypted request in, the reply decrypted as the contact would.
func TestHandleReq_AnswersAContactAndNotAStranger(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	self := meshcore.NewLocalIdentityFromSeed([32]byte{1})
	friend := meshcore.NewLocalIdentityFromSeed([32]byte{2})
	stranger := meshcore.NewLocalIdentityFromSeed([32]byte{3})
	comp := store.Companion{Name: "home"}
	st.WriteSync(func() {
		if err = st.Companions.Create(ctx, &comp); err == nil {
			err = st.Contacts.Add(ctx, comp.ID, friend.PublicKeyBytes(), "friend", "CHAT")
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	radio := &recordingRadio{}
	n := node.New(self, radio)
	t.Cleanup(n.Stop)
	// Heard advertising, so only the contact gate keeps the stranger unanswered.
	n.Peers().Insert(&node.Peer{Identity: stranger.Identity, Name: "stranger"})
	c := telemetryCompanion(t, config.CompanionConfig{
		ID:                   comp.ID,
		TelemetryBase:        mode(config.TelemetryContacts),
		TelemetryEnvironment: mode(config.TelemetryContacts),
	}, oneRow(2, meshcore.LPPTemperature, sensor.Temperature, 22.5))
	c.node, c.store, c.runCtx = n, st, ctx

	reply := func(from meshcore.LocalIdentity) (string, bool) {
		t.Helper()
		secret, _ := from.SharedSecret(self.Identity)
		plain := []byte{7, 0, 0, 0, reqTypeGetTelemetryData, 0}
		enc, err := meshcore.EncryptThenMAC(secret, plain)
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := (&meshcore.Request{
			Destination: self.PublicKey()[0], Source: from.PublicKey()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:],
		}).ToBytes()
		c.handleReq(&meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeReq, 0), Payload: payload})

		sent := radio.take()
		if len(sent) == 0 {
			return "", false
		}
		pkt, err := meshcore.PacketFromBytes(sent[0])
		if err != nil {
			t.Fatal(err)
		}
		resp, err := meshcore.ResponseFromBytes(pkt.Payload)
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Decrypt(secret)
		if len(got) < 4 || binary.LittleEndian.Uint32(got) != 7 {
			t.Fatalf("the reply %x does not reflect the request's tag", got)
		}
		return strings.ToUpper(hex.EncodeToString(bytes.TrimRight(got[4:], "\x00"))), true
	}

	if got, ok := reply(friend); !ok || got != "01740190026700E1" {
		t.Errorf("the contact got %q (answered %v), want the battery and the map", got, ok)
	}
	if _, ok := reply(stranger); ok {
		t.Error("answered a stranger whose shared secret is no credential")
	}
}

// A grant opens only the classes set to "selected", or granting both lets a contact read a class the operator denied.
func TestTelemetry_AGrantCannotOpenADeniedClass(t *testing.T) {
	lat, lon := -41.2865, 174.7762
	c := telemetryCompanion(t, config.CompanionConfig{
		TelemetryBase:        mode(config.TelemetryContacts),
		TelemetryLocation:    mode(config.TelemetryDeny),
		TelemetryEnvironment: mode(config.TelemetrySelected),
		Latitude:             &lat, Longitude: &lon,
	}, oneRow(2, meshcore.LPPTemperature, sensor.Temperature, 22.5))

	got, _ := replyHex(t, c, sensor.PermLocation|sensor.PermEnvironment, 0)
	if strings.Contains(got, "0188") {
		t.Errorf("reply %s carries a position the operator denied", got)
	}
	if !strings.Contains(got, "026700E1") {
		t.Errorf("reply %s lacks the map the grant did open, so the test proves nothing", got)
	}
}
