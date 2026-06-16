package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type ContactMetadata struct {
	IsRepeater       bool   `json:"isRepeater,omitempty"`
	RepeaterPassword string `json:"repeaterPassword,omitempty"`
	RoomPassword     string `json:"roomPassword,omitempty"`

	// Monitor enables background analytics/history polling for this node. Set
	// via the UI alongside the repeater password — runtime per-node state, same
	// pattern as RepeaterPassword (not file config).
	Monitor bool `json:"monitor,omitempty"`
	// MonitorIntervalSecs overrides the default poll cadence for this node.
	// 0 = use the poller's built-in default.
	MonitorIntervalSecs int64 `json:"monitorIntervalSecs,omitempty"`
	// MonitorProbes selects which request bundles the poller runs for this node
	// ("status", "telemetry", "neighbors"). Empty/nil = all (the default). The
	// firmware returns each bundle whole, so this is the real RF-airtime lever.
	MonitorProbes []string `json:"monitorProbes,omitempty"`
	// MonitorRetrySecs overrides how soon to re-attempt after a failed poll.
	// 0 = use the poller's built-in default.
	MonitorRetrySecs int64 `json:"monitorRetrySecs,omitempty"`
}

type Contact struct {
	CompanionID int64
	PeerPubKey  []byte
	// A contact is a self-contained address-book record: it caches its own
	// identity, location, routing path, feature flags and last-seen (refreshed
	// from adverts / path learning), so it survives deletion of the discovered
	// peer intact and owns its path per-companion.
	Name            string
	Type            string
	Lat             int32
	Lon             int32
	Feat1           uint16
	Feat2           uint16
	OutPath         []byte
	OutPathHashSize uint8
	LastSeen        time.Time // zero when never heard
	LastAdvertTS    uint32
	AddedAt         time.Time
	Metadata        ContactMetadata
}

const contactColumns = `companion_id, peer_pubkey, name, type, lat, lon,
	feat1, feat2, out_path, out_path_hash_size, last_seen, last_advert_ts,
	added_at, metadata`

func scanContact(s interface{ Scan(...any) error }) (*Contact, error) {
	var c Contact
	var metaStr string
	var feat1, feat2, lastAdvertTS int64
	var lastSeen sql.NullTime
	if err := s.Scan(
		&c.CompanionID, &c.PeerPubKey, &c.Name, &c.Type, &c.Lat, &c.Lon,
		&feat1, &feat2, &c.OutPath, &c.OutPathHashSize, &lastSeen, &lastAdvertTS,
		&c.AddedAt, &metaStr,
	); err != nil {
		return nil, err
	}
	c.Feat1 = uint16(feat1)
	c.Feat2 = uint16(feat2)
	c.LastAdvertTS = uint32(lastAdvertTS)
	if lastSeen.Valid {
		c.LastSeen = lastSeen.Time
	}
	json.Unmarshal([]byte(metaStr), &c.Metadata)
	return &c, nil
}

type ContactRepo struct {
	db *sql.DB
}

func (r *ContactRepo) Add(companionID int64, peerPubKey []byte, name, contactType string) error {
	// On re-add, only overwrite the cached identity when the new value is
	// non-empty, so adding by bare pubkey never blanks a known name.
	_, err := r.db.Exec(`
		INSERT INTO companion_contacts (companion_id, peer_pubkey, name, type)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(companion_id, peer_pubkey) DO UPDATE SET
			name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE companion_contacts.name END,
			type = CASE WHEN excluded.type <> '' THEN excluded.type ELSE companion_contacts.type END`,
		companionID, peerPubKey, name, contactType,
	)
	return err
}

// RefreshFromAdvert updates every companion's contact row for this peer when an
// advert is heard, so a contact's cached record stays current. Identity (name/
// type) is only overwritten when non-empty; location only when the advert
// actually carries one (hasLocation) — so a no-GPS advert never wipes a
// hand-set location, while a real one wins. No-op unless the peer is a contact.
func (r *ContactRepo) RefreshFromAdvert(
	peerPubKey []byte, name, contactType string,
	lat, lon int32, feat1, feat2 uint16, lastSeen time.Time, lastAdvertTS uint32,
	hasLocation bool,
) error {
	_, err := r.db.Exec(`
		UPDATE companion_contacts SET
			name           = CASE WHEN ? <> '' THEN ? ELSE name END,
			type           = CASE WHEN ? <> '' THEN ? ELSE type END,
			lat            = CASE WHEN ? THEN ? ELSE lat END,
			lon            = CASE WHEN ? THEN ? ELSE lon END,
			feat1          = ?,
			feat2          = ?,
			last_seen      = ?,
			last_advert_ts = ?
		WHERE peer_pubkey = ?`,
		name, name, contactType, contactType,
		hasLocation, lat, hasLocation, lon,
		feat1, feat2, lastSeen, lastAdvertTS,
		peerPubKey,
	)
	return err
}

// UpdateOutPath persists a learned route onto this companion's contact row only.
// A contact belongs to a single companion and companions never share a route to
// a peer, so the path is scoped by companion_id — a DM then routes from it
// directly without the in-memory peer table, and it survives a peer sweep.
func (r *ContactRepo) UpdateOutPath(companionID int64, peerPubKey []byte, path []byte, hashSize uint8) error {
	_, err := r.db.Exec(`
		UPDATE companion_contacts SET out_path = ?, out_path_hash_size = ?
		WHERE companion_id = ? AND peer_pubkey = ?`,
		path, hashSize, companionID, peerPubKey,
	)
	return err
}

// SetLocation hand-sets a contact's location. "Advert wins" still applies: a
// later advert carrying a position will overwrite this (RefreshFromAdvert).
func (r *ContactRepo) SetLocation(companionID int64, peerPubKey []byte, lat, lon int32) error {
	_, err := r.db.Exec(`
		UPDATE companion_contacts SET lat = ?, lon = ?
		WHERE companion_id = ? AND peer_pubkey = ?`,
		lat, lon, companionID, peerPubKey,
	)
	return err
}

func (r *ContactRepo) List(companionID int64) ([]Contact, error) {
	rows, err := r.db.Query(`
		SELECT `+contactColumns+`
		FROM companion_contacts
		WHERE companion_id = ?
		ORDER BY added_at DESC`, companionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		contacts = append(contacts, *c)
	}
	return contacts, rows.Err()
}

func (r *ContactRepo) UpdateMetadata(companionID int64, peerPubKey []byte, meta ContactMetadata) error {
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		UPDATE companion_contacts SET metadata = ?
		WHERE companion_id = ? AND peer_pubkey = ?`,
		string(metaJSON), companionID, peerPubKey,
	)
	return err
}

func (r *ContactRepo) Get(companionID int64, peerPubKey []byte) (*Contact, error) {
	return scanContact(r.db.QueryRow(`
		SELECT `+contactColumns+`
		FROM companion_contacts
		WHERE companion_id = ? AND peer_pubkey = ?`,
		companionID, peerPubKey,
	))
}

func (r *ContactRepo) Delete(companionID int64, peerPubKey []byte) error {
	_, err := r.db.Exec(`
		DELETE FROM companion_contacts
		WHERE companion_id = ? AND peer_pubkey = ?`,
		companionID, peerPubKey,
	)
	return err
}

func (r *ContactRepo) DeleteAll(companionID int64) error {
	_, err := r.db.Exec("DELETE FROM companion_contacts WHERE companion_id = ?", companionID)
	return err
}
