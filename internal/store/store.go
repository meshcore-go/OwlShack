package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

// writerQueueDepth bounds how many pending write closures the store will
// buffer. Reached only when sqlite is contended for a sustained period; the
// RX goroutine drops on full instead of stalling.
const writerQueueDepth = 1024

type Store struct {
	db             *sql.DB
	Peers          *PeerRepo
	Contacts       *ContactRepo
	Packets        *PacketRepo
	Messages       *MessageRepo
	Conversations  *ConversationRepo
	Echoes         *EchoRepo
	BlockedSenders *BlockedSenderRepo
	Metrics        *MetricsRepo
	AppConfig      *AppConfigRepo
	Settings       *SettingsRepo
	Mqtt           *MqttRepo
	Brokers        *BrokerRepo
	Companions     *CompanionRepo
	Channels       *ChannelRepo
	Triggers       *TriggerRepo

	writerCh   chan func()
	writerDone chan struct{}
	closeOnce  sync.Once
	dropped    atomic.Uint64
}

func Open(path string) (*Store, error) {
	// modernc.org/sqlite (NOT mattn/go-sqlite3) configures pragmas via "_pragma="
	// query params applied to every pooled connection. The old mattn-style
	// "_journal_mode=WAL&_busy_timeout=..." form was silently ignored, leaving the
	// DB in rollback-journal mode (writer takes an exclusive lock) with no busy
	// timeout — so concurrent reads raced the writer goroutine and failed with
	// SQLITE_BUSY. WAL lets readers run alongside the single writer; busy_timeout
	// makes the rest wait instead of erroring.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{
		db:             db,
		Peers:          &PeerRepo{db: db},
		Contacts:       &ContactRepo{db: db},
		Packets:        &PacketRepo{db: db, maxRows: DefaultMaxPackets},
		Messages:       &MessageRepo{db: db, maxRows: DefaultMaxMessages},
		Conversations:  &ConversationRepo{db: db},
		Echoes:         &EchoRepo{db: db},
		BlockedSenders: &BlockedSenderRepo{db: db},
		Metrics:        &MetricsRepo{db: db},
		AppConfig:      &AppConfigRepo{db: db},
		Settings:       &SettingsRepo{db: db},
		Mqtt:           &MqttRepo{db: db},
		Brokers:        &BrokerRepo{db: db},
		Companions:     &CompanionRepo{db: db},
		Channels:       &ChannelRepo{db: db},
		Triggers:       &TriggerRepo{db: db},
		writerCh:       make(chan func(), writerQueueDepth),
		writerDone:     make(chan struct{}),
	}

	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	go s.writerLoop()

	return s, nil
}

// WriteAsync runs fn on the dedicated writer goroutine, decoupling callers
// from sqlite latency. Returns true if the closure was queued, false if the
// queue was full (closure dropped). Safe to call from any goroutine,
// including the modem RX dispatch thread — never blocks.
//
// fn typically performs an Insert/Update plus any follow-up work (WS
// broadcast, dependent inserts) that needs to run after the write commits.
func (s *Store) WriteAsync(fn func()) bool {
	select {
	case s.writerCh <- fn:
		return true
	default:
		dropped := s.dropped.Add(1)
		if dropped == 1 || dropped%100 == 0 {
			slog.Warn("store writer queue full, dropping write", "dropped", dropped)
		}
		return false
	}
}

// WriteSync runs fn on the writer goroutine and blocks until it returns.
// Use when the caller needs side effects (e.g. an Insert's auto-generated ID)
// before continuing. Do NOT call from the modem RX dispatch thread or from
// within another writer-loop closure — both will deadlock.
func (s *Store) WriteSync(fn func()) {
	done := make(chan struct{})
	s.writerCh <- func() {
		defer close(done)
		fn()
	}
	<-done
}

// QueueLen returns the current depth of the writer queue, for stats/diagnostics.
func (s *Store) QueueLen() int {
	return len(s.writerCh)
}

// Dropped returns the running count of writes dropped due to a full queue.
func (s *Store) Dropped() uint64 {
	return s.dropped.Load()
}

