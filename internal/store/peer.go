package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Peer struct {
	PubKey          []byte
	Name            string
	Type            string
	Lat             int32
	Lon             int32
	Feat1           uint16
	Feat2           uint16
	OutPath         []byte
	OutPathHashSize uint8
	LastAdvertTS    uint32
	LastSeen        time.Time
	SNR             *float64
	RSSI            *int8
}

type PeerRepo struct {
	db *sql.DB
}

func (r *PeerRepo) Upsert(ctx context.Context, p *Peer) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO discovered_peers (pubkey, name, type, lat, lon, feat1, feat2, out_path, out_path_hash_size, last_advert_ts, last_seen, snr, rssi)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			name               = excluded.name,
			type               = excluded.type,
			lat                = excluded.lat,
			lon                = excluded.lon,
			feat1              = excluded.feat1,
			feat2              = excluded.feat2,
			out_path           = excluded.out_path,
			out_path_hash_size = excluded.out_path_hash_size,
			last_advert_ts     = excluded.last_advert_ts,
			last_seen          = excluded.last_seen,
			snr                = excluded.snr,
			rssi               = excluded.rssi`,
		p.PubKey, p.Name, p.Type, p.Lat, p.Lon, p.Feat1, p.Feat2,
		p.OutPath, p.OutPathHashSize, p.LastAdvertTS, p.LastSeen, p.SNR, p.RSSI,
	)
	if err != nil {
		return fmt.Errorf("upserting peer: %w", err)
	}
	return nil
}

func (r *PeerRepo) List(ctx context.Context) ([]Peer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT pubkey, name, type, lat, lon, feat1, feat2, out_path, out_path_hash_size, last_advert_ts, last_seen, snr, rssi
		FROM discovered_peers
		ORDER BY last_seen DESC`)
	if err != nil {
		return nil, fmt.Errorf("querying peers: %w", err)
	}
	defer rows.Close()
	return scanPeers(rows)
}

func (r *PeerRepo) UpdateOutPath(ctx context.Context, pubkey []byte, path []byte, hashSize uint8) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE discovered_peers SET out_path = ?, out_path_hash_size = ? WHERE pubkey = ?`,
		path, hashSize, pubkey)
	if err != nil {
		return fmt.Errorf("updating peer out_path: %w", err)
	}
	return nil
}

func (r *PeerRepo) Delete(ctx context.Context, pubkey []byte) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM discovered_peers WHERE pubkey = ?", pubkey)
	if err != nil {
		return fmt.Errorf("deleting peer: %w", err)
	}
	return nil
}

// DeleteMany removes a batch of peers (chunked to stay under SQLite's
// bound-parameter limit).
func (r *PeerRepo) DeleteMany(ctx context.Context, pubkeys [][]byte) error {
	return eachInChunk(ctx, pubkeys, func(ctx context.Context, placeholders string, args []any) error {
		_, err := r.db.ExecContext(ctx,
			"DELETE FROM discovered_peers WHERE pubkey IN ("+placeholders+")", args...,
		)
		if err != nil {
			return fmt.Errorf("deleting peers: %w", err)
		}
		return nil
	})
}

func (r *PeerRepo) GetByPubKey(ctx context.Context, pubkey []byte) (*Peer, error) {
	var p Peer
	var feat1, feat2, lastAdvertTS int64
	var snr sql.NullFloat64
	var rssi sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT pubkey, name, type, lat, lon, feat1, feat2, out_path, out_path_hash_size, last_advert_ts, last_seen, snr, rssi
		FROM discovered_peers WHERE pubkey = ?`, pubkey,
	).Scan(&p.PubKey, &p.Name, &p.Type, &p.Lat, &p.Lon,
		&feat1, &feat2, &p.OutPath, &p.OutPathHashSize, &lastAdvertTS, &p.LastSeen, &snr, &rssi)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting peer by pubkey: %w", err)
	}
	p.Feat1 = uint16(feat1)
	p.Feat2 = uint16(feat2)
	p.LastAdvertTS = uint32(lastAdvertTS)
	if snr.Valid {
		v := snr.Float64
		p.SNR = &v
	}
	if rssi.Valid {
		v := int8(rssi.Int64)
		p.RSSI = &v
	}
	return &p, nil
}

func (r *PeerRepo) LoadAll(ctx context.Context) ([]Peer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT pubkey, name, type, lat, lon, feat1, feat2, out_path, out_path_hash_size, last_advert_ts, last_seen, snr, rssi
		FROM discovered_peers`)
	if err != nil {
		return nil, fmt.Errorf("querying peers: %w", err)
	}
	defer rows.Close()
	return scanPeers(rows)
}

func scanPeers(rows *sql.Rows) ([]Peer, error) {
	var peers []Peer
	for rows.Next() {
		var p Peer
		var feat1, feat2 int64
		var snr sql.NullFloat64
		var rssi sql.NullInt64
		var lastAdvertTS int64
		if err := rows.Scan(
			&p.PubKey, &p.Name, &p.Type, &p.Lat, &p.Lon,
			&feat1, &feat2, &p.OutPath, &p.OutPathHashSize, &lastAdvertTS,
			&p.LastSeen, &snr, &rssi,
		); err != nil {
			return nil, err
		}
		p.Feat1 = uint16(feat1)
		p.Feat2 = uint16(feat2)
		p.LastAdvertTS = uint32(lastAdvertTS)
		if snr.Valid {
			v := snr.Float64
			p.SNR = &v
		}
		if rssi.Valid {
			v := int8(rssi.Int64)
			p.RSSI = &v
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

func (r *PeerRepo) LookupByHash(ctx context.Context, hash []byte) ([]string, error) {
	if len(hash) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT name FROM discovered_peers
		WHERE substr(pubkey, 1, ?) = ?`, len(hash), hash)
	if err != nil {
		return nil, fmt.Errorf("querying peers by hash: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning peer name row: %w", err)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating peer names: %w", err)
	}
	return names, nil
}
