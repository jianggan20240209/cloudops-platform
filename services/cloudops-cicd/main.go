package main

import (
	"encoding/json"
	"fmt"
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

	writeJSON(w, http.StatusOK, envelope{
		"items": apps,
		"total": len(apps),
	})
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
	app, ok := findApp(parts[0])
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

func findApp(name string) (AppSummary, bool) {
	for _, app := range apps {
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

func label(value string) string {
	return strconv.Quote(value)
}
