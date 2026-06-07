package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func (s *Server) handleListPackets(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 100
	}

	packets, err := s.store.Packets.List(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type packetJSON struct {
		ID           int64    `json:"id"`
		ReceivedAt   string   `json:"receivedAt"`
		Direction    string   `json:"direction"`
		Raw          string   `json:"raw"`
		RouteType    *uint8   `json:"routeType,omitempty"`
		PayloadType  *uint8   `json:"payloadType,omitempty"`
		Route        string   `json:"route,omitempty"`
		PathHashSize *uint8   `json:"pathHashSize,omitempty"`
		Hops         *uint8   `json:"hops,omitempty"`
		PacketHash   string   `json:"packetHash,omitempty"`
		Summary      string   `json:"summary,omitempty"`
		SNR          *float64 `json:"snr,omitempty"`
		RSSI         *int8    `json:"rssi,omitempty"`
	}

	out := make([]packetJSON, 0, len(packets))
	for _, p := range packets {
		j := packetJSON{
			ID:          p.ID,
			ReceivedAt:  p.ReceivedAt.UTC().Format(time.RFC3339),
			Direction:   p.Direction,
			Raw:         hex.EncodeToString(p.Raw),
			RouteType:   p.RouteType,
			PayloadType: p.PayloadType,
			SNR:         p.SNR,
			RSSI:        p.RSSI,
		}

		if pkt, err := meshcore.PacketFromBytes(p.Raw); err == nil {
			j.Route = pkt.RouteTypeString()
			phs := pkt.PathHashSize()
			phc := pkt.PathHashCount()
			j.PathHashSize = &phs
			j.Hops = &phc
			j.PacketHash = PacketHash(pkt)
			j.Summary = PacketSummary(pkt, s.ChannelLookup())
		}

		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}
