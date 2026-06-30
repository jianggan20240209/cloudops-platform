package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var (
	version     = "dev"
	commit      = "unknown"
	buildTime   = "unknown"
	startedAt   = time.Now()
	recordStore ReleaseRecordStore
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
		Name:             "cloudops-gateway-rollout",
		Env:              "dev",
		Namespace:        "cloudops-dev",
		ArgoCDApp:        "cloudops-gateway-rollout-dev",
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
	{
		Name:             "rollouts-demo",
		Env:              "dev",
		Namespace:        "cloudops-dev",
		ArgoCDApp:        "rollouts-demo-dev",
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
		Name:             "rollouts-demo-istio",
		Env:              "dev",
		Namespace:        "cloudops-dev",
		ArgoCDApp:        "rollouts-demo-istio-dev",
		HarborProject:    "cloudops",
		HarborRepository: "cloudops-gateway",
		Image:            "harbor-server.jianggan.cn/cloudops/cloudops-gateway:main-14",
		CurrentTag:       "main-14",
		Sync:             "Synced",
		Health:           "Healthy",
		LastRelease:      "main-14",
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

type KubernetesClient struct {
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
	Name          string                `json:"name"`
	Env           string                `json:"env"`
	Namespace     string                `json:"namespace"`
	ArgoCDApp     string                `json:"argocd_app"`
	Image         string                `json:"image"`
	CurrentTag    string                `json:"current_tag"`
	Sync          string                `json:"sync"`
	Health        string                `json:"health"`
	Revision      string                `json:"revision,omitempty"`
	UpdatedAt     string                `json:"updated_at,omitempty"`
	Images        []ImageTagSummary     `json:"images"`
	Metrics       MetricsSummary        `json:"metrics"`
	Rollout       *RolloutSummary       `json:"rollout,omitempty"`
	Traffic       *TrafficSummary       `json:"traffic,omitempty"`
	Observability *ObservabilitySummary `json:"observability,omitempty"`
	Checks        []CheckResult         `json:"checks"`
	Ready         bool                  `json:"ready"`
	Source        string                `json:"source"`
	Warnings      []string              `json:"warnings,omitempty"`
	GeneratedAt   string                `json:"generated_at"`
}

type ReleaseVerification struct {
	Ready         bool                  `json:"ready"`
	Checks        []CheckResult         `json:"checks"`
	Metrics       MetricsSummary        `json:"metrics"`
	Rollout       *RolloutSummary       `json:"rollout,omitempty"`
	Traffic       *TrafficSummary       `json:"traffic,omitempty"`
	Observability *ObservabilitySummary `json:"observability,omitempty"`
	Warnings      []string              `json:"warnings,omitempty"`
	VerifiedAt    string                `json:"verified_at"`
}

type ReleaseRecord struct {
	ID             string              `json:"id"`
	AppName        string              `json:"app_name"`
	Env            string              `json:"env"`
	Namespace      string              `json:"namespace"`
	JenkinsJob     string              `json:"jenkins_job"`
	JenkinsBuild   string              `json:"jenkins_build,omitempty"`
	Image          string              `json:"image"`
	ImageTag       string              `json:"image_tag"`
	ImageDigest    string              `json:"image_digest,omitempty"`
	ArgoCDApp      string              `json:"argocd_app"`
	ArgoCDRevision string              `json:"argocd_revision,omitempty"`
	ArgoCDSync     string              `json:"argocd_sync"`
	ArgoCDHealth   string              `json:"argocd_health"`
	Status         string              `json:"status"`
	Verification   ReleaseVerification `json:"verification"`
	Source         string              `json:"source"`
	CreatedAt      string              `json:"created_at"`
}

type ReleaseRecordStore interface {
	Save(ctx context.Context, record ReleaseRecord) (ReleaseRecord, error)
	ListByApp(ctx context.Context, appName string) ([]ReleaseRecord, error)
	Get(ctx context.Context, id string) (ReleaseRecord, bool, error)
	Source() string
}

type MemoryReleaseRecordStore struct {
	mu      sync.RWMutex
	records map[string]ReleaseRecord
}

type PostgresReleaseRecordStore struct {
	db *sql.DB
}

type RolloutSummary struct {
	Name              string               `json:"name"`
	Namespace         string               `json:"namespace"`
	Phase             string               `json:"phase,omitempty"`
	CurrentStepIndex  int                  `json:"current_step_index,omitempty"`
	CurrentPodHash    string               `json:"current_pod_hash,omitempty"`
	StableRS          string               `json:"stable_rs,omitempty"`
	Replicas          int                  `json:"replicas"`
	UpdatedReplicas   int                  `json:"updated_replicas"`
	AvailableReplicas int                  `json:"available_replicas"`
	Conditions        []RolloutCondition   `json:"conditions,omitempty"`
	AnalysisRuns      []AnalysisRunSummary `json:"analysis_runs,omitempty"`
	Healthy           bool                 `json:"healthy"`
	Source            string               `json:"source"`
}

type RolloutCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type AnalysisRunSummary struct {
	Name      string                  `json:"name"`
	Namespace string                  `json:"namespace"`
	Phase     string                  `json:"phase,omitempty"`
	Message   string                  `json:"message,omitempty"`
	StartedAt string                  `json:"started_at,omitempty"`
	Metric    []AnalysisMetricSummary `json:"metrics,omitempty"`
}

type AnalysisMetricSummary struct {
	Name    string `json:"name"`
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
}

type TrafficSummary struct {
	Name             string                   `json:"name"`
	Namespace        string                   `json:"namespace"`
	VirtualService   *VirtualServiceSummary   `json:"virtual_service,omitempty"`
	DestinationRules []DestinationRuleSummary `json:"destination_rules,omitempty"`
	Source           string                   `json:"source"`
}

type VirtualServiceSummary struct {
	Name      string         `json:"name"`
	Namespace string         `json:"namespace"`
	Hosts     []string       `json:"hosts,omitempty"`
	Gateways  []string       `json:"gateways,omitempty"`
	HTTP      []HTTPRouteSum `json:"http,omitempty"`
}

type HTTPRouteSum struct {
	Name    string       `json:"name,omitempty"`
	Timeout string       `json:"timeout,omitempty"`
	Retries *RetryPolicy `json:"retries,omitempty"`
	Routes  []RouteDest  `json:"routes,omitempty"`
}

type RetryPolicy struct {
	Attempts      int    `json:"attempts,omitempty"`
	PerTryTimeout string `json:"per_try_timeout,omitempty"`
	RetryOn       string `json:"retry_on,omitempty"`
}

type RouteDest struct {
	Host   string `json:"host"`
	Port   int    `json:"port,omitempty"`
	Weight int    `json:"weight"`
}

type DestinationRuleSummary struct {
	Name             string                 `json:"name"`
	Namespace        string                 `json:"namespace"`
	Host             string                 `json:"host"`
	ConnectionPool   *ConnectionPoolSummary `json:"connection_pool,omitempty"`
	OutlierDetection *OutlierDetectionSum   `json:"outlier_detection,omitempty"`
}

type ConnectionPoolSummary struct {
	TCP  *TCPPoolSummary  `json:"tcp,omitempty"`
	HTTP *HTTPPoolSummary `json:"http,omitempty"`
}

type TCPPoolSummary struct {
	MaxConnections int `json:"max_connections,omitempty"`
}

type HTTPPoolSummary struct {
	HTTP1MaxPendingRequests  int `json:"http1_max_pending_requests,omitempty"`
	MaxRequestsPerConnection int `json:"max_requests_per_connection,omitempty"`
}

type OutlierDetectionSum struct {
	Consecutive5xxErrors int    `json:"consecutive_5xx_errors,omitempty"`
	Interval             string `json:"interval,omitempty"`
	BaseEjectionTime     string `json:"base_ejection_time,omitempty"`
	MaxEjectionPercent   int    `json:"max_ejection_percent,omitempty"`
}

type CanaryStageSummary struct {
	Phase            string `json:"phase,omitempty"`
	CurrentStepIndex int    `json:"current_step_index,omitempty"`
	StableWeight     int    `json:"stable_weight,omitempty"`
	CanaryWeight     int    `json:"canary_weight,omitempty"`
	Stage            string `json:"stage"`
}

type IstioDestinationMetric struct {
	Destination      string  `json:"destination"`
	RequestRateRPS   float64 `json:"request_rate_rps,omitempty"`
	ErrorRateRPS     float64 `json:"error_rate_rps,omitempty"`
	ErrorRatePercent float64 `json:"error_rate_percent,omitempty"`
}

type IstioMetricsSummary struct {
	RequestRateRPS   float64                  `json:"request_rate_rps,omitempty"`
	ErrorRateRPS     float64                  `json:"error_rate_rps,omitempty"`
	ErrorRatePercent float64                  `json:"error_rate_percent,omitempty"`
	P95LatencyMS     float64                  `json:"p95_latency_ms,omitempty"`
	ByDestination    []IstioDestinationMetric `json:"by_destination,omitempty"`
	MatchedSelector  string                   `json:"matched_selector,omitempty"`
	Source           string                   `json:"source"`
	Message          string                   `json:"message,omitempty"`
}

type istioMetricSelector struct {
	Name       string
	Selector   string
	GroupLabel string
}

type ObservabilitySummary struct {
	CanaryStage  *CanaryStageSummary  `json:"canary_stage,omitempty"`
	IstioMetrics *IstioMetricsSummary `json:"istio_metrics,omitempty"`
	Source       string               `json:"source"`
}

type k8sRollout struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Status struct {
		Phase             string `json:"phase"`
		CurrentStepIndex  int    `json:"currentStepIndex"`
		CurrentPodHash    string `json:"currentPodHash"`
		StableRS          string `json:"stableRS"`
		Replicas          int    `json:"replicas"`
		UpdatedReplicas   int    `json:"updatedReplicas"`
		AvailableReplicas int    `json:"availableReplicas"`
		Conditions        []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}

type k8sAnalysisRunList struct {
	Items []k8sAnalysisRun `json:"items"`
}

type k8sAnalysisRun struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		CreationTimestamp string            `json:"creationTimestamp"`
		Labels            map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Phase         string `json:"phase"`
		Message       string `json:"message"`
		StartedAt     string `json:"startedAt"`
		MetricResults []struct {
			Name    string `json:"name"`
			Phase   string `json:"phase"`
			Message string `json:"message"`
		} `json:"metricResults"`
	} `json:"status"`
}

