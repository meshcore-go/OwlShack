package companion

import (
	"context"
	"math"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
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
	appData := meshcore.AdvertAppData{
		Type: "CHAT",
		Name: c.cfg.Name,
		Lat:  0,
		Lon:  0,
	}

	if c.cfg.HasLatLon() {
		appData.Lat = int32(math.Round(*c.cfg.Latitude * 1_000_000.0))
		appData.Lon = int32(math.Round(*c.cfg.Longitude * 1_000_000.0))
	}

	rawAppData, err := appData.ToBytes()
	if err != nil {
		return err
	}

	advert := meshcore.Advert{
		PublicKey:  c.node.Identity().Identity,
		Timestamp:  uint32(time.Now().Unix()),
		RawAppData: rawAppData,
	}
	advert.Sign(c.node.Identity().PrivateKey())

	payload, err := advert.ToBytes()
	if err != nil {
		return err
	}

	// Flood: ROUTE_TYPE_FLOOD with the path-hash-size nibble and a zero hop
	// count. Zero-hop: ROUTE_TYPE_DIRECT with path length 0 (path_len==0 is what
	// the firmware reads as "zero hop"), so neighbours accept but never relay it.
	routeType := meshcore.RouteTypeFlood
	pathLength := byte(meshcore.PathHashSize - 1)
	if !flood {
		routeType = meshcore.RouteTypeDirect
		pathLength = 0
	}

	pkt := meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeAdvert, 0),
		PathLength: pathLength,
		Payload:    payload,
	}

	mode := "flood"
	if !flood {
		mode = "zero-hop"
	}
	c.log.Info("sending self-advert", "mode", mode)

	return c.node.SendPacket(&pkt)
}
