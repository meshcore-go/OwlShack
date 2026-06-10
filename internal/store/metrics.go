package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// MetricsRepo is the persistence layer for node monitoring: the generic
// time-series table (node_metrics), per-node latest snapshots (node_state),
// the active-monitor registry (node_monitors), and neighbour SNR topology
// (node_neighbors). See migrateV2.
//
// Writes are issued by the monitor poller goroutine and HTTP handlers and must
// be wrapped by the caller in store.WriteAsync/WriteSync — these methods do the
// raw db.Exec and are not goroutine-safe against the writer loop on their own.
// Reads (Query*) run on the calling goroutine and are safe concurrently.
type MetricsRepo struct {
	db *sql.DB
}

// Metric is a single time-series reading. Channel is the CayenneLPP channel for
// sensor readings (0 for status fields that have no channel).
type Metric struct {
	TS      int64
	Pubkey  []byte
	Metric  string
	Channel int
	Value   float64
}

// HistoryPoint is one bucket of a history query: the bucket's start time (unix
// seconds) and the averaged value of all readings that fell in it.
type HistoryPoint struct {
	TS    int64   `json:"ts"`
	Value float64 `json:"value"`
}

// NodeState is the latest-known snapshot for a monitored node, for instant
// dashboard rendering without replaying history. State is a JSON object of the
// most recent value per metric.
type NodeState struct {
	Pubkey      []byte `json:"pubkey"`
	CompanionID string `json:"companionId"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	LastPollTS  int64  `json:"lastPollTs"`
	LastOkTS    int64  `json:"lastOkTs"`
	LastError   string `json:"lastError"`
	State       string `json:"state"` // raw JSON object
}

// Neighbor is one observed neighbour SNR sample at a point in time.
type Neighbor struct {
	TS             int64
	Pubkey         []byte
	NeighborPubkey []byte
	SNR            *float64
}

// RecordMetrics inserts a batch of readings in a single transaction. A nil/empty
// batch is a no-op. Call inside WriteAsync/WriteSync.
func (r *MetricsRepo) RecordMetrics(metrics []Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO node_metrics (ts, pubkey, metric, channel, value)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range metrics {
		if _, err := stmt.Exec(m.TS, m.Pubkey, m.Metric, m.Channel, m.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryHistory returns readings for one (pubkey, metric) between from..to (unix
// seconds, inclusive), averaged into fixed-width buckets so charts stay smooth
// and cheap regardless of polling cadence. bucketSecs <= 0 disables bucketing
// (raw points). Points are ordered ascending by time.
func (r *MetricsRepo) QueryHistory(pubkey []byte, metric string, from, to, bucketSecs int64) ([]HistoryPoint, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if bucketSecs > 0 {
		// Integer bucket key: floor(ts/bucket)*bucket gives the bucket start.
		rows, err = r.db.Query(`
			SELECT (ts / ?) * ? AS bucket, AVG(value)
			FROM node_metrics
			WHERE pubkey = ? AND metric = ? AND ts >= ? AND ts <= ?
			GROUP BY bucket
			ORDER BY bucket ASC`,
			bucketSecs, bucketSecs, pubkey, metric, from, to)
	} else {
		rows, err = r.db.Query(`
			SELECT ts, value
			FROM node_metrics
			WHERE pubkey = ? AND metric = ? AND ts >= ? AND ts <= ?
			ORDER BY ts ASC`,
			pubkey, metric, from, to)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.TS, &p.Value); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// ListMetricNames returns the distinct metric names recorded for a node, so the
// frontend can populate a metric picker without hardcoding the catalogue.
func (r *MetricsRepo) ListMetricNames(pubkey []byte) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT metric FROM node_metrics WHERE pubkey = ? ORDER BY metric ASC`, pubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// UpsertNodeState writes the latest snapshot for a node. The new snapshot is
// MERGED onto the stored one (per metric), so a partial poll — e.g. status
// succeeded but telemetry failed — keeps the last-known value of metrics it
// didn't refresh rather than dropping them from the dashboard. Call inside
// WriteAsync/WriteSync.
func (r *MetricsRepo) UpsertNodeState(s *NodeState) error {
	state := s.State
	if strings.TrimSpace(state) == "" {
		state = "{}"
	}
	var prev string
	if err := r.db.QueryRow(`SELECT state FROM node_state WHERE pubkey = ?`, s.Pubkey).Scan(&prev); err == nil {
		if merged, mErr := mergeStateJSON(prev, state); mErr == nil {
			state = merged
		}
	}
	_, err := r.db.Exec(`
		INSERT INTO node_state (pubkey, companion_id, kind, name, last_poll_ts, last_ok_ts, last_error, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			companion_id = excluded.companion_id,
			kind         = excluded.kind,
			name         = excluded.name,
			last_poll_ts = excluded.last_poll_ts,
			last_ok_ts   = excluded.last_ok_ts,
			last_error   = excluded.last_error,
			state        = excluded.state`,
		s.Pubkey, s.CompanionID, s.Kind, s.Name, s.LastPollTS, s.LastOkTS, s.LastError, state)
	return err
}

// mergeStateJSON returns prev overlaid with cur (cur wins on conflict), so the
// merged snapshot retains metrics present only in the previous poll. Values are
// kept as RawMessage to preserve their original numeric formatting. If either
// side won't parse as a JSON object, cur is returned unchanged.
func mergeStateJSON(prev, cur string) (string, error) {
	pm := map[string]json.RawMessage{}
	cm := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(prev), &pm); err != nil {
		return cur, err
	}
	if err := json.Unmarshal([]byte(cur), &cm); err != nil {
		return cur, err
	}
	for k, v := range cm {
		pm[k] = v
	}
	out, err := json.Marshal(pm)
	if err != nil {
		return cur, err
	}
	return string(out), nil
}

