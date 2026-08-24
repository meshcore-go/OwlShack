package repeater

import (
	"context"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/meshcore-go/meshcore-bot/internal/config"
	"github.com/meshcore-go/meshcore-bot/internal/node/advert"
)

// bootAdvertDelay matches the firmware's sendSelfAdvertisement(16000, false) in
// setup(): the boot advert is held ~16s to let the radio/system settle.
const bootAdvertDelay = 16 * time.Second

// advertLoop runs the two independent advert schedules (firmware
// updateAdvertTimer / updateFloodAdvertTimer): a zero-hop (local, direct-only)
// advert on AdvertInterval and a mesh-wide flood advert on FloodAdvertInterval.
// Either interval of 0 disables that schedule. On boot it sends ONE zero-hop
// advert after bootAdvertDelay (firmware setup() — announce to direct
// neighbours, deliberately NOT flooding the whole mesh on every restart).
// Effective values (incl. defaults — zero-hop off, flood 47h) come from the
// config *Or accessors.
func (r *Repeater) advertLoop(ctx context.Context) {
	localSecs := r.cfg.AdvertIntervalOr()
	floodSecs := r.cfg.FloodAdvertIntervalOr()

	// One-shot boot advert (zero-hop, delayed).
	boot := time.NewTimer(bootAdvertDelay)
	defer boot.Stop()

	// A nil channel blocks forever in select, which cleanly disables a
	// schedule whose interval is 0.
	var localC, floodC <-chan time.Time
	if localSecs > 0 {
		t := time.NewTicker(time.Duration(localSecs) * time.Second)
		defer t.Stop()
		localC = t.C
	}
	if floodSecs > 0 {
		t := time.NewTicker(time.Duration(floodSecs) * time.Second)
		defer t.Stop()
		floodC = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-boot.C:
			if err := r.sendAdvert(false); err != nil { // zero-hop, matching firmware boot
				r.log.Error("initial advert error", "error", err)
			}
		case <-localC:
			if err := r.sendAdvert(false); err != nil {
				r.log.Error("zero-hop advert error", "error", err)
			}
		case <-floodC:
			if err := r.sendAdvert(true); err != nil {
				r.log.Error("flood advert error", "error", err)
			}
		}
	}
}

// SendAdvert broadcasts a self-advert on demand (flood=true is mesh-wide and
// rebuilt as it propagates; flood=false is a zero-hop advert to direct
// neighbours only). Exposed for the API's manual "advertise now" action.
func (r *Repeater) SendAdvert(flood bool) error { return r.sendAdvert(flood) }

// sendAdvert emits a signed REPEATER advert (type REPEATER so other nodes
// classify us as a repeater and neighbouring repeaters record us). The advert
// carries our configured flood path-hash width; everything else is the shared
// self-advert build. Flood adverts are scoped through the configured default
// region (firmware default_scope) when one is set.
func (r *Repeater) sendAdvert(flood bool) error {
	return advert.SendSelf(r.node, r.log, "REPEATER", r.cfg.Name,
		r.cfg.Latitude, r.cfg.Longitude, flood, r.cfg.PathHashModeOr(), r.defaultRegionScope())
}

// defaultRegionScope resolves the configured default advert scope to the live
// region (nil = unscoped flood, the firmware default_scope=<null> case). A
// dangling name (region deleted out from under it) falls back to unscoped.
func (r *Repeater) defaultRegionScope() *meshcore.Region {
	name := r.cfgSnapshot().DefaultRegion
	if name == "" || name == config.WildcardRegion {
		return nil
	}
	return r.node.Regions().Get(name)
}
