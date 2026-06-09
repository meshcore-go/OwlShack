package store

import (
	"database/sql"
	"time"
)

type Conversation struct {
	ID          string
	Type        string // "channel" or "contact"
	Name        string
	LastMessage *ConversationMessage
	UnreadCount int
	LastActive  time.Time
}

type ConversationMessage struct {
	Text      string
	Sender    string
	Direction string
	Timestamp time.Time
}

type ConversationRepo struct {
	db *sql.DB
}

type ContactInfo struct {
	PubKeyHex string
	Name      string
}

func (r *ConversationRepo) List(companionID string, channelNames []string, contacts []ContactInfo) ([]Conversation, error) {
	convos := make([]Conversation, 0)

	for _, ch := range channelNames {
		conv, err := r.channelConversation(companionID, ch)
		if err != nil {
			return nil, err
		}
		convos = append(convos, *conv)
	}

	for _, ct := range contacts {
		conv, err := r.contactConversation(companionID, ct)
		if err != nil {
			return nil, err
		}
		convos = append(convos, *conv)
	}

	return convos, nil
}

func (r *ConversationRepo) channelConversation(companionID, channel string) (*Conversation, error) {
	conv := &Conversation{
		ID:   "channel:" + channel,
		Type: "channel",
		Name: channel,
	}

	var text, sender, direction sql.NullString
	var ts sql.NullTime
	err := r.db.QueryRow(`
		SELECT text, sender, direction, timestamp
		FROM messages
		WHERE companion_id = ? AND channel = ?
		ORDER BY id DESC LIMIT 1`,
		companionID, channel,
	).Scan(&text, &sender, &direction, &ts)

	if err == nil && text.Valid {
		conv.LastMessage = &ConversationMessage{
			Text:      text.String,
			Sender:    sender.String,
			Direction: direction.String,
			Timestamp: ts.Time,
		}
		conv.LastActive = ts.Time
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	var lastReadID int64
	_ = r.db.QueryRow(`
		SELECT last_read_id FROM conversation_reads
		WHERE companion_id = ? AND conversation_id = ?`,
		companionID, "channel:"+channel,
	).Scan(&lastReadID)

	var unread int
	err = r.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE companion_id = ? AND channel = ? AND id > ? AND direction = 'rx'`,
		companionID, channel, lastReadID,
	).Scan(&unread)
	if err != nil {
		return nil, err
	}
	conv.UnreadCount = unread

	return conv, nil
}

func (r *ConversationRepo) contactConversation(companionID string, ct ContactInfo) (*Conversation, error) {
	channelKey := "dm:" + ct.PubKeyHex
	conv := &Conversation{
		ID:   "contact:" + ct.PubKeyHex,
		Type: "contact",
		Name: ct.Name,
	}

	var text, sender, direction sql.NullString
	var ts sql.NullTime
	err := r.db.QueryRow(`
		SELECT text, sender, direction, timestamp
		FROM messages
		WHERE companion_id = ? AND channel = ?
		ORDER BY id DESC LIMIT 1`,
		companionID, channelKey,
	).Scan(&text, &sender, &direction, &ts)

	if err == nil && text.Valid {
		conv.LastMessage = &ConversationMessage{
			Text:      text.String,
			Sender:    sender.String,
			Direction: direction.String,
			Timestamp: ts.Time,
		}
		conv.LastActive = ts.Time
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	var lastReadID int64
	_ = r.db.QueryRow(`
		SELECT last_read_id FROM conversation_reads
		WHERE companion_id = ? AND conversation_id = ?`,
		companionID, "contact:"+ct.PubKeyHex,
	).Scan(&lastReadID)

	var unread int
	err = r.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE companion_id = ? AND channel = ? AND id > ? AND direction = 'rx'`,
		companionID, channelKey, lastReadID,
	).Scan(&unread)
	if err != nil {
		return nil, err
	}
	conv.UnreadCount = unread

	return conv, nil
}

func (r *ConversationRepo) MarkRead(companionID, conversationID string, lastReadID int64) error {
	_, err := r.db.Exec(`
		INSERT INTO conversation_reads (companion_id, conversation_id, last_read_id)
		VALUES (?, ?, ?)
		ON CONFLICT (companion_id, conversation_id)
		DO UPDATE SET last_read_id = MAX(excluded.last_read_id, conversation_reads.last_read_id)`,
		companionID, conversationID, lastReadID,
	)
	return err
}