type k8sVirtualService struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Hosts    []string `json:"hosts"`
		Gateways []string `json:"gateways"`
		HTTP     []struct {
			Name    string `json:"name"`
			Timeout string `json:"timeout"`
			Retries *struct {
				Attempts      int    `json:"attempts"`
				PerTryTimeout string `json:"perTryTimeout"`
				RetryOn       string `json:"retryOn"`
			} `json:"retries"`
			Route []struct {
				Destination struct {
					Host string `json:"host"`
					Port struct {
						Number int `json:"number"`
					} `json:"port"`
				} `json:"destination"`
				Weight int `json:"weight"`
			} `json:"route"`
		} `json:"http"`
	} `json:"spec"`
}

type k8sDestinationRuleList struct {
	Items []k8sDestinationRule `json:"items"`
}

type k8sDestinationRule struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Host          string `json:"host"`
		TrafficPolicy *struct {
			ConnectionPool *struct {
				TCP *struct {
					MaxConnections int `json:"maxConnections"`
				} `json:"tcp"`
				HTTP *struct {
					HTTP1MaxPendingRequests  int `json:"http1MaxPendingRequests"`
					MaxRequestsPerConnection int `json:"maxRequestsPerConnection"`
				} `json:"http"`
			} `json:"connectionPool"`
			OutlierDetection *struct {
				Consecutive5xxErrors int    `json:"consecutive5xxErrors"`
				Interval             string `json:"interval"`
				BaseEjectionTime     string `json:"baseEjectionTime"`
				MaxEjectionPercent   int    `json:"maxEjectionPercent"`
			} `json:"outlierDetection"`
		} `json:"trafficPolicy"`
	} `json:"spec"`
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
	recordStore = newReleaseRecordStore()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	mux.HandleFunc("/api/healthz", healthzHandler)
	mux.HandleFunc("/api/readyz", readyzHandler)
	mux.HandleFunc("/api/v1/version", versionHandler)
	mux.HandleFunc("/api/v1/cicd/apps", appsHandler)
	mux.HandleFunc("/api/v1/cicd/apps/", appDetailHandler)
	mux.HandleFunc("/api/v1/cicd/releases/records", releaseRecordsHandler)
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
	warnings := make([]string, 0)
	for _, fallback := range apps {
		app, err := client.GetApplication(fallback.ArgoCDApp)
		if err != nil {
			staticApp := fallback
			staticApp.Source = "static"
			items = append(items, staticApp)
			warnings = append(warnings, fmt.Sprintf("%s: %v", fallback.ArgoCDApp, err))
			continue
		}
		items = append(items, appFromArgo(fallback, app))
	}
	if len(warnings) > 0 {
		return items, "argocd-partial", fmt.Errorf("argocd query partially failed: %s", strings.Join(warnings, "; "))
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
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
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
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, http.StatusOK, app)
		return
	}

	if r.Method == http.MethodPost {
		if len(parts) == 3 && parts[1] == "records" && parts[2] == "snapshot" {
			saveReleaseSnapshotHandler(w, r, app)
			return
		}
		methodNotAllowed(w)
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
	case "records":
		records := loadReleaseRecords(r.Context(), app)
		if len(parts) == 2 {
			writeJSON(w, http.StatusOK, envelope{
				"name":   app.Name,
				"items":  records,
				"total":  len(records),
				"source": recordStore.Source(),
			})
			return
		}
		if len(parts) > 3 {
			notFoundHandler(w, r)
			return
		}
		if parts[2] == "latest" {
			if len(records) == 0 {
				writeJSON(w, http.StatusNotFound, envelope{
					"error":   "release_record_not_found",
					"message": "release record not found",
					"name":    app.Name,
				})
				return
			}
			writeJSON(w, http.StatusOK, records[0])
			return
		}
		record, ok := findReleaseRecord(records, parts[2])
		if !ok {
			writeJSON(w, http.StatusNotFound, envelope{
				"error":     "release_record_not_found",
				"message":   "release record not found",
				"name":      app.Name,
				"record_id": parts[2],
			})
			return
		}
		writeJSON(w, http.StatusOK, record)
	case "rollback-candidates":
		if len(parts) != 2 {
			notFoundHandler(w, r)
			return
		}
		records := loadReleaseRecords(r.Context(), app)
		candidates := rollbackCandidates(app, records)
		writeJSON(w, http.StatusOK, envelope{
			"name":        app.Name,
			"current_tag": app.CurrentTag,
			"items":       candidates,
			"total":       len(candidates),
			"source":      recordStore.Source(),
		})
	case "rollout":
		if len(parts) != 2 {
			notFoundHandler(w, r)
			return
		}
		rollout, ok, err := loadRolloutSummary(r.Context(), app)
		if err != nil {
			writeJSON(w, http.StatusOK, envelope{
				"name":    app.Name,
				"source":  "kubernetes",
				"warning": err.Error(),
			})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, envelope{
				"error":   "rollout_not_found",
				"message": "rollout resource not found for app",
				"name":    app.Name,
			})
			return
		}
		writeJSON(w, http.StatusOK, rollout)
	case "analysisruns":
		if len(parts) != 2 {
			notFoundHandler(w, r)
			return
		}
		items, err := loadAnalysisRuns(r.Context(), app)
		if err != nil {
			writeJSON(w, http.StatusOK, envelope{
				"name":    app.Name,
				"source":  "kubernetes",
				"warning": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, envelope{
			"name":   app.Name,
			"items":  items,
			"total":  len(items),
			"source": "kubernetes",
		})
	case "traffic":
		if len(parts) != 2 {
			notFoundHandler(w, r)
			return
		}
		summary, err := loadTrafficSummary(r.Context(), app)
		if err != nil {
			writeJSON(w, http.StatusOK, envelope{
				"name":    app.Name,
				"source":  "kubernetes",
				"warning": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, summary)
	case "observability":
		if len(parts) != 2 {
			notFoundHandler(w, r)
			return
		}
		summary, err := loadObservabilitySummary(r.Context(), app)
		if err != nil {
			writeJSON(w, http.StatusOK, envelope{
				"name":    app.Name,
				"source":  "prometheus",
				"warning": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, summary)
	default:
		notFoundHandler(w, r)
	}
}

func releaseRecordsHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/cicd/releases/records" {
		notFoundHandler(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !authorizedReleaseRecordWrite(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{
			"error":   "unauthorized",
			"message": "release record write token is invalid",
		})
		return
	}

	defer r.Body.Close()
	var record ReleaseRecord
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&record); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{
			"error":   "invalid_json",
			"message": err.Error(),
		})
		return
	}

	record, err := recordStore.Save(r.Context(), record)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{
			"error":   "release_record_save_failed",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusCreated, record)
}

