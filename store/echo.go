package store

import (
	"database/sql"
	"time"
)

type MessageEcho struct {
	ID           int64
	MessageID    int64
	ReceivedAt   time.Time
	PathHashes   []byte
	PathHashSize int
	Hops         int
	SNR          *float64
	RSSI         *int8
}

type EchoRepo struct {
	db *sql.DB
}

func (r *EchoRepo) Insert(e *MessageEcho) error {
	var exists bool
	err := r.db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM message_echoes WHERE message_id = ? AND path_hashes = ?
			UNION ALL
			SELECT 1 FROM messages WHERE id = ? AND path_hashes = ?
		)`,
		e.MessageID, e.PathHashes, e.MessageID, e.PathHashes,
	).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	res, err := r.db.Exec(`
		INSERT INTO message_echoes (message_id, received_at, path_hashes, path_hash_size, hops, snr, rssi)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.MessageID, e.ReceivedAt, e.PathHashes, e.PathHashSize, e.Hops, e.SNR, e.RSSI,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	e.ID = id
	return nil
}

func (r *EchoRepo) ListByMessage(messageID int64) ([]MessageEcho, error) {
	rows, err := r.db.Query(`
		SELECT id, message_id, received_at, path_hashes, path_hash_size, hops, snr, rssi
		FROM message_echoes
		WHERE message_id = ?
		ORDER BY received_at ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var echoes []MessageEcho
	for rows.Next() {
		var e MessageEcho
		if err := rows.Scan(
			&e.ID, &e.MessageID, &e.ReceivedAt, &e.PathHashes,
			&e.PathHashSize, &e.Hops, &e.SNR, &e.RSSI,
		); err != nil {
			return nil, err
		}
		echoes = append(echoes, e)
	}
	return echoes, rows.Err()
}

func (r *EchoRepo) CountByMessage(messageID int64) (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM message_echoes WHERE message_id = ?`, messageID).Scan(&count)
	return count, err
}
