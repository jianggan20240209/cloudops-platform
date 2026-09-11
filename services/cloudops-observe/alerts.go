package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func defaultAlertmanagerURL() string {
	return "http://kube-prometheus-stack-alertmanager.monitoring.svc:9093"
}

func defaultPrometheusURL() string {
	return "http://kube-prometheus-stack-prometheus.monitoring.svc:9090"
}

type AlertmanagerClient struct {
	server string
	client *http.Client
}

type PrometheusClient struct {
	server string
	client *http.Client
}

func newAlertmanagerClientFromEnv() *AlertmanagerClient {
	return &AlertmanagerClient{
		server: strings.TrimRight(env("ALERTMANAGER_URL", defaultAlertmanagerURL()), "/"),
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

func newPrometheusClientFromEnv() *PrometheusClient {
	return &PrometheusClient{
		server: strings.TrimRight(env("PROMETHEUS_SERVER", defaultPrometheusURL()), "/"),
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

func alertsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	q := r.URL.Query()
	filter := strings.TrimSpace(q.Get("filter"))
	severity := strings.TrimSpace(q.Get("severity"))
	activeOnly := q.Get("active") != "0"

	if err := validateSelector("filter", filter); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_filter", "message": err.Error()})
		return
	}
	if err := validateSelector("severity", severity); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_severity", "message": err.Error()})
		return
	}

	client := newAlertmanagerClientFromEnv()
	raw, err := client.ListAlerts(r.Context(), filter, activeOnly)
	if err != nil {
		logJSON("error", "alertmanager_query_failed", map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusBadGateway, envelope{
			"error":   "alertmanager_unavailable",
			"message": err.Error(),
		})
		return
	}

	items := make([]map[string]any, 0, len(raw))
	for _, a := range raw {
		item := summarizeAlert(a)
		if severity != "" {
			sev, _ := item["severity"].(string)
			if !strings.EqualFold(sev, severity) {
				continue
			}
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, envelope{
		"service": appName,
		"source":  client.server,
		"count":   len(items),
		"items":   items,
	})
}

func alertDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	q := r.URL.Query()
	fingerprint := strings.TrimSpace(q.Get("fingerprint"))
	alertname := strings.TrimSpace(q.Get("alertname"))
	namespace := strings.TrimSpace(q.Get("namespace"))
	pod := strings.TrimSpace(q.Get("pod"))

	if fingerprint == "" && alertname == "" {
		writeJSON(w, http.StatusBadRequest, envelope{
			"error":   "missing_selector",
			"message": "provide fingerprint or alertname",
		})
		return
	}
	if err := validateSelector("alertname", alertname); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_alertname", "message": err.Error()})
		return
	}
	if err := validateSelector("namespace", namespace); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_namespace", "message": err.Error()})
		return
	}
	if err := validateSelector("pod", pod); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_pod", "message": err.Error()})
		return
	}

	am := newAlertmanagerClientFromEnv()
	filter := ""
	if alertname != "" {
		filter = `alertname="` + alertname + `"`
	}
	raw, err := am.ListAlerts(r.Context(), filter, true)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, envelope{"error": "alertmanager_unavailable", "message": err.Error()})
		return
	}

	var matched map[string]any
	for _, a := range raw {
		sum := summarizeAlert(a)
		fp, _ := sum["fingerprint"].(string)
		an, _ := sum["alertname"].(string)
		ns, _ := sum["namespace"].(string)
		if fingerprint != "" && fp != fingerprint {
			continue
		}
		if alertname != "" && !strings.EqualFold(an, alertname) {
			continue
		}
		if namespace != "" && ns != namespace {
			continue
		}
		matched = sum
		matched["raw"] = a
		break
	}
	if matched == nil {
		writeJSON(w, http.StatusNotFound, envelope{"error": "alert_not_found"})
		return
	}

	if namespace == "" {
		if v, ok := matched["namespace"].(string); ok {
			namespace = v
		}
	}
	if pod == "" {
		if v, ok := matched["pod"].(string); ok {
			pod = v
		}
	}
	an, _ := matched["alertname"].(string)

	prom := newPrometheusClientFromEnv()
	metrics, _ := prom.Query(r.Context(), suggestPromQL(an, namespace, pod))

	logs := []map[string]any{}
	if namespace != "" || pod != "" {
		vl := newVictoriaLogsClientFromEnv()
		query, qerr := buildLogsQL(namespace, pod, "", an, "")
		if qerr == nil {
			entries, _, lerr := vl.Query(r.Context(), query, 20, "", "")
			if lerr == nil {
				logs = entries
			}
		}
	}

	writeJSON(w, http.StatusOK, envelope{
		"service":  appName,
		"alert":    matched,
		"metrics":  metrics,
		"logs":     logs,
		"events":   []map[string]any{}, // Day 60: kubectl events via future k8s client; placeholder
		"runbook":  runbookFor(an),
		"links": map[string]string{
			"prometheus":   "https://prometheus.jianggan.cn",
			"alertmanager": "https://alertmanager.jianggan.cn",
			"grafana":      "https://grafana.jianggan.cn",
			"cloudops_logs": fmt.Sprintf("https://cloudops.jianggan.cn/?namespace=%s&pod=%s&auto=1",
				url.QueryEscape(namespace), url.QueryEscape(pod)),
		},
	})
}

