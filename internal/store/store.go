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
	SignalTests    *SignalTestRepo
	LinkMonitors   *LinkMonitorRepo

	writerCh   chan func()
	writerDone chan struct{}
	closeOnce  sync.Once
	dropped    atomic.Uint64
}

func Open(ctx context.Context, path string) (*Store, error) {
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

	if err := db.PingContext(ctx); err != nil {
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
		SignalTests:    &SignalTestRepo{db: db},
		LinkMonitors:   &LinkMonitorRepo{db: db},
		writerCh:       make(chan func(), writerQueueDepth),
		writerDone:     make(chan struct{}),
	}

	if err := s.migrate(ctx); err != nil {
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

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	// migrateV1 squashed the original v1–v3 incremental migrations into one
	// baseline. Fresh DBs created since the squash sit at user_version 1, but
	// DBs that predate it are already at 2 or 3 with the identical schema. A new
	// migration must therefore be appended past that gap (version > 3) so it
	// runs exactly once on both old and fresh DBs; migrateNoop fills the
	// squashed-away slots that pre-squash DBs already counted.
	migrations := []func(context.Context, *sql.DB) error{
		migrateV1,   // user_version 1
		migrateNoop, // 2 — squashed into migrateV1
		migrateNoop, // 3 — squashed into migrateV1
		migrateV2,   // 4 — indexed packet_hash/path columns
		migrateV3,   // 5 — signal_tests, signal_test_runs, link_monitors
		migrateV4,   // 6 — idempotent repair pass 1 (see ensureLinkMonitorColumns)
		migrateV5,   // 7 — idempotent repair pass 2, catches DBs already at version 6
		migrateV6,   // 8 — idempotent repair pass 3, catches DBs already at version 7
	}

	for i := version; i < len(migrations); i++ {
		if err := migrations[i](ctx, s.db); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("setting schema version %d: %w", i+1, err)
		}
	}

	return nil
}

// migrateV1 creates the full baseline schema (the prior incremental migrations
// were squashed into it). Append future changes as migrateV2+ to the migrations
// slice above — never edit this function. Statements are CREATE ... IF NOT
// EXISTS and tables precede their foreign-key referrers. snr columns hold real
// decibels (REAL); rssi is whole dBm (INTEGER). companion_contacts is a
// self-contained address-book record (its own name/type/location/path/feat/
// last-seen, no discovered_peers FK), so deleting a peer never touches a contact.
func migrateV1(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
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
			companion_id       INTEGER  NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			peer_pubkey        BLOB     NOT NULL,
			name               TEXT     NOT NULL DEFAULT '',
			type               TEXT     NOT NULL DEFAULT '',
			lat                INTEGER  NOT NULL DEFAULT 0,
			lon                INTEGER  NOT NULL DEFAULT 0,
			feat1              INTEGER  NOT NULL DEFAULT 0,
			feat2              INTEGER  NOT NULL DEFAULT 0,
			out_path           BLOB,
			out_path_hash_size INTEGER  NOT NULL DEFAULT 0,
			last_seen          DATETIME,
			last_advert_ts     INTEGER  NOT NULL DEFAULT 0,
			added_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metadata           TEXT     NOT NULL DEFAULT '{}',
			PRIMARY KEY (companion_id, peer_pubkey)
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

// migrateNoop is a placeholder for a migration slot that migrateV1 already
// covers (see the squash note in migrate). It keeps later migrations at the
// version index pre-squash DBs expect.
func migrateNoop(context.Context, *sql.DB) error { return nil }

// migrateV2 adds the indexed packet_hash and path columns so the Packets page
// can filter/search across all stored history server-side, and backfills them
// for rows inserted before the columns existed.
func migrateV2(ctx context.Context, db *sql.DB) error {
	// Everything runs in one transaction (SQLite DDL is transactional) so a
	// failure mid-backfill rolls back the ADD COLUMNs too, keeping the
	// migration atomic and safe to re-run.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE packets ADD COLUMN packet_hash TEXT NOT NULL DEFAULT '';
		ALTER TABLE packets ADD COLUMN path TEXT NOT NULL DEFAULT '';
	`); err != nil {
		return fmt.Errorf("adding packet columns: %w", err)
	}

	rows, err := tx.QueryContext(ctx, "SELECT id, raw FROM packets")
	if err != nil {
		return fmt.Errorf("reading packets for backfill: %w", err)
	}
	type row struct {
		id  int64
		raw []byte
	}
	var batch []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.raw); err != nil {
			rows.Close()
			return fmt.Errorf("scanning packet for backfill: %w", err)
		}
		batch = append(batch, rr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating packets for backfill: %w", err)
	}

	for _, rr := range batch {
		packetHash, path := derivePacketFields(rr.raw)
		if packetHash == "" && path == "" {
			continue // unparseable row; leave the '' defaults
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE packets SET packet_hash = ?, path = ? WHERE id = ?",
			packetHash, path, rr.id); err != nil {
			return fmt.Errorf("backfilling packet %d: %w", rr.id, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_packets_packet_hash ON packets(packet_hash);
		CREATE INDEX IF NOT EXISTS idx_packets_path ON packets(path);
	`); err != nil {
		return fmt.Errorf("indexing packet columns: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backfill: %w", err)
	}
	return nil
}

