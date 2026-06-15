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
	AddedAt     time.Time
	Metadata    ContactMetadata
}

type ContactRepo struct {
	db *sql.DB
}

func (r *ContactRepo) Add(companionID int64, peerPubKey []byte) error {
	_, err := r.db.Exec(`
		INSERT INTO companion_contacts (companion_id, peer_pubkey)
		VALUES (?, ?)
		ON CONFLICT DO NOTHING`,
		companionID, peerPubKey,
	)
	return err
}

func (r *ContactRepo) List(companionID int64) ([]Contact, error) {
	rows, err := r.db.Query(`
		SELECT companion_id, peer_pubkey, added_at, metadata
		FROM companion_contacts
		WHERE companion_id = ?
		ORDER BY added_at DESC`, companionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contacts []Contact
	for rows.Next() {
		var c Contact
		var metaStr string
		if err := rows.Scan(&c.CompanionID, &c.PeerPubKey, &c.AddedAt, &metaStr); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(metaStr), &c.Metadata)
		contacts = append(contacts, c)
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
	var c Contact
	var metaStr string
	err := r.db.QueryRow(`
		SELECT companion_id, peer_pubkey, added_at, metadata
		FROM companion_contacts
		WHERE companion_id = ? AND peer_pubkey = ?`,
		companionID, peerPubKey,
	).Scan(&c.CompanionID, &c.PeerPubKey, &c.AddedAt, &metaStr)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(metaStr), &c.Metadata)
	return &c, nil
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
