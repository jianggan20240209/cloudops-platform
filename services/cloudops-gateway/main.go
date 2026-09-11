package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

const appName = "cloudops-gateway"

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
	startedAt = time.Now()
)

type envelope map[string]any

func main() {
	addr := env("HTTP_ADDR", ":8080")
	ctx := context.Background()

	shutdown, err := initTracer(ctx)
	if err != nil {
		logJSON("error", "otel_init_failed", map[string]any{"error": err.Error()})
		shutdown = func(context.Context) error { return nil }
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(shCtx)
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	mux.HandleFunc("/api/healthz", healthzHandler)
	mux.HandleFunc("/api/readyz", readyzHandler)
	mux.HandleFunc("/api/v1/version", versionHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/", notFoundHandler)

	handler := otelhttp.NewHandler(requestLogger(mux), appName)

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logJSON("info", "service_starting", map[string]any{
		"addr":    addr,
		"version": version,
		"commit":  commit,
	})
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logJSON("error", "service_stopped", map[string]any{"error": err.Error()})
		os.Exit(1)
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
		logJSON("error", "write_response_failed", map[string]any{"error": err.Error()})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		traceID, requestID := resolveTraceIDs(r)
		w.Header().Set("X-Request-Id", requestID)
		w.Header().Set("X-Trace-Id", traceID)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Probe noise: keep fields optional; skip access log for health/metrics.
		if isProbePath(r.URL.Path) {
			return
		}

		logJSON("info", "http_request", map[string]any{
			"method":      r.Method,
			"path":        r.URL.Path,
			"remote":      r.RemoteAddr,
			"status":      rec.status,
			"duration_ms": time.Since(start).Milliseconds(),
			"trace_id":    traceID,
			"request_id":  requestID,
		})
	})
}

func isProbePath(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/api/healthz", "/api/readyz", "/metrics":
		return true
	default:
		return false
	}
}

func resolveTraceIDs(r *http.Request) (traceID, requestID string) {
	requestID = firstNonEmpty(
		r.Header.Get("X-Request-Id"),
		r.Header.Get("X-Request-ID"),
	)
	if span := trace.SpanFromContext(r.Context()); span.SpanContext().IsValid() {
		traceID = span.SpanContext().TraceID().String()
	}
	if traceID == "" {
		traceID = firstNonEmpty(
			r.Header.Get("X-Trace-Id"),
			r.Header.Get("X-Trace-ID"),
			traceIDFromTraceparent(r.Header.Get("traceparent")),
			requestID,
		)
	}
	if requestID == "" {
		requestID = newID()
	}
	if traceID == "" {
		traceID = requestID
	}
	return traceID, requestID
}

func traceIDFromTraceparent(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) >= 2 && len(parts[1]) == 32 {
		return parts[1]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func logJSON(level, msg string, fields map[string]any) {
	payload := map[string]any{
		"level": level,
		"msg":   msg,
		"app":   appName,
		"time":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range fields {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		log.Printf(`{"level":"error","msg":"log_marshal_failed","app":%q,"error":%q}`, appName, err.Error())
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
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
