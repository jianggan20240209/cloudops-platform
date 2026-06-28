package main

import (
	"context"
	"testing"
)

func TestJenkinsBuildNumber(t *testing.T) {
	tests := []struct {
		tag  string
		want string
	}{
		{tag: "main-14", want: "14"},
		{tag: "feature-x", want: ""},
		{tag: "latest", want: ""},
	}

	for _, tt := range tests {
		if got := jenkinsBuildNumber(tt.tag); got != tt.want {
			t.Fatalf("jenkinsBuildNumber(%q) = %q, want %q", tt.tag, got, tt.want)
		}
	}
}

func TestImageWithTag(t *testing.T) {
	got := imageWithTag("harbor-server.jianggan.cn/cloudops/cloudops-gateway:main-14", "main-15")
	want := "harbor-server.jianggan.cn/cloudops/cloudops-gateway:main-15"
	if got != want {
		t.Fatalf("imageWithTag() = %q, want %q", got, want)
	}
}

func TestReleaseRecordID(t *testing.T) {
	got := releaseRecordID("dev", "cloudops-gateway", "main-14")
	want := "dev-cloudops-gateway-main-14"
	if got != want {
		t.Fatalf("releaseRecordID() = %q, want %q", got, want)
	}
}

func TestNormalizeReleaseRecord(t *testing.T) {
	record, err := normalizeReleaseRecord(ReleaseRecord{
		AppName:  "cloudops-gateway",
		ImageTag: "main-14",
		Verification: ReleaseVerification{
			Ready: true,
		},
	})
	if err != nil {
		t.Fatalf("normalizeReleaseRecord() error = %v", err)
	}
	if record.ID != "dev-cloudops-gateway-main-14" {
		t.Fatalf("record.ID = %q", record.ID)
	}
	if record.Status != "succeeded" {
		t.Fatalf("record.Status = %q", record.Status)
	}
	if record.JenkinsBuild != "14" {
		t.Fatalf("record.JenkinsBuild = %q", record.JenkinsBuild)
	}
	if record.Verification.VerifiedAt == "" {
		t.Fatal("record.Verification.VerifiedAt is empty")
	}
}

func TestMemoryReleaseRecordStore(t *testing.T) {
	store := NewMemoryReleaseRecordStore()
	record, err := store.Save(context.Background(), ReleaseRecord{
		AppName:  "cloudops-gateway",
		ImageTag: "main-14",
		Verification: ReleaseVerification{
			Ready: true,
		},
	})
	if err != nil {
		t.Fatalf("store.Save() error = %v", err)
	}

	got, ok, err := store.Get(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("store.Get() ok = false")
	}
	if got.ID != record.ID {
		t.Fatalf("got.ID = %q, want %q", got.ID, record.ID)
	}
}

func TestRollbackCandidates(t *testing.T) {
	app := AppSummary{Name: "cloudops-gateway", CurrentTag: "main-14"}
	records := []ReleaseRecord{
		{AppName: "cloudops-gateway", ImageTag: "main-14", Status: "succeeded", Verification: ReleaseVerification{Ready: true}},
		{AppName: "cloudops-gateway", ImageTag: "main-13", Status: "succeeded", Verification: ReleaseVerification{Ready: true}},
		{AppName: "cloudops-gateway", ImageTag: "main-12", Status: "failed", Verification: ReleaseVerification{Ready: false}},
	}

	candidates := rollbackCandidates(app, records)
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].ImageTag != "main-13" {
		t.Fatalf("candidate tag = %q", candidates[0].ImageTag)
	}
}

func TestServiceUpQuery(t *testing.T) {
	got := serviceUpQuery("cloudops-dev", "rollouts-demo-istio")
	want := `up{namespace="cloudops-dev",service="rollouts-demo-istio"} or up{namespace="cloudops-dev",service=~"rollouts-demo-istio-(stable|canary)"}`
	if got != want {
		t.Fatalf("serviceUpQuery() = %q, want %q", got, want)
	}
}

func TestReleaseRecordSnapshotID(t *testing.T) {
	got := releaseRecordSnapshotID("dev", "cloudops-gateway-rollout", "main-14", "2026-06-28T01:23:45Z")
	want := "dev-cloudops-gateway-rollout-main-14-snapshot-20260628012345"
	if got != want {
		t.Fatalf("releaseRecordSnapshotID() = %q, want %q", got, want)
	}
}

func TestBuildReleaseSnapshotStoresNotReady(t *testing.T) {
	app := AppSummary{
		Name:       "unit-test-app",
		Env:        "dev",
		Namespace:  "cloudops-dev",
		Image:      "harbor-server.jianggan.cn/cloudops/unit-test-app:main-1",
		CurrentTag: "main-1",
		Sync:       "OutOfSync",
		Health:     "Degraded",
		Source:     "static",
	}

	record := buildReleaseSnapshot(app)
	if record.Source != "snapshot" {
		t.Fatalf("record.Source = %q, want snapshot", record.Source)
	}
	if record.Status != "failed" {
		t.Fatalf("record.Status = %q, want failed", record.Status)
	}
	if record.ID == releaseRecordID(app.Env, app.Name, app.CurrentTag) {
		t.Fatalf("snapshot record ID should not overwrite base release record ID")
	}
}

func TestDestinationRuleMatchesApp(t *testing.T) {
	item := k8sDestinationRule{}
	item.Metadata.Name = "cloudops-gateway-rollout-canary"
	item.Spec.Host = "cloudops-gateway-rollout-canary"
	if !destinationRuleMatchesApp(item, "cloudops-gateway-rollout") {
		t.Fatal("destinationRuleMatchesApp() = false, want true")
	}
}

func TestVirtualServiceFromKubernetes(t *testing.T) {
	item := k8sVirtualService{}
	item.Metadata.Name = "cloudops-gateway-rollout"
	item.Metadata.Namespace = "cloudops-dev"
	item.Spec.Hosts = []string{"api.cloudops.jianggan.cn"}
	item.Spec.Gateways = []string{"cloudops-gateway-rollout"}
	item.Spec.HTTP = append(item.Spec.HTTP, struct {
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
	}{Name: "primary"})
	item.Spec.HTTP[0].Route = append(item.Spec.HTTP[0].Route, struct {
		Destination struct {
			Host string `json:"host"`
			Port struct {
				Number int `json:"number"`
			} `json:"port"`
		} `json:"destination"`
		Weight int `json:"weight"`
	}{Weight: 100})
	item.Spec.HTTP[0].Route[0].Destination.Host = "cloudops-gateway-rollout-stable"
	item.Spec.HTTP[0].Route[0].Destination.Port.Number = 80

	summary := virtualServiceFromKubernetes(item)
	if summary.HTTP[0].Routes[0].Host != "cloudops-gateway-rollout-stable" {
		t.Fatalf("route host = %q", summary.HTTP[0].Routes[0].Host)
	}
	if summary.HTTP[0].Routes[0].Weight != 100 {
		t.Fatalf("route weight = %d", summary.HTTP[0].Routes[0].Weight)
	}
}
