package store

import (
	"context"
	"database/sql"
	"fmt"
)

type BlockedSenderRepo struct {
	db *sql.DB
}

func (r *BlockedSenderRepo) Block(ctx context.Context, companionID int64, conversationID, sender string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO blocked_senders (companion_id, conversation_id, sender)
		VALUES (?, ?, ?)`, companionID, conversationID, sender)
	if err != nil {
		return fmt.Errorf("blocking sender: %w", err)
	}
	return nil
}

func (r *BlockedSenderRepo) Unblock(ctx context.Context, companionID int64, conversationID, sender string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM blocked_senders
		WHERE companion_id = ? AND conversation_id = ? AND sender = ?`,
		companionID, conversationID, sender)
	if err != nil {
		return fmt.Errorf("unblocking sender: %w", err)
	}
	return nil
}

func (r *BlockedSenderRepo) IsBlocked(ctx context.Context, companionID int64, conversationID, sender string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM blocked_senders
		WHERE companion_id = ? AND conversation_id = ? AND sender = ?`,
		companionID, conversationID, sender).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking blocked sender: %w", err)
	}
	return count > 0, nil
}

func (r *BlockedSenderRepo) List(ctx context.Context, companionID int64, conversationID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT sender FROM blocked_senders
		WHERE companion_id = ? AND conversation_id = ?
		ORDER BY sender`, companionID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("querying blocked senders: %w", err)
	}
	defer rows.Close()

	var senders []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scanning blocked sender row: %w", err)
		}
		senders = append(senders, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating blocked senders: %w", err)
	}
	return senders, nil
}
