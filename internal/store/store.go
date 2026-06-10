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
