package companion

import (
	"context"
	"time"

	"github.com/meshcore-go/OwlShack/internal/node/advert"
)

func (c *Companion) advertLoop(ctx context.Context) {
	// Send initial advert
	err := c.advert()
	if err != nil {
		c.log.Error("initial advert error", "error", err)
	}

	advertInterval := c.cfg.AdvertInterval
	if advertInterval == nil || *advertInterval < 1 {
		oneDay := 86400
		advertInterval = &oneDay
	}

	// Get tick
	interval := time.Duration(*advertInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Start loop
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := c.advert()
			if err != nil {
				c.log.Error("advert error", "error", err)
			}
		}
	}
}

// advert sends the periodic self-advert used by advertLoop: always flood-routed.
func (c *Companion) advert() error {
	return c.sendAdvert(true)
}

// SendAdvert broadcasts a self-advert on demand. flood=true is a mesh-wide
// advert that repeaters rebroadcast (path built up as it propagates);
// flood=false is a zero-hop advert that only direct neighbours receive and is
// never rebroadcast. Mirrors the firmware's `advert` / `advert.zerohop` CLI
// commands (Mesh::sendFlood vs Mesh::sendZeroHop): same signed payload, the
// route type + path length differ.
func (c *Companion) SendAdvert(flood bool) error {
	return c.sendAdvert(flood)
}

func (c *Companion) sendAdvert(flood bool) error {
	// Companions always use 1-byte path hashes (mode 0); everything else is the
	// shared self-advert build.
	return advert.SendSelf(c.node, c.log, "CHAT", c.cfg.Name,
		c.cfg.Latitude, c.cfg.Longitude, flood, 0, nil) // companions don't scope their floods
}
