package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const DefaultMaxPackets = 10000

type PacketRecord struct {
	ID          int64
	ReceivedAt  time.Time
	Direction   string
	Raw         []byte
	RouteType   *uint8
	PayloadType *uint8
	SNR         *float64
	RSSI        *int8
}

type PacketRepo struct {
	db      *sql.DB
	maxRows int
}

func (r *PacketRepo) Insert(ctx context.Context, p *PacketRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO packets (received_at, direction, raw, route_type, payload_type, snr, rssi)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ReceivedAt, p.Direction, p.Raw, p.RouteType, p.PayloadType, p.SNR, p.RSSI,
	)
	if err != nil {
		return fmt.Errorf("inserting packet: %w", err)
	}
	return r.prune(ctx)
}

func (r *PacketRepo) List(ctx context.Context, limit, offset int) ([]PacketRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, received_at, direction, raw, route_type, payload_type, snr, rssi
		FROM packets
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying packets: %w", err)
	}
	defer rows.Close()

	var packets []PacketRecord
	for rows.Next() {
		var p PacketRecord
		if err := rows.Scan(
			&p.ID, &p.ReceivedAt, &p.Direction, &p.Raw,
			&p.RouteType, &p.PayloadType, &p.SNR, &p.RSSI,
		); err != nil {
			return nil, fmt.Errorf("scanning packet row: %w", err)
		}
		packets = append(packets, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating packets: %w", err)
	}
	return packets, nil
}

func (r *PacketRepo) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM packets").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting packets: %w", err)
	}
	return count, nil
}

func (r *PacketRepo) prune(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM packets WHERE id IN (
			SELECT id FROM packets ORDER BY id ASC LIMIT MAX(0,
				(SELECT COUNT(*) FROM packets) - ?
			)
		)`, r.maxRows)
	if err != nil {
		return fmt.Errorf("pruning packets: %w", err)
	}
	return nil
}