func (s *Store) writerLoop() {
	defer close(s.writerDone)
	for fn := range s.writerCh {
		fn()
	}
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.writerCh)
		<-s.writerDone
	})
	return s.db.Close()
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	migrations := []func(*sql.DB) error{
		migrateV1,
		migrateV2,
		migrateV3,
		migrateV4,
		migrateV5,
		migrateV6,
	}

	for i := version; i < len(migrations); i++ {
		if err := migrations[i](s.db); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("setting schema version %d: %w", i+1, err)
		}
	}

	return nil
}

// migrateV1 creates the full baseline schema (earlier incremental migrations
// were squashed into it). snr columns hold real decibels (REAL); rssi is whole
// dBm (INTEGER).
func migrateV1(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS discovered_peers (
			pubkey             BLOB PRIMARY KEY,
			name               TEXT NOT NULL DEFAULT '',
			type               TEXT NOT NULL DEFAULT 'NONE',
			lat                INTEGER NOT NULL DEFAULT 0,
			lon                INTEGER NOT NULL DEFAULT 0,
			feat1              INTEGER NOT NULL DEFAULT 0,
			feat2              INTEGER NOT NULL DEFAULT 0,
			out_path           BLOB,
			last_advert_ts     INTEGER NOT NULL DEFAULT 0,
			last_seen          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			snr                REAL,
			rssi               INTEGER,
			out_path_hash_size INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS companion_contacts (
			companion_id TEXT     NOT NULL,
			peer_pubkey  BLOB     NOT NULL,
			added_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metadata     TEXT     NOT NULL DEFAULT '{}',
			PRIMARY KEY (companion_id, peer_pubkey),
			FOREIGN KEY (peer_pubkey) REFERENCES discovered_peers(pubkey) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS packets (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			received_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			direction     TEXT NOT NULL,
			raw           BLOB NOT NULL,
			route_type    INTEGER,
			payload_type  INTEGER,
			snr           REAL,
			rssi          INTEGER
		);

		CREATE TABLE IF NOT EXISTS messages (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			companion_id   TEXT NOT NULL,
			channel        TEXT NOT NULL,
			channel_hash   INTEGER NOT NULL,
			sender         TEXT NOT NULL DEFAULT '',
			text           TEXT NOT NULL DEFAULT '',
			direction      TEXT NOT NULL,
			timestamp      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			snr            REAL,
			rssi           INTEGER,
			confirmed      INTEGER,
			path_hashes    BLOB,
			path_hash_size INTEGER,
			hops           INTEGER,
			status         TEXT
		);

		CREATE TABLE IF NOT EXISTS conversation_reads (
			companion_id    TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			last_read_id    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (companion_id, conversation_id)
		);

		CREATE TABLE IF NOT EXISTS message_echoes (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id      INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			received_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			path_hashes     BLOB,
			path_hash_size  INTEGER NOT NULL DEFAULT 1,
			hops            INTEGER NOT NULL DEFAULT 0,
			snr             REAL,
			rssi            INTEGER
		);

		CREATE TABLE IF NOT EXISTS blocked_senders (
			companion_id    TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			sender          TEXT NOT NULL,
			blocked_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (companion_id, conversation_id, sender)
		);

		CREATE INDEX IF NOT EXISTS idx_packets_received_at ON packets(received_at);
		CREATE INDEX IF NOT EXISTS idx_companion_contacts_companion ON companion_contacts(companion_id);
		CREATE INDEX IF NOT EXISTS idx_messages_companion_channel ON messages(companion_id, channel);
		CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp);
		CREATE INDEX IF NOT EXISTS idx_message_echoes_message ON message_echoes(message_id);
	`)
	return err
}

// migrateV2 adds the node-monitoring schema: a generic time-series table
// (node_metrics) that stores any reading from any node type identically — a
// repeater's battery_mv and a sensor's humidity@ch2 are stored the same way —
// plus a latest-snapshot table (node_state) and neighbour SNR topology over
// time (node_neighbors).
//
// Which nodes are monitored is NOT stored here: that is per-node interactive
// state and lives in companion_contacts.metadata (monitor / monitorIntervalSecs)
// alongside the repeater password it's set with — the same pattern, no extra
// table. This keeps declarative config in the file and runtime state in the DB.
//
// All snr/value columns hold real decibels / decoded values (REAL), matching
// the migrateV1 convention. ts columns are unix seconds (INTEGER).
func migrateV2(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS node_metrics (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			ts      INTEGER NOT NULL,
			pubkey  BLOB    NOT NULL,
			metric  TEXT    NOT NULL,
			channel INTEGER NOT NULL DEFAULT 0,
			value   REAL    NOT NULL
		);

		CREATE TABLE IF NOT EXISTS node_state (
			pubkey       BLOB PRIMARY KEY,
			kind         TEXT    NOT NULL DEFAULT '',
			name         TEXT    NOT NULL DEFAULT '',
			last_poll_ts INTEGER NOT NULL DEFAULT 0,
			last_ok_ts   INTEGER NOT NULL DEFAULT 0,
			last_error   TEXT    NOT NULL DEFAULT '',
			state        TEXT    NOT NULL DEFAULT '{}'
		);

		CREATE TABLE IF NOT EXISTS node_neighbors (
			ts              INTEGER NOT NULL,
			pubkey          BLOB    NOT NULL,
			neighbor_pubkey BLOB    NOT NULL,
			snr             REAL,
			PRIMARY KEY (ts, pubkey, neighbor_pubkey)
		);

		CREATE INDEX IF NOT EXISTS idx_node_metrics_q ON node_metrics(pubkey, metric, ts);
		CREATE INDEX IF NOT EXISTS idx_node_neighbors_pubkey ON node_neighbors(pubkey, ts);
	`)
	return err
}

