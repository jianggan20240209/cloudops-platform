package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const appName = "cloudops-observe"

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
	startedAt = time.Now()
)

type envelope map[string]any

type VictoriaLogsClient struct {
	server string
	client *http.Client
}

func main() {
	addr := env("HTTP_ADDR", ":8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	mux.HandleFunc("/api/healthz", healthzHandler)
	mux.HandleFunc("/api/readyz", readyzHandler)
	mux.HandleFunc("/api/v1/version", versionHandler)
	mux.HandleFunc("/api/v1/observe/logs", logsHandler)
	mux.HandleFunc("/api/v1/observe/alerts", alertsHandler)
	mux.HandleFunc("/api/v1/observe/alerts/detail", alertDetailHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/", notFoundHandler)

	server := &http.Server{
		Addr:              addr,
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logJSON("info", "service_starting", map[string]any{
		"addr":              addr,
		"version":           version,
		"commit":            commit,
		"victoria_logs_url":  env("VICTORIA_LOGS_URL", defaultVictoriaLogsURL()),
		"alertmanager_url":   env("ALERTMANAGER_URL", defaultAlertmanagerURL()),
		"prometheus_server":  env("PROMETHEUS_SERVER", defaultPrometheusURL()),
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
		"service": appName,
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
		"service": appName,
	})
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		"service":    appName,
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
	_, _ = fmt.Fprintf(w, "# HELP cloudops_observe_info Build information for cloudops-observe.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_observe_info gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_observe_info{version=%s,commit=%s,build_time=%s} 1\n", label(version), label(commit), label(buildTime))
	_, _ = fmt.Fprintf(w, "# HELP cloudops_observe_uptime_seconds Uptime of cloudops-observe in seconds.\n")
	_, _ = fmt.Fprintf(w, "# TYPE cloudops_observe_uptime_seconds gauge\n")
	_, _ = fmt.Fprintf(w, "cloudops_observe_uptime_seconds %.0f\n", uptime)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	q := r.URL.Query()
	namespace := strings.TrimSpace(q.Get("namespace"))
	pod := strings.TrimSpace(q.Get("pod"))
	container := strings.TrimSpace(q.Get("container"))
	keyword := strings.TrimSpace(q.Get("q"))
	traceID := strings.TrimSpace(firstNonEmpty(q.Get("trace_id"), q.Get("traceId")))
	limit := parseLimit(q.Get("limit"), 50, 1, 200)
	start := strings.TrimSpace(q.Get("start"))
	end := strings.TrimSpace(q.Get("end"))

	if err := validateSelector("namespace", namespace); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_namespace", "message": err.Error()})
		return
	}
	if err := validateSelector("pod", pod); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_pod", "message": err.Error()})
		return
	}
	if err := validateSelector("container", container); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_container", "message": err.Error()})
		return
	}
	if namespace == "" && pod == "" && container == "" && keyword == "" && traceID == "" {
		writeJSON(w, http.StatusBadRequest, envelope{
			"error":   "missing_filter",
			"message": "provide at least one of namespace, pod, container, q, trace_id",
		})
		return
	}

	query, err := buildLogsQL(namespace, pod, container, keyword, traceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_query", "message": err.Error()})
		return
	}

	client := newVictoriaLogsClientFromEnv()
	entries, rawCount, err := client.Query(r.Context(), query, limit, start, end)
	if err != nil {
		logJSON("error", "victorialogs_query_failed", map[string]any{
			"error": err.Error(),
			"query": query,
		})
		writeJSON(w, http.StatusBadGateway, envelope{
			"error":   "victorialogs_unavailable",
			"message": err.Error(),
			"query":   query,
		})
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		"service": appName,
		"query":   query,
		"limit":   limit,
		"count":   len(entries),
		"raw":     rawCount,
		"items":   entries,
	})
}