func saveReleaseSnapshotHandler(w http.ResponseWriter, r *http.Request, app AppSummary) {
	if !authorizedReleaseRecordWrite(r) {
		writeJSON(w, http.StatusUnauthorized, envelope{
			"error":   "unauthorized",
			"message": "release record write token is invalid",
		})
		return
	}

	record := buildReleaseSnapshot(app)
	record, err := recordStore.Save(r.Context(), record)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{
			"error":   "release_record_save_failed",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusCreated, record)
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

	var rollout *RolloutSummary
	rolloutSummary, ok, err := loadRolloutSummary(context.Background(), app)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else if ok {
		rollout = &rolloutSummary
	}

	var traffic *TrafficSummary
	trafficSummary, err := loadTrafficSummary(context.Background(), app)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else if trafficSummary.Source != "" && (trafficSummary.VirtualService != nil || len(trafficSummary.DestinationRules) > 0) {
		traffic = &trafficSummary
	}

	var observability *ObservabilitySummary
	observabilitySummary, err := loadObservabilitySummary(context.Background(), app)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else if observabilitySummary.Source != "" {
		observability = &observabilitySummary
	}

	checks := buildReleaseChecks(app, images, metrics, rollout)
	ready := true
	for _, check := range checks {
		if check.Status == "fail" {
			ready = false
			break
		}
	}

	return ReleaseDetail{
		Name:          app.Name,
		Env:           app.Env,
		Namespace:     app.Namespace,
		ArgoCDApp:     app.ArgoCDApp,
		Image:         app.Image,
		CurrentTag:    app.CurrentTag,
		Sync:          app.Sync,
		Health:        app.Health,
		Revision:      app.Revision,
		UpdatedAt:     app.UpdatedAt,
		Images:        images,
		Metrics:       metrics,
		Rollout:       rollout,
		Traffic:       traffic,
		Observability: observability,
		Checks:        checks,
		Ready:         ready,
		Source:        fmt.Sprintf("app:%s,images:%s,metrics:%s", app.Source, imageSource, metrics.Source),
		Warnings:      warnings,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func buildReleaseSnapshot(app AppSummary) ReleaseRecord {
	detail := buildReleaseDetail(app)
	record := releaseRecordFromDetail(app, detail)
	snapshotAt := firstNonEmpty(detail.GeneratedAt, time.Now().UTC().Format(time.RFC3339))
	record.CreatedAt = snapshotAt
	record.ID = releaseRecordSnapshotID(record.Env, record.AppName, record.ImageTag, snapshotAt)
	record.Source = "snapshot"
	return record
}

func buildReleaseRecords(app AppSummary) []ReleaseRecord {
	detail := buildReleaseDetail(app)
	records := []ReleaseRecord{releaseRecordFromDetail(app, detail)}

	for _, release := range releases[app.Name] {
		if release.Version == app.CurrentTag {
			continue
		}
		records = append(records, releaseRecordFromSummary(app, release))
	}

	return records
}

func loadReleaseRecords(ctx context.Context, app AppSummary) []ReleaseRecord {
	recordsByID := make(map[string]ReleaseRecord)
	for _, record := range buildReleaseRecords(app) {
		recordsByID[record.ID] = record
	}

	if recordStore != nil {
		stored, err := recordStore.ListByApp(ctx, app.Name)
		if err != nil {
			log.Printf("failed to list release records app=%s: %v", app.Name, err)
		}
		for _, record := range stored {
			recordsByID[record.ID] = record
		}
	}

	records := make([]ReleaseRecord, 0, len(recordsByID))
	for _, record := range recordsByID {
		records = append(records, record)
	}
	sortReleaseRecords(records)
	return records
}

func newReleaseRecordStore() ReleaseRecordStore {
	dsn := firstNonEmpty(os.Getenv("RELEASE_RECORD_DATABASE_URL"), os.Getenv("POSTGRES_DSN"))
	if strings.TrimSpace(dsn) == "" {
		log.Printf("release record store=memory")
		return NewMemoryReleaseRecordStore()
	}

	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			lastErr = err
			log.Printf("failed to open postgres release record store attempt=%d: %v", attempt, err)
			time.Sleep(3 * time.Second)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := db.PingContext(ctx); err != nil {
			cancel()
			_ = db.Close()
			lastErr = err
			log.Printf("failed to ping postgres release record store attempt=%d: %v", attempt, err)
			time.Sleep(3 * time.Second)
			continue
		}

		store := &PostgresReleaseRecordStore{db: db}
		if err := store.ensureSchema(ctx); err != nil {
			cancel()
			_ = db.Close()
			lastErr = err
			log.Printf("failed to initialize postgres release record schema attempt=%d: %v", attempt, err)
			time.Sleep(3 * time.Second)
			continue
		}
		cancel()

		log.Printf("release record store=postgres")
		return store
	}

	log.Fatalf("postgres release record store is configured but unavailable: %v", lastErr)
	return nil
}

func NewMemoryReleaseRecordStore() *MemoryReleaseRecordStore {
	return &MemoryReleaseRecordStore{records: make(map[string]ReleaseRecord)}
}

func (s *MemoryReleaseRecordStore) Save(_ context.Context, record ReleaseRecord) (ReleaseRecord, error) {
	normalized, err := normalizeReleaseRecord(record)
	if err != nil {
		return ReleaseRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[normalized.ID] = normalized
	return normalized, nil
}

func (s *MemoryReleaseRecordStore) ListByApp(_ context.Context, appName string) ([]ReleaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]ReleaseRecord, 0)
	for _, record := range s.records {
		if record.AppName == appName {
			records = append(records, record)
		}
	}
	sortReleaseRecords(records)
	return records, nil
}