// migrateV3 adds storage for the signal-test runner (repeatable trace runs
// with saved per-hop stats) and for link monitors (a monitored path, polled
// by the existing node-monitoring engine under a synthetic pubkey — see
// LinkMonitorRepo). signal_test_runs is never pruned by PruneMetrics; rows
// are bounded per test and removed by deleting the parent test.
func migrateV3(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS signal_tests (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			companion_id   INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			label          TEXT    NOT NULL DEFAULT '',
			notes          TEXT    NOT NULL DEFAULT '',
			path           BLOB    NOT NULL,
			path_hash_size INTEGER NOT NULL DEFAULT 1,
			count          INTEGER NOT NULL,
			interval_secs  INTEGER NOT NULL,
			status         TEXT    NOT NULL DEFAULT 'running',
			started_at     INTEGER NOT NULL,
			finished_at    INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS signal_test_runs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			test_id    INTEGER NOT NULL REFERENCES signal_tests(id) ON DELETE CASCADE,
			seq        INTEGER NOT NULL,
			sent_at    INTEGER NOT NULL,
			ok         INTEGER NOT NULL DEFAULT 0,
			hop_snrs   TEXT    NOT NULL DEFAULT '[]',
			snr        REAL,
			elapsed_ms INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_signal_test_runs_test ON signal_test_runs(test_id, seq);

		CREATE TABLE IF NOT EXISTS link_monitors (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			key              BLOB    NOT NULL UNIQUE,
			companion_id     INTEGER NOT NULL REFERENCES companions(id) ON DELETE CASCADE,
			label            TEXT    NOT NULL DEFAULT '',
			path             BLOB    NOT NULL,
			path_hash_size   INTEGER NOT NULL DEFAULT 1,
			interval_secs    INTEGER NOT NULL DEFAULT 900,
			enabled          INTEGER NOT NULL DEFAULT 1,
			-- ignore_first_hop hides the "you → <first node>" hop from the UI: that
			-- leg is a local companion-to-base-station radio link that's normally
			-- rock solid, so its SNR is noise the user doesn't want cluttering the
			-- charts. Display-only — the collector still records it.
			ignore_first_hop INTEGER NOT NULL DEFAULT 0,
			-- retry_secs/max_retries override the node-monitoring poller's
			-- failed-poll retry cadence (internal/monitor.retryInterval/
			-- defaultMaxRetries) for this link; 0 means "use the poller default".
			retry_secs       INTEGER NOT NULL DEFAULT 0,
			max_retries      INTEGER NOT NULL DEFAULT 0,
			-- hide_last_snr hides the "SNR" tile/chart (the local radio's RX SNR
			-- of the final returning packet): for an out-and-back path that's the
			-- same physical leg as the first hop (the return trip is relayed back
			-- through that same nearby node), so it's exactly as static and
			-- uninteresting as ignore_first_hop's reading, for the same reason.
			-- Display-only — the collector still records it.
			hide_last_snr    INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		return fmt.Errorf("creating signal test / link monitor tables: %w", err)
	}
	return nil
}

// ensureLinkMonitorColumns idempotently adds any of link_monitors'
// ignore_first_hop/retry_secs/max_retries/hide_last_snr columns that are
// missing (checked via PRAGMA table_info, so it's a safe no-op once they're
// all present). Those columns are part of migrateV3's CREATE TABLE for any
// database created fresh, but this feature was developed across several
// local iterations before it shipped, so a database that already ran an
// earlier migrateV3 — without these columns, or with only some of them
// added by a since-removed migrateV4 — needs to catch up without losing its
// data.
func ensureLinkMonitorColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(link_monitors)`)
	if err != nil {
		return fmt.Errorf("inspecting link_monitors columns: %w", err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scanning link_monitors column info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating link_monitors column info: %w", err)
	}
	rows.Close()

	for _, col := range []string{"ignore_first_hop", "retry_secs", "max_retries", "hide_last_snr"} {
		if existing[col] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE link_monitors ADD COLUMN %s INTEGER NOT NULL DEFAULT 0`, col)); err != nil {
			return fmt.Errorf("adding link_monitors.%s: %w", col, err)
		}
	}
	return nil
}

func migrateV4(ctx context.Context, db *sql.DB) error {
	return ensureLinkMonitorColumns(ctx, db)
}

// migrateV5 re-runs the same idempotent check as migrateV4. It exists solely
// to reach databases that already sit at user_version 6 from an earlier,
// since-removed migrateV4 (which only ever added ignore_first_hop): the
// migration loop only runs entries at or past a database's current version,
// so a repair landing at index 5 (version 6) would be silently skipped by a
// database that's already there. Bumping the target to 7 guarantees at
// least one repair pass runs no matter which of this feature's in-progress
// local schema states a database is coming from.
func migrateV5(ctx context.Context, db *sql.DB) error {
	return ensureLinkMonitorColumns(ctx, db)
}

// migrateV6 re-runs ensureLinkMonitorColumns once more, for the same reason
// as migrateV5: hide_last_snr was added to the column set after some local
// databases had already reached user_version 7 via migrateV4/migrateV5, so
// without a repair pass landing at (or past) version 7 those databases would
// never pick it up.
func migrateV6(ctx context.Context, db *sql.DB) error {
	return ensureLinkMonitorColumns(ctx, db)
}
