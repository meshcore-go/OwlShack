package store

import (
	"database/sql"
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

func (r *PacketRepo) Insert(p *PacketRecord) error {
	_, err := r.db.Exec(`
		INSERT INTO packets (received_at, direction, raw, route_type, payload_type, snr, rssi)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ReceivedAt, p.Direction, p.Raw, p.RouteType, p.PayloadType, p.SNR, p.RSSI,
	)
	if err != nil {
		return err
	}
	return r.prune()
}

func (r *PacketRepo) List(limit, offset int) ([]PacketRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(`
		SELECT id, received_at, direction, raw, route_type, payload_type, snr, rssi
		FROM packets
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var packets []PacketRecord
	for rows.Next() {
		var p PacketRecord
		if err := rows.Scan(
			&p.ID, &p.ReceivedAt, &p.Direction, &p.Raw,
			&p.RouteType, &p.PayloadType, &p.SNR, &p.RSSI,
		); err != nil {
			return nil, err
		}
		packets = append(packets, p)
	}
	return packets, rows.Err()
}

func (r *PacketRepo) Count() (int64, error) {
	var count int64
	err := r.db.QueryRow("SELECT COUNT(*) FROM packets").Scan(&count)
	return count, err
}

func (r *PacketRepo) prune() error {
	_, err := r.db.Exec(`
		DELETE FROM packets WHERE id IN (
			SELECT id FROM packets ORDER BY id ASC LIMIT MAX(0,
				(SELECT COUNT(*) FROM packets) - ?
			)
		)`, r.maxRows)
	return err
}
