package store

import (
	"context"
	"database/sql"
	"time"
)

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
)

type Job struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Payload     string    `json:"-"`
	Status      JobStatus `json:"status"`
	Stage       string    `json:"stage,omitempty"`
	Result      string    `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	Attempts    int       `json:"attempts"`
	MaxAttempts int       `json:"-"`
	CreatedAt   int64     `json:"createdAt"`
	UpdatedAt   int64     `json:"updatedAt"`
}

const jobCols = `id, type, payload, status, COALESCE(stage,''), COALESCE(result,''), COALESCE(error,''), attempts, max_attempts, created_at, updated_at`

func scanJob(r interface{ Scan(...any) error }) (*Job, error) {
	j := &Job{}
	err := r.Scan(&j.ID, &j.Type, &j.Payload, &j.Status, &j.Stage, &j.Result, &j.Error, &j.Attempts, &j.MaxAttempts, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// Enqueue inserts a job. If idemKey matches an existing job, that job is returned instead.
func (s *Store) Enqueue(ctx context.Context, typ, payload, idemKey string) (*Job, bool, error) {
	id := NewID("job")
	var key any
	if idemKey != "" {
		key = idemKey
	}
	t := now()
	r, err := s.db.ExecContext(ctx, `INSERT INTO jobs (id,type,payload,status,idem_key,run_after,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(idem_key) DO NOTHING`, id, typ, payload, JobQueued, key, t, t, t)
	if err != nil {
		return nil, false, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE idem_key=?`, idemKey))
		return j, false, err
	}
	j, err := s.GetJob(ctx, id)
	return j, true, err
}

// Claim atomically moves the oldest ready job to running.
func (s *Store) Claim(ctx context.Context) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `UPDATE jobs SET status=?, attempts=attempts+1, updated_at=?
		WHERE id=(SELECT id FROM jobs WHERE status=? AND run_after<=? ORDER BY created_at LIMIT 1)
		RETURNING `+jobCols, JobRunning, now(), JobQueued, now()))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id=?`, id))
	return j, notFound(err)
}

func (s *Store) SetStage(ctx context.Context, id, stage string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET stage=?, updated_at=? WHERE id=?`, stage, now(), id)
	return err
}

func (s *Store) Complete(ctx context.Context, id, result string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, stage='done', result=?, error=NULL, updated_at=? WHERE id=?`, JobSucceeded, result, now(), id)
	return err
}

// Fail records an error and requeues with backoff while attempts remain. It reports whether the job will retry.
func (s *Store) Fail(ctx context.Context, j *Job, msg string, retryable bool) (bool, error) {
	if retryable && j.Attempts < j.MaxAttempts {
		backoff := time.Duration(1<<j.Attempts) * time.Second
		_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, error=?, run_after=?, updated_at=? WHERE id=?`,
			JobQueued, msg, now()+backoff.Milliseconds(), now(), j.ID)
		return true, err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, error=?, updated_at=? WHERE id=?`, JobFailed, msg, now(), j.ID)
	return false, err
}

// RecoverRunning requeues jobs left running by a crashed process.
func (s *Store) RecoverRunning(ctx context.Context) (int64, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, updated_at=? WHERE status=?`, JobQueued, now(), JobRunning)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
