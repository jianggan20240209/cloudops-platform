package main

import (
	"testing"
)

func TestBuildLogsQL(t *testing.T) {
	q, err := buildLogsQL("cloudops-dev", "cloudops-gateway-abc", "cloudops-gateway", "", "day46-trace-001")
	if err != nil {
		t.Fatal(err)
	}
	want := `namespace:cloudops-dev pod:cloudops-gateway-abc container:cloudops-gateway "day46-trace-001"`
	if q != want {
		t.Fatalf("got %q want %q", q, want)
	}
}

func TestParseVictoriaLogsBodyNestedJSON(t *testing.T) {
	line := `{"_msg":"2026-09-10T00:00:00Z stdout F {\"app\":\"cloudops-gateway\",\"msg\":\"http_request\",\"trace_id\":\"day46-trace-001\",\"request_id\":\"day46-trace-001\"}","_time":"2026-09-10T00:00:00Z","namespace":"cloudops-dev","pod":"cloudops-gateway-1","container":"cloudops-gateway","cluster":"lab-k8s"}`
	items, raw, err := parseVictoriaLogsBody([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if raw != 1 || len(items) != 1 {
		t.Fatalf("raw=%d len=%d", raw, len(items))
	}
	if items[0]["trace_id"] != "day46-trace-001" {
		t.Fatalf("trace_id=%v", items[0]["trace_id"])
	}
	if items[0]["namespace"] != "cloudops-dev" {
		t.Fatalf("namespace=%v", items[0]["namespace"])
	}
}

func TestValidateSelectorRejectsInjection(t *testing.T) {
	if err := validateSelector("namespace", `cloudops-dev" OR foo`); err == nil {
		t.Fatal("expected error")
	}
}
