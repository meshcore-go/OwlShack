package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RepeaterACLEntry is one admin-over-mesh client; the firmware keeps this list on flash, we keep it in the DB so reauth survives a restart.
type RepeaterACLEntry struct {
	PubKey        string // 64-hex client public key
	Permissions   int    // firmware PERM_ACL_* (0=guest,1=read-only,2=read-write,3=admin)
	LastTimestamp uint32 // newest login/request timestamp seen (replay guard)
	LastSeen      time.Time
}

type RepeaterACLRepo struct{ db *sql.DB }

// Get returns the ACL entry for a client pubkey, or sql.ErrNoRows if absent.
func (r *RepeaterACLRepo) Get(ctx context.Context, pubkey string) (*RepeaterACLEntry, error) {
	var e RepeaterACLEntry
	var lastSeen int64
	err := r.db.QueryRowContext(ctx,
		`SELECT pubkey, permissions, last_timestamp, last_seen FROM repeater_acl WHERE pubkey = ?`, pubkey).
		Scan(&e.PubKey, &e.Permissions, &e.LastTimestamp, &lastSeen)
	if err != nil {
		return nil, err
	}
	e.LastSeen = time.Unix(lastSeen, 0)
	return &e, nil
}

// List returns every ACL entry (used by the GET_ACCESS_LIST response).
func (r *RepeaterACLRepo) List(ctx context.Context) ([]RepeaterACLEntry, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT pubkey, permissions, last_timestamp, last_seen FROM repeater_acl ORDER BY pubkey`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepeaterACLEntry
	for rows.Next() {
		var e RepeaterACLEntry
		var lastSeen int64
		if err := rows.Scan(&e.PubKey, &e.Permissions, &e.LastTimestamp, &lastSeen); err != nil {
			return nil, err
		}
		e.LastSeen = time.Unix(lastSeen, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *RepeaterACLRepo) Upsert(ctx context.Context, e *RepeaterACLEntry) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO repeater_acl (pubkey, permissions, last_timestamp, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			permissions    = excluded.permissions,
			last_timestamp = excluded.last_timestamp,
			last_seen      = excluded.last_seen`,
		e.PubKey, e.Permissions, e.LastTimestamp, e.LastSeen.Unix())
	if err != nil {
		return fmt.Errorf("upserting repeater ACL: %w", err)
	}
	return nil
}

func (r *RepeaterACLRepo) Delete(ctx context.Context, pubkey string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM repeater_acl WHERE pubkey = ?`, pubkey); err != nil {
		return fmt.Errorf("deleting repeater ACL entry: %w", err)
	}
	return nil
}

// Clear removes all ACL entries (called when the repeater is deleted).
func (r *RepeaterACLRepo) Clear(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM repeater_acl`); err != nil {
		return fmt.Errorf("clearing repeater ACL: %w", err)
	}
	return nil
}
