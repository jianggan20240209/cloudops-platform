package main

import "testing"

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
