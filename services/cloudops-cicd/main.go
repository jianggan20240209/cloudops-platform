package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
	startedAt = time.Now()
)

type envelope map[string]any

type AppSummary struct {
	Name        string `json:"name"`
	Env         string `json:"env"`
	Namespace   string `json:"namespace"`
	ArgoCDApp   string `json:"argocd_app"`
	Image       string `json:"image"`
	CurrentTag  string `json:"current_tag"`
	Sync        string `json:"sync"`
	Health      string `json:"health"`
	LastRelease string `json:"last_release"`
	Revision    string `json:"revision,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	Source      string `json:"source"`
}

type ReleaseSummary struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	Status    string `json:"status"`
}

var apps = []AppSummary{
	{
		Name:        "cloudops-gateway",
		Env:         "dev",
		Namespace:   "cloudops-dev",
		ArgoCDApp:   "cloudops-gateway-dev",
		Image:       "harbor-server.jianggan.cn/cloudops/cloudops-gateway:main-14",
		CurrentTag:  "main-14",
		Sync:        "Synced",
		Health:      "Healthy",
		LastRelease: "main-14",
		Source:      "static",
	},
	{
		Name:        "cloudops-web",
		Env:         "dev",
		Namespace:   "cloudops-dev",
		ArgoCDApp:   "cloudops-web-dev",
		Image:       "harbor-server.jianggan.cn/cloudops/cloudops-web:main-8",
		CurrentTag:  "main-8",
		Sync:        "Synced",
		Health:      "Healthy",
		LastRelease: "main-8",
		Source:      "static",
	},
	{
		Name:        "cloudops-cicd",
		Env:         "dev",
		Namespace:   "cloudops-dev",
		ArgoCDApp:   "cloudops-cicd-dev",
		Image:       "harbor-server.jianggan.cn/cloudops/cloudops-cicd:main-2",
		CurrentTag:  "main-2",
		Sync:        "Synced",
		Health:      "Healthy",
		LastRelease: "main-2",
		Source:      "static",
	},
}

var releases = map[string][]ReleaseSummary{
	"cloudops-gateway": {
		{
			Version:   "main-14",
			Commit:    "c308075261dc",
			BuildTime: "2026-06-25T15:44:22Z",
			Status:    "Healthy",
		},
	},
	"cloudops-web": {
		{
			Version:   "main-8",
			Commit:    "unknown",
			BuildTime: "2026-06-25T15:48:14Z",
			Status:    "Healthy",
		},
	},
	"cloudops-cicd": {
		{
			Version:   "main-2",
			Commit:    "unknown",
			BuildTime: "2026-06-26T00:00:00Z",
			Status:    "Healthy",
		},
	},
}

type ArgoCDClient struct {
	server string
	token  string
	client *http.Client
}

type argoApplication struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Destination struct {
			Namespace string `json:"namespace"`
		} `json:"destination"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
		Summary struct {
			Images []string `json:"images"`
		} `json:"summary"`
		ReconciledAt string `json:"reconciledAt"`
	} `json:"status"`
}

func main() {
	addr := env("HTTP_ADDR", ":8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	mux.HandleFunc("/api/healthz", healthzHandler)
	mux.HandleFunc("/api/readyz", readyzHandler)
	mux.HandleFunc("/api/v1/version", versionHandler)
	mux.HandleFunc("/api/v1/cicd/apps", appsHandler)
	mux.HandleFunc("/api/v1/cicd/apps/", appDetailHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/", notFoundHandler)

	server := &http.Server{
		Addr:              addr,
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("cloudops-cicd starting addr=%s version=%s commit=%s", addr, version, commit)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("cloudops-cicd stopped: %v", err)
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		"status":  "ok",
		"service": "cloudops-cicd",
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func readyzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		"status":  "ready",
		"service": "cloudops-cicd",
	})
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		"service":    "cloudops-cicd",
		"version":    version,
		"commit":     commit,
		"build_time": buildTime,
		"uptime_sec": int64(time.Since(startedAt).Seconds()),
	})
}

func appsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if r.URL.Path != "/api/v1/cicd/apps" {
		notFoundHandler(w, r)
		return
	}

	items, source, err := loadApps()
	payload := envelope{
		"items":  items,
		"total":  len(items),
		"source": source,
	}
	if err != nil {
		payload["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, payload)
}

func loadApps() ([]AppSummary, string, error) {
	client, ok := newArgoCDClientFromEnv()
	if !ok {
		return apps, "static", nil
	}

	items := make([]AppSummary, 0, len(apps))
	for _, fallback := range apps {
		app, err := client.GetApplication(fallback.ArgoCDApp)
		if err != nil {
			return apps, "static", fmt.Errorf("argocd query failed, fallback to static data: %w", err)
		}
		items = append(items, appFromArgo(fallback, app))
	}
	return items, "argocd", nil
}

func newArgoCDClientFromEnv() (*ArgoCDClient, bool) {
	server := strings.TrimRight(env("ARGOCD_SERVER", ""), "/")
	token := strings.TrimSpace(os.Getenv("ARGOCD_AUTH_TOKEN"))
	if server == "" || token == "" {
		return nil, false
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: envBool("ARGOCD_INSECURE", true)} //nolint:gosec

	return &ArgoCDClient{
		server: server,
		token:  token,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}, true
}

func (c *ArgoCDClient) GetApplication(name string) (argoApplication, error) {
	var app argoApplication
	req, err := http.NewRequest(http.MethodGet, c.server+"/api/v1/applications/"+name, nil)
	if err != nil {
		return app, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return app, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return app, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return app, fmt.Errorf("argocd api status=%d body=%s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, &app); err != nil {
		return app, err
	}
	return app, nil
}

func appFromArgo(fallback AppSummary, app argoApplication) AppSummary {
	image := fallback.Image
	if len(app.Status.Summary.Images) > 0 {
		image = app.Status.Summary.Images[0]
	}

	summary := fallback
	summary.Namespace = firstNonEmpty(app.Spec.Destination.Namespace, fallback.Namespace)
	summary.Image = image
	summary.CurrentTag = imageTag(image, fallback.CurrentTag)
	summary.Sync = firstNonEmpty(app.Status.Sync.Status, fallback.Sync)
	summary.Health = firstNonEmpty(app.Status.Health.Status, fallback.Health)
	summary.LastRelease = summary.CurrentTag
	summary.Revision = app.Status.Sync.Revision
	summary.UpdatedAt = app.Status.ReconciledAt
	summary.Source = "argocd"
	return summary
}

func appDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/cicd/apps/")
	path = strings.Trim(path, "/")
	if path == "" {
		notFoundHandler(w, r)
		return
	}

	parts := strings.Split(path, "/")
	items, _, _ := loadApps()
	app, ok := findApp(items, parts[0])
	if !ok {
		writeJSON(w, http.StatusNotFound, envelope{
			"error":   "app_not_found",
			"message": "cloudops cicd app not found",
			"name":    parts[0],
		})
		return
	}

	if len(parts) == 1 {
		writeJSON(w, http.StatusOK, app)
		return
	}

	switch parts[1] {
	case "status":
		writeJSON(w, http.StatusOK, envelope{
			"name":   app.Name,
			"sync":   app.Sync,
			"health": app.Health,
			"image":  app.Image,
			"tag":    app.CurrentTag,
		})
	case "releases":
		writeJSON(w, http.StatusOK, envelope{
			"name":  app.Name,
			"items": releases[app.Name],
			"total": len(releases[app.Name]),
		})
	default:
		notFoundHandler(w, r)
	}
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	uptime := time.Since(startedAt).Seconds()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP cloudops_cicd_info Build information for cloudops-cicd.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_cicd_info gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_cicd_info{version=%s,commit=%s,build_time=%s} 1\n", label(version), label(commit), label(buildTime))
	_, _ = fmt.Fprintf(w, "# HELP cloudops_cicd_uptime_seconds Uptime of cloudops-cicd in seconds.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_cicd_uptime_seconds gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_cicd_uptime_seconds %.0f\n", uptime)
	_, _ = fmt.Fprintf(w, "# HELP cloudops_cicd_managed_apps Number of applications managed by cloudops-cicd.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_cicd_managed_apps gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_cicd_managed_apps %d\n", len(apps))
}

func findApp(items []AppSummary, name string) (AppSummary, bool) {
	for _, app := range items {
		if app.Name == name {
			return app, true
		}
	}
	return AppSummary{}, false
}

func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, envelope{
		"error":   "not_found",
		"message": "route not found",
		"path":    r.URL.Path,
	})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, envelope{
		"error":   "method_not_allowed",
		"message": "only GET is supported",
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("method=%s path=%s remote=%s duration_ms=%d", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start).Milliseconds())
	})
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func imageTag(image string, fallback string) string {
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[lastColon+1:]
	}
	return fallback
}

func label(value string) string {
	return strconv.Quote(value)
}
