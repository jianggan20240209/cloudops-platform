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

func main() {
	addr := env("HTTP_ADDR", ":8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	mux.HandleFunc("/api/v1/version", versionHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/", notFoundHandler)

	server := &http.Server{
		Addr:              addr,
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("cloudops-gateway starting addr=%s version=%s commit=%s", addr, version, commit)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("cloudops-gateway stopped: %v", err)
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		"status":  "ok",
		"service": "cloudops-gateway",
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
		"service": "cloudops-gateway",
	})
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		"service":    "cloudops-gateway",
		"version":    version,
		"commit":     commit,
		"build_time": buildTime,
		"uptime_sec": int64(time.Since(startedAt).Seconds()),
	})
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	uptime := time.Since(startedAt).Seconds()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprintf(w, "# HELP cloudops_gateway_info Build information for cloudops-gateway.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_gateway_info gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_gateway_info{version=%s,commit=%s,build_time=%s} 1\n", label(version), label(commit), label(buildTime))
	_, _ = fmt.Fprintf(w, "# HELP cloudops_gateway_uptime_seconds Uptime of cloudops-gateway in seconds.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_gateway_uptime_seconds gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_gateway_uptime_seconds %.0f\n", uptime)
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

func writeJSON(w http.ResponseWriter, status int, payload envelope) {
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
