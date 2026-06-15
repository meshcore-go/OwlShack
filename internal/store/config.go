package store

import (
	"database/sql"
	"strings"
)

// This file and its config_* siblings hold the relational config schema (the
// config tables in migrateV1): one row per entity with a surrogate INTEGER id,
// so names, pubkeys and private keys are all mutable columns that nothing
// references. Repos are
// dumb persistence — validation and the supervisor reload are orchestrated one
// layer up (internal/app), after a write.

// encodeList / decodeList store a leaf string list (a trigger's match patterns
// or contacts, a broker's disallowed packet types) in a single TEXT column,
// newline-separated so a value may legally contain a comma.
func encodeList(items []string) string {
	return strings.Join(items, "\n")
}

func decodeList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Settings is the single-row base configuration (radio + process).
type Settings struct {
	LogLevel       *string
	ConnectionType string
	Connection     *string
	BaudRate       *int
	Freq           *float64
	BW             *float64
	SF             *int
	CR             *int
	TX             *int
	ListenAddr     *string
	SetupComplete  bool
}

type SettingsRepo struct{ db *sql.DB }

func (r *SettingsRepo) Get() (*Settings, error) {
	var s Settings
	err := r.db.QueryRow(`
		SELECT log_level, connection_type, connection, baud_rate, freq, bw, sf, cr, tx, listen_addr, setup_complete
		FROM settings WHERE id = 1`).Scan(
		&s.LogLevel, &s.ConnectionType, &s.Connection, &s.BaudRate,
		&s.Freq, &s.BW, &s.SF, &s.CR, &s.TX, &s.ListenAddr, &s.SetupComplete,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SettingsRepo) Set(s *Settings) error {
	_, err := r.db.Exec(`
		INSERT INTO settings
			(id, log_level, connection_type, connection, baud_rate, freq, bw, sf, cr, tx, listen_addr, setup_complete)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			log_level=excluded.log_level, connection_type=excluded.connection_type,
			connection=excluded.connection, baud_rate=excluded.baud_rate, freq=excluded.freq,
			bw=excluded.bw, sf=excluded.sf, cr=excluded.cr, tx=excluded.tx,
			listen_addr=excluded.listen_addr, setup_complete=excluded.setup_complete`,
		s.LogLevel, s.ConnectionType, s.Connection, s.BaudRate,
		s.Freq, s.BW, s.SF, s.CR, s.TX, s.ListenAddr, s.SetupComplete,
	)
	return err
}

// MqttSettings is the single-row top-level MQTT config. NodeCompanionID selects
// the feeding companion by surrogate id (not name), so renaming it is harmless.
type MqttSettings struct {
	Enabled         *bool
	NodeCompanionID *int64
	IataCode        *string
	StatusInterval  *int
	Owner           *string
	Email           *string
}

type MqttRepo struct{ db *sql.DB }

func (r *MqttRepo) Get() (*MqttSettings, error) {
	var m MqttSettings
	err := r.db.QueryRow(`
		SELECT enabled, node_companion_id, iata_code, status_interval, owner, email
		FROM mqtt_settings WHERE id = 1`).Scan(
		&m.Enabled, &m.NodeCompanionID, &m.IataCode, &m.StatusInterval, &m.Owner, &m.Email,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *MqttRepo) Set(m *MqttSettings) error {
	_, err := r.db.Exec(`
		INSERT INTO mqtt_settings
			(id, enabled, node_companion_id, iata_code, status_interval, owner, email)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled=excluded.enabled, node_companion_id=excluded.node_companion_id,
			iata_code=excluded.iata_code, status_interval=excluded.status_interval,
			owner=excluded.owner, email=excluded.email`,
		m.Enabled, m.NodeCompanionID, m.IataCode, m.StatusInterval, m.Owner, m.Email,
	)
	return err
}
