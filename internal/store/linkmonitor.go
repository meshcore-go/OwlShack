package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LinkMonitorRepo persists monitored paths under a synthetic 32-byte key; writes must be wrapped in store.WriteAsync/WriteSync.
type LinkMonitorRepo struct {
	db *sql.DB
}

type LinkMonitor struct {
	ID           int64
	Key          []byte
	CompanionID  int64
	Label        string
	Path         []byte
	PathHashSize int
	IntervalSecs int
	Enabled      bool
	// Display-only UI toggles; the collector keeps recording both readings.
	IgnoreFirstHop bool
	HideLastSnr    bool
	// 0 = the poller's built-in default, as with ContactMetadata.MonitorRetrySecs.
	RetrySecs  int
	MaxRetries int
}

func scanLinkMonitor(s interface{ Scan(...any) error }) (*LinkMonitor, error) {
	var l LinkMonitor
	if err := s.Scan(&l.ID, &l.Key, &l.CompanionID, &l.Label, &l.Path, &l.PathHashSize, &l.IntervalSecs, &l.Enabled, &l.IgnoreFirstHop, &l.RetrySecs, &l.MaxRetries, &l.HideLastSnr); err != nil {
		return nil, err
	}
	return &l, nil
}

const linkMonitorCols = `id, key, companion_id, label, path, path_hash_size, interval_secs, enabled, ignore_first_hop, retry_secs, max_retries, hide_last_snr`

// Create inserts a link monitor and sets l.ID; a duplicate key (the same path already monitored) errors. Call inside WriteSync.
func (r *LinkMonitorRepo) Create(ctx context.Context, l *LinkMonitor) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO link_monitors (key, companion_id, label, path, path_hash_size, interval_secs, enabled, ignore_first_hop, retry_secs, max_retries, hide_last_snr)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Key, l.CompanionID, l.Label, l.Path, l.PathHashSize, l.IntervalSecs, l.Enabled, l.IgnoreFirstHop, l.RetrySecs, l.MaxRetries, l.HideLastSnr)
	if err != nil {
		return fmt.Errorf("creating link monitor: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("reading link monitor id: %w", err)
	}
	l.ID = id
	return nil
}

// List returns every link monitor, ordered by label then id.
func (r *LinkMonitorRepo) List(ctx context.Context) ([]LinkMonitor, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+linkMonitorCols+` FROM link_monitors ORDER BY label ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing link monitors: %w", err)
	}
	defer rows.Close()

	var out []LinkMonitor
	for rows.Next() {
		l, err := scanLinkMonitor(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning link monitor: %w", err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating link monitors: %w", err)
	}
	return out, nil
}

// ListEnabled returns only the enabled link monitors, for the monitor Lister each cycle.
func (r *LinkMonitorRepo) ListEnabled(ctx context.Context) ([]LinkMonitor, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+linkMonitorCols+` FROM link_monitors WHERE enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("listing enabled link monitors: %w", err)
	}
	defer rows.Close()

	var out []LinkMonitor
	for rows.Next() {
		l, err := scanLinkMonitor(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning link monitor: %w", err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating enabled link monitors: %w", err)
	}
	return out, nil
}

// GetByID returns one link monitor by its surrogate id, or nil if absent.
func (r *LinkMonitorRepo) GetByID(ctx context.Context, id int64) (*LinkMonitor, error) {
	l, err := scanLinkMonitor(r.db.QueryRowContext(ctx, `SELECT `+linkMonitorCols+` FROM link_monitors WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting link monitor: %w", err)
	}
	return l, nil
}

// GetByKey returns the link monitor for a synthetic key, or nil if absent.
func (r *LinkMonitorRepo) GetByKey(ctx context.Context, key []byte) (*LinkMonitor, error) {
	l, err := scanLinkMonitor(r.db.QueryRowContext(ctx, `SELECT `+linkMonitorCols+` FROM link_monitors WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting link monitor: %w", err)
	}
	return l, nil
}

// Update patches only non-nil fields: a nil arg becomes SQL NULL, so COALESCE keeps the current column. Call inside WriteSync.
func (r *LinkMonitorRepo) Update(ctx context.Context, id int64, label *string, intervalSecs *int, enabled *bool, ignoreFirstHop *bool, retrySecs *int, maxRetries *int, hideLastSnr *bool) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE link_monitors SET
			label = COALESCE(?, label),
			interval_secs = COALESCE(?, interval_secs),
			enabled = COALESCE(?, enabled),
			ignore_first_hop = COALESCE(?, ignore_first_hop),
			retry_secs = COALESCE(?, retry_secs),
			max_retries = COALESCE(?, max_retries),
			hide_last_snr = COALESCE(?, hide_last_snr)
		WHERE id = ?`,
		label, intervalSecs, enabled, ignoreFirstHop, retrySecs, maxRetries, hideLastSnr, id)
	if err != nil {
		return fmt.Errorf("updating link monitor: %w", err)
	}
	return nil
}

// Delete removes a link monitor; callers must also drop its node_state/node_metrics rows. Call inside WriteSync.
func (r *LinkMonitorRepo) Delete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM link_monitors WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting link monitor: %w", err)
	}
	return nil
}

// DeleteNodeStateAndMetrics clears a synthetic link key's node_state and node_metrics rows. Call inside the same WriteSync as Delete.
func (r *LinkMonitorRepo) DeleteNodeStateAndMetrics(ctx context.Context, key []byte) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_state WHERE pubkey = ?`, key); err != nil {
		return fmt.Errorf("deleting link node state: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM node_metrics WHERE pubkey = ?`, key); err != nil {
		return fmt.Errorf("deleting link node metrics: %w", err)
	}
	return nil
}
