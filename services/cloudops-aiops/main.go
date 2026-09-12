package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

const appName = "cloudops-aiops"

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
	mux.HandleFunc("/healthz", healthz)
	mux.HandleFunc("/readyz", readyz)
	mux.HandleFunc("/api/healthz", healthz)
	mux.HandleFunc("/api/readyz", readyz)
	mux.HandleFunc("/api/v1/version", versionHandler)
	mux.HandleFunc("/api/v1/aiops/ask", askHandler)
	mux.HandleFunc("/api/v1/aiops/diagnose", diagnoseHandler)
	mux.HandleFunc("/api/v1/aiops/tools", toolsHandler)
	mux.HandleFunc("/api/v1/aiops/tools/", toolInvokeHandler)
	mux.HandleFunc("/api/v1/aiops/remediation/suggest", remediationSuggestHandler)
	mux.HandleFunc("/api/v1/aiops/backup/jobs", backupJobsHandler)
	mux.HandleFunc("/api/v1/aiops/retro/draft", retroDraftHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/", notFound)

	logJSON("info", "service_starting", map[string]any{"addr": addr, "version": version})
	if err := http.ListenAndServe(addr, requestLogger(mux)); err != nil {
		logJSON("error", "service_stopped", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
}

func healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, envelope{"status": "ok", "service": appName, "time": time.Now().UTC().Format(time.RFC3339)})
}
func readyz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, envelope{"status": "ready", "service": appName})
}
func versionHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, envelope{"service": appName, "version": version, "commit": commit, "build_time": buildTime, "uptime_seconds": int(time.Since(startedAt).Seconds())})
}
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# HELP cloudops_aiops_up 1 if process is up\n# TYPE cloudops_aiops_up gauge\ncloudops_aiops_up 1\n")
}
func notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 404, envelope{"error": "not_found", "path": r.URL.Path})
}

func askHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Question string `json:"question"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	q := strings.TrimSpace(req.Question)
	if q == "" {
		writeJSON(w, 400, envelope{"error": "question_required"})
		return
	}
	hits := searchKB(q, 3)
	answer := synthesizeAnswer(q, hits)
	writeJSON(w, 200, envelope{
		"service":  appName,
		"mode":     "local_rag_fts",
		"question": q,
		"answer":   answer,
		"citations": hits,
		"prompt_hint": "ADR-010: local keyword/FTS first; LLM optional later",
	})
}

func diagnoseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	alertname := r.URL.Query().Get("alertname")
	namespace := firstNonEmpty(r.URL.Query().Get("namespace"), "cloudops-dev")
	pod := r.URL.Query().Get("pod")
	if r.Method == http.MethodPost {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		alertname = firstNonEmpty(body["alertname"], alertname)
		namespace = firstNonEmpty(body["namespace"], namespace)
		pod = firstNonEmpty(body["pod"], pod)
	}
	if alertname == "" {
		alertname = "KubePodCrashLooping"
	}
	observe := strings.TrimRight(env("OBSERVE_BASE_URL", "http://cloudops-observe.cloudops-dev.svc.cluster.local"), "/")
	detailURL := observe + "/api/v1/observe/alerts/detail?alertname=" + urlQuery(alertname) + "&namespace=" + urlQuery(namespace)
	if pod != "" {
		detailURL += "&pod=" + urlQuery(pod)
	}
	detail, err := httpGetJSON(detailURL)
	summary := "基于告警与知识库的启发式诊断（非模型推理）。"
	rootCauses := []string{}
	actions := []map[string]any{}
	switch {
	case strings.Contains(strings.ToLower(alertname), "oom"):
		rootCauses = []string{"容器 memory limit 过小", "内存泄漏", "突发流量"}
		actions = []map[string]any{
			{"risk": "low", "action": "collect_heap_or_rss", "need_approval": false},
			{"risk": "medium", "action": "raise_memory_limit", "need_approval": true},
			{"risk": "high", "action": "restart_pod", "need_approval": true},
		}
	case strings.Contains(strings.ToLower(alertname), "crash"):
		rootCauses = []string{"启动失败/配置错误", "依赖不可达", "探针过严"}
		actions = []map[string]any{
			{"risk": "low", "action": "read_logs_events", "need_approval": false},
			{"risk": "medium", "action": "rollback_image", "need_approval": true},
		}
	default:
		rootCauses = []string{"需结合指标/日志/Event 继续排查"}
		actions = []map[string]any{{"risk": "low", "action": "open_alert_detail", "need_approval": false}}
	}
	kb := searchKB(alertname+" "+namespace+" 诊断", 2)
	writeJSON(w, 200, envelope{
		"service":            appName,
		"alertname":          alertname,
		"namespace":          namespace,
		"pod":                pod,
		"summary":            summary,
		"root_cause_candidates": rootCauses,
		"suggested_actions":  actions,
		"observe_detail":     detail,
		"observe_error":      errString(err),
		"citations":          kb,
		"flow":               []string{"alert", "metrics", "logs", "events", "runbook", "approval"},
	})
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mutating    bool   `json:"mutating"`
}

func toolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	writeJSON(w, 200, envelope{
		"service": appName,
		"policy":  "query_only_by_default; mutating requires approval",
		"tools": []toolDef{
			{Name: "observe_logs", Description: "Query VictoriaLogs via cloudops-observe", Mutating: false},
			{Name: "observe_alerts", Description: "List Alertmanager alerts", Mutating: false},
			{Name: "observe_resources", Description: "FinOps resource waste list", Mutating: false},
			{Name: "cicd_apps", Description: "List Argo/Harbor app status via cicd", Mutating: false},
			{Name: "cicd_builds", Description: "List Jenkins build history", Mutating: false},
			{Name: "ask_kb", Description: "RAG ask over local KB", Mutating: false},
		},
	})
}

func toolInvokeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/aiops/tools/"), "/")
	name = strings.TrimSuffix(name, "/invoke")
	var args map[string]any
	_ = json.NewDecoder(r.Body).Decode(&args)
	observe := strings.TrimRight(env("OBSERVE_BASE_URL", "http://cloudops-observe.cloudops-dev.svc.cluster.local"), "/")
	cicd := strings.TrimRight(env("CICD_BASE_URL", "http://cloudops-cicd.cloudops-dev.svc.cluster.local"), "/")
	var (
		data any
		err  error
		url  string
	)
	switch name {
	case "observe_logs":
		url = observe + "/api/v1/observe/logs?namespace=" + urlQuery(strArg(args, "namespace", "cloudops-dev")) + "&limit=20"
		data, err = httpGetJSON(url)
	case "observe_alerts":
		url = observe + "/api/v1/observe/alerts"
		data, err = httpGetJSON(url)
	case "observe_resources":
		url = observe + "/api/v1/observe/resources?namespace=" + urlQuery(strArg(args, "namespace", "cloudops-dev"))
		data, err = httpGetJSON(url)
	case "cicd_apps":
		url = cicd + "/api/v1/cicd/apps"
		data, err = httpGetJSON(url)
	case "cicd_builds":
		url = cicd + "/api/v1/cicd/builds?limit=10"
		data, err = httpGetJSON(url)
	case "ask_kb":
		q := strArg(args, "question", "CloudOps 如何回滚")
		hits := searchKB(q, 3)
		data = envelope{"answer": synthesizeAnswer(q, hits), "citations": hits}
	default:
		writeJSON(w, 404, envelope{"error": "unknown_tool", "name": name})
		return
	}
	if err != nil {
		writeJSON(w, 502, envelope{"error": "tool_failed", "tool": name, "message": err.Error(), "url": url})
		return
	}
	writeJSON(w, 200, envelope{"tool": name, "mutating": false, "result": data})
}

func remediationSuggestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Symptom   string `json:"symptom"`
		Namespace string `json:"namespace"`
		Workload  string `json:"workload"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	sym := strings.ToLower(firstNonEmpty(req.Symptom, "crashloop"))
	suggestions := []map[string]any{}
	switch {
	case strings.Contains(sym, "oom"):
		suggestions = append(suggestions,
			map[string]any{"action": "restart_pod", "risk": "medium", "executable": false, "reason": "临时恢复；需审批"},
			map[string]any{"action": "scale_replicas", "risk": "low", "executable": false, "reason": "缓解压力；建议人工确认"},
		)
	case strings.Contains(sym, "rollback") || strings.Contains(sym, "500"):
		suggestions = append(suggestions,
			map[string]any{"action": "rollback_image", "risk": "high", "executable": false, "reason": "高风险；必须人工确认"},
		)
	default:
		suggestions = append(suggestions,
			map[string]any{"action": "collect_diagnostics", "risk": "low", "executable": false, "reason": "只读采集"},
		)
	}
	writeJSON(w, 200, envelope{
		"service":     appName,
		"policy":      "suggest_only_never_execute",
		"namespace":   firstNonEmpty(req.Namespace, "cloudops-dev"),
		"workload":    req.Workload,
		"suggestions": suggestions,
		"note":        "Week21/22: Operator 执行面延期；见 deferred-week15-22-runtime-followups.md",
	})
}

func backupJobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	writeJSON(w, 200, envelope{
		"service": appName,
		"source":  "fixture+longhorn_plan",
		"items": []map[string]any{
			{"id": "longhorn-pvc-cloudops-cicd-pg", "type": "longhorn", "status": "Available", "scope": "PVC", "note": "inventory Longhorn backup"},
			{"id": "velero-cloudops-dev-plan", "type": "velero", "status": "planned", "scope": "Namespace/cloudops-dev", "note": "install deferred"},
			{"id": "etcd-snapshot-sop", "type": "etcd", "status": "sop_only", "scope": "control-plane", "note": "see day113-etcd-backup-sop.md"},
		},
	})
}

func retroDraftHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, envelope{"error": "method_not_allowed"})
		return
	}
	var req struct {
		Incident string `json:"incident"`
		Impact   string `json:"impact"`
		Timeline string `json:"timeline"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	draft := fmt.Sprintf(`# 故障复盘草稿（AI 生成）

## 概要
- 事件：%s
- 影响：%s

## 时间线
%s

## 根因候选
- 待人工确认（本服务仅生成草稿）

## 改进项
1. 补齐告警到 Runbook 链接
2. 低风险动作走审批审计
3. 高风险动作禁止自动执行

（模板来源：day90-release-risk-and-retro-templates.md）
`, firstNonEmpty(req.Incident, "未命名事件"), firstNonEmpty(req.Impact, "待填写"), firstNonEmpty(req.Timeline, "- T0 发现\n- T1 定位\n- T2 恢复"))
	writeJSON(w, 200, envelope{"service": appName, "draft_markdown": draft})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func strArg(args map[string]any, key, def string) string {
	if args == nil {
		return def
	}
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return def
}
func urlQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "%20"), `"`, "")
}
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func httpGetJSON(u string) (any, error) {
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
	}
	var out any
	if err := json.Unmarshal(body, &out); err != nil {
		return string(body), nil
	}
	return out, nil
}
func logJSON(level, msg string, fields map[string]any) {
	payload := map[string]any{"level": level, "msg": msg, "service": appName, "time": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range fields {
		payload[k] = v
	}
	b, _ := json.Marshal(payload)
	log.Println(string(b))
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
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		id := newID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
			return
		}
		logJSON("info", "http_request", map[string]any{
			"method": r.Method, "path": r.URL.Path, "status": rec.status,
			"latency_ms": time.Since(start).Milliseconds(), "request_id": id,
		})
	})
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- local RAG ---

type kbDoc struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Score float64 `json:"score,omitempty"`
}

func kbCorpus() []kbDoc {
	return []kbDoc{
		{ID: "day62", Title: "Incident SOP", Body: "告警后先看 Alertmanager 详情，再查 Prometheus 指标、VictoriaLogs 日志、K8s Event，对照 Runbook，禁止未审批变更。"},
		{ID: "day69", Title: "AI Operator 边界", Body: "AI 默认只读；低风险动作需审计；高风险重启扩容回滚必须人工审批；Operator 执行面受 CRD 策略约束。"},
		{ID: "day90", Title: "发布复盘模板", Body: "发布风险表包含影响面、回滚点、灰度指标；复盘包含时间线、根因、改进项。"},
		{ID: "day113", Title: "etcd 备份", Body: "etcd 定期 snapshot；恢复属于控制面高风险操作，延期到环境完成后再演练。"},
		{ID: "day54", Title: "网络故障实验", Body: "DNS 拒绝、Service Endpoint 破坏、NetPol deny 用于验证 Hubble 与告警联动，优先于 Chaos Mesh。"},
		{ID: "day61", Title: "故障演练", Body: "OOM、接口 500、依赖不可达演练脚本已存在，可作为诊断准确性评估夹具。"},
		{ID: "adr004", Title: "Istio 引入", Body: "先南北向 Gateway；东西向 sidecar 与 STRICT mTLS 延期。"},
		{ID: "adr006", Title: "Jenkins Harbor Argo", Body: "Jenkins 构建制品，Harbor 存镜像，Argo CD 声明式发布，职责分离。"},
		{ID: "finops", Title: "资源浪费", Body: "requests 远高于 usage 视为浪费；HPA 默认关闭；VPA/KEDA 选型见 ADR-008。"},
		{ID: "rag", Title: "RAG 选型", Body: "先用本地关键词/FTS 检索运维文档，再考虑 Chroma 与外部 LLM。"},
	}
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) > 1 {
			out = append(out, f)
		}
	}
	return out
}

func searchKB(q string, limit int) []kbDoc {
	tokens := tokenize(q)
	type scored struct {
		doc   kbDoc
		score float64
	}
	all := make([]scored, 0)
	for _, doc := range kbCorpus() {
		text := strings.ToLower(doc.Title + " " + doc.Body)
		score := 0.0
		for _, t := range tokens {
			if strings.Contains(text, t) {
				score += 1
			}
		}
		if score > 0 {
			d := doc
			d.Score = score
			all = append(all, scored{doc: d, score: score})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	if limit <= 0 {
		limit = 3
	}
	out := make([]kbDoc, 0, limit)
	for i := 0; i < len(all) && i < limit; i++ {
		out = append(out, all[i].doc)
	}
	if len(out) == 0 && len(kbCorpus()) > 0 {
		out = append(out, kbCorpus()[0])
	}
	return out
}

func synthesizeAnswer(q string, hits []kbDoc) string {
	if len(hits) == 0 {
		return "知识库暂无命中。请补充 SOP/ADR 语料后再问。"
	}
	parts := []string{fmt.Sprintf("针对「%s」的检索摘要：", q)}
	for i, h := range hits {
		parts = append(parts, fmt.Sprintf("%d. [%s] %s", i+1, h.Title, h.Body))
	}
	parts = append(parts, "说明：当前为本地 RAG（无外部模型调用）；变更类操作仍需人工审批。")
	return strings.Join(parts, "\n")
}
