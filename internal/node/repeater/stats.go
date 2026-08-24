package repeater

import (
	"encoding/hex"
	"sort"
	"time"
)

// Stats is a live snapshot of the repeater's relay activity, for the API.
type Stats struct {
	Name             string  `json:"name"`
	PubKey           string  `json:"pubkey"`
	UptimeSecs       int64   `json:"uptimeSecs"`
	PacketsReceived  uint64  `json:"packetsReceived"`
	PacketsForwarded uint64  `json:"packetsForwarded"`
	TxQueueLen       int     `json:"txQueueLen"`
	Neighbors        int     `json:"neighbors"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
}

// NeighborInfo is a directly-heard repeater, for the API neighbours list.
type NeighborInfo struct {
	PubKey  string  `json:"pubkey"`
	Name    string  `json:"name"`
	SNR     float64 `json:"snr"`
	SecsAgo int64   `json:"secsAgo"`
}

func (r *Repeater) Stats() Stats {
	r.mu.Lock()
	started := r.startedAt
	r.mu.Unlock()

	uptime := int64(0)
	if !started.IsZero() {
		uptime = int64(time.Since(started).Seconds())
	}

	r.neighbors.Lock()
	nCount := len(r.neighbors.m)
	r.neighbors.Unlock()

	s := Stats{
		Name:             r.cfg.Name,
		PubKey:           hex.EncodeToString(r.node.Identity().PublicKeyBytes()),
		UptimeSecs:       uptime,
		PacketsReceived:  r.recvCount.Load(),
		PacketsForwarded: r.fwdCount.Load(),
		TxQueueLen:       r.node.TxQueueLen(),
		Neighbors:        nCount,
	}
	if r.cfg.Latitude != nil {
		s.Latitude = *r.cfg.Latitude
	}
	if r.cfg.Longitude != nil {
		s.Longitude = *r.cfg.Longitude
	}
	return s
}

// snapshotNeighbors returns a copy of the directly-heard neighbours, newest
// first. It owns the lock + sort so callers only format the result (CLI text /
// wire bytes / JSON) — see neighborsList, neighboursBody, Neighbors.
func (r *Repeater) snapshotNeighbors() []neighbor {
	r.neighbors.Lock()
	out := make([]neighbor, 0, len(r.neighbors.m))
	for _, n := range r.neighbors.m {
		out = append(out, *n)
	}
	r.neighbors.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].heard.After(out[j].heard) })
	return out
}

// Neighbors returns the directly-heard repeaters, newest first.
func (r *Repeater) Neighbors() []NeighborInfo {
	now := time.Now()
	list := r.snapshotNeighbors()
	out := make([]NeighborInfo, len(list))
	for i, n := range list {
		out[i] = NeighborInfo{
			PubKey:  hex.EncodeToString(n.pubkey[:]),
			Name:    n.name,
			SNR:     n.snr,
			SecsAgo: int64(now.Sub(n.heard).Seconds()),
		}
	}
	return out
}