func (s *MemoryReleaseRecordStore) Get(_ context.Context, id string) (ReleaseRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	return record, ok, nil
}

func (s *MemoryReleaseRecordStore) Source() string {
	return "memory"
}

func (s *PostgresReleaseRecordStore) ensureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS release_records (
  id TEXT PRIMARY KEY,
  app_name TEXT NOT NULL,
  env TEXT NOT NULL,
  namespace TEXT NOT NULL,
  jenkins_job TEXT NOT NULL,
  jenkins_build TEXT NOT NULL,
  image TEXT NOT NULL,
  image_tag TEXT NOT NULL,
  image_digest TEXT NOT NULL,
  argocd_app TEXT NOT NULL,
  argocd_revision TEXT NOT NULL,
  argocd_sync TEXT NOT NULL,
  argocd_health TEXT NOT NULL,
  status TEXT NOT NULL,
  verification JSONB NOT NULL,
  source TEXT NOT NULL,
  created_at TEXT NOT NULL
)`)
	return err
}

func (s *PostgresReleaseRecordStore) Save(ctx context.Context, record ReleaseRecord) (ReleaseRecord, error) {
	normalized, err := normalizeReleaseRecord(record)
	if err != nil {
		return ReleaseRecord{}, err
	}
	verification, err := json.Marshal(normalized.Verification)
	if err != nil {
		return ReleaseRecord{}, err
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO release_records (
  id, app_name, env, namespace, jenkins_job, jenkins_build,
  image, image_tag, image_digest,
  argocd_app, argocd_revision, argocd_sync, argocd_health,
  status, verification, source, created_at
) VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9,
  $10, $11, $12, $13,
  $14, $15::jsonb, $16, $17
)
ON CONFLICT (id) DO UPDATE SET
  app_name = EXCLUDED.app_name,
  env = EXCLUDED.env,
  namespace = EXCLUDED.namespace,
  jenkins_job = EXCLUDED.jenkins_job,
  jenkins_build = EXCLUDED.jenkins_build,
  image = EXCLUDED.image,
  image_tag = EXCLUDED.image_tag,
  image_digest = EXCLUDED.image_digest,
  argocd_app = EXCLUDED.argocd_app,
  argocd_revision = EXCLUDED.argocd_revision,
  argocd_sync = EXCLUDED.argocd_sync,
  argocd_health = EXCLUDED.argocd_health,
  status = EXCLUDED.status,
  verification = EXCLUDED.verification,
  source = EXCLUDED.source,
  created_at = EXCLUDED.created_at`,
		normalized.ID, normalized.AppName, normalized.Env, normalized.Namespace, normalized.JenkinsJob, normalized.JenkinsBuild,
		normalized.Image, normalized.ImageTag, normalized.ImageDigest,
		normalized.ArgoCDApp, normalized.ArgoCDRevision, normalized.ArgoCDSync, normalized.ArgoCDHealth,
		normalized.Status, string(verification), normalized.Source, normalized.CreatedAt,
	)
	if err != nil {
		return ReleaseRecord{}, err
	}
	return normalized, nil
}

func (s *PostgresReleaseRecordStore) ListByApp(ctx context.Context, appName string) ([]ReleaseRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, app_name, env, namespace, jenkins_job, jenkins_build,
       image, image_tag, image_digest,
       argocd_app, argocd_revision, argocd_sync, argocd_health,
       status, verification, source, created_at
FROM release_records
WHERE app_name = $1
ORDER BY created_at DESC`, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]ReleaseRecord, 0)
	for rows.Next() {
		record, err := scanReleaseRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortReleaseRecords(records)
	return records, nil
}

func (s *PostgresReleaseRecordStore) Get(ctx context.Context, id string) (ReleaseRecord, bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, app_name, env, namespace, jenkins_job, jenkins_build,
       image, image_tag, image_digest,
       argocd_app, argocd_revision, argocd_sync, argocd_health,
       status, verification, source, created_at
FROM release_records
WHERE id = $1`, id)

	record, err := scanReleaseRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseRecord{}, false, nil
	}
	if err != nil {
		return ReleaseRecord{}, false, err
	}
	return record, true, nil
}

func (s *PostgresReleaseRecordStore) Source() string {
	return "postgres"
}

type releaseRecordScanner interface {
	Scan(dest ...any) error
}

func scanReleaseRecord(scanner releaseRecordScanner) (ReleaseRecord, error) {
	var record ReleaseRecord
	var verification []byte
	if err := scanner.Scan(
		&record.ID, &record.AppName, &record.Env, &record.Namespace, &record.JenkinsJob, &record.JenkinsBuild,
		&record.Image, &record.ImageTag, &record.ImageDigest,
		&record.ArgoCDApp, &record.ArgoCDRevision, &record.ArgoCDSync, &record.ArgoCDHealth,
		&record.Status, &verification, &record.Source, &record.CreatedAt,
	); err != nil {
		return ReleaseRecord{}, err
	}
	if err := json.Unmarshal(verification, &record.Verification); err != nil {
		return ReleaseRecord{}, err
	}
	return record, nil
}

func releaseRecordFromDetail(app AppSummary, detail ReleaseDetail) ReleaseRecord {
	image := imageByTag(detail.Images, app.CurrentTag)
	createdAt := firstNonEmpty(image.PushedAt, app.UpdatedAt, detail.GeneratedAt)

	return ReleaseRecord{
		ID:             releaseRecordID(app.Env, app.Name, app.CurrentTag),
		AppName:        app.Name,
		Env:            app.Env,
		Namespace:      app.Namespace,
		JenkinsJob:     jenkinsJobName(app.Name),
		JenkinsBuild:   jenkinsBuildNumber(app.CurrentTag),
		Image:          app.Image,
		ImageTag:       app.CurrentTag,
		ImageDigest:    image.Digest,
		ArgoCDApp:      app.ArgoCDApp,
		ArgoCDRevision: app.Revision,
		ArgoCDSync:     app.Sync,
		ArgoCDHealth:   app.Health,
		Status:         releaseRecordStatus(detail.Ready),
		Verification: ReleaseVerification{
			Ready:         detail.Ready,
			Checks:        detail.Checks,
			Metrics:       detail.Metrics,
			Rollout:       detail.Rollout,
			Traffic:       detail.Traffic,
			Observability: detail.Observability,
			Warnings:      detail.Warnings,
			VerifiedAt:    detail.GeneratedAt,
		},
		Source:    detail.Source,
		CreatedAt: createdAt,
	}
}

