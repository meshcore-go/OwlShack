package store

import (
	"database/sql"
	"time"
)

const DefaultMaxMessages = 5000

type Message struct {
	ID           int64
	CompanionID  string
	Channel      string
	ChannelHash  byte
	Sender       string
	Text         string
	Direction    string
	Timestamp    time.Time
	SNR          *float64
	RSSI         *int8
	RepeatCount  *int
	PathHashes   []byte
	PathHashSize *int
	Hops         *int
	Status       *string
}

type MessageRepo struct {
	db      *sql.DB
	maxRows int
}

func (r *MessageRepo) Insert(m *Message) error {
	res, err := r.db.Exec(`
		INSERT INTO messages (companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.CompanionID, m.Channel, m.ChannelHash, m.Sender, m.Text, m.Direction, m.Timestamp, m.SNR, m.RSSI, m.RepeatCount,
		m.PathHashes, m.PathHashSize, m.Hops, m.Status,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	m.ID = id
	return r.prune()
}

func (r *MessageRepo) UpdateStatus(id int64, status string) error {
	_, err := r.db.Exec(`UPDATE messages SET status = ? WHERE id = ?`, status, id)
	return err
}

func (r *MessageRepo) GetByID(id int64) (*Message, error) {
	var m Message
	err := r.db.QueryRow(`
		SELECT id, companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status
		FROM messages WHERE id = ?`, id).Scan(
		&m.ID, &m.CompanionID, &m.Channel, &m.ChannelHash,
		&m.Sender, &m.Text, &m.Direction, &m.Timestamp,
		&m.SNR, &m.RSSI, &m.RepeatCount,
		&m.PathHashes, &m.PathHashSize, &m.Hops, &m.Status,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *MessageRepo) IncrementRepeatCount(id int64) (int, error) {
	var count int
	err := r.db.QueryRow(`
		UPDATE messages SET confirmed = COALESCE(confirmed, 0) + 1 WHERE id = ?
		RETURNING confirmed`, id).Scan(&count)
	return count, err
}

func (r *MessageRepo) List(companionID, channel string, limit, offset int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT id, companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status
		FROM messages
		WHERE companion_id = ? AND channel = ?
		ORDER BY id DESC
		LIMIT ? OFFSET ?`

	rows, err := r.db.Query(query, companionID, channel, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.CompanionID, &m.Channel, &m.ChannelHash,
			&m.Sender, &m.Text, &m.Direction, &m.Timestamp,
			&m.SNR, &m.RSSI, &m.RepeatCount,
			&m.PathHashes, &m.PathHashSize, &m.Hops, &m.Status,
		); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (r *MessageRepo) ListAfter(companionID, channel string, afterID int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}

	rows, err := r.db.Query(`
		SELECT id, companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status
		FROM messages
		WHERE companion_id = ? AND channel = ? AND id > ?
		ORDER BY id ASC
		LIMIT ?`, companionID, channel, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.CompanionID, &m.Channel, &m.ChannelHash,
			&m.Sender, &m.Text, &m.Direction, &m.Timestamp,
			&m.SNR, &m.RSSI, &m.RepeatCount,
			&m.PathHashes, &m.PathHashSize, &m.Hops, &m.Status,
		); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (r *MessageRepo) ListAll(companionID string, limit, offset int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := r.db.Query(`
		SELECT id, companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status
		FROM messages
		WHERE companion_id = ?
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, companionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.CompanionID, &m.Channel, &m.ChannelHash,
			&m.Sender, &m.Text, &m.Direction, &m.Timestamp,
			&m.SNR, &m.RSSI, &m.RepeatCount,
			&m.PathHashes, &m.PathHashSize, &m.Hops, &m.Status,
		); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// LatestRx returns the most recently inserted rx message for a channel, or nil.
func (r *MessageRepo) LatestRx(companionID, channel string) (*Message, error) {
	var m Message
	err := r.db.QueryRow(`
		SELECT id, companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status
		FROM messages
		WHERE companion_id = ? AND channel = ? AND direction = 'rx'
		ORDER BY id DESC LIMIT 1`, companionID, channel).Scan(
		&m.ID, &m.CompanionID, &m.Channel, &m.ChannelHash,
		&m.Sender, &m.Text, &m.Direction, &m.Timestamp,
		&m.SNR, &m.RSSI, &m.RepeatCount,
		&m.PathHashes, &m.PathHashSize, &m.Hops, &m.Status,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *MessageRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

func (r *MessageRepo) DeleteByChannel(companionID, channel string) error {
	_, err := r.db.Exec(`DELETE FROM messages WHERE companion_id = ? AND channel = ?`, companionID, channel)
	return err
}

func (r *MessageRepo) DistinctSenders(companionID, channel string) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT sender FROM messages
		WHERE companion_id = ? AND channel = ? AND direction = 'rx' AND sender != ''
		ORDER BY sender`, companionID, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var senders []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		senders = append(senders, s)
	}
	return senders, rows.Err()
}

func (r *MessageRepo) prune() error {
	_, err := r.db.Exec(`
		DELETE FROM messages WHERE id IN (
			SELECT id FROM messages ORDER BY id ASC LIMIT MAX(0,
				(SELECT COUNT(*) FROM messages) - ?
			)
		)`, r.maxRows)
	return err
}
