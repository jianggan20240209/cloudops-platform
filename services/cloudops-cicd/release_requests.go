package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ReleaseRequest is the Day 83 release application model (approval fields).
type ReleaseRequest struct {
	ID            string `json:"id"`
	AppName       string `json:"app_name"`
	Env           string `json:"env"`
	Image         string `json:"image,omitempty"`
	ImageTag      string `json:"image_tag"`
	JenkinsJob    string `json:"jenkins_job,omitempty"`
	JenkinsBuild  string `json:"jenkins_build,omitempty"`
	ArgoCDApp     string `json:"argocd_app,omitempty"`
	GitRevision   string `json:"git_revision,omitempty"`
	Status        string `json:"status"` // draft|pending|approved|rejected|cancelled
	Requester     string `json:"requester,omitempty"`
	Approver      string `json:"approver,omitempty"`
	ApprovalNote  string `json:"approval_note,omitempty"`
	RiskLevel     string `json:"risk_level,omitempty"` // low|medium|high
	ChangeSummary string `json:"change_summary,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type ReleaseRequestStore interface {
	Save(ctx context.Context, req ReleaseRequest) (ReleaseRequest, error)
	Get(ctx context.Context, id string) (ReleaseRequest, bool, error)
	List(ctx context.Context, limit int) ([]ReleaseRequest, error)
}

var releaseRequestStore ReleaseRequestStore

type memoryReleaseRequestStore struct {
	mu    sync.RWMutex
	items map[string]ReleaseRequest
	order []string
}

func NewMemoryReleaseRequestStore() *memoryReleaseRequestStore {
	return &memoryReleaseRequestStore{items: map[string]ReleaseRequest{}}
}

func (s *memoryReleaseRequestStore) Save(_ context.Context, req ReleaseRequest) (ReleaseRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.CreatedAt == "" {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	if _, ok := s.items[req.ID]; !ok {
		s.order = append([]string{req.ID}, s.order...)
	}
	s.items[req.ID] = req
	return req, nil
}

func (s *memoryReleaseRequestStore) Get(_ context.Context, id string) (ReleaseRequest, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	return item, ok, nil
}

func (s *memoryReleaseRequestStore) List(_ context.Context, limit int) ([]ReleaseRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]ReleaseRequest, 0, limit)
	for _, id := range s.order {
		if len(out) >= limit {
			break
		}
		out = append(out, s.items[id])
	}
	return out, nil
}

type postgresReleaseRequestStore struct {
	db *sql.DB
}

func newReleaseRequestStore() ReleaseRequestStore {
	dsn := firstNonEmpty(os.Getenv("RELEASE_RECORD_DATABASE_URL"), os.Getenv("POSTGRES_DSN"))
	if strings.TrimSpace(dsn) == "" {
		return NewMemoryReleaseRequestStore()
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		logJSON("error", "release_request_store_open_failed", map[string]any{"error": err.Error()})
		return NewMemoryReleaseRequestStore()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logJSON("error", "release_request_store_ping_failed", map[string]any{"error": err.Error()})
		return NewMemoryReleaseRequestStore()
	}
	store := &postgresReleaseRequestStore{db: db}
	if err := store.ensureSchema(ctx); err != nil {
		logJSON("error", "release_request_schema_failed", map[string]any{"error": err.Error()})
		return NewMemoryReleaseRequestStore()
	}
	return store
}

func (s *postgresReleaseRequestStore) ensureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS release_requests (
  id TEXT PRIMARY KEY,
  app_name TEXT NOT NULL,
  env TEXT NOT NULL,
  image TEXT,
  image_tag TEXT NOT NULL,
  jenkins_job TEXT,
  jenkins_build TEXT,
  argocd_app TEXT,
  git_revision TEXT,
  status TEXT NOT NULL,
  requester TEXT,
  approver TEXT,
  approval_note TEXT,
  risk_level TEXT,
  change_summary TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_release_requests_created_at ON release_requests (created_at DESC);
`)
	return err
}