// MarkPollFailure records a failed poll WITHOUT clobbering the last-known-good
// snapshot: it updates only last_poll_ts + last_error (and companion/kind),
// preserving name, last_ok_ts and the metric state on conflict. A transient
// failure shouldn't blank a node's dashboard. Call inside WriteAsync/WriteSync.
func (r *MetricsRepo) MarkPollFailure(s *NodeState) error {
	_, err := r.db.Exec(`
		INSERT INTO node_state (pubkey, companion_id, kind, last_poll_ts, last_error)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			companion_id = excluded.companion_id,
			kind         = excluded.kind,
			last_poll_ts = excluded.last_poll_ts,
			last_error   = excluded.last_error`,
		s.Pubkey, s.CompanionID, s.Kind, s.LastPollTS, s.LastError)
	return err
}

// ListNodeStates returns every node snapshot, most-recently-polled first.
func (r *MetricsRepo) ListNodeStates() ([]NodeState, error) {
	rows, err := r.db.Query(`
		SELECT pubkey, companion_id, kind, name, last_poll_ts, last_ok_ts, last_error, state
		FROM node_state
		ORDER BY last_poll_ts DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []NodeState
	for rows.Next() {
		var s NodeState
		if err := rows.Scan(&s.Pubkey, &s.CompanionID, &s.Kind, &s.Name, &s.LastPollTS, &s.LastOkTS, &s.LastError, &s.State); err != nil {
			return nil, err
		}
		states = append(states, s)
	}
	return states, rows.Err()
}

// RecordNeighbors inserts a batch of neighbour SNR samples. Call inside
// WriteAsync/WriteSync.
func (r *MetricsRepo) RecordNeighbors(neighbors []Neighbor) error {
	if len(neighbors) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO node_neighbors (ts, pubkey, neighbor_pubkey, snr)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range neighbors {
		if _, err := stmt.Exec(n.TS, n.Pubkey, n.NeighborPubkey, n.SNR); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneMetrics deletes time-series rows older than the given unix-seconds
// cutoff, bounding raw storage growth. Returns the number of rows removed.
// Call inside WriteAsync/WriteSync.
func (r *MetricsRepo) PruneMetrics(cutoff int64) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM node_metrics WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	mRows, _ := res.RowsAffected()
	if _, err := r.db.Exec(`DELETE FROM node_neighbors WHERE ts < ?`, cutoff); err != nil {
		return mRows, err
	}
	return mRows, nil
}