func buildLogsQL(namespace, pod, container, keyword, traceID string) (string, error) {
	parts := make([]string, 0, 5)
	if namespace != "" {
		parts = append(parts, "namespace:"+namespace)
	}
	if pod != "" {
		parts = append(parts, "pod:"+pod)
	}
	if container != "" {
		parts = append(parts, "container:"+container)
	}
	if keyword != "" {
		if len(keyword) > 256 {
			return "", fmt.Errorf("q too long")
		}
		parts = append(parts, quoteLogsQL(keyword))
	}
	if traceID != "" {
		if err := validateTraceID(traceID); err != nil {
			return "", err
		}
		// Field-level filter may be empty until Alloy unpacks JSON; keyword still hits _msg.
		parts = append(parts, quoteLogsQL(traceID))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("empty LogsQL")
	}
	return strings.Join(parts, " "), nil
}

func quoteLogsQL(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func validateSelector(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 253 {
		return fmt.Errorf("%s too long", name)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == '*' {
			continue
		}
		return fmt.Errorf("%s contains invalid character %q", name, r)
	}
	return nil
}

func validateTraceID(value string) error {
	if len(value) > 128 {
		return fmt.Errorf("trace_id too long")
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("trace_id contains invalid character %q", r)
	}
	return nil
}

func parseLimit(raw string, fallback, min, max int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func defaultVictoriaLogsURL() string {
	return "http://vls-victoria-logs-single-server.logging.svc.cluster.local:9428"
}

func newVictoriaLogsClientFromEnv() *VictoriaLogsClient {
	return &VictoriaLogsClient{
		server: strings.TrimRight(env("VICTORIA_LOGS_URL", defaultVictoriaLogsURL()), "/"),
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *VictoriaLogsClient) Query(ctx context.Context, query string, limit int, start, end string) ([]map[string]any, int, error) {
	apiURL, err := url.Parse(c.server + "/select/logsql/query")
	if err != nil {
		return nil, 0, err
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("limit", strconv.Itoa(limit))
	if start != "" {
		params.Set("start", start)
	}
	if end != "" {
		params.Set("end", end)
	}
	apiURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/stream+json, application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("victorialogs status=%d body=%s", resp.StatusCode, truncate(string(body), 512))
	}

	return parseVictoriaLogsBody(body)
}

func parseVictoriaLogsBody(body []byte) ([]map[string]any, int, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return []map[string]any{}, 0, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, 0, err
		}
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			out = append(out, normalizeLogEntry(item))
		}
		return out, len(arr), nil
	}

	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	rawCount := 0
	out := make([]map[string]any, 0, 32)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		rawCount++
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, 0, fmt.Errorf("parse log line: %w", err)
		}
		out = append(out, normalizeLogEntry(item))
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return out, rawCount, nil
}

func normalizeLogEntry(item map[string]any) map[string]any {
	msg, _ := item["_msg"].(string)
	entry := map[string]any{
		"time":      firstNonEmpty(asString(item["_time"]), asString(item["time"])),
		"namespace": asString(item["namespace"]),
		"pod":       asString(item["pod"]),
		"container": asString(item["container"]),
		"cluster":   asString(item["cluster"]),
		"msg":       msg,
	}
	if nested := extractNestedJSON(msg); nested != nil {
		if v := asString(nested["trace_id"]); v != "" {
			entry["trace_id"] = v
		}
		if v := asString(nested["request_id"]); v != "" {
			entry["request_id"] = v
		}
		if v := asString(nested["level"]); v != "" {
			entry["level"] = v
		}
		if v := asString(nested["msg"]); v != "" {
			entry["app_msg"] = v
		}
		if v := asString(nested["path"]); v != "" {
			entry["path"] = v
		}
		if v := asString(nested["status"]); v != "" {
			entry["status"] = v
		}
		entry["app_json"] = nested
	}
	if v := asString(item["trace_id"]); v != "" {
		entry["trace_id"] = v
	}
	if v := asString(item["request_id"]); v != "" {
		entry["request_id"] = v
	}
	return entry
}

func extractNestedJSON(msg string) map[string]any {
	start := strings.Index(msg, "{")
	end := strings.LastIndex(msg, "}")
	if start < 0 || end <= start {
		return nil
	}
	var nested map[string]any
	if err := json.Unmarshal([]byte(msg[start:end+1]), &nested); err != nil {
		return nil
	}
	return nested
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	traceID = firstNonEmpty(
		r.Header.Get("X-Trace-Id"),
		r.Header.Get("X-Trace-ID"),
		traceIDFromTraceparent(r.Header.Get("traceparent")),
		requestID,
	)
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