func (s *postgresReleaseRequestStore) Save(ctx context.Context, req ReleaseRequest) (ReleaseRequest, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.CreatedAt == "" {
		req.CreatedAt = now
	}
	req.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO release_requests (
  id, app_name, env, image, image_tag, jenkins_job, jenkins_build, argocd_app, git_revision,
  status, requester, approver, approval_note, risk_level, change_summary, created_at, updated_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::timestamptz,$17::timestamptz
)
ON CONFLICT (id) DO UPDATE SET
  app_name=EXCLUDED.app_name, env=EXCLUDED.env, image=EXCLUDED.image, image_tag=EXCLUDED.image_tag,
  jenkins_job=EXCLUDED.jenkins_job, jenkins_build=EXCLUDED.jenkins_build, argocd_app=EXCLUDED.argocd_app,
  git_revision=EXCLUDED.git_revision, status=EXCLUDED.status, requester=EXCLUDED.requester,
  approver=EXCLUDED.approver, approval_note=EXCLUDED.approval_note, risk_level=EXCLUDED.risk_level,
  change_summary=EXCLUDED.change_summary, updated_at=EXCLUDED.updated_at
`, req.ID, req.AppName, req.Env, req.Image, req.ImageTag, req.JenkinsJob, req.JenkinsBuild, req.ArgoCDApp, req.GitRevision,
		req.Status, req.Requester, req.Approver, req.ApprovalNote, req.RiskLevel, req.ChangeSummary, req.CreatedAt, req.UpdatedAt)
	if err != nil {
		return ReleaseRequest{}, err
	}
	return req, nil
}

func (s *postgresReleaseRequestStore) Get(ctx context.Context, id string) (ReleaseRequest, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, app_name, env, COALESCE(image,''), image_tag, COALESCE(jenkins_job,''), COALESCE(jenkins_build,''),
       COALESCE(argocd_app,''), COALESCE(git_revision,''), status, COALESCE(requester,''), COALESCE(approver,''),
       COALESCE(approval_note,''), COALESCE(risk_level,''), COALESCE(change_summary,''),
       created_at::text, updated_at::text
FROM release_requests WHERE id=$1`, id)
	var req ReleaseRequest
	err := row.Scan(&req.ID, &req.AppName, &req.Env, &req.Image, &req.ImageTag, &req.JenkinsJob, &req.JenkinsBuild,
		&req.ArgoCDApp, &req.GitRevision, &req.Status, &req.Requester, &req.Approver, &req.ApprovalNote,
		&req.RiskLevel, &req.ChangeSummary, &req.CreatedAt, &req.UpdatedAt)
	if err == sql.ErrNoRows {
		return ReleaseRequest{}, false, nil
	}
	if err != nil {
		return ReleaseRequest{}, false, err
	}
	return req, true, nil
}

func (s *postgresReleaseRequestStore) List(ctx context.Context, limit int) ([]ReleaseRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, app_name, env, COALESCE(image,''), image_tag, COALESCE(jenkins_job,''), COALESCE(jenkins_build,''),
       COALESCE(argocd_app,''), COALESCE(git_revision,''), status, COALESCE(requester,''), COALESCE(approver,''),
       COALESCE(approval_note,''), COALESCE(risk_level,''), COALESCE(change_summary,''),
       created_at::text, updated_at::text
FROM release_requests ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ReleaseRequest, 0)
	for rows.Next() {
		var req ReleaseRequest
		if err := rows.Scan(&req.ID, &req.AppName, &req.Env, &req.Image, &req.ImageTag, &req.JenkinsJob, &req.JenkinsBuild,
			&req.ArgoCDApp, &req.GitRevision, &req.Status, &req.Requester, &req.Approver, &req.ApprovalNote,
			&req.RiskLevel, &req.ChangeSummary, &req.CreatedAt, &req.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func releaseRequestsRootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 50
		fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
		items, err := releaseRequestStore.List(r.Context(), limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, envelope{"error": "list_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, envelope{"items": items, "count": len(items)})
	case http.MethodPost:
		var req ReleaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_json", "message": err.Error()})
			return
		}
		req.AppName = strings.TrimSpace(req.AppName)
		req.ImageTag = strings.TrimSpace(req.ImageTag)
		if req.AppName == "" || req.ImageTag == "" {
			writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_request", "message": "app_name and image_tag required"})
			return
		}
		if req.Env == "" {
			req.Env = "dev"
		}
		if req.Status == "" {
			req.Status = "pending"
		}
		if app, ok := findApp(apps, req.AppName); ok {
			req.ArgoCDApp = firstNonEmpty(req.ArgoCDApp, app.ArgoCDApp)
			req.JenkinsJob = firstNonEmpty(req.JenkinsJob, jenkinsJobName(app.Name))
			req.Image = firstNonEmpty(req.Image, imageWithTag(app.Image, req.ImageTag))
		}
		saved, err := releaseRequestStore.Save(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, envelope{"error": "save_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, envelope{"request": saved})
	default:
		methodNotAllowed(w)
	}
}

func releaseRequestsSubHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/cicd/release-requests/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		notFoundHandler(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		item, ok, err := releaseRequestStore.Get(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, envelope{"error": "get_failed", "message": err.Error()})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, envelope{"error": "not_found", "id": id})
			return
		}
		writeJSON(w, http.StatusOK, envelope{"request": item})
		return
	}
	if len(parts) == 2 && parts[1] == "decide" && r.Method == http.MethodPost {
		var body struct {
			Decision string `json:"decision"` // approved|rejected
			Approver string `json:"approver"`
			Note     string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_json", "message": err.Error()})
			return
		}
		decision := strings.ToLower(strings.TrimSpace(body.Decision))
		if decision != "approved" && decision != "rejected" {
			writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_decision", "message": "decision must be approved|rejected"})
			return
		}
		item, ok, err := releaseRequestStore.Get(r.Context(), id)
		if err != nil || !ok {
			writeJSON(w, http.StatusNotFound, envelope{"error": "not_found", "id": id})
			return
		}
		item.Status = decision
		item.Approver = firstNonEmpty(body.Approver, "operator")
		item.ApprovalNote = body.Note
		saved, err := releaseRequestStore.Save(r.Context(), item)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, envelope{"error": "save_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, envelope{"request": saved})
		return
	}
	notFoundHandler(w, r)
}
