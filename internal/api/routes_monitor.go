package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/meshcore-go/meshcore-bot/internal/monitor"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// NodePoller triggers an immediate, out-of-band poll of a single monitored
// node. Implemented by *monitor.Service and installed via Server.SetPoller; it's
// the one monitoring write path (the rest are reads off the store directly).
type NodePoller interface {
	PollNow(ctx context.Context, pubkey []byte) error
}

// Node monitoring read endpoints. Which nodes are monitored is toggled through
// the existing contact-metadata PATCH (monitor / monitorIntervalSecs), so there
// is no write endpoint here — these only expose the polled time-series data.

// handleListMonitoredNodes returns the latest snapshot of every node that has
// been polled (one row per node in node_state), most-recently-polled first.
func (s *Server) handleListMonitoredNodes(w http.ResponseWriter, r *http.Request) {
	states, err := s.store.Metrics.ListNodeStates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type nodeJSON struct {
		PubKey       string             `json:"pubkey"`
		CompanionID  string             `json:"companionId"`
		Kind         string             `json:"kind"`
		Name         string             `json:"name"`
		LastPollTS   int64              `json:"lastPollTs"`
		LastOkTS     int64              `json:"lastOkTs"`
		LastError    string             `json:"lastError,omitempty"`
		IntervalSecs int64              `json:"intervalSecs"`
		Metrics      map[string]float64 `json:"metrics"`
	}

	out := make([]nodeJSON, 0, len(states))
	for _, st := range states {
		metrics := map[string]float64{}
		if st.State != "" {
			json.Unmarshal([]byte(st.State), &metrics)
		}
		// Effective poll cadence: the contact's override, or the scheduler
		// default. The UI derives its staleness threshold from this.
		interval := monitor.DefaultIntervalSecs
		if c, err := s.store.Contacts.Get(st.CompanionID, st.Pubkey); err == nil && c != nil && c.Metadata.MonitorIntervalSecs > 0 {
			interval = c.Metadata.MonitorIntervalSecs
		}
		out = append(out, nodeJSON{
			PubKey:       hex.EncodeToString(st.Pubkey),
			CompanionID:  st.CompanionID,
			Kind:         st.Kind,
			Name:         st.Name,
			LastPollTS:   st.LastPollTS,
			LastOkTS:     st.LastOkTS,
			LastError:    st.LastError,
			IntervalSecs: interval,
			Metrics:      metrics,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListNodeMetricNames returns the distinct metric names recorded for a
// node, so the frontend can populate a metric picker.
func (s *Server) handleListNodeMetricNames(w http.ResponseWriter, r *http.Request) {
	pubkey, err := hex.DecodeString(r.PathValue("pubkey"))
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}
	names, err := s.store.Metrics.ListMetricNames(pubkey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

// handleNodeHistory returns a single metric's time-series for a node, averaged
// into fixed-width buckets. Query params: metric (required), from/to (unix
// seconds, optional), bucket (seconds, optional — default 300 / 5 min).
func (s *Server) handleNodeHistory(w http.ResponseWriter, r *http.Request) {
	pubkey, err := hex.DecodeString(r.PathValue("pubkey"))
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}
	q := r.URL.Query()
	metric := q.Get("metric")
	if metric == "" {
		writeError(w, http.StatusBadRequest, "metric query parameter required")
		return
	}

	from := parseInt64(q.Get("from"), 0)
	to := parseInt64(q.Get("to"), 1<<62)
	bucket := parseInt64(q.Get("bucket"), 300)

	points, err := s.store.Metrics.QueryHistory(pubkey, metric, from, to, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if points == nil {
		points = []store.HistoryPoint{}
	}
	writeJSON(w, http.StatusOK, points)
}

// handlePollNode triggers an immediate poll of a node, bypassing the schedule.
// Used by the per-node "poll" button. Blocks until the poll completes (RF
// round-trips take a few seconds); on success the fresh metrics are pushed over
// the "metrics" WS topic, so the caller just needs to react to that.
func (s *Server) handlePollNode(w http.ResponseWriter, r *http.Request) {
	pubkey, err := hex.DecodeString(r.PathValue("pubkey"))
	if err != nil || len(pubkey) == 0 {
		writeError(w, http.StatusBadRequest, "invalid pubkey hex")
		return
	}
	poller := s.pollerRef()
	if poller == nil {
		writeError(w, http.StatusServiceUnavailable, "monitor not ready")
		return
	}
	if err := poller.PollNow(r.Context(), pubkey); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func parseInt64(s string, def int64) int64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}
