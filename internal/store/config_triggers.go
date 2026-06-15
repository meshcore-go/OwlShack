package store

import "database/sql"

// Trigger is a bot rule owned by a companion (companion_id FK). ChannelIDs are
// the companion_channels it listens on, resolved from the trigger_channels join.
// MatchPatterns and Contacts are leaf string lists (newline-encoded TEXT).
type Trigger struct {
	ID                 int64
	CompanionID        int64
	Type               string
	Template           string
	CharLimitBehaviour *string
	MatchPatterns      []string
	Contacts           []string
	RetryTimeout       *int64
	MaxRetries         *int
	PathHashSize       *int
	Schedule           *string
	ChannelIDs         []int64
}

type TriggerRepo struct{ db *sql.DB }

func (r *TriggerRepo) scanRow(s interface{ Scan(...any) error }) (*Trigger, error) {
	var t Trigger
	var match, contacts string
	if err := s.Scan(
		&t.ID, &t.CompanionID, &t.Type, &t.Template, &t.CharLimitBehaviour,
		&match, &contacts, &t.RetryTimeout, &t.MaxRetries, &t.PathHashSize, &t.Schedule,
	); err != nil {
		return nil, err
	}
	t.MatchPatterns = decodeList(match)
	t.Contacts = decodeList(contacts)
	return &t, nil
}

// channelIDs loads the channel ids a trigger listens on, in stable order.
func (r *TriggerRepo) channelIDs(triggerID int64) ([]int64, error) {
	rows, err := r.db.Query(`SELECT channel_id FROM trigger_channels WHERE trigger_id = ? ORDER BY channel_id ASC`, triggerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// List returns every trigger across all companions (for the bots page).
func (r *TriggerRepo) List() ([]Trigger, error) {
	rows, err := r.db.Query(`
		SELECT id, companion_id, type, template, char_limit_behaviour,
		       match_patterns, contacts, retry_timeout, max_retries, path_hash_size, schedule
		FROM triggers ORDER BY companion_id ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		t, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ids, err := r.channelIDs(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].ChannelIDs = ids
	}
	return out, nil
}

func (r *TriggerRepo) ListByCompanion(companionID int64) ([]Trigger, error) {
	rows, err := r.db.Query(`
		SELECT id, companion_id, type, template, char_limit_behaviour,
		       match_patterns, contacts, retry_timeout, max_retries, path_hash_size, schedule
		FROM triggers WHERE companion_id = ? ORDER BY id ASC`, companionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		t, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Resolve channel links after the rows cursor is closed (separate queries).
	for i := range out {
		ids, err := r.channelIDs(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].ChannelIDs = ids
	}
	return out, nil
}

func (r *TriggerRepo) Get(id int64) (*Trigger, error) {
	t, err := r.scanRow(r.db.QueryRow(`
		SELECT id, companion_id, type, template, char_limit_behaviour,
		       match_patterns, contacts, retry_timeout, max_retries, path_hash_size, schedule
		FROM triggers WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if t.ChannelIDs, err = r.channelIDs(t.ID); err != nil {
		return nil, err
	}
	return t, nil
}

func (r *TriggerRepo) Create(t *Trigger) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO triggers
			(companion_id, type, template, char_limit_behaviour, match_patterns, contacts, retry_timeout, max_retries, path_hash_size, schedule)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.CompanionID, t.Type, t.Template, t.CharLimitBehaviour,
		encodeList(t.MatchPatterns), encodeList(t.Contacts),
		t.RetryTimeout, t.MaxRetries, t.PathHashSize, t.Schedule)
	if err != nil {
		return err
	}
	if t.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	if err := setTriggerChannels(tx, t.ID, t.ChannelIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TriggerRepo) Update(t *Trigger) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE triggers SET type=?, template=?, char_limit_behaviour=?, match_patterns=?, contacts=?,
			retry_timeout=?, max_retries=?, path_hash_size=?, schedule=?
		WHERE id=?`,
		t.Type, t.Template, t.CharLimitBehaviour, encodeList(t.MatchPatterns), encodeList(t.Contacts),
		t.RetryTimeout, t.MaxRetries, t.PathHashSize, t.Schedule, t.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM trigger_channels WHERE trigger_id = ?`, t.ID); err != nil {
		return err
	}
	if err := setTriggerChannels(tx, t.ID, t.ChannelIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TriggerRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM triggers WHERE id = ?`, id)
	return err
}

func setTriggerChannels(tx *sql.Tx, triggerID int64, channelIDs []int64) error {
	for _, cid := range channelIDs {
		if _, err := tx.Exec(`INSERT INTO trigger_channels (trigger_id, channel_id) VALUES (?, ?)`, triggerID, cid); err != nil {
			return err
		}
	}
	return nil
}
