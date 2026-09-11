package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type JenkinsClient struct {
	server   string
	username string
	password string
	client   *http.Client

	mu           sync.Mutex
	crumbChecked bool
	crumbHeader  string
	crumbValue   string
}

type JenkinsJob struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Class string `json:"class,omitempty"`
}

type JenkinsMatch struct {
	Service     string     `json:"service"`
	Branch      string     `json:"branch,omitempty"`
	Job         JenkinsJob `json:"job"`
	BranchParam string     `json:"branch_param,omitempty"`
	ExpectedTag string     `json:"expected_image_tag,omitempty"`
	Image       string     `json:"expected_image,omitempty"`
}

func newJenkinsClientFromEnv() (*JenkinsClient, bool) {
	server := strings.TrimRight(env("JENKINS_URL", env("JENKINS_SERVER", "")), "/")
	username := strings.TrimSpace(firstNonEmpty(os.Getenv("JENKINS_USER"), os.Getenv("JENKINS_USERNAME")))
	password := strings.TrimSpace(firstNonEmpty(os.Getenv("JENKINS_TOKEN"), os.Getenv("JENKINS_PASSWORD")))
	if server == "" || username == "" || password == "" {
		return nil, false
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: envBool("JENKINS_INSECURE", true)} //nolint:gosec

	return &JenkinsClient{
		server:   server,
		username: username,
		password: password,
		client: &http.Client{
			Timeout:   time.Duration(envInt("JENKINS_HTTP_TIMEOUT", 30)) * time.Second,
			Transport: transport,
		},
	}, true
}

func (c *JenkinsClient) authHeader() string {
	raw := c.username + ":" + c.password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

func (c *JenkinsClient) ensureCrumb() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crumbChecked {
		return nil
	}
	c.crumbChecked = true

	req, err := http.NewRequest(http.MethodGet, c.server+"/crumbIssuer/api/json", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("User-Agent", "cloudops-cicd/jenkins")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("jenkins crumb status=%d body=%s", resp.StatusCode, string(body))
	}
	var payload struct {
		Crumb             string `json:"crumb"`
		CrumbRequestField string `json:"crumbRequestField"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	c.crumbHeader = payload.CrumbRequestField
	c.crumbValue = payload.Crumb
	return nil
}

func (c *JenkinsClient) doJSON(method, apiURL string, form url.Values) (body []byte, header http.Header, status int, err error) {
	if err := c.ensureCrumb(); err != nil {
		return nil, nil, 0, err
	}

	var reader io.Reader
	contentType := ""
	if form != nil {
		reader = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequest(method, apiURL, reader)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("User-Agent", "cloudops-cicd/jenkins")
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.mu.Lock()
	if c.crumbHeader != "" && c.crumbValue != "" {
		req.Header.Set(c.crumbHeader, c.crumbValue)
	}
	c.mu.Unlock()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	return body, resp.Header, resp.StatusCode, err
}

func (c *JenkinsClient) ListJobs() ([]JenkinsJob, error) {
	return c.listJobsRecursive(c.server)
}

func (c *JenkinsClient) listJobsRecursive(containerURL string) ([]JenkinsJob, error) {
	apiURL := strings.TrimRight(containerURL, "/") + "/api/json?tree=jobs[name,url,_class]"
	body, _, status, err := c.doJSON(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("jenkins jobs status=%d body=%s", status, string(body))
	}
	var payload struct {
		Jobs []struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Class string `json:"_class"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	out := make([]JenkinsJob, 0)
	seen := map[string]struct{}{}
	for _, item := range payload.Jobs {
		name := strings.TrimSpace(item.Name)
		jobURL := strings.TrimSpace(item.URL)
		if name == "" || jobURL == "" {
			continue
		}
		if strings.Contains(strings.ToLower(item.Class), "folder") {
			nested, err := c.listJobsRecursive(jobURL)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
			continue
		}
		key := strings.TrimRight(jobURL, "/")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, JenkinsJob{Name: name, URL: jobURL, Class: item.Class})
	}
	return out, nil
}

func (c *JenkinsClient) ParameterNames(jobURL string) ([]string, error) {
	apiURL := strings.TrimRight(jobURL, "/") + "/api/json?tree=property[parameterDefinitions[name]]"
	body, _, status, err := c.doJSON(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("jenkins job params status=%d body=%s", status, string(body))
	}
	var payload struct {
		Property []struct {
			ParameterDefinitions []struct {
				Name string `json:"name"`
			} `json:"parameterDefinitions"`
		} `json:"property"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	names := make([]string, 0)
	for _, prop := range payload.Property {
		for _, def := range prop.ParameterDefinitions {
			if strings.TrimSpace(def.Name) != "" {
				names = append(names, def.Name)
			}
		}
	}
	return names, nil
}

func (c *JenkinsClient) TriggerBuild(jobURL, branchParam, branch string) (queueURL string, err error) {
	base := strings.TrimRight(jobURL, "/")
	form := url.Values{}
	target := base + "/build"
	if strings.TrimSpace(branchParam) != "" && strings.TrimSpace(branch) != "" {
		target = base + "/buildWithParameters"
		form.Set(branchParam, branch)
	}
	_, header, status, err := c.doJSON(http.MethodPost, target, form)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated && status != http.StatusOK && status != http.StatusFound {
		return "", fmt.Errorf("jenkins trigger status=%d", status)
	}
	queueURL = strings.TrimSpace(header.Get("Location"))
	if queueURL == "" {
		return "", fmt.Errorf("jenkins did not return queue Location")
	}
	return queueURL, nil
}

func (c *JenkinsClient) WaitQueue(queueURL string, timeoutSec, pollSec int) (buildURL string, err error) {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	apiURL := strings.TrimRight(queueURL, "/") + "/api/json"
	for {
		body, _, status, err := c.doJSON(http.MethodGet, apiURL, nil)
		if err != nil {
			return "", err
		}
		if status < 200 || status >= 300 {
			return "", fmt.Errorf("jenkins queue status=%d body=%s", status, string(body))
		}
		var payload struct {
			Cancelled  bool `json:"cancelled"`
			Why        string `json:"why"`
			Executable *struct {
				URL string `json:"url"`
			} `json:"executable"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return "", err
		}
		if payload.Executable != nil && strings.TrimSpace(payload.Executable.URL) != "" {
			return payload.Executable.URL, nil
		}
		if payload.Cancelled {
			reason := firstNonEmpty(payload.Why, "queue item cancelled")
			return "", fmt.Errorf("%s", reason)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for queue %s", queueURL)
		}
		time.Sleep(time.Duration(pollSec) * time.Second)
	}
}