func releaseRecordFromSummary(app AppSummary, release ReleaseSummary) ReleaseRecord {
	status := "unknown"
	if release.Status == "Healthy" {
		status = "succeeded"
	}

	return ReleaseRecord{
		ID:             releaseRecordID(app.Env, app.Name, release.Version),
		AppName:        app.Name,
		Env:            app.Env,
		Namespace:      app.Namespace,
		JenkinsJob:     jenkinsJobName(app.Name),
		JenkinsBuild:   jenkinsBuildNumber(release.Version),
		Image:          imageWithTag(app.Image, release.Version),
		ImageTag:       release.Version,
		ArgoCDApp:      app.ArgoCDApp,
		ArgoCDRevision: release.Commit,
		ArgoCDSync:     app.Sync,
		ArgoCDHealth:   release.Status,
		Status:         status,
		Verification: ReleaseVerification{
			Ready: release.Status == "Healthy",
			Checks: []CheckResult{
				checkNotEmpty("image_tag", release.Version, "Release image tag is available"),
			},
			VerifiedAt: release.BuildTime,
		},
		Source:    "static",
		CreatedAt: release.BuildTime,
	}
}

func findReleaseRecord(records []ReleaseRecord, id string) (ReleaseRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return ReleaseRecord{}, false
}

func imageByTag(images []ImageTagSummary, tag string) ImageTagSummary {
	for _, image := range images {
		if image.Tag == tag {
			return image
		}
	}
	return ImageTagSummary{}
}

func buildReleaseChecks(app AppSummary, images []ImageTagSummary, metrics MetricsSummary, rollout *RolloutSummary) []CheckResult {
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

	if rollout != nil {
		if rollout.Healthy {
			checks = append(checks, CheckResult{
				Name:    "rollout_health",
				Status:  "pass",
				Message: "Argo Rollout is healthy",
			})
		} else {
			checks = append(checks, CheckResult{
				Name:    "rollout_health",
				Status:  "fail",
				Message: "Argo Rollout is not healthy",
			})
		}
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

	summary, err := client.QueryUp(app.Name)
	if err != nil {
		return MetricsSummary{}, err
	}
	if summary.Targets > 0 {
		return summary, nil
	}
	return client.QueryServiceUp(app.Namespace, app.Name)
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

func newKubernetesClientFromEnv() (*KubernetesClient, bool, error) {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := firstNonEmpty(os.Getenv("KUBERNETES_SERVICE_PORT"), "443")
	server := strings.TrimRight(os.Getenv("KUBERNETES_API_SERVER"), "/")
	if server == "" && host != "" {
		server = "https://" + host + ":" + port
	}
	if server == "" {
		return nil, false, nil
	}

	tokenBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, false, fmt.Errorf("read kubernetes service account token: %w", err)
	}
	caBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, false, fmt.Errorf("read kubernetes service account ca: %w", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return nil, false, fmt.Errorf("load kubernetes service account ca")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}

	return &KubernetesClient{
		server: server,
		token:  strings.TrimSpace(string(tokenBytes)),
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}, true, nil
}

func (c *KubernetesClient) GetJSON(ctx context.Context, apiPath string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+apiPath, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("kubernetes api status=%d body=%s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func loadRolloutSummary(ctx context.Context, app AppSummary) (RolloutSummary, bool, error) {
	client, ok, err := newKubernetesClientFromEnv()
	if err != nil || !ok {
		return RolloutSummary{}, false, err
	}

	var rollout k8sRollout
	apiPath := fmt.Sprintf("/apis/argoproj.io/v1alpha1/namespaces/%s/rollouts/%s", url.PathEscape(app.Namespace), url.PathEscape(app.Name))
	status, err := client.GetJSON(ctx, apiPath, &rollout)
	if err != nil {
		if status == http.StatusNotFound {
			return RolloutSummary{}, false, nil
		}
		return RolloutSummary{}, false, err
	}

	analysisRuns, err := loadAnalysisRunsWithClient(ctx, client, app)
	if err != nil {
		log.Printf("failed to list analysisruns app=%s: %v", app.Name, err)
	}
	return rolloutFromKubernetes(rollout, analysisRuns), true, nil
}

func loadAnalysisRuns(ctx context.Context, app AppSummary) ([]AnalysisRunSummary, error) {
	client, ok, err := newKubernetesClientFromEnv()
	if err != nil || !ok {
		return nil, err
	}
	return loadAnalysisRunsWithClient(ctx, client, app)
}

func loadAnalysisRunsWithClient(ctx context.Context, client *KubernetesClient, app AppSummary) ([]AnalysisRunSummary, error) {
	var list k8sAnalysisRunList
	apiPath := fmt.Sprintf("/apis/argoproj.io/v1alpha1/namespaces/%s/analysisruns", url.PathEscape(app.Namespace))
	if _, err := client.GetJSON(ctx, apiPath, &list); err != nil {
		return nil, err
	}

	items := make([]AnalysisRunSummary, 0)
	prefix := app.Name + "-"
	for _, item := range list.Items {
		if !strings.HasPrefix(item.Metadata.Name, prefix) {
			continue
		}
		items = append(items, analysisRunFromKubernetes(item))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].StartedAt > items[j].StartedAt
	})
	return items, nil
}

func loadTrafficSummary(ctx context.Context, app AppSummary) (TrafficSummary, error) {
	client, ok, err := newKubernetesClientFromEnv()
	if err != nil || !ok {
		return TrafficSummary{}, err
	}

	var virtualService *VirtualServiceSummary
	var vs k8sVirtualService
	vsPath := fmt.Sprintf("/apis/networking.istio.io/v1beta1/namespaces/%s/virtualservices/%s", url.PathEscape(app.Namespace), url.PathEscape(app.Name))
	status, err := client.GetJSON(ctx, vsPath, &vs)
	if err != nil && status != http.StatusNotFound {
		return TrafficSummary{}, err
	}
	if err == nil {
		summary := virtualServiceFromKubernetes(vs)
		virtualService = &summary
	}

	destinationRules, err := loadDestinationRules(ctx, client, app)
	if err != nil {
		return TrafficSummary{}, err
	}

	return TrafficSummary{
		Name:             app.Name,
		Namespace:        app.Namespace,
		VirtualService:   virtualService,
		DestinationRules: destinationRules,
		Source:           "kubernetes",
	}, nil
}