// migrateV3 records which companion polled a node, so the monitoring UI (keyed
// only by pubkey) can resolve the contact whose metadata holds the config.
func migrateV3(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE node_state ADD COLUMN companion_id TEXT NOT NULL DEFAULT ''`)
	return err
}

// migrateV4 adds the single-row app_config table — the bot's config lives in
// the database; config files are one-time imports.
func migrateV4(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS app_config (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			config     TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	return err
}

// migrateV5 replaces the single-row JSON config blob (app_config) with a proper
// relational schema. Every entity gets a surrogate INTEGER primary key, so
// names, pubkeys and private keys are all mutable columns that nothing
// references — a companion can be renamed or have its keypair rotated without
// breaking any reference (mqtt node selection, trigger ownership, message
// history FKs).
//
// This migration creates SCHEMA ONLY. The one-shot import of the existing
// app_config blob into these tables (and the reconcile of name-keyed FKs on
// messages/contacts/etc to the new companion ids) runs at the app layer after
// store.Open, where the config types are available to parse the blob.
//
// Leaf string lists (a trigger's match patterns / contacts, a broker's
// disallowed packet types) are CSV TEXT columns rather than child tables — they
// are edited as a set and never queried individually. Channels DO get their own
// table because they carry a private key and are referenced by triggers via the
// trigger_channels join, so the key is stored once and shared by reference.
func migrateV5(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			id              INTEGER PRIMARY KEY CHECK (id = 1),
			log_level       TEXT,
			connection_type TEXT NOT NULL DEFAULT 'kiss',
			connection      TEXT,
			baud_rate       INTEGER,
			freq            REAL,
			bw              REAL,
			sf              INTEGER,
			cr              INTEGER,
			tx              INTEGER,
			listen_addr     TEXT,
			setup_complete  INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS companions (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT    NOT NULL,
			private_key     TEXT    NOT NULL DEFAULT '',
			pubkey          TEXT    NOT NULL DEFAULT '',
			latitude        REAL,
			longitude       REAL,
			advert_interval INTEGER
		);

		CREATE TABLE IF NOT EXISTS companion_channels (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			companion_id INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			name         TEXT    NOT NULL,
			private_key  TEXT    NOT NULL DEFAULT '',
			UNIQUE (companion_id, name)
		);

		CREATE TABLE IF NOT EXISTS triggers (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			companion_id         INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			type                 TEXT    NOT NULL,
			template             TEXT    NOT NULL DEFAULT '',
			char_limit_behaviour TEXT,
			match_patterns       TEXT    NOT NULL DEFAULT '',
			contacts             TEXT    NOT NULL DEFAULT '',
			retry_timeout        INTEGER,
			max_retries          INTEGER,
			path_hash_size       INTEGER,
			schedule             TEXT
		);

		CREATE TABLE IF NOT EXISTS trigger_channels (
			trigger_id INTEGER NOT NULL REFERENCES triggers(id) ON DELETE CASCADE,
			channel_id INTEGER NOT NULL REFERENCES companion_channels(id) ON DELETE CASCADE,
			PRIMARY KEY (trigger_id, channel_id)
		);

		CREATE TABLE IF NOT EXISTS mqtt_settings (
			id                INTEGER PRIMARY KEY CHECK (id = 1),
			enabled           INTEGER,
			node_companion_id INTEGER REFERENCES companions(id) ON DELETE SET NULL,
			iata_code         TEXT,
			status_interval   INTEGER,
			owner             TEXT,
			email             TEXT
		);

		CREATE TABLE IF NOT EXISTS mqtt_brokers (
			id                      INTEGER PRIMARY KEY AUTOINCREMENT,
			name                    TEXT    NOT NULL,
			enabled                 INTEGER NOT NULL DEFAULT 1,
			dedup                   INTEGER NOT NULL DEFAULT 0,
			transport               TEXT    NOT NULL DEFAULT 'tcp',
			host                    TEXT    NOT NULL DEFAULT '',
			port                    INTEGER NOT NULL DEFAULT 0,
			packet_topic            TEXT,
			status_topic            TEXT,
			disallowed_packet_types TEXT    NOT NULL DEFAULT '',
			retain_status           INTEGER NOT NULL DEFAULT 0,
			tls_enabled             INTEGER NOT NULL DEFAULT 0,
			tls_insecure            INTEGER NOT NULL DEFAULT 0,
			auth_type               TEXT    NOT NULL DEFAULT 'none',
			username                TEXT    NOT NULL DEFAULT '',
			password                TEXT    NOT NULL DEFAULT '',
			path                    TEXT    NOT NULL DEFAULT '',
			audience                TEXT    NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_companion_channels_companion ON companion_channels(companion_id);
		CREATE INDEX IF NOT EXISTS idx_triggers_companion ON triggers(companion_id);
		CREATE INDEX IF NOT EXISTS idx_trigger_channels_trigger ON trigger_channels(trigger_id);
	`)
	return err
}