func (c *JenkinsClient) GetBuild(buildURL string) (building bool, result, description string, number int, err error) {
	apiURL := strings.TrimRight(buildURL, "/") + "/api/json"
	body, _, status, err := c.doJSON(http.MethodGet, apiURL, nil)
	if err != nil {
		return false, "", "", 0, err
	}
	if status < 200 || status >= 300 {
		return false, "", "", 0, fmt.Errorf("jenkins build status=%d body=%s", status, string(body))
	}
	var payload struct {
		Building    bool   `json:"building"`
		Result      string `json:"result"`
		Description string `json:"description"`
		Number      int    `json:"number"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, "", "", 0, err
	}
	return payload.Building, payload.Result, payload.Description, payload.Number, nil
}

func (c *JenkinsClient) WaitBuild(buildURL string, timeoutSec, pollSec int) (result, description string, number int, err error) {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		building, res, desc, num, err := c.GetBuild(buildURL)
		if err != nil {
			return "", "", 0, err
		}
		if !building {
			return firstNonEmpty(res, "UNKNOWN"), desc, num, nil
		}
		if time.Now().After(deadline) {
			return "", "", 0, fmt.Errorf("timed out waiting for build %s", buildURL)
		}
		time.Sleep(time.Duration(pollSec) * time.Second)
	}
}

func matchJenkinsJob(service string, jobs []JenkinsJob) (JenkinsJob, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return JenkinsJob{}, fmt.Errorf("service is required")
	}
	lower := strings.ToLower(service)

	preferred := []string{
		"test-" + lower + "-kaniko",
		lower + "-kaniko",
		"test-" + lower,
		lower,
	}
	byName := map[string]JenkinsJob{}
	for _, job := range jobs {
		byName[strings.ToLower(job.Name)] = job
	}
	for _, name := range preferred {
		if job, ok := byName[name]; ok {
			return job, nil
		}
	}

	matches := make([]JenkinsJob, 0)
	for _, job := range jobs {
		name := strings.ToLower(job.Name)
		if strings.Contains(name, lower) {
			matches = append(matches, job)
		}
	}
	if len(matches) == 0 {
		return JenkinsJob{}, fmt.Errorf("no jenkins job matched service %q", service)
	}
	if len(matches) > 1 {
		// Prefer *-kaniko when ambiguous.
		kaniko := make([]JenkinsJob, 0)
		for _, job := range matches {
			if strings.HasSuffix(strings.ToLower(job.Name), "-kaniko") {
				kaniko = append(kaniko, job)
			}
		}
		if len(kaniko) == 1 {
			return kaniko[0], nil
		}
		names := make([]string, 0, len(matches))
		for _, job := range matches {
			names = append(names, job.Name)
		}
		sort.Strings(names)
		return JenkinsJob{}, fmt.Errorf("service %q matched multiple jobs: %s", service, strings.Join(names, ", "))
	}
	return matches[0], nil
}

func resolveBranchParam(names []string) string {
	if len(names) == 0 {
		return ""
	}
	priority := []string{"BRANCH", "branch", "gitBranch", "buildBranch", "GIT_BRANCH"}
	lowerMap := map[string]string{}
	for _, name := range names {
		lowerMap[strings.ToLower(name)] = name
	}
	for _, want := range priority {
		if actual, ok := lowerMap[strings.ToLower(want)]; ok {
			return actual
		}
	}
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "branch") {
			return name
		}
	}
	return ""
}

func expectedImageForService(service, branch string) (image, tag string) {
	service = strings.TrimSpace(service)
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "main"
	}
	// Lab convention: Harbor tag main-<BUILD_NUMBER>; before build we only know branch prefix.
	tag = branch + "-pending"
	image = fmt.Sprintf("harbor-server.jianggan.cn/cloudops/%s:%s", service, tag)
	for _, app := range apps {
		if app.Name == service {
			repo := firstNonEmpty(app.HarborRepository, service)
			project := firstNonEmpty(app.HarborProject, "cloudops")
			image = fmt.Sprintf("harbor-server.jianggan.cn/%s/%s:%s", project, repo, tag)
			return image, tag
		}
	}
	return image, tag
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
