package main

import (
	"crypto/tls"
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
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
	startedAt = time.Now()
)

type envelope map[string]any

type AppSummary struct {
	Name             string `json:"name"`
	Env              string `json:"env"`
	Namespace        string `json:"namespace"`
	ArgoCDApp        string `json:"argocd_app"`
	HarborProject    string `json:"harbor_project"`
	HarborRepository string `json:"harbor_repository"`
	Image            string `json:"image"`
	CurrentTag       string `json:"current_tag"`
	Sync             string `json:"sync"`
	Health           string `json:"health"`
	LastRelease      string `json:"last_release"`
	Revision         string `json:"revision,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	Source           string `json:"source"`
}

type ReleaseSummary struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	Status    string `json:"status"`
}

var apps = []AppSummary{
	{
		Name:             "cloudops-gateway",
		Env:              "dev",
		Namespace:        "cloudops-dev",
		ArgoCDApp:        "cloudops-gateway-dev",
		HarborProject:    "cloudops",
		HarborRepository: "cloudops-gateway",
		Image:            "harbor-server.jianggan.cn/cloudops/cloudops-gateway:main-14",
		CurrentTag:       "main-14",
		Sync:             "Synced",
		Health:           "Healthy",
		LastRelease:      "main-14",
		Source:           "static",
	},
	{
		Name:             "cloudops-web",
		Env:              "dev",
		Namespace:        "cloudops-dev",
		ArgoCDApp:        "cloudops-web-dev",
		HarborProject:    "cloudops",
		HarborRepository: "cloudops-web",
		Image:            "harbor-server.jianggan.cn/cloudops/cloudops-web:main-8",
		CurrentTag:       "main-8",
		Sync:             "Synced",
		Health:           "Healthy",
		LastRelease:      "main-8",
		Source:           "static",
	},
	{
		Name:             "cloudops-cicd",
		Env:              "dev",
		Namespace:        "cloudops-dev",
		ArgoCDApp:        "cloudops-cicd-dev",
		HarborProject:    "cloudops",
		HarborRepository: "cloudops-cicd",
		Image:            "harbor-server.jianggan.cn/cloudops/cloudops-cicd:main-2",
		CurrentTag:       "main-2",
		Sync:             "Synced",
		Health:           "Healthy",
		LastRelease:      "main-2",
		Source:           "static",
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

type HarborClient struct {
	server   string
	username string
	password string
	client   *http.Client
}

type PrometheusClient struct {
	server string
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

type ImageTagSummary struct {
	Tag      string `json:"tag"`
	Digest   string `json:"digest"`
	PushedAt string `json:"pushed_at"`
	Source   string `json:"source"`
}

type harborArtifact struct {
	Digest   string `json:"digest"`
	PushTime string `json:"push_time"`
	Tags     []struct {
		Name     string `json:"name"`
		PushTime string `json:"push_time"`
	} `json:"tags"`
}

type MetricsSummary struct {
	Name    string  `json:"name"`
	Source  string  `json:"source"`
	Up      float64 `json:"up"`
	Targets int     `json:"targets"`
	Healthy bool    `json:"healthy"`
	Message string  `json:"message,omitempty"`
}

type CheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ReleaseDetail struct {
	Name        string            `json:"name"`
	Env         string            `json:"env"`
	Namespace   string            `json:"namespace"`
	ArgoCDApp   string            `json:"argocd_app"`
	Image       string            `json:"image"`
	CurrentTag  string            `json:"current_tag"`
	Sync        string            `json:"sync"`
	Health      string            `json:"health"`
	Revision    string            `json:"revision,omitempty"`
	UpdatedAt   string            `json:"updated_at,omitempty"`
	Images      []ImageTagSummary `json:"images"`
	Metrics     MetricsSummary    `json:"metrics"`
	Checks      []CheckResult     `json:"checks"`
	Ready       bool              `json:"ready"`
	Source      string            `json:"source"`
	Warnings    []string          `json:"warnings,omitempty"`
	GeneratedAt string            `json:"generated_at"`
}

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
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
	case "images":
		items, source, err := loadImages(app)
		payload := envelope{
			"name":       app.Name,
			"repository": app.HarborProject + "/" + app.HarborRepository,
			"items":      items,
			"total":      len(items),
			"source":     source,
		}
		if err != nil {
			payload["warning"] = err.Error()
		}
		writeJSON(w, http.StatusOK, payload)
	case "metrics":
		summary, err := loadMetrics(app)
		if err != nil {
			writeJSON(w, http.StatusOK, envelope{
				"name":    app.Name,
				"source":  "static",
				"healthy": app.Health == "Healthy",
				"message": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, summary)
	case "release":
		detail := buildReleaseDetail(app)
		writeJSON(w, http.StatusOK, detail)
	case "health":
		detail := buildReleaseDetail(app)
		writeJSON(w, http.StatusOK, envelope{
			"name":         detail.Name,
			"ready":        detail.Ready,
			"sync":         detail.Sync,
			"health":       detail.Health,
			"metrics":      detail.Metrics,
			"checks":       detail.Checks,
			"warnings":     detail.Warnings,
			"generated_at": detail.GeneratedAt,
		})
	case "verify":
		detail := buildReleaseDetail(app)
		writeJSON(w, http.StatusOK, envelope{
			"name":         detail.Name,
			"image":        detail.Image,
			"tag":          detail.CurrentTag,
			"ready":        detail.Ready,
			"checks":       detail.Checks,
			"warnings":     detail.Warnings,
			"generated_at": detail.GeneratedAt,
		})
	default:
		notFoundHandler(w, r)
	}
}

func buildReleaseDetail(app AppSummary) ReleaseDetail {
	warnings := make([]string, 0)

	images, imageSource, err := loadImages(app)
	if err != nil {
		warnings = append(warnings, err.Error())
	}

	metrics, err := loadMetrics(app)
	if err != nil {
		metrics = MetricsSummary{
			Name:    app.Name,
			Source:  "static",
			Healthy: app.Health == "Healthy",
			Message: err.Error(),
		}
		warnings = append(warnings, err.Error())
	}

	checks := buildReleaseChecks(app, images, metrics)
	ready := true
	for _, check := range checks {
		if check.Status == "fail" {
			ready = false
			break
		}
	}

	return ReleaseDetail{
		Name:        app.Name,
		Env:         app.Env,
		Namespace:   app.Namespace,
		ArgoCDApp:   app.ArgoCDApp,
		Image:       app.Image,
		CurrentTag:  app.CurrentTag,
		Sync:        app.Sync,
		Health:      app.Health,
		Revision:    app.Revision,
		UpdatedAt:   app.UpdatedAt,
		Images:      images,
		Metrics:     metrics,
		Checks:      checks,
		Ready:       ready,
		Source:      fmt.Sprintf("app:%s,images:%s,metrics:%s", app.Source, imageSource, metrics.Source),
		Warnings:    warnings,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func buildReleaseChecks(app AppSummary, images []ImageTagSummary, metrics MetricsSummary) []CheckResult {
	checks := []CheckResult{
		checkEqual("argocd_sync", app.Sync, "Synced", "Argo CD Application is synced"),
		checkEqual("argocd_health", app.Health, "Healthy", "Argo CD Application is healthy"),
		checkNotEmpty("image_tag", app.CurrentTag, "Current image tag is available"),
	}

	if len(images) > 0 && app.CurrentTag != "" {
		if hasImageTag(images, app.CurrentTag) {
			checks = append(checks, CheckResult{
				Name:    "harbor_image",
				Status:  "pass",
				Message: "Current image tag exists in Harbor image list",
			})
		} else {
			checks = append(checks, CheckResult{
				Name:    "harbor_image",
				Status:  "fail",
				Message: "Current image tag was not found in Harbor image list",
			})
		}
	}

	switch {
	case metrics.Source == "prometheus" && metrics.Healthy:
		checks = append(checks, CheckResult{
			Name:    "prometheus_up",
			Status:  "pass",
			Message: "All matched Prometheus up targets are healthy",
		})
	case metrics.Source == "prometheus":
		message := metrics.Message
		if message == "" {
			message = "Prometheus up targets are not fully healthy"
		}
		checks = append(checks, CheckResult{
			Name:    "prometheus_up",
			Status:  "fail",
			Message: message,
		})
	default:
		checks = append(checks, CheckResult{
			Name:    "prometheus_up",
			Status:  "warn",
			Message: firstNonEmpty(metrics.Message, "Prometheus metrics are unavailable, using application health only"),
		})
	}

	return checks
}

func checkEqual(name string, got string, want string, passMessage string) CheckResult {
	if got == want {
		return CheckResult{Name: name, Status: "pass", Message: passMessage}
	}
	return CheckResult{
		Name:    name,
		Status:  "fail",
		Message: fmt.Sprintf("expected %s, got %s", want, firstNonEmpty(got, "unknown")),
	}
}

func checkNotEmpty(name string, value string, passMessage string) CheckResult {
	if strings.TrimSpace(value) != "" {
		return CheckResult{Name: name, Status: "pass", Message: passMessage}
	}
	return CheckResult{Name: name, Status: "fail", Message: "value is empty"}
}

func hasImageTag(images []ImageTagSummary, tag string) bool {
	for _, image := range images {
		if image.Tag == tag {
			return true
		}
	}
	return false
}

func loadImages(app AppSummary) ([]ImageTagSummary, string, error) {
	client, ok := newHarborClientFromEnv()
	if !ok {
		return staticImages(app), "static", nil
	}

	items, err := client.ListImageTags(app.HarborProject, app.HarborRepository)
	if err != nil {
		return staticImages(app), "static", fmt.Errorf("harbor query failed, fallback to static data: %w", err)
	}
	return items, "harbor", nil
}

func staticImages(app AppSummary) []ImageTagSummary {
	items := make([]ImageTagSummary, 0, len(releases[app.Name]))
	for _, release := range releases[app.Name] {
		items = append(items, ImageTagSummary{
			Tag:      release.Version,
			Digest:   "",
			PushedAt: release.BuildTime,
			Source:   "static",
		})
	}
	if len(items) == 0 && app.CurrentTag != "" {
		items = append(items, ImageTagSummary{
			Tag:    app.CurrentTag,
			Source: "static",
		})
	}
	return items
}

func newHarborClientFromEnv() (*HarborClient, bool) {
	server := strings.TrimRight(env("HARBOR_SERVER", ""), "/")
	username := strings.TrimSpace(os.Getenv("HARBOR_USERNAME"))
	password := strings.TrimSpace(os.Getenv("HARBOR_PASSWORD"))
	if server == "" || username == "" || password == "" {
		return nil, false
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: envBool("HARBOR_INSECURE", true)} //nolint:gosec

	return &HarborClient{
		server:   server,
		username: username,
		password: password,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}, true
}

func (c *HarborClient) ListImageTags(project string, repository string) ([]ImageTagSummary, error) {
	project = url.PathEscape(project)
	repository = url.PathEscape(repository)
	apiURL := fmt.Sprintf("%s/api/v2.0/projects/%s/repositories/%s/artifacts?with_tag=true&page_size=20&sort=-push_time", c.server, project, repository)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("harbor api status=%d body=%s", resp.StatusCode, string(body))
	}

	var artifacts []harborArtifact
	if err := json.Unmarshal(body, &artifacts); err != nil {
		return nil, err
	}

	items := make([]ImageTagSummary, 0)
	for _, artifact := range artifacts {
		for _, tag := range artifact.Tags {
			pushedAt := firstNonEmpty(tag.PushTime, artifact.PushTime)
			items = append(items, ImageTagSummary{
				Tag:      tag.Name,
				Digest:   artifact.Digest,
				PushedAt: pushedAt,
				Source:   "harbor",
			})
		}
	}
	return items, nil
}

func loadMetrics(app AppSummary) (MetricsSummary, error) {
	client, ok := newPrometheusClientFromEnv()
	if !ok {
		return MetricsSummary{}, fmt.Errorf("prometheus server is not configured")
	}

	return client.QueryUp(app.Name)
}

func newPrometheusClientFromEnv() (*PrometheusClient, bool) {
	server := strings.TrimRight(env("PROMETHEUS_SERVER", ""), "/")
	if server == "" {
		return nil, false
	}

	return &PrometheusClient{
		server: server,
		client: &http.Client{Timeout: 10 * time.Second},
	}, true
}

func (c *PrometheusClient) QueryUp(job string) (MetricsSummary, error) {
	query := fmt.Sprintf(`up{job=%q}`, job)
	apiURL := c.server + "/api/v1/query?query=" + url.QueryEscape(query)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return MetricsSummary{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return MetricsSummary{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return MetricsSummary{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MetricsSummary{}, fmt.Errorf("prometheus api status=%d body=%s", resp.StatusCode, string(body))
	}

	var result prometheusQueryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return MetricsSummary{}, err
	}
	if result.Status != "success" {
		return MetricsSummary{}, fmt.Errorf("prometheus query failed: %s", result.Error)
	}

	var up float64
	for _, item := range result.Data.Result {
		if len(item.Value) < 2 {
			continue
		}
		valueText, ok := item.Value[1].(string)
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(valueText, 64)
		if err != nil {
			continue
		}
		up += value
	}

	targets := len(result.Data.Result)
	summary := MetricsSummary{
		Name:    job,
		Source:  "prometheus",
		Up:      up,
		Targets: targets,
		Healthy: targets > 0 && int(up) == targets,
	}
	if targets == 0 {
		summary.Message = "no prometheus targets matched up{job=\"" + job + "\"}"
	}
	return summary, nil
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
