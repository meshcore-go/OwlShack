package api

import (
	"encoding/hex"
	"net/http"
	"time"
)

func (s *Server) handleListPeers(w http.ResponseWriter, r *http.Request) {
	peers, err := s.store.Peers.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type peerJSON struct {
		PubKey          string   `json:"pubkey"`
		Name            string   `json:"name"`
		Type            string   `json:"type"`
		Lat             int32    `json:"lat"`
		Lon             int32    `json:"lon"`
		Feat1           uint16   `json:"feat1"`
		Feat2           uint16   `json:"feat2"`
		OutPath         string   `json:"outPath,omitempty"`
		OutPathHashSize uint8    `json:"outPathHashSize"`
		LastAdvertTS    uint32   `json:"lastAdvertTs"`
		LastSeen        string   `json:"lastSeen"`
		SNR             *float64 `json:"snr"`
		RSSI            *int8    `json:"rssi"`
	}

	out := make([]peerJSON, 0, len(peers))
	for _, p := range peers {
		out = append(out, peerJSON{
			PubKey:          hex.EncodeToString(p.PubKey),
			Name:            p.Name,
			Type:            p.Type,
			Lat:             p.Lat,
			Lon:             p.Lon,
			Feat1:           p.Feat1,
			Feat2:           p.Feat2,
			OutPath:         hex.EncodeToString(p.OutPath),
			OutPathHashSize: p.OutPathHashSize,
			LastAdvertTS:    p.LastAdvertTS,
			LastSeen:        p.LastSeen.UTC().Format(time.RFC3339),
			SNR:             p.SNR,
			RSSI:            p.RSSI,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeletePeer(w http.ResponseWriter, r *http.Request) {
	pubkeyHex := r.PathValue("pubkey")
	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}

	// Contacts are decoupled from discovered_peers (they cache their own
	// identity), so deleting a peer never affects a saved contact — no guard
	// needed.
	var delErr error
	s.store.WriteSync(func() {
		delErr = s.store.Peers.Delete(pubkey)
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, delErr.Error())
		return
	}
	s.afterPeerDelete([]string{pubkeyHex}, [][]byte{pubkey})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBulkDeletePeers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pubkeys []string `json:"pubkeys"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.Pubkeys) == 0 {
		writeError(w, http.StatusBadRequest, "no pubkeys provided")
		return
	}

	pubkeys := make([][]byte, 0, len(body.Pubkeys))
	hexes := make([]string, 0, len(body.Pubkeys))
	for _, h := range body.Pubkeys {
		pk, err := hex.DecodeString(h)
		if err != nil || len(pk) == 0 {
			writeError(w, http.StatusBadRequest, "invalid pubkey hex: "+h)
			return
		}
		pubkeys = append(pubkeys, pk)
		hexes = append(hexes, h)
	}

	var delErr error
	s.store.WriteSync(func() {
		delErr = s.store.Peers.DeleteMany(pubkeys)
	})
	if delErr != nil {
		writeError(w, http.StatusInternalServerError, delErr.Error())
		return
	}
	s.afterPeerDelete(hexes, pubkeys)
	writeJSON(w, http.StatusOK, map[string]int{"deleted": len(pubkeys)})
}

// afterPeerDelete evicts deleted peers from the in-memory tables and tells all
// WS clients to drop them, so the Peers/Map/Overview views stay consistent
// without a manual refresh.
func (s *Server) afterPeerDelete(hexes []string, pubkeys [][]byte) {
	if remove := s.peerRemover(); remove != nil {
		remove(pubkeys)
	}
	s.hub.Broadcast("peers", map[string]any{
		"action":  "delete",
		"pubkeys": hexes,
	})
}