func (c *AlertmanagerClient) ListAlerts(ctx context.Context, filter string, activeOnly bool) ([]map[string]any, error) {
	apiURL, err := url.Parse(c.server + "/api/v2/alerts")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if filter != "" {
		params.Set("filter", filter)
	}
	if activeOnly {
		params.Set("active", "true")
		params.Set("silenced", "false")
		params.Set("inhibited", "false")
	}
	apiURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("alertmanager status=%d body=%s", resp.StatusCode, truncate(string(body), 512))
	}

	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *PrometheusClient) Query(ctx context.Context, query string) (map[string]any, error) {
	if strings.TrimSpace(query) == "" {
		return map[string]any{"query": "", "result": []any{}}, nil
	}
	apiURL, err := url.Parse(c.server + "/api/v1/query")
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", query)
	apiURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus status=%d body=%s", resp.StatusCode, truncate(string(body), 512))
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func summarizeAlert(a map[string]any) map[string]any {
	labels, _ := a["labels"].(map[string]any)
	annotations, _ := a["annotations"].(map[string]any)
	status, _ := a["status"].(map[string]any)
	get := func(m map[string]any, k string) string {
		if m == nil {
			return ""
		}
		if v, ok := m[k]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	out := map[string]any{
		"fingerprint": getString(a, "fingerprint"),
		"alertname":   get(labels, "alertname"),
		"severity":   firstNonEmpty(get(labels, "severity"), get(labels, "priority"), "unknown"),
		"namespace":   get(labels, "namespace"),
		"pod":         firstNonEmpty(get(labels, "pod"), get(labels, "pod_name")),
		"instance":    get(labels, "instance"),
		"job":         get(labels, "job"),
		"summary":     get(annotations, "summary"),
		"description": get(annotations, "description"),
		"runbook_url": get(annotations, "runbook_url"),
		"startsAt":    getString(a, "startsAt"),
		"updatedAt":   getString(a, "updatedAt"),
		"state":       get(status, "state"),
	}
	return out
}

func getString(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func suggestPromQL(alertname, namespace, pod string) string {
	switch alertname {
	case "CloudOpsGatewayHighErrorRate":
		return `sum(rate(http_requests_total{job="cloudops-gateway",code=~"5.."}[5m])) / sum(rate(http_requests_total{job="cloudops-gateway"}[5m]))`
	case "KubePodCrashLooping", "KubePodNotReady":
		if namespace != "" && pod != "" {
			return fmt.Sprintf(`kube_pod_status_phase{namespace="%s",pod="%s"}`, namespace, pod)
		}
		if namespace != "" {
			return fmt.Sprintf(`kube_pod_container_status_waiting_reason{namespace="%s",reason="CrashLoopBackOff"}`, namespace)
		}
	case "NodeFilesystemAlmostOutOfSpace", "NodeDiskPressure":
		return `node_filesystem_avail_bytes{fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{fstype!~"tmpfs|overlay"}`
	case "CoreDNSDown", "CoreDNSErrorsHigh":
		return `up{job="coredns"}`
	case "Day61RedisDown":
		return `up{job="day61-redis"}`
	}
	if namespace != "" {
		return fmt.Sprintf(`up{namespace="%s"}`, namespace)
	}
	return `up`
}

func runbookFor(alertname string) map[string]any {
	base := map[string]any{
		"title": "通用故障定位",
		"steps": []string{
			"确认告警仍 firing（Alertmanager）",
			"看 Prometheus 表达式与标签",
			"CloudOps 日志按 namespace/pod/trace_id 查证",
			"必要时 Hubble / Tempo 交叉验证",
		},
		"doc": "专用测试环境/23_故障定位SOP.md",
	}
	switch alertname {
	case "KubePodOOMKilled", "KubePodCrashLooping":
		base["title"] = "Pod OOM / CrashLoop"
		base["steps"] = []string{
			"kubectl describe pod / events 看 OOMKilled",
			"调大 limits 或查内存泄漏",
			"看最近发布 imageTag",
		}
	case "CloudOpsGatewayHighErrorRate":
		base["title"] = "CloudOps 网关 5xx"
		base["steps"] = []string{
			"看 gateway 日志 trace_id",
			"Tempo 打开同一 trace",
			"核对 rollout/canary 权重",
		}
	case "Day61RedisDown":
		base["title"] = "演练 Redis 断连"
		base["steps"] = []string{
			"确认 day61-redis Deployment/Service",
			"恢复: kubectl -n cloudops-dev scale deploy/day61-redis --replicas=1",
		}
	}
	return base
}
