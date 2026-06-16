package store

import (
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

// migrateV2 decouples contacts from discovered_peers. A contact now carries its
// own name/type (backfilled from the peer table) and no longer FK-cascades to
// discovered_peers, so deleting a discovered peer never affects a saved
// contact. SQLite can't drop a column-level FK in place, so the table is
// rebuilt; nothing references companion_contacts, so the drop/rename is safe.
func migrateV2(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE companion_contacts_new (
			companion_id INTEGER  NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			peer_pubkey  BLOB     NOT NULL,
			name         TEXT     NOT NULL DEFAULT '',
			type         TEXT     NOT NULL DEFAULT '',
			added_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metadata     TEXT     NOT NULL DEFAULT '{}',
			PRIMARY KEY (companion_id, peer_pubkey)
		);
		INSERT INTO companion_contacts_new (companion_id, peer_pubkey, name, type, added_at, metadata)
			SELECT cc.companion_id, cc.peer_pubkey,
			       COALESCE(dp.name, ''), COALESCE(dp.type, ''),
			       cc.added_at, cc.metadata
			FROM companion_contacts cc
			LEFT JOIN discovered_peers dp ON dp.pubkey = cc.peer_pubkey;
		DROP TABLE companion_contacts;
		ALTER TABLE companion_contacts_new RENAME TO companion_contacts;
		CREATE INDEX IF NOT EXISTS idx_companion_contacts_companion ON companion_contacts(companion_id);
	`)
	return err
}

// migrateV3 makes a contact a self-contained address-book record: it gains its
// own location, routing path, feature flags and last-seen (backfilled from
// discovered_peers), so a contact survives a peer sweep with everything it
// needs and the path is owned per-companion. Plain ADD COLUMN — no rebuild.
func migrateV3(db *sql.DB) error {
	_, err := db.Exec(`
		ALTER TABLE companion_contacts ADD COLUMN lat                INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE companion_contacts ADD COLUMN lon                INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE companion_contacts ADD COLUMN feat1              INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE companion_contacts ADD COLUMN feat2              INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE companion_contacts ADD COLUMN out_path           BLOB;
		ALTER TABLE companion_contacts ADD COLUMN out_path_hash_size INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE companion_contacts ADD COLUMN last_seen          DATETIME;
		ALTER TABLE companion_contacts ADD COLUMN last_advert_ts     INTEGER NOT NULL DEFAULT 0;
		UPDATE companion_contacts SET
			lat                = COALESCE((SELECT lat                FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey), 0),
			lon                = COALESCE((SELECT lon                FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey), 0),
			feat1              = COALESCE((SELECT feat1              FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey), 0),
			feat2              = COALESCE((SELECT feat2              FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey), 0),
			out_path           =          (SELECT out_path           FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey),
			out_path_hash_size = COALESCE((SELECT out_path_hash_size FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey), 0),
			last_seen          =          (SELECT last_seen          FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey),
			last_advert_ts     = COALESCE((SELECT last_advert_ts     FROM discovered_peers dp WHERE dp.pubkey = companion_contacts.peer_pubkey), 0);
	`)
	return err
}

// migrateV1 creates the full baseline schema (the prior incremental migrations
// were squashed into it). Append future changes as migrateV2+ to the migrations
// slice above — never edit this function. Statements are CREATE ... IF NOT
// EXISTS and tables precede their foreign-key referrers. snr columns hold real
// decibels (REAL); rssi is whole dBm (INTEGER).
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

		CREATE TABLE IF NOT EXISTS companions (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT    NOT NULL,
			private_key     TEXT    NOT NULL DEFAULT '',
			pubkey          TEXT    NOT NULL DEFAULT '',
			latitude        REAL,
			longitude       REAL,
			advert_interval INTEGER
		);

		CREATE TABLE IF NOT EXISTS companion_contacts (
			companion_id INTEGER  NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
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

		CREATE TABLE IF NOT EXISTS conversation_reads (
			companion_id    INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			conversation_id TEXT NOT NULL,
			last_read_id    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (companion_id, conversation_id)
		);

		CREATE TABLE IF NOT EXISTS blocked_senders (
			companion_id    INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			conversation_id TEXT NOT NULL,
			sender          TEXT NOT NULL,
			blocked_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (companion_id, conversation_id, sender)
		);

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
			state        TEXT    NOT NULL DEFAULT '{}',
			companion_id TEXT    NOT NULL DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS node_neighbors (
			ts              INTEGER NOT NULL,
			pubkey          BLOB    NOT NULL,
			neighbor_pubkey BLOB    NOT NULL,
			snr             REAL,
			PRIMARY KEY (ts, pubkey, neighbor_pubkey)
		);

		CREATE TABLE IF NOT EXISTS app_config (
			id         INTEGER PRIMARY KEY CHECK (id = 1),
			config     TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

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

		CREATE INDEX IF NOT EXISTS idx_packets_received_at ON packets(received_at);
		CREATE INDEX IF NOT EXISTS idx_companion_contacts_companion ON companion_contacts(companion_id);
		CREATE INDEX IF NOT EXISTS idx_messages_companion_channel ON messages(companion_id, channel);
		CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp);
		CREATE INDEX IF NOT EXISTS idx_message_echoes_message ON message_echoes(message_id);
		CREATE INDEX IF NOT EXISTS idx_node_metrics_q ON node_metrics(pubkey, metric, ts);
		CREATE INDEX IF NOT EXISTS idx_node_neighbors_pubkey ON node_neighbors(pubkey, ts);
		CREATE INDEX IF NOT EXISTS idx_companion_channels_companion ON companion_channels(companion_id);
		CREATE INDEX IF NOT EXISTS idx_triggers_companion ON triggers(companion_id);
		CREATE INDEX IF NOT EXISTS idx_trigger_channels_trigger ON trigger_channels(trigger_id);
	`)
	return err
}
