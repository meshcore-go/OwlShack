package store

import "database/sql"

type BlockedSenderRepo struct {
	db *sql.DB
}

func (r *BlockedSenderRepo) Block(companionID, conversationID, sender string) error {
	_, err := r.db.Exec(`
		INSERT OR IGNORE INTO blocked_senders (companion_id, conversation_id, sender)
		VALUES (?, ?, ?)`, companionID, conversationID, sender)
	return err
}

func (r *BlockedSenderRepo) Unblock(companionID, conversationID, sender string) error {
	_, err := r.db.Exec(`
		DELETE FROM blocked_senders
		WHERE companion_id = ? AND conversation_id = ? AND sender = ?`,
		companionID, conversationID, sender)
	return err
}

func (r *BlockedSenderRepo) IsBlocked(companionID, conversationID, sender string) (bool, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM blocked_senders
		WHERE companion_id = ? AND conversation_id = ? AND sender = ?`,
		companionID, conversationID, sender).Scan(&count)
	return count > 0, err
}

func (r *BlockedSenderRepo) List(companionID, conversationID string) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT sender FROM blocked_senders
		WHERE companion_id = ? AND conversation_id = ?
		ORDER BY sender`, companionID, conversationID)
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
