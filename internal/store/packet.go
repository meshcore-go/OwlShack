package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

const DefaultMaxPackets = 10000

// PacketFieldsFromPkt returns the hex packet hash and hex hop-path of a parsed
// packet. This is the single canonical derivation of those two values: the RX
// path (packet logger), the API read path, and the migration backfill all go
// through it (directly or via derivePacketFields) so the stored, broadcast,
// and displayed forms can never drift.
func PacketFieldsFromPkt(pkt *meshcore.Packet) (packetHash, path string) {
	h := pkt.PacketHash()
	return hex.EncodeToString(h[:]), hex.EncodeToString(pkt.Path)
}

// derivePacketFields is the raw-bytes form of PacketFieldsFromPkt, used where
// no parsed packet is on hand (the migration backfill, and the Insert fallback
// for callers that didn't pre-derive). Unparseable bytes yield empty strings
// (still searchable as "").
func derivePacketFields(raw []byte) (packetHash, path string) {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return "", ""
	}
	return PacketFieldsFromPkt(pkt)
}

// PacketFilter narrows a packet listing. The zero value lists everything.
type PacketFilter struct {
	PayloadType *uint8 // nil = any type
	Search      string // substring match against packet_hash or path (hex); "" = no search
}

type PacketRecord struct {
	ID          int64
	ReceivedAt  time.Time
	Direction   string
	Raw         []byte
	RouteType   *uint8
	PayloadType *uint8
	SNR         *float64
	RSSI        *int8
	// PacketHash and Path are the indexed search columns. Callers that already
	// parsed the packet should set them (via PacketFieldsFromPkt) to avoid a
	// re-parse; Insert derives them from Raw when both are left empty.
	PacketHash string
	Path       string
}

type PacketRepo struct {
	db      *sql.DB
	maxRows int
}

func (r *PacketRepo) Insert(ctx context.Context, p *PacketRecord) error {
	packetHash, path := p.PacketHash, p.Path
	if packetHash == "" && path == "" {
		packetHash, path = derivePacketFields(p.Raw)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO packets (received_at, direction, raw, route_type, payload_type, snr, rssi, packet_hash, path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ReceivedAt, p.Direction, p.Raw, p.RouteType, p.PayloadType, p.SNR, p.RSSI, packetHash, path,
	)
	if err != nil {
		return fmt.Errorf("inserting packet: %w", err)
	}
	return r.prune(ctx)
}

func (r *PacketRepo) List(ctx context.Context, limit, offset int, filter PacketFilter) ([]PacketRecord, error) {
	if limit <= 0 {
		limit = 100
	}

	where := ""
	var args []any
	if filter.PayloadType != nil {
		where += " AND payload_type = ?"
		args = append(args, *filter.PayloadType)
	}
	if q := strings.ToLower(strings.TrimSpace(filter.Search)); q != "" {
		// packet_hash and path are stored as lowercase hex, so a lowercased
		// needle makes instr() a case-insensitive substring match.
		where += " AND (instr(packet_hash, ?) > 0 OR instr(path, ?) > 0)"
		args = append(args, q, q)
	}
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, received_at, direction, raw, route_type, payload_type, snr, rssi
		FROM packets
		WHERE 1=1`+where+`
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, args...)
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
