package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SignalTestRepo persists trace-route tests and their runs; stats are computed on read, and writes must be wrapped in store.WriteAsync/WriteSync.
type SignalTestRepo struct {
	db *sql.DB
}

// SignalTestStatus values; a "running" test becomes "interrupted" at startup (MarkInterrupted).
const (
	SignalTestStatusRunning     = "running"
	SignalTestStatusDone        = "done"
	SignalTestStatusCancelled   = "cancelled"
	SignalTestStatusInterrupted = "interrupted"
)

type SignalTest struct {
	ID           int64
	CompanionID  int64
	Label        string
	Notes        string
	Path         []byte
	PathHashSize int
	Count        int
	IntervalSecs int
	Status       string
	StartedAt    int64
	FinishedAt   int64
	// RunsDone/OKCount are populated only by List; zero elsewhere.
	RunsDone int
	OKCount  int
}

type SignalTestRun struct {
	ID        int64
	TestID    int64
	Seq       int
	SentAt    int64
	OK        bool
	HopSNRs   string // raw JSON array
	SNR       *float64
	ElapsedMs int64
}

// Create inserts a test row (status defaults to "running") and sets t.ID. Call inside WriteSync.
func (r *SignalTestRepo) Create(ctx context.Context, t *SignalTest) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO signal_tests (companion_id, label, notes, path, path_hash_size, count, interval_secs, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.CompanionID, t.Label, t.Notes, t.Path, t.PathHashSize, t.Count, t.IntervalSecs, SignalTestStatusRunning, t.StartedAt)
	if err != nil {
		return fmt.Errorf("creating signal test: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("reading signal test id: %w", err)
	}
	t.ID = id
	t.Status = SignalTestStatusRunning
	return nil
}

func scanSignalTest(s interface{ Scan(...any) error }) (*SignalTest, error) {
	var t SignalTest
	if err := s.Scan(&t.ID, &t.CompanionID, &t.Label, &t.Notes, &t.Path, &t.PathHashSize,
		&t.Count, &t.IntervalSecs, &t.Status, &t.StartedAt, &t.FinishedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// Get returns one test by id, or nil if it doesn't exist.
func (r *SignalTestRepo) Get(ctx context.Context, id int64) (*SignalTest, error) {
	t, err := scanSignalTest(r.db.QueryRowContext(ctx, `
		SELECT id, companion_id, label, notes, path, path_hash_size, count, interval_secs, status, started_at, finished_at
		FROM signal_tests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting signal test: %w", err)
	}
	return t, nil
}

// List returns every test, most recent first, with run and success counts aggregated.
func (r *SignalTestRepo) List(ctx context.Context, companionID int64) ([]SignalTest, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.companion_id, t.label, t.notes, t.path, t.path_hash_size, t.count, t.interval_secs,
		       t.status, t.started_at, t.finished_at,
		       COUNT(r.id), COALESCE(SUM(r.ok), 0)
		FROM signal_tests t
		LEFT JOIN signal_test_runs r ON r.test_id = t.id
		WHERE (? = 0 OR t.companion_id = ?)
		GROUP BY t.id
		ORDER BY t.started_at DESC`, companionID, companionID)
	if err != nil {
		return nil, fmt.Errorf("listing signal tests: %w", err)
	}
	defer rows.Close()

	var out []SignalTest
	for rows.Next() {
		var t SignalTest
		if err := rows.Scan(&t.ID, &t.CompanionID, &t.Label, &t.Notes, &t.Path, &t.PathHashSize,
			&t.Count, &t.IntervalSecs, &t.Status, &t.StartedAt, &t.FinishedAt, &t.RunsDone, &t.OKCount); err != nil {
			return nil, fmt.Errorf("scanning signal test row: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating signal tests: %w", err)
	}
	return out, nil
}

// ListRuns returns every run for a test, ordered by sequence.
func (r *SignalTestRepo) ListRuns(ctx context.Context, testID int64) ([]SignalTestRun, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, test_id, seq, sent_at, ok, hop_snrs, snr, elapsed_ms
		FROM signal_test_runs WHERE test_id = ? ORDER BY seq ASC`, testID)
	if err != nil {
		return nil, fmt.Errorf("listing signal test runs: %w", err)
	}
	defer rows.Close()

	var out []SignalTestRun
	for rows.Next() {
		var run SignalTestRun
		var snr sql.NullFloat64
		if err := rows.Scan(&run.ID, &run.TestID, &run.Seq, &run.SentAt, &run.OK, &run.HopSNRs, &snr, &run.ElapsedMs); err != nil {
			return nil, fmt.Errorf("scanning signal test run: %w", err)
		}
		if snr.Valid {
			v := snr.Float64
			run.SNR = &v
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating signal test runs: %w", err)
	}
	return out, nil
}

// InsertRun records one trace attempt. Call inside WriteAsync/WriteSync.
func (r *SignalTestRepo) InsertRun(ctx context.Context, run *SignalTestRun) error {
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO signal_test_runs (test_id, seq, sent_at, ok, hop_snrs, snr, elapsed_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.TestID, run.Seq, run.SentAt, run.OK, run.HopSNRs, run.SNR, run.ElapsedMs); err != nil {
		return fmt.Errorf("inserting signal test run: %w", err)
	}
	return nil
}

// SetStatus updates a test's status, and finished_at when non-zero. Call inside WriteAsync/WriteSync.
func (r *SignalTestRepo) SetStatus(ctx context.Context, id int64, status string, finishedAt int64) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE signal_tests SET status = ?, finished_at = ? WHERE id = ?`,
		status, finishedAt, id); err != nil {
		return fmt.Errorf("updating signal test status: %w", err)
	}
	return nil
}

// UpdateLabelNotes renames/annotates a test. Call inside WriteSync.
func (r *SignalTestRepo) UpdateLabelNotes(ctx context.Context, id int64, label, notes *string) error {
	if label != nil {
		if _, err := r.db.ExecContext(ctx, `UPDATE signal_tests SET label = ? WHERE id = ?`, *label, id); err != nil {
			return fmt.Errorf("updating signal test label: %w", err)
		}
	}
	if notes != nil {
		if _, err := r.db.ExecContext(ctx, `UPDATE signal_tests SET notes = ? WHERE id = ?`, *notes, id); err != nil {
			return fmt.Errorf("updating signal test notes: %w", err)
		}
	}
	return nil
}

// Delete removes a test and its runs (ON DELETE CASCADE). Call inside WriteSync.
func (r *SignalTestRepo) Delete(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM signal_tests WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting signal test: %w", err)
	}
	return nil
}

// MarkInterrupted flips tests left "running" by a previous process to "interrupted". Call inside WriteSync.
func (r *SignalTestRepo) MarkInterrupted(ctx context.Context, now int64) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE signal_tests SET status = ?, finished_at = ? WHERE status = ?`,
		SignalTestStatusInterrupted, now, SignalTestStatusRunning); err != nil {
		return fmt.Errorf("marking interrupted signal tests: %w", err)
	}
	return nil
}