func loadDestinationRules(ctx context.Context, client *KubernetesClient, app AppSummary) ([]DestinationRuleSummary, error) {
	var list k8sDestinationRuleList
	apiPath := fmt.Sprintf("/apis/networking.istio.io/v1beta1/namespaces/%s/destinationrules", url.PathEscape(app.Namespace))
	if _, err := client.GetJSON(ctx, apiPath, &list); err != nil {
		return nil, err
	}

	items := make([]DestinationRuleSummary, 0)
	for _, item := range list.Items {
		if !destinationRuleMatchesApp(item, app.Name) {
			continue
		}
		items = append(items, destinationRuleFromKubernetes(item))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func loadObservabilitySummary(ctx context.Context, app AppSummary) (ObservabilitySummary, error) {
	var rollout *RolloutSummary
	rolloutSummary, ok, err := loadRolloutSummary(ctx, app)
	if err != nil {
		return ObservabilitySummary{}, err
	}
	if ok {
		rollout = &rolloutSummary
	}

	var traffic *TrafficSummary
	trafficSummary, err := loadTrafficSummary(ctx, app)
	if err == nil && trafficSummary.Source != "" {
		traffic = &trafficSummary
	}

	summary := ObservabilitySummary{Source: "static"}
	if rollout != nil || traffic != nil {
		stage := buildCanaryStageSummary(rollout, traffic)
		summary.CanaryStage = &stage
		summary.Source = "kubernetes"
	}

	istioMetrics, promErr := loadIstioMetrics(app)
	if promErr == nil && istioMetrics.Source != "" {
		summary.IstioMetrics = &istioMetrics
		if summary.Source == "static" {
			summary.Source = "prometheus"
		} else {
			summary.Source = "kubernetes,prometheus"
		}
	}
	if summary.Source == "static" {
		return ObservabilitySummary{}, promErr
	}
	return summary, promErr
}

func buildCanaryStageSummary(rollout *RolloutSummary, traffic *TrafficSummary) CanaryStageSummary {
	stableWeight, canaryWeight := extractCanaryWeights(traffic)
	stage := inferCanaryStage(rollout, stableWeight, canaryWeight)

	summary := CanaryStageSummary{
		StableWeight: stableWeight,
		CanaryWeight: canaryWeight,
		Stage:        stage,
	}
	if rollout != nil {
		summary.Phase = rollout.Phase
		summary.CurrentStepIndex = rollout.CurrentStepIndex
	}
	return summary
}

func extractCanaryWeights(traffic *TrafficSummary) (int, int) {
	if traffic == nil || traffic.VirtualService == nil {
		return 100, 0
	}
	for _, httpRoute := range traffic.VirtualService.HTTP {
		stableWeight, canaryWeight := 0, 0
		for _, route := range httpRoute.Routes {
			if strings.Contains(route.Host, "-canary") {
				canaryWeight = route.Weight
			} else {
				stableWeight = route.Weight
			}
		}
		if stableWeight > 0 || canaryWeight > 0 {
			return stableWeight, canaryWeight
		}
	}
	return 100, 0
}

func inferCanaryStage(rollout *RolloutSummary, stableWeight, canaryWeight int) string {
	if canaryWeight == 0 && stableWeight > 0 {
		return "stable"
	}
	if canaryWeight == 100 {
		return "canary_full"
	}
	if canaryWeight > 0 {
		return fmt.Sprintf("canary_%d", canaryWeight)
	}
	if rollout != nil && rollout.Phase == "Progressing" {
		return "progressing"
	}
	if rollout != nil && rollout.Phase != "" {
		return strings.ToLower(rollout.Phase)
	}
	return "unknown"
}

func loadIstioMetrics(app AppSummary) (IstioMetricsSummary, error) {
	client, ok := newPrometheusClientFromEnv()
	if !ok {
		return IstioMetricsSummary{}, fmt.Errorf("prometheus server is not configured")
	}

	selectors := istioMetricSelectors(app)
	requestRate, matched, err := queryIstioRequestRate(client, selectors)
	if err != nil {
		return IstioMetricsSummary{}, err
	}
	if matched.Name == "" {
		names := make([]string, 0, len(selectors))
		for _, sel := range selectors {
			names = append(names, sel.Name)
		}
		message := fmt.Sprintf("no istio request metrics matched selectors: %s", strings.Join(names, ", "))
		hasIstio, err := prometheusHasIstioMetrics(client)
		if err != nil {
			return IstioMetricsSummary{}, err
		}
		if !hasIstio {
			message += "; istio_requests_total is not present in Prometheus (enable istio-ingressgateway PodMonitor scrape)"
		}
		return IstioMetricsSummary{
			Source:  "prometheus",
			Message: message,
		}, nil
	}

	errorRate, err := queryIstioErrorRate(client, matched)
	if err != nil {
		return IstioMetricsSummary{}, err
	}

	p95Latency, err := queryIstioP95Latency(client, matched)
	if err != nil {
		return IstioMetricsSummary{}, err
	}

	errorPercent := 0.0
	if requestRate > 0 {
		errorPercent = (errorRate / requestRate) * 100
	}

	byDestination, err := loadIstioDestinationMetrics(client, matched)
	if err != nil {
		log.Printf("failed to load istio destination metrics app=%s: %v", app.Name, err)
	}

	return IstioMetricsSummary{
		RequestRateRPS:   requestRate,
		ErrorRateRPS:     errorRate,
		ErrorRatePercent: errorPercent,
		P95LatencyMS:     p95Latency,
		ByDestination:    byDestination,
		MatchedSelector:  matched.Name,
		Source:           "prometheus",
	}, nil
}

func istioMetricSelectors(app AppSummary) []istioMetricSelector {
	name := app.Name
	ns := app.Namespace
	serviceFQDN := name + `-.*\\.` + ns + `\\.svc\\.cluster\\.local`
	return []istioMetricSelector{
		{
			Name:       "destination_service_name",
			Selector:   fmt.Sprintf(`destination_service_name=~%q`, name+`-.*`),
			GroupLabel: "destination_service_name",
		},
		{
			Name:       "destination_service_fqdn",
			Selector:   fmt.Sprintf(`destination_service=~%q`, serviceFQDN),
			GroupLabel: "destination_service",
		},
		{
			Name:       "destination_service_short",
			Selector:   fmt.Sprintf(`destination_service=~%q`, name+`-(stable|canary)`),
			GroupLabel: "destination_service",
		},
		{
			Name:       "destination_workload",
			Selector:   fmt.Sprintf(`destination_workload_namespace=%q,destination_workload=~%q`, ns, name+`.*`),
			GroupLabel: "destination_workload",
		},
		{
			Name:       "ingress_to_service",
			Selector:   fmt.Sprintf(`source_workload="istio-ingressgateway",destination_service=~%q`, serviceFQDN),
			GroupLabel: "destination_service",
		},
		{
			Name:       "ingress_to_namespace",
			Selector:   fmt.Sprintf(`source_workload="istio-ingressgateway",destination_service_namespace=%q,destination_service=~%q`, ns, name+`-.*`),
			GroupLabel: "destination_service",
		},
		{
			Name:       "destination_service_namespace",
			Selector:   fmt.Sprintf(`destination_service_namespace=%q,destination_service=~%q`, ns, serviceFQDN),
			GroupLabel: "destination_service",
		},
	}
}

func prometheusHasIstioMetrics(client *PrometheusClient) (bool, error) {
	_, series, err := client.QueryScalar(`count(istio_requests_total)`)
	if err != nil {
		return false, err
	}
	return series > 0, nil
}

func queryIstioRequestRate(client *PrometheusClient, selectors []istioMetricSelector) (float64, istioMetricSelector, error) {
	for _, sel := range selectors {
		query := fmt.Sprintf(`sum(rate(istio_requests_total{%s}[5m]))`, sel.Selector)
		value, series, err := client.QueryScalar(query)
		if err != nil {
			return 0, istioMetricSelector{}, err
		}
		if series > 0 {
			return value, sel, nil
		}
	}
	return 0, istioMetricSelector{}, nil
}

func queryIstioErrorRate(client *PrometheusClient, sel istioMetricSelector) (float64, error) {
	query := fmt.Sprintf(`sum(rate(istio_requests_total{%s,response_code=~"5.."}[5m]))`, sel.Selector)
	value, _, err := client.QueryScalar(query)
	return value, err
}

func queryIstioP95Latency(client *PrometheusClient, sel istioMetricSelector) (float64, error) {
	queries := []string{
		fmt.Sprintf(`histogram_quantile(0.95, sum(rate(istio_request_duration_milliseconds_bucket{%s}[5m])) by (le))`, sel.Selector),
		fmt.Sprintf(`histogram_quantile(0.95, sum(rate(istio_request_duration_seconds_bucket{%s}[5m])) by (le)) * 1000`, sel.Selector),
	}
	for _, query := range queries {
		value, series, err := client.QueryScalar(query)
		if err != nil {
			return 0, err
		}
		if series > 0 {
			return value, nil
		}
	}
	return 0, nil
}

func loadIstioDestinationMetrics(client *PrometheusClient, sel istioMetricSelector) ([]IstioDestinationMetric, error) {
	requestQuery := fmt.Sprintf(`sum by (%s) (rate(istio_requests_total{%s}[5m]))`, sel.GroupLabel, sel.Selector)
	errorQuery := fmt.Sprintf(`sum by (%s) (rate(istio_requests_total{%s,response_code=~"5.."}[5m]))`, sel.GroupLabel, sel.Selector)

	requests, err := client.QueryVector(requestQuery, sel.GroupLabel)
	if err != nil {
		return nil, err
	}
	errors, err := client.QueryVector(errorQuery, sel.GroupLabel)
	if err != nil {
		return nil, err
	}

	destinations := make(map[string]IstioDestinationMetric)
	for label, value := range requests {
		destinations[label] = IstioDestinationMetric{
			Destination:    label,
			RequestRateRPS: value,
		}
	}
	for label, value := range errors {
		item := destinations[label]
		item.Destination = label
		item.ErrorRateRPS = value
		if item.RequestRateRPS > 0 {
			item.ErrorRatePercent = (value / item.RequestRateRPS) * 100
		}
		destinations[label] = item
	}

	items := make([]IstioDestinationMetric, 0, len(destinations))
	for _, item := range destinations {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Destination < items[j].Destination
	})
	return items, nil
}

func destinationRuleMatchesApp(item k8sDestinationRule, appName string) bool {
	return item.Metadata.Name == appName ||
		strings.HasPrefix(item.Metadata.Name, appName+"-") ||
		item.Spec.Host == appName ||
		strings.HasPrefix(item.Spec.Host, appName+"-")
}

func virtualServiceFromKubernetes(item k8sVirtualService) VirtualServiceSummary {
	routes := make([]HTTPRouteSum, 0, len(item.Spec.HTTP))
	for _, httpRoute := range item.Spec.HTTP {
		destinations := make([]RouteDest, 0, len(httpRoute.Route))
		for _, route := range httpRoute.Route {
			destinations = append(destinations, RouteDest{
				Host:   route.Destination.Host,
				Port:   route.Destination.Port.Number,
				Weight: route.Weight,
			})
		}
		var retries *RetryPolicy
		if httpRoute.Retries != nil {
			retries = &RetryPolicy{
				Attempts:      httpRoute.Retries.Attempts,
				PerTryTimeout: httpRoute.Retries.PerTryTimeout,
				RetryOn:       httpRoute.Retries.RetryOn,
			}
		}
		routes = append(routes, HTTPRouteSum{
			Name:    httpRoute.Name,
			Timeout: httpRoute.Timeout,
			Retries: retries,
			Routes:  destinations,
		})
	}
	return VirtualServiceSummary{
		Name:      item.Metadata.Name,
		Namespace: item.Metadata.Namespace,
		Hosts:     item.Spec.Hosts,
		Gateways:  item.Spec.Gateways,
		HTTP:      routes,
	}
}

func destinationRuleFromKubernetes(item k8sDestinationRule) DestinationRuleSummary {
	var connectionPool *ConnectionPoolSummary
	var outlierDetection *OutlierDetectionSum
	if item.Spec.TrafficPolicy != nil {
		if item.Spec.TrafficPolicy.ConnectionPool != nil {
			connectionPool = &ConnectionPoolSummary{}
			if item.Spec.TrafficPolicy.ConnectionPool.TCP != nil {
				connectionPool.TCP = &TCPPoolSummary{
					MaxConnections: item.Spec.TrafficPolicy.ConnectionPool.TCP.MaxConnections,
				}
			}
			if item.Spec.TrafficPolicy.ConnectionPool.HTTP != nil {
				connectionPool.HTTP = &HTTPPoolSummary{
					HTTP1MaxPendingRequests:  item.Spec.TrafficPolicy.ConnectionPool.HTTP.HTTP1MaxPendingRequests,
					MaxRequestsPerConnection: item.Spec.TrafficPolicy.ConnectionPool.HTTP.MaxRequestsPerConnection,
				}
			}
		}
		if item.Spec.TrafficPolicy.OutlierDetection != nil {
			outlierDetection = &OutlierDetectionSum{
				Consecutive5xxErrors: item.Spec.TrafficPolicy.OutlierDetection.Consecutive5xxErrors,
				Interval:             item.Spec.TrafficPolicy.OutlierDetection.Interval,
				BaseEjectionTime:     item.Spec.TrafficPolicy.OutlierDetection.BaseEjectionTime,
				MaxEjectionPercent:   item.Spec.TrafficPolicy.OutlierDetection.MaxEjectionPercent,
			}
		}
	}
	return DestinationRuleSummary{
		Name:             item.Metadata.Name,
		Namespace:        item.Metadata.Namespace,
		Host:             item.Spec.Host,
		ConnectionPool:   connectionPool,
		OutlierDetection: outlierDetection,
	}
}

func rolloutFromKubernetes(rollout k8sRollout, analysisRuns []AnalysisRunSummary) RolloutSummary {
	conditions := make([]RolloutCondition, 0, len(rollout.Status.Conditions))
	healthy := rollout.Status.Phase == "Healthy"
	for _, condition := range rollout.Status.Conditions {
		conditions = append(conditions, RolloutCondition{
			Type:    condition.Type,
			Status:  condition.Status,
			Reason:  condition.Reason,
			Message: condition.Message,
		})
		if condition.Type == "Healthy" && condition.Status == "True" {
			healthy = true
		}
	}
	return RolloutSummary{
		Name:              rollout.Metadata.Name,
		Namespace:         rollout.Metadata.Namespace,
		Phase:             rollout.Status.Phase,
		CurrentStepIndex:  rollout.Status.CurrentStepIndex,
		CurrentPodHash:    rollout.Status.CurrentPodHash,
		StableRS:          rollout.Status.StableRS,
		Replicas:          rollout.Status.Replicas,
		UpdatedReplicas:   rollout.Status.UpdatedReplicas,
		AvailableReplicas: rollout.Status.AvailableReplicas,
		Conditions:        conditions,
		AnalysisRuns:      analysisRuns,
		Healthy:           healthy,
		Source:            "kubernetes",
	}
}

func analysisRunFromKubernetes(item k8sAnalysisRun) AnalysisRunSummary {
	metrics := make([]AnalysisMetricSummary, 0, len(item.Status.MetricResults))
	for _, metric := range item.Status.MetricResults {
		metrics = append(metrics, AnalysisMetricSummary{
			Name:    metric.Name,
			Phase:   metric.Phase,
			Message: metric.Message,
		})
	}
	return AnalysisRunSummary{
		Name:      item.Metadata.Name,
		Namespace: item.Metadata.Namespace,
		Phase:     item.Status.Phase,
		Message:   item.Status.Message,
		StartedAt: firstNonEmpty(item.Status.StartedAt, item.Metadata.CreationTimestamp),
		Metric:    metrics,
	}
}

func (c *PrometheusClient) QueryUp(job string) (MetricsSummary, error) {
	query := fmt.Sprintf(`up{job=%q}`, job)
	return c.queryUp(job, query, "no prometheus targets matched up{job=\""+job+"\"}")
}

func (c *PrometheusClient) QueryServiceUp(namespace string, service string) (MetricsSummary, error) {
	query := serviceUpQuery(namespace, service)
	message := fmt.Sprintf("no prometheus targets matched service %q or stable/canary services in namespace %q", service, namespace)
	return c.queryUp(service, query, message)
}

func serviceUpQuery(namespace string, service string) string {
	return fmt.Sprintf(`up{namespace=%q,service=%q} or up{namespace=%q,service=~%q}`, namespace, service, namespace, service+"-(stable|canary)")
}

func (c *PrometheusClient) queryUp(name string, query string, noTargetsMessage string) (MetricsSummary, error) {
	result, err := c.query(query)
	if err != nil {
		return MetricsSummary{}, err
	}

	var up float64
	for _, item := range result.Data.Result {
		value, ok := prometheusItemValue(item)
		if !ok {
			continue
		}
		up += value
	}

	targets := len(result.Data.Result)
	summary := MetricsSummary{
		Name:    name,
		Source:  "prometheus",
		Up:      up,
		Targets: targets,
		Healthy: targets > 0 && int(up) == targets,
	}
	if targets == 0 {
		summary.Message = noTargetsMessage
	}
	return summary, nil
}

func (c *PrometheusClient) QueryScalar(query string) (float64, int, error) {
	result, err := c.query(query)
	if err != nil {
		return 0, 0, err
	}
	value, ok := prometheusScalarValue(result)
	if !ok {
		return 0, 0, nil
	}
	return value, len(result.Data.Result), nil
}

func (c *PrometheusClient) QueryVector(query string, groupLabel string) (map[string]float64, error) {
	result, err := c.query(query)
	if err != nil {
		return nil, err
	}
	values := make(map[string]float64)
	for _, item := range result.Data.Result {
		label := destinationLabelFromMetric(item.Metric, groupLabel)
		if label == "" {
			continue
		}
		value, ok := prometheusItemValue(item)
		if !ok {
			continue
		}
		values[label] += value
	}
	return values, nil
}

func destinationLabelFromMetric(metric map[string]string, preferred string) string {
	if preferred != "" && metric[preferred] != "" {
		return metric[preferred]
	}
	for _, key := range []string{"destination_service_name", "destination_service", "destination_workload", "destination_canonical_service"} {
		if metric[key] != "" {
			return metric[key]
		}
	}
	return ""
}

func (c *PrometheusClient) query(query string) (prometheusQueryResponse, error) {
	apiURL := c.server + "/api/v1/query?query=" + url.QueryEscape(query)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return prometheusQueryResponse{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return prometheusQueryResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return prometheusQueryResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return prometheusQueryResponse{}, fmt.Errorf("prometheus api status=%d body=%s", resp.StatusCode, string(body))
	}

	var result prometheusQueryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return prometheusQueryResponse{}, err
	}
	if result.Status != "success" {
		return prometheusQueryResponse{}, fmt.Errorf("prometheus query failed: %s", result.Error)
	}
	return result, nil
}

