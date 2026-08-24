package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// RepeaterRegion is a transport scope the repeater relays. Stored as JSON in the
// repeater.regions column (the repeater is a singleton, so no child table).
type RepeaterRegion struct {
	Name      string `json:"name"`
	DenyFlood bool   `json:"denyFlood,omitempty"`
}

// Repeater is the single repeater node the bot runs (at most one — a radio can
// host only one relay identity). Stored as a singleton row (id=1); a missing
// row means no repeater is configured. Unlike Companion it has no child history
// tables (no messages/contacts), so it needs no surrogate-id indirection.
type Repeater struct {
	Name                string
	PrivateKey          string
	PubKey              string
	Latitude            *float64
	Longitude           *float64
	AdvertInterval      *int
	FloodAdvertInterval *int
	DisableFwd          *bool
	FloodMax            *int
	FloodMaxUnscoped    *int
	FloodMaxAdvert      *int
	LoopDetect          *string
	PathHashMode        *int
	DefaultRegion       string
	AdminPassword       string
	GuestPassword       string
	OwnerInfo           string
	Regions             []RepeaterRegion
}

type RepeaterRepo struct{ db *sql.DB }

// Get returns the configured repeater, or sql.ErrNoRows when none is set.
func (r *RepeaterRepo) Get(ctx context.Context) (*Repeater, error) {
	var rep Repeater
	var regionsJSON string
	err := r.db.QueryRowContext(ctx, `
		SELECT name, private_key, pubkey, latitude, longitude, advert_interval,
		       flood_advert_interval, disable_fwd, flood_max, flood_max_unscoped, flood_max_advert,
		       loop_detect, path_hash_mode, default_region, admin_password, guest_password, owner_info, regions
		FROM repeater WHERE id = 1`).Scan(
		&rep.Name, &rep.PrivateKey, &rep.PubKey, &rep.Latitude, &rep.Longitude, &rep.AdvertInterval,
		&rep.FloodAdvertInterval, &rep.DisableFwd, &rep.FloodMax, &rep.FloodMaxUnscoped, &rep.FloodMaxAdvert,
		&rep.LoopDetect, &rep.PathHashMode, &rep.DefaultRegion, &rep.AdminPassword, &rep.GuestPassword, &rep.OwnerInfo, &regionsJSON)
	if err != nil {
		return nil, err // includes sql.ErrNoRows for "no repeater configured"
	}
	if regionsJSON != "" {
		if err := json.Unmarshal([]byte(regionsJSON), &rep.Regions); err != nil {
			return nil, fmt.Errorf("decoding repeater regions: %w", err)
		}
	}
	return &rep, nil
}

// Set upserts the single repeater row (id=1).
func (r *RepeaterRepo) Set(ctx context.Context, rep *Repeater) error {
	regions := rep.Regions
	if regions == nil {
		regions = []RepeaterRegion{}
	}
	regionsJSON, err := json.Marshal(regions)
	if err != nil {
		return fmt.Errorf("encoding repeater regions: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO repeater
			(id, name, private_key, pubkey, latitude, longitude, advert_interval,
			 flood_advert_interval, disable_fwd, flood_max, flood_max_unscoped, flood_max_advert,
			 loop_detect, path_hash_mode, default_region, admin_password, guest_password, owner_info, regions)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rep.Name, rep.PrivateKey, rep.PubKey, rep.Latitude, rep.Longitude, rep.AdvertInterval,
		rep.FloodAdvertInterval, rep.DisableFwd, rep.FloodMax, rep.FloodMaxUnscoped, rep.FloodMaxAdvert,
		rep.LoopDetect, rep.PathHashMode, rep.DefaultRegion, rep.AdminPassword, rep.GuestPassword, rep.OwnerInfo, string(regionsJSON))
	if err != nil {
		return fmt.Errorf("upserting repeater: %w", err)
	}
	return nil
}

// Clear removes the repeater row (no repeater configured).
func (r *RepeaterRepo) Clear(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM repeater`); err != nil {
		return fmt.Errorf("clearing repeater: %w", err)
	}
	return nil
}
