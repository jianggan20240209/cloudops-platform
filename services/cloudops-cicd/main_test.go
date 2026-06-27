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
