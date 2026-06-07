package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type ContactMetadata struct {
	IsRepeater      bool   `json:"isRepeater,omitempty"`
	RepeaterPassword string `json:"repeaterPassword,omitempty"`
}

type Contact struct {
	CompanionID string
	PeerPubKey  []byte
	AddedAt     time.Time
	Metadata    ContactMetadata
}

type ContactRepo struct {
	db *sql.DB
}

func (r *ContactRepo) Add(companionID string, peerPubKey []byte) error {
	_, err := r.db.Exec(`
		INSERT INTO companion_contacts (companion_id, peer_pubkey)
		VALUES (?, ?)
		ON CONFLICT DO NOTHING`,
		companionID, peerPubKey,
	)
	return err
}

func (r *ContactRepo) List(companionID string) ([]Contact, error) {
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

func (r *ContactRepo) UpdateMetadata(companionID string, peerPubKey []byte, meta ContactMetadata) error {
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

func (r *ContactRepo) Get(companionID string, peerPubKey []byte) (*Contact, error) {
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

func (r *ContactRepo) Delete(companionID string, peerPubKey []byte) error {
	_, err := r.db.Exec(`
		DELETE FROM companion_contacts
		WHERE companion_id = ? AND peer_pubkey = ?`,
		companionID, peerPubKey,
	)
	return err
}

func (r *ContactRepo) DeleteAll(companionID string) error {
	_, err := r.db.Exec("DELETE FROM companion_contacts WHERE companion_id = ?", companionID)
	return err
}
