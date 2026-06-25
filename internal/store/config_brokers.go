package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Broker is one MQTT broker target. DisallowedPacketTypes is a leaf string list
// (newline-encoded TEXT). Password is a secret (redacted on the wire).
type Broker struct {
	ID                    int64
	Name                  string
	Enabled               bool
	Dedup                 bool
	Transport             string
	Host                  string
	Port                  int
	PacketTopic           *string
	StatusTopic           *string
	DisallowedPacketTypes []string
	RetainStatus          bool
	TLSEnabled            bool
	TLSInsecure           bool
	AuthType              string
	Username              string
	Password              string
	Path                  string
	Audience              string
}

type BrokerRepo struct{ db *sql.DB }

func (r *BrokerRepo) scanRow(s interface{ Scan(...any) error }) (*Broker, error) {
	var b Broker
	var disallowed string
	if err := s.Scan(
		&b.ID, &b.Name, &b.Enabled, &b.Dedup, &b.Transport, &b.Host, &b.Port,
		&b.PacketTopic, &b.StatusTopic, &disallowed, &b.RetainStatus,
		&b.TLSEnabled, &b.TLSInsecure, &b.AuthType, &b.Username, &b.Password, &b.Path, &b.Audience,
	); err != nil {
		return nil, err
	}
	b.DisallowedPacketTypes = decodeList(disallowed)
	return &b, nil
}

const brokerCols = `id, name, enabled, dedup, transport, host, port, packet_topic, status_topic,
	disallowed_packet_types, retain_status, tls_enabled, tls_insecure, auth_type, username, password, path, audience`

func (r *BrokerRepo) List(ctx context.Context) ([]Broker, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+brokerCols+` FROM mqtt_brokers ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("querying brokers: %w", err)
	}
	defer rows.Close()
	var out []Broker
	for rows.Next() {
		b, err := r.scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning broker row: %w", err)
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating brokers: %w", err)
	}
	return out, nil
}

func (r *BrokerRepo) Get(ctx context.Context, id int64) (*Broker, error) {
	b, err := r.scanRow(r.db.QueryRowContext(ctx, `SELECT `+brokerCols+` FROM mqtt_brokers WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("getting broker: %w", err)
	}
	return b, nil
}

func (r *BrokerRepo) Create(ctx context.Context, b *Broker) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO mqtt_brokers
			(name, enabled, dedup, transport, host, port, packet_topic, status_topic,
			 disallowed_packet_types, retain_status, tls_enabled, tls_insecure, auth_type, username, password, path, audience)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.Name, b.Enabled, b.Dedup, b.Transport, b.Host, b.Port, b.PacketTopic, b.StatusTopic,
		encodeList(b.DisallowedPacketTypes), b.RetainStatus, b.TLSEnabled, b.TLSInsecure,
		b.AuthType, b.Username, b.Password, b.Path, b.Audience)
	if err != nil {
		return fmt.Errorf("inserting broker: %w", err)
	}
	b.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("reading inserted broker id: %w", err)
	}
	return nil
}

func (r *BrokerRepo) Update(ctx context.Context, b *Broker) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE mqtt_brokers SET name=?, enabled=?, dedup=?, transport=?, host=?, port=?,
			packet_topic=?, status_topic=?, disallowed_packet_types=?, retain_status=?,
			tls_enabled=?, tls_insecure=?, auth_type=?, username=?, password=?, path=?, audience=?
		WHERE id=?`,
		b.Name, b.Enabled, b.Dedup, b.Transport, b.Host, b.Port, b.PacketTopic, b.StatusTopic,
		encodeList(b.DisallowedPacketTypes), b.RetainStatus, b.TLSEnabled, b.TLSInsecure,
		b.AuthType, b.Username, b.Password, b.Path, b.Audience, b.ID)
	if err != nil {
		return fmt.Errorf("updating broker: %w", err)
	}
	return nil
}

func (r *BrokerRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM mqtt_brokers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting broker: %w", err)
	}
	return nil
}
