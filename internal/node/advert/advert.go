// Package advert builds and sends MeshCore self-adverts, shared by the node
// personalities (companion, repeater) that each announce themselves on the mesh.
// The only per-node differences are the advert type and the flood path-hash
// width, so they're parameters here rather than duplicated build logic.
package advert

import (
	"log/slog"
	"math"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

// SendSelf builds, signs, and transmits a self-advert of the given type.
//
// flood=true is a mesh-wide advert repeaters rebuild as it propagates;
// flood=false is a zero-hop advert only direct neighbours receive (never
// relayed). pathHashSize is the flood path-hash width in BYTES (1-4); the wire
// carries size-1 in the top 2 bits of PathLength. Ignored for zero-hop. lat/lon
// are included only when set and not both zero.
//
// scope (the firmware default_scope) optionally wraps a flood advert in a
// transport region; nil sends a plain unscoped flood.
// floodPathLength encodes PathLength: top 2 bits (pathHashSize - 1), low 6 the
// hop count (0 when we originate). Clamped — a 0 would underflow to 0xC0.
func floodPathLength(pathHashSize int) byte {
	if pathHashSize < 1 || pathHashSize > 3 {
		pathHashSize = 1
	}
	return byte((pathHashSize - 1) << 6)
}

func SendSelf(n *node.Node, log *slog.Logger, advType, name string, lat, lon *float64, flood bool, pathHashSize int, scope *meshcore.Region) error {
	appData := meshcore.AdvertAppData{Type: advType, Name: name}
	if lat != nil && lon != nil && (*lat != 0 || *lon != 0) {
		appData.Lat = int32(math.Round(*lat * 1_000_000.0))
		appData.Lon = int32(math.Round(*lon * 1_000_000.0))
	}
	rawAppData, err := appData.ToBytes()
	if err != nil {
		return err
	}

	adv := meshcore.Advert{
		PublicKey:  n.Identity().Identity,
		Timestamp:  uint32(time.Now().Unix()),
		RawAppData: rawAppData,
	}
	// SignWith (not Sign(PrivateKey())) so an imported expanded-key identity
	// signs correctly — its PrivateKey() has no usable seed.
	adv.SignWith(n.Identity())

	payload, err := adv.ToBytes()
	if err != nil {
		return err
	}

	// Flood: ROUTE_TYPE_FLOOD, path-hash width in the top 2 bits, hop count 0.
	// Zero-hop: ROUTE_TYPE_DIRECT with PathLength 0 (firmware reads path_len==0
	// as "zero hop"), so neighbours accept but never relay it.
	routeType := meshcore.RouteTypeFlood
	pathLength := floodPathLength(pathHashSize)
	if !flood {
		routeType = meshcore.RouteTypeDirect
		pathLength = 0
	}

	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeAdvert, 0),
		PathLength: pathLength,
		Payload:    payload,
	}
	if flood && scope != nil { // scoped flood advert (firmware sendFloodScoped(default_scope, ...)); code 2 stays 0
		pkt.Header = meshcore.MakeHeader(meshcore.RouteTypeTransportFlood, meshcore.PayloadTypeAdvert, 0)
		pkt.TransportCode1 = scope.CalcTransportCode(pkt)
	}

	mode := "flood"
	if !flood {
		mode = "zero-hop"
	} else if scope != nil {
		mode += " scoped"
	}
	log.Info("sending self-advert", "mode", mode)

	return n.SendPacket(pkt)
}