// migrateV6 re-keys the per-companion history tables (messages,
// companion_contacts, conversation_reads, blocked_senders) from the mutable
// companion NAME to the surrogate companions.id, with a real FK + ON DELETE
// CASCADE. Until now these stored the name in a TEXT companion_id column, so
// renaming a companion orphaned all of its history; keyed by id, a rename is
// just an UPDATE to companions.name and the history follows.
//
// node_state is intentionally NOT migrated: it is keyed by pubkey (node-centric)
// and its companion_id is a "last polled by" label refreshed on every poll, not
// durable history — re-keying it would force monitor.Target to carry an id and
// change the monitoring WS/API shape for no real benefit.
//
// The rebuild runs on a single pinned connection with foreign_keys OFF: with
// enforcement on, DROP TABLE messages does an implicit row delete that would
// cascade-wipe message_echoes. The id backfill JOINs companions by name, so
// orphaned history (a companion that no longer exists by that name, e.g. from a
// past rename) can't be re-linked and is dropped — logged for transparency,
// none on a clean DB.
func migrateV6(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	// Restore enforcement before the connection returns to the pool.
	defer conn.ExecContext(ctx, "PRAGMA foreign_keys=ON")

	for _, tbl := range []string{"messages", "companion_contacts", "conversation_reads", "blocked_senders"} {
		var n int64
		if err := conn.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT COUNT(*) FROM %s t WHERE NOT EXISTS (SELECT 1 FROM companions c WHERE c.name = t.companion_id)`, tbl,
		)).Scan(&n); err == nil && n > 0 {
			slog.Warn("migrateV6 dropping orphaned history with no matching companion", "table", tbl, "rows", n)
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE messages_v6 (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			companion_id   INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			channel        TEXT NOT NULL,
			channel_hash   INTEGER NOT NULL,
			sender         TEXT NOT NULL DEFAULT '',
			text           TEXT NOT NULL DEFAULT '',
			direction      TEXT NOT NULL,
			timestamp      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			snr            REAL,
			rssi           INTEGER,
			confirmed      INTEGER,
			path_hashes    BLOB,
			path_hash_size INTEGER,
			hops           INTEGER,
			status         TEXT
		)`,
		`INSERT INTO messages_v6 (id, companion_id, channel, channel_hash, sender, text, direction, timestamp, snr, rssi, confirmed, path_hashes, path_hash_size, hops, status)
			SELECT m.id, c.id, m.channel, m.channel_hash, m.sender, m.text, m.direction, m.timestamp, m.snr, m.rssi, m.confirmed, m.path_hashes, m.path_hash_size, m.hops, m.status
			FROM messages m JOIN companions c ON c.name = m.companion_id`,
		`DROP TABLE messages`,
		`ALTER TABLE messages_v6 RENAME TO messages`,

		`CREATE TABLE companion_contacts_v6 (
			companion_id INTEGER  NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			peer_pubkey  BLOB     NOT NULL,
			added_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metadata     TEXT     NOT NULL DEFAULT '{}',
			PRIMARY KEY (companion_id, peer_pubkey),
			FOREIGN KEY (peer_pubkey) REFERENCES discovered_peers(pubkey) ON DELETE CASCADE
		)`,
		`INSERT INTO companion_contacts_v6 (companion_id, peer_pubkey, added_at, metadata)
			SELECT c.id, cc.peer_pubkey, cc.added_at, cc.metadata
			FROM companion_contacts cc JOIN companions c ON c.name = cc.companion_id`,
		`DROP TABLE companion_contacts`,
		`ALTER TABLE companion_contacts_v6 RENAME TO companion_contacts`,

		`CREATE TABLE conversation_reads_v6 (
			companion_id    INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			conversation_id TEXT NOT NULL,
			last_read_id    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (companion_id, conversation_id)
		)`,
		`INSERT INTO conversation_reads_v6 (companion_id, conversation_id, last_read_id)
			SELECT c.id, cr.conversation_id, cr.last_read_id
			FROM conversation_reads cr JOIN companions c ON c.name = cr.companion_id`,
		`DROP TABLE conversation_reads`,
		`ALTER TABLE conversation_reads_v6 RENAME TO conversation_reads`,

		`CREATE TABLE blocked_senders_v6 (
			companion_id    INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			conversation_id TEXT NOT NULL,
			sender          TEXT NOT NULL,
			blocked_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (companion_id, conversation_id, sender)
		)`,
		`INSERT INTO blocked_senders_v6 (companion_id, conversation_id, sender, blocked_at)
			SELECT c.id, bs.conversation_id, bs.sender, bs.blocked_at
			FROM blocked_senders bs JOIN companions c ON c.name = bs.companion_id`,
		`DROP TABLE blocked_senders`,
		`ALTER TABLE blocked_senders_v6 RENAME TO blocked_senders`,

		// Indexes were dropped with their tables; recreate them.
		`CREATE INDEX IF NOT EXISTS idx_companion_contacts_companion ON companion_contacts(companion_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_companion_channel ON messages(companion_id, channel)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrateV6 rebuild: %w", err)
		}
	}

	// Verify referential integrity before committing.
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	hasViolation := rows.Next()
	rows.Close()
	if hasViolation {
		return fmt.Errorf("migrateV6: foreign key check reported violations after rebuild")
	}

	return tx.Commit()
}
