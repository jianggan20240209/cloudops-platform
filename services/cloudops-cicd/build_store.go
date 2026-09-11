package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type BuildRecord struct {
	ID            string `json:"id"`
	Service       string `json:"service"`
	Branch        string `json:"branch,omitempty"`
	JenkinsJob    string `json:"jenkins_job,omitempty"`
	JenkinsBuild  string `json:"jenkins_build,omitempty"`
	QueueURL      string `json:"queue_url,omitempty"`
	BuildURL      string `json:"build_url,omitempty"`
	Status        string `json:"status"`
	Image         string `json:"image,omitempty"`
	ImageTag      string `json:"image_tag,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	LogSummary    string `json:"log_summary,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	NotifySent    bool   `json:"notify_sent,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type BuildRecordStore interface {
	Save(ctx context.Context, record BuildRecord) (BuildRecord, error)
	Get(ctx context.Context, id string) (BuildRecord, bool, error)
	List(ctx context.Context, limit int) ([]BuildRecord, error)
}

var buildStore BuildRecordStore

type memoryBuildStore struct {
	mu      sync.RWMutex
	records map[string]BuildRecord
	order   []string
}

func NewMemoryBuildRecordStore() *memoryBuildStore {
	return &memoryBuildStore{records: map[string]BuildRecord{}}
}

func (s *memoryBuildStore) Save(_ context.Context, record BuildRecord) (BuildRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.CreatedAt == "" {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if _, ok := s.records[record.ID]; !ok {
		s.order = append([]string{record.ID}, s.order...)
	}
	s.records[record.ID] = record
	return record, nil
}

func (s *memoryBuildStore) Get(_ context.Context, id string) (BuildRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	return rec, ok, nil
}

func (s *memoryBuildStore) List(_ context.Context, limit int) ([]BuildRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]BuildRecord, 0, limit)
	for _, id := range s.order {
		if len(out) >= limit {
			break
		}
		out = append(out, s.records[id])
	}
	return out, nil
}

type postgresBuildStore struct {
	db *sql.DB
}

func newBuildRecordStore() BuildRecordStore {
	dsn := firstNonEmpty(os.Getenv("RELEASE_RECORD_DATABASE_URL"), os.Getenv("POSTGRES_DSN"))
	if strings.TrimSpace(dsn) == "" {
		logJSON("info", "build_store_memory", map[string]any{"reason": "no postgres dsn"})
		return NewMemoryBuildRecordStore()
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		logJSON("error", "build_store_postgres_open_failed", map[string]any{"error": err.Error()})
		return NewMemoryBuildRecordStore()
	}
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logJSON("error", "build_store_postgres_ping_failed", map[string]any{"error": err.Error()})
		return NewMemoryBuildRecordStore()
	}
	store := &postgresBuildStore{db: db}
	if err := store.ensureSchema(ctx); err != nil {
		logJSON("error", "build_store_schema_failed", map[string]any{"error": err.Error()})
		return NewMemoryBuildRecordStore()
	}
	logJSON("info", "build_store_postgres", nil)
	return store
}

func (s *postgresBuildStore) ensureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS build_history (
  id TEXT PRIMARY KEY,
  service TEXT NOT NULL,
  branch TEXT,
  jenkins_job TEXT,
  jenkins_build TEXT,
  queue_url TEXT,
  build_url TEXT,
  status TEXT NOT NULL,
  image TEXT,
  image_tag TEXT,
  failure_reason TEXT,
  log_summary TEXT,
  dry_run BOOLEAN NOT NULL DEFAULT FALSE,
  notify_sent BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_build_history_created_at ON build_history (created_at DESC);
`)
	return err
}

func (s *postgresBuildStore) Save(ctx context.Context, record BuildRecord) (BuildRecord, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	if record.CreatedAt == "" {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO build_history (
  id, service, branch, jenkins_job, jenkins_build, queue_url, build_url,
  status, image, image_tag, failure_reason, log_summary, dry_run, notify_sent, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::timestamptz,$16::timestamptz
)
ON CONFLICT (id) DO UPDATE SET
  service=EXCLUDED.service,
  branch=EXCLUDED.branch,
  jenkins_job=EXCLUDED.jenkins_job,
  jenkins_build=EXCLUDED.jenkins_build,
  queue_url=EXCLUDED.queue_url,
  build_url=EXCLUDED.build_url,
  status=EXCLUDED.status,
  image=EXCLUDED.image,
  image_tag=EXCLUDED.image_tag,
  failure_reason=EXCLUDED.failure_reason,
  log_summary=EXCLUDED.log_summary,
  dry_run=EXCLUDED.dry_run,
  notify_sent=EXCLUDED.notify_sent,
  updated_at=EXCLUDED.updated_at
`, record.ID, record.Service, record.Branch, record.JenkinsJob, record.JenkinsBuild, record.QueueURL, record.BuildURL,
		record.Status, record.Image, record.ImageTag, record.FailureReason, record.LogSummary, record.DryRun, record.NotifySent,
		record.CreatedAt, record.UpdatedAt)
	if err != nil {
		return BuildRecord{}, err
	}
	return record, nil
}

func (s *postgresBuildStore) Get(ctx context.Context, id string) (BuildRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, service, COALESCE(branch,''), COALESCE(jenkins_job,''), COALESCE(jenkins_build,''),
       COALESCE(queue_url,''), COALESCE(build_url,''), status, COALESCE(image,''), COALESCE(image_tag,''),
       COALESCE(failure_reason,''), COALESCE(log_summary,''), dry_run, notify_sent,
       created_at::text, updated_at::text
FROM build_history WHERE id=$1`, id)
	var rec BuildRecord
	err := row.Scan(&rec.ID, &rec.Service, &rec.Branch, &rec.JenkinsJob, &rec.JenkinsBuild, &rec.QueueURL, &rec.BuildURL,
		&rec.Status, &rec.Image, &rec.ImageTag, &rec.FailureReason, &rec.LogSummary, &rec.DryRun, &rec.NotifySent,
		&rec.CreatedAt, &rec.UpdatedAt)
	if err == sql.ErrNoRows {
		return BuildRecord{}, false, nil
	}
	if err != nil {
		return BuildRecord{}, false, err
	}
	return rec, true, nil
}

func (s *postgresBuildStore) List(ctx context.Context, limit int) ([]BuildRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, service, COALESCE(branch,''), COALESCE(jenkins_job,''), COALESCE(jenkins_build,''),
       COALESCE(queue_url,''), COALESCE(build_url,''), status, COALESCE(image,''), COALESCE(image_tag,''),
       COALESCE(failure_reason,''), COALESCE(log_summary,''), dry_run, notify_sent,
       created_at::text, updated_at::text
FROM build_history ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BuildRecord, 0)
	for rows.Next() {
		var rec BuildRecord
		if err := rows.Scan(&rec.ID, &rec.Service, &rec.Branch, &rec.JenkinsJob, &rec.JenkinsBuild, &rec.QueueURL, &rec.BuildURL,
			&rec.Status, &rec.Image, &rec.ImageTag, &rec.FailureReason, &rec.LogSummary, &rec.DryRun, &rec.NotifySent,
			&rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
