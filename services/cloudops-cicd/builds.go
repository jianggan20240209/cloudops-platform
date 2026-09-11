package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type buildItemRequest struct {
	Service string `json:"service"`
	Branch  string `json:"branch"`
}

type buildsRequest struct {
	Items   []buildItemRequest `json:"items"`
	DryRun  bool               `json:"dry_run"`
	Notify  *bool              `json:"notify,omitempty"`
	Service string             `json:"service"`
	Branch  string             `json:"branch"`
}

type notifyRequest struct {
	Service    string `json:"service"`
	Branch     string `json:"branch"`
	JobName    string `json:"job_name"`
	BuildURL   string `json:"build_url"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	DryRun     bool   `json:"dry_run"`
	PreviewOnly bool  `json:"preview_only"`
}

var (
	buildSemOnce sync.Once
	buildSem     chan struct{}
)

func initBuildSemaphore() {
	buildSemOnce.Do(func() {
		n := envInt("JENKINS_MAX_CONCURRENT", 3)
		if n < 1 {
			n = 1
		}
		buildSem = make(chan struct{}, n)
	})
}

func buildsRootHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		listBuildsHandler(w, r)
	case http.MethodPost:
		triggerBuildsHandler(w, r)
	default:
		methodNotAllowed(w)
	}
}

func buildsSubHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/cicd/builds/")
	path = strings.Trim(path, "/")
	switch {
	case path == "match" && r.Method == http.MethodPost:
		matchBuildHandler(w, r)
	case path == "dry-run" && r.Method == http.MethodPost:
		dryRunBuildsHandler(w, r)
	case path == "notify" && r.Method == http.MethodPost:
		notifyBuildHandler(w, r)
	case path != "" && !strings.Contains(path, "/") && r.Method == http.MethodGet:
		getBuildHandler(w, r, path)
	default:
		notFoundHandler(w, r)
	}
}

func matchBuildHandler(w http.ResponseWriter, r *http.Request) {
	var req buildItemRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_json", "message": err.Error()})
		return
	}
	match, err := resolveBuildMatch(req.Service, req.Branch)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "match_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, envelope{
		"match":  match,
		"source": jenkinsSourceLabel(),
	})
}

func dryRunBuildsHandler(w http.ResponseWriter, r *http.Request) {
	req, err := parseBuildsRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_request", "message": err.Error()})
		return
	}
	matches, failures := matchAll(req.Items)
	writeJSON(w, http.StatusOK, envelope{
		"dry_run":  true,
		"matches":  matches,
		"failures": failures,
		"source":   jenkinsSourceLabel(),
	})
}

func triggerBuildsHandler(w http.ResponseWriter, r *http.Request) {
	req, err := parseBuildsRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_request", "message": err.Error()})
		return
	}
	if req.DryRun {
		matches, failures := matchAll(req.Items)
		writeJSON(w, http.StatusOK, envelope{
			"dry_run":  true,
			"matches":  matches,
			"failures": failures,
			"source":   jenkinsSourceLabel(),
		})
		return
	}

	client, ok := newJenkinsClientFromEnv()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, envelope{
			"error":   "jenkins_not_configured",
			"message": "set JENKINS_URL / JENKINS_USER / JENKINS_TOKEN",
		})
		return
	}

	notify := envBool("SEND_WEBHOOK", false)
	if req.Notify != nil {
		notify = *req.Notify
	}

	initBuildSemaphore()
	records := make([]BuildRecord, 0, len(req.Items))
	failures := make([]map[string]any, 0)

	for _, item := range req.Items {
		match, err := resolveBuildMatchWithClient(client, item.Service, item.Branch)
		if err != nil {
			failures = append(failures, map[string]any{"service": item.Service, "branch": item.Branch, "error": err.Error()})
			continue
		}
		rec := BuildRecord{
			Service:    match.Service,
			Branch:     match.Branch,
			JenkinsJob: match.Job.Name,
			Status:     "queued",
			Image:      match.Image,
			ImageTag:   match.ExpectedTag,
		}
		saved, err := buildStore.Save(r.Context(), rec)
		if err != nil {
			failures = append(failures, map[string]any{"service": item.Service, "error": err.Error()})
			continue
		}
		records = append(records, saved)
		go runBuildJob(client, saved.ID, match, notify)
	}

	writeJSON(w, http.StatusAccepted, envelope{
		"accepted": len(records),
		"builds":   records,
		"failures": failures,
		"source":   "jenkins",
	})
}

func listBuildsHandler(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := buildStore.List(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{"error": "list_failed", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, envelope{"items": items, "count": len(items)})
}

func getBuildHandler(w http.ResponseWriter, r *http.Request, id string) {
	rec, ok, err := buildStore.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, envelope{"error": "get_failed", "message": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, envelope{"error": "not_found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, envelope{"build": rec})
}

func notifyBuildHandler(w http.ResponseWriter, r *http.Request) {
	var req notifyRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_json", "message": err.Error()})
		return
	}
	msg := renderNotifyMessage(req)
	result := envelope{
		"message": msg,
		"rule":    "success hides build_url; failure includes build_url",
	}
	if req.PreviewOnly || req.DryRun {
		result["preview_only"] = true
		writeJSON(w, http.StatusOK, result)
		return
	}
	sent, err := sendWebhookMessage(msg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, envelope{"error": "webhook_failed", "message": err.Error(), "preview": msg})
		return
	}
	result["sent"] = sent
	writeJSON(w, http.StatusOK, result)
}

func parseBuildsRequest(r *http.Request) (buildsRequest, error) {
	var req buildsRequest
	if err := decodeJSONBody(r, &req); err != nil {
		return req, err
	}
	if len(req.Items) == 0 && strings.TrimSpace(req.Service) != "" {
		req.Items = []buildItemRequest{{Service: req.Service, Branch: req.Branch}}
	}
	if len(req.Items) == 0 {
		return req, fmt.Errorf("items is required")
	}
	for i := range req.Items {
		req.Items[i].Service = strings.TrimSpace(req.Items[i].Service)
		req.Items[i].Branch = strings.TrimSpace(req.Items[i].Branch)
		if req.Items[i].Service == "" {
			return req, fmt.Errorf("items[%d].service is required", i)
		}
		if req.Items[i].Branch == "" {
			req.Items[i].Branch = "main"
		}
	}
	return req, nil
}

func decodeJSONBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(dst)
}

func jenkinsSourceLabel() string {
	if _, ok := newJenkinsClientFromEnv(); ok {
		return "jenkins"
	}
	return "unconfigured"
}

func matchAll(items []buildItemRequest) ([]JenkinsMatch, []map[string]any) {
	matches := make([]JenkinsMatch, 0, len(items))
	failures := make([]map[string]any, 0)
	for _, item := range items {
		match, err := resolveBuildMatch(item.Service, item.Branch)
		if err != nil {
			failures = append(failures, map[string]any{
				"service": item.Service,
				"branch":  item.Branch,
				"error":   err.Error(),
			})
			continue
		}
		matches = append(matches, match)
	}
	return matches, failures
}

func resolveBuildMatch(service, branch string) (JenkinsMatch, error) {
	client, ok := newJenkinsClientFromEnv()
	if !ok {
		// Offline / dry local: synthesize expected lab job name.
		jobName := jenkinsJobName(service)
		image, tag := expectedImageForService(service, branch)
		return JenkinsMatch{
			Service:     service,
			Branch:      branch,
			Job:         JenkinsJob{Name: jobName, URL: strings.TrimRight(env("JENKINS_URL", "https://jenkins.jianggan.cn"), "/") + "/job/" + jobName + "/"},
			BranchParam: "",
			ExpectedTag: tag,
			Image:       image,
		}, nil
	}
	return resolveBuildMatchWithClient(client, service, branch)
}

func resolveBuildMatchWithClient(client *JenkinsClient, service, branch string) (JenkinsMatch, error) {
	jobs, err := client.ListJobs()
	if err != nil {
		return JenkinsMatch{}, err
	}
	job, err := matchJenkinsJob(service, jobs)
	if err != nil {
		return JenkinsMatch{}, err
	}
	params, err := client.ParameterNames(job.URL)
	if err != nil {
		return JenkinsMatch{}, err
	}
	branchParam := resolveBranchParam(params)
	image, tag := expectedImageForService(service, branch)
	return JenkinsMatch{
		Service:     service,
		Branch:      branch,
		Job:         job,
		BranchParam: branchParam,
		ExpectedTag: tag,
		Image:       image,
	}, nil
}

func runBuildJob(client *JenkinsClient, id string, match JenkinsMatch, notify bool) {
	ctx := context.Background()
	buildSem <- struct{}{}
	defer func() { <-buildSem }()

	rec, ok, err := buildStore.Get(ctx, id)
	if err != nil || !ok {
		return
	}
	rec.Status = "triggering"
	rec, _ = buildStore.Save(ctx, rec)

	queueURL, err := client.TriggerBuild(match.Job.URL, match.BranchParam, match.Branch)
	if err != nil {
		rec.Status = "FAILED"
		rec.FailureReason = err.Error()
		rec.LogSummary = summarizeFailure(err.Error())
		_, _ = buildStore.Save(ctx, rec)
		if notify {
			_ = maybeNotify(rec)
		}
		return
	}
	rec.QueueURL = queueURL
	rec.Status = "queued"
	rec, _ = buildStore.Save(ctx, rec)

	queueTimeout := envInt("JENKINS_QUEUE_TIMEOUT", 300)
	buildTimeout := envInt("JENKINS_BUILD_TIMEOUT", 7200)
	poll := envInt("JENKINS_POLL_INTERVAL", 10)

	buildURL, err := client.WaitQueue(queueURL, queueTimeout, poll)
	if err != nil {
		rec.Status = "FAILED"
		rec.FailureReason = err.Error()
		rec.LogSummary = summarizeFailure(err.Error())
		_, _ = buildStore.Save(ctx, rec)
		if notify {
			_ = maybeNotify(rec)
		}
		return
	}
	rec.BuildURL = buildURL
	rec.Status = "RUNNING"
	_, _, _, number, err := client.GetBuild(buildURL)
	if err == nil && number > 0 {
		rec.JenkinsBuild = strconv.Itoa(number)
		if strings.HasSuffix(rec.ImageTag, "-pending") {
			prefix := strings.TrimSuffix(rec.ImageTag, "-pending")
			if prefix == "" {
				prefix = firstNonEmpty(match.Branch, "main")
			}
			rec.ImageTag = fmt.Sprintf("%s-%d", prefix, number)
			if idx := strings.LastIndex(rec.Image, ":"); idx >= 0 {
				rec.Image = rec.Image[:idx+1] + rec.ImageTag
			}
		}
	}
	rec, _ = buildStore.Save(ctx, rec)

	finalResult, finalDesc, finalNumber, err := client.WaitBuild(buildURL, buildTimeout, poll)
	if err != nil {
		rec.Status = "FAILED"
		rec.FailureReason = err.Error()
		rec.LogSummary = summarizeFailure(err.Error())
		_, _ = buildStore.Save(ctx, rec)
		if notify {
			_ = maybeNotify(rec)
		}
		return
	}
	if finalNumber > 0 {
		rec.JenkinsBuild = strconv.Itoa(finalNumber)
	}
	rec.Status = strings.ToUpper(finalResult)
	if rec.Status != "SUCCESS" {
		rec.FailureReason = firstNonEmpty(finalDesc, finalResult)
		rec.LogSummary = summarizeFailure(rec.FailureReason)
	} else {
		rec.LogSummary = "build succeeded"
		rec.FailureReason = ""
	}
	rec, _ = buildStore.Save(ctx, rec)
	if notify {
		_ = maybeNotify(rec)
	}
}

func summarizeFailure(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 240 {
		return msg[:240] + "…"
	}
	return msg
}

func renderNotifyMessage(req notifyRequest) string {
	status := strings.ToUpper(strings.TrimSpace(req.Status))
	service := firstNonEmpty(req.Service, "unknown")
	job := firstNonEmpty(req.JobName, "unknown-job")
	branch := firstNonEmpty(req.Branch, "main")
	base := fmt.Sprintf("[CloudOps] %s %s branch=%s job=%s", status, service, branch, job)
	if status == "SUCCESS" {
		// Day 75: success does not include build URL.
		if strings.TrimSpace(req.Message) != "" {
			return base + " " + strings.TrimSpace(req.Message)
		}
		return base + " ok"
	}
	msg := base
	if strings.TrimSpace(req.BuildURL) != "" {
		msg += " build=" + strings.TrimSpace(req.BuildURL)
	}
	if strings.TrimSpace(req.Message) != "" {
		msg += " reason=" + strings.TrimSpace(req.Message)
	}
	return msg
}

func maybeNotify(rec BuildRecord) error {
	msg := renderNotifyMessage(notifyRequest{
		Service:  rec.Service,
		Branch:   rec.Branch,
		JobName:  rec.JenkinsJob,
		BuildURL: rec.BuildURL,
		Status:   rec.Status,
		Message:  rec.FailureReason,
	})
	sent, err := sendWebhookMessage(msg)
	if err != nil {
		return err
	}
	if sent {
		rec.NotifySent = true
		_, _ = buildStore.Save(context.Background(), rec)
	}
	return nil
}

func sendWebhookMessage(message string) (bool, error) {
	infoURL := strings.TrimSpace(os.Getenv("WEBHOOK_INFO_URL"))
	if infoURL == "" {
		logJSON("info", "webhook_skipped", map[string]any{"reason": "WEBHOOK_INFO_URL empty", "preview": message})
		return false, nil
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(infoURL)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("webhook info status=%d body=%s", resp.StatusCode, string(body))
	}
	var info struct {
		Webhook string `json:"webhook"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return false, err
	}
	if info.Webhook == "" || info.Token == "" {
		return false, fmt.Errorf("webhook info missing webhook/token")
	}
	req, err := http.NewRequest(http.MethodPost, info.Webhook, strings.NewReader(message))
	if err != nil {
		return false, err
	}
	req.Header.Set("vv-token", info.Token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "cloudops-cicd/webhook")
	out, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer out.Body.Close()
	if out.StatusCode < 200 || out.StatusCode >= 300 {
		b, _ := io.ReadAll(out.Body)
		return false, fmt.Errorf("webhook post status=%d body=%s", out.StatusCode, string(b))
	}
	return true, nil
}
