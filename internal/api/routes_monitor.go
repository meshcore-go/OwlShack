package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/meshcore-go/meshcore-bot/internal/monitor"
	"github.com/meshcore-go/meshcore-bot/internal/store"
)

// NodePoller is the monitor-service seam: triggering an immediate out-of-band
// poll, and reading the current target set (which nodes have the monitor toggle
// on). Implemented by *monitor.Service and installed via Server.SetPoller;
// PollNow is the one monitoring write path (the rest are reads off the store).
type NodePoller interface {
	PollNow(ctx context.Context, pubkey []byte) error
	Targets() ([]monitor.Target, error)
}

// Node monitoring read endpoints. Which nodes are monitored is toggled through
// the existing contact-metadata PATCH (monitor / monitorIntervalSecs), so there
// is no write endpoint here — these only expose the polled time-series data.

// handleListMonitoredNodes returns one entry per node whose monitor toggle is
// on (the poller's current target set), merged with the latest node_state
// snapshot when one exists. Membership is decided by the toggle, not by polled
// data: a freshly enrolled node appears immediately with zero timestamps and
// empty metrics ("waiting for first poll"), and a node toggled off disappears
// even though its node_state row is retained.
func (s *Server) handleListMonitoredNodes(w http.ResponseWriter, r *http.Request) {
	poller := s.pollerRef()
	if poller == nil {
		writeError(w, http.StatusServiceUnavailable, "monitor not ready")
		return
	}
	targets, err := poller.Targets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	states, err := s.store.Metrics.ListNodeStates()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stateByKey := make(map[string]*store.NodeState, len(states))
	for i := range states {
		stateByKey[hex.EncodeToString(states[i].Pubkey)] = &states[i]
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

	out := make([]nodeJSON, 0, len(targets))
	for _, t := range targets {
		key := hex.EncodeToString(t.Pubkey)
		// Effective poll cadence: the target carries the contact's override, or
		// 0 for the scheduler default. The UI derives its staleness threshold
		// from this.
		interval := monitor.DefaultIntervalSecs
		if t.IntervalSecs > 0 {
			interval = t.IntervalSecs
		}
		n := nodeJSON{
			PubKey:       key,
			CompanionID:  t.CompanionID,
			Kind:         t.Kind,
			IntervalSecs: interval,
			Metrics:      map[string]float64{},
		}
		if st := stateByKey[key]; st != nil {
			n.Name = st.Name
			n.LastPollTS = st.LastPollTS
			n.LastOkTS = st.LastOkTS
			n.LastError = st.LastError
			if st.State != "" {
				if err := json.Unmarshal([]byte(st.State), &n.Metrics); err != nil {
					// Degrade to empty metrics rather than failing the list,
					// but don't hide the corruption.
					s.log.Warn("corrupt node_state snapshot", "pubkey", key, "error", err)
				}
			}
		}
		// node_state only learns the name from a successful poll; fall back to
		// the discovered-peers table so an unpolled node isn't nameless.
		if n.Name == "" {
			if p, err := s.store.Peers.GetByPubKey(t.Pubkey); err == nil && p != nil {
				n.Name = p.Name
			}
		}
		out = append(out, n)
	}
	// Stable order regardless of lister map iteration: name (case-insensitive),
	// pubkey as tiebreaker for unnamed nodes.
	slices.SortFunc(out, func(a, b nodeJSON) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.PubKey, b.PubKey)
	})
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