func prometheusScalarValue(result prometheusQueryResponse) (float64, bool) {
	if len(result.Data.Result) == 0 {
		return 0, false
	}
	return prometheusItemValue(result.Data.Result[0])
}

func prometheusItemValue(item struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}) (float64, bool) {
	if len(item.Value) < 2 {
		return 0, false
	}
	valueText, ok := item.Value[1].(string)
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil {
		return 0, false
	}
	return value, true
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
		"message": "method is not supported for this route",
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

func normalizeReleaseRecord(record ReleaseRecord) (ReleaseRecord, error) {
	record.AppName = strings.TrimSpace(record.AppName)
	record.ImageTag = strings.TrimSpace(record.ImageTag)
	if record.AppName == "" {
		return ReleaseRecord{}, fmt.Errorf("app_name is required")
	}
	if record.ImageTag == "" {
		return ReleaseRecord{}, fmt.Errorf("image_tag is required")
	}

	record.Env = firstNonEmpty(record.Env, "dev")
	record.Namespace = firstNonEmpty(record.Namespace, "cloudops-dev")
	record.ID = firstNonEmpty(record.ID, releaseRecordID(record.Env, record.AppName, record.ImageTag))
	record.JenkinsJob = firstNonEmpty(record.JenkinsJob, jenkinsJobName(record.AppName))
	record.JenkinsBuild = firstNonEmpty(record.JenkinsBuild, jenkinsBuildNumber(record.ImageTag))
	record.Status = firstNonEmpty(record.Status, releaseRecordStatus(record.Verification.Ready))
	record.Source = firstNonEmpty(record.Source, "api")
	record.CreatedAt = firstNonEmpty(record.CreatedAt, time.Now().UTC().Format(time.RFC3339))
	record.Verification.VerifiedAt = firstNonEmpty(record.Verification.VerifiedAt, record.CreatedAt)
	return record, nil
}

func rollbackCandidates(app AppSummary, records []ReleaseRecord) []ReleaseRecord {
	candidates := make([]ReleaseRecord, 0)
	for _, record := range records {
		if record.AppName != app.Name {
			continue
		}
		if record.ImageTag == "" || record.ImageTag == app.CurrentTag {
			continue
		}
		if record.Status != "succeeded" || !record.Verification.Ready {
			continue
		}
		candidates = append(candidates, record)
	}
	sortReleaseRecords(candidates)
	return candidates
}

func sortReleaseRecords(records []ReleaseRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		left, leftOK := parseReleaseRecordTime(records[i].CreatedAt)
		right, rightOK := parseReleaseRecordTime(records[j].CreatedAt)
		if leftOK && rightOK {
			return left.After(right)
		}
		return records[i].CreatedAt > records[j].CreatedAt
	})
}

func parseReleaseRecordTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func authorizedReleaseRecordWrite(r *http.Request) bool {
	token := strings.TrimSpace(os.Getenv("RELEASE_RECORD_WRITE_TOKEN"))
	if token == "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(auth, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == token {
		return true
	}
	return strings.TrimSpace(r.Header.Get("X-Release-Record-Token")) == token
}

func imageTag(image string, fallback string) string {
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[lastColon+1:]
	}
	return fallback
}

func imageWithTag(image string, tag string) string {
	if strings.TrimSpace(tag) == "" {
		return image
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[:lastColon+1] + tag
	}
	return image + ":" + tag
}

func releaseRecordID(envName string, appName string, tag string) string {
	return strings.Join([]string{
		firstNonEmpty(envName, "unknown"),
		firstNonEmpty(appName, "unknown"),
		firstNonEmpty(tag, "unknown"),
	}, "-")
}

func releaseRecordSnapshotID(envName string, appName string, tag string, timestamp string) string {
	suffix := strings.NewReplacer("-", "", ":", "", "T", "", "Z", "", ".", "").Replace(firstNonEmpty(timestamp, time.Now().UTC().Format(time.RFC3339)))
	if len(suffix) > 14 {
		suffix = suffix[:14]
	}
	return releaseRecordID(envName, appName, tag) + "-snapshot-" + suffix
}

func jenkinsJobName(appName string) string {
	return "test-" + appName + "-kaniko"
}

func jenkinsBuildNumber(tag string) string {
	index := strings.LastIndex(tag, "-")
	if index < 0 || index == len(tag)-1 {
		return ""
	}
	candidate := tag[index+1:]
	if _, err := strconv.Atoi(candidate); err != nil {
		return ""
	}
	return candidate
}

func releaseRecordStatus(ready bool) string {
	if ready {
		return "succeeded"
	}
	return "failed"
}

func label(value string) string {
	return strconv.Quote(value)
}
