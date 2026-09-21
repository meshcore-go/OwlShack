package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Sensor is one configured local sensor. Options are provider-specific and stored as JSON because
// only the provider knows what they mean; everything between here and the provider passes them through.
type Sensor struct {
	ID       int64
	Provider string
	Kind     string
	Name     string
	Options  map[string]string
}

type SensorRepo struct{ db *sql.DB }

func (r *SensorRepo) List(ctx context.Context) ([]Sensor, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, provider, kind, name, options FROM sensors ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("querying sensors: %w", err)
	}
	defer rows.Close()
	var out []Sensor
	for rows.Next() {
		var s Sensor
		var opts string
		if err := rows.Scan(&s.ID, &s.Provider, &s.Kind, &s.Name, &opts); err != nil {
			return nil, fmt.Errorf("scanning sensor row: %w", err)
		}
		if s.Options, err = decodeOptions(opts); err != nil {
			return nil, fmt.Errorf("sensor %d: %w", s.ID, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating sensors: %w", err)
	}
	return out, nil
}

func (r *SensorRepo) Create(ctx context.Context, s *Sensor) error {
	opts, err := encodeOptions(s.Options)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO sensors (provider, kind, name, options) VALUES (?, ?, ?, ?)`,
		s.Provider, s.Kind, s.Name, opts)
	if err != nil {
		return fmt.Errorf("inserting sensor: %w", err)
	}
	if s.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("reading inserted id: %w", err)
	}
	return nil
}

func (r *SensorRepo) Delete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sensors WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting sensor: %w", err)
	}
	return nil
}

// encodeOptions stores an absent map as {} rather than NULL, so a read never has to decide what a
// missing value meant.
func encodeOptions(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encoding sensor options: %w", err)
	}
	return string(b), nil
}

func decodeOptions(s string) (map[string]string, error) {
	if s == "" || s == "{}" {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("decoding sensor options: %w", err)
	}
	return m, nil
}
