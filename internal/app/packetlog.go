package app

import (
	"context"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/meshcore-go/meshcore-bot/internal/api"
	"github.com/meshcore-go/meshcore-bot/internal/store"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/node"
)

// wirePacketLogger taps the mux to persist every rx/tx packet and broadcast it
// to WebSocket subscribers.
func wirePacketLogger(mux *node.RadioMux, db *store.Store, srv *api.Server) {
	hub := srv.Hub()
	logRadio := mux.NewRadio()

	logRadio.SetRawDataHandler(func(data []byte, snr float32, rssi int8, hasSignalInfo bool) {
		pkt, err := meshcore.PacketFromBytes(data)
		routeType, payloadType := packetTypes(pkt, err)

		var snrPtr *float64
		var rssiPtr *int8
		if hasSignalInfo {
			s := float64(snr)
			snrPtr = &s
			rssiPtr = &rssi
		}

		rec := &store.PacketRecord{
			ReceivedAt:  time.Now(),
			Direction:   "rx",
			Raw:         data,
			RouteType:   routeType,
			PayloadType: payloadType,
			SNR:         snrPtr,
			RSSI:        rssiPtr,
		}
		if err == nil {
			rec.PacketHash, rec.Path = store.PacketFieldsFromPkt(pkt)
		}

		db.WriteAsync(func() {
			if insertErr := db.Packets.Insert(context.Background(), rec); insertErr != nil {
				slog.Debug("failed to log rx packet", "error", insertErr)
			}
		})

		msg := packetBroadcastMsg("rx", rec.ReceivedAt, data, pkt, err, srv.ChannelLookup())
		if hasSignalInfo {
			msg["snr"] = snr
			msg["rssi"] = rssi
		}
		hub.Broadcast("packets", msg)
	})

	logRadio.AddOutboundHandler(func(data []byte) {
		pkt, err := meshcore.PacketFromBytes(data)
		routeType, payloadType := packetTypes(pkt, err)

		rec := &store.PacketRecord{
			ReceivedAt:  time.Now(),
			Direction:   "tx",
			Raw:         data,
			RouteType:   routeType,
			PayloadType: payloadType,
		}
		if err == nil {
			rec.PacketHash, rec.Path = store.PacketFieldsFromPkt(pkt)
		}

		db.WriteAsync(func() {
			if insertErr := db.Packets.Insert(context.Background(), rec); insertErr != nil {
				slog.Debug("failed to log tx packet", "error", insertErr)
			}
		})

		hub.Broadcast("packets", packetBroadcastMsg("tx", rec.ReceivedAt, data, pkt, err, srv.ChannelLookup()))
	})
}

// packetTypes extracts the route and payload type pointers from a parsed
// packet, or (nil, nil) when parsing failed.
func packetTypes(pkt *meshcore.Packet, parseErr error) (routeType, payloadType *uint8) {
	if parseErr != nil {
		return nil, nil
	}
	rt := pkt.RouteType()
	pt := pkt.PayloadType()
	return &rt, &pt
}

func packetBroadcastMsg(direction string, receivedAt time.Time, data []byte, pkt *meshcore.Packet, parseErr error, channels api.ChannelLookup) map[string]any {
	msg := map[string]any{
		"direction":  direction,
		"receivedAt": receivedAt.Format(time.RFC3339),
		"raw":        hex.EncodeToString(data),
	}
	if parseErr != nil {
		return msg
	}
	msg["routeType"] = pkt.RouteType()
	msg["payloadType"] = pkt.PayloadType()
	msg["route"] = pkt.RouteTypeString()
	msg["pathHashSize"] = pkt.PathHashSize()
	msg["hops"] = pkt.PathHashCount()
	msg["packetHash"], msg["path"] = store.PacketFieldsFromPkt(pkt)
	msg["summary"] = api.PacketSummary(pkt, channels)
	return msg
}
