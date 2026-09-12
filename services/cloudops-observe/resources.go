package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

type resourceWasteItem struct {
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod,omitempty"`
	Container string  `json:"container,omitempty"`
	Resource  string  `json:"resource"`
	Request   float64 `json:"request"`
	Usage     float64 `json:"usage"`
	Waste     float64 `json:"waste"`
	WastePct  float64 `json:"waste_pct"`
	Reason    string  `json:"reason"`
}

func resourcesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	ns := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if ns == "" {
		ns = "cloudops-dev"
	}
	if err := validateSelector("namespace", ns); err != nil {
		writeJSON(w, http.StatusBadRequest, envelope{"error": "invalid_namespace", "message": err.Error()})
		return
	}

	prom := newPrometheusClientFromEnv()
	cpuReqQ := `sum by (namespace,pod,container) (kube_pod_container_resource_requests{resource="cpu",namespace="` + ns + `"})`
	memReqQ := `sum by (namespace,pod,container) (kube_pod_container_resource_requests{resource="memory",namespace="` + ns + `"})`
	cpuUseQ := `sum by (namespace,pod,container) (rate(container_cpu_usage_seconds_total{namespace="` + ns + `",container!="",container!="POD"}[5m]))`
	memUseQ := `sum by (namespace,pod,container) (container_memory_working_set_bytes{namespace="` + ns + `",container!="",container!="POD"})`

	cpuReq, err1 := prom.Query(r.Context(), cpuReqQ)
	memReq, err2 := prom.Query(r.Context(), memReqQ)
	cpuUse, err3 := prom.Query(r.Context(), cpuUseQ)
	memUse, err4 := prom.Query(r.Context(), memUseQ)

	warnings := []string{}
	if err1 != nil {
		warnings = append(warnings, "cpu_requests: "+err1.Error())
	}
	if err2 != nil {
		warnings = append(warnings, "memory_requests: "+err2.Error())
	}
	if err3 != nil {
		warnings = append(warnings, "cpu_usage: "+err3.Error())
	}
	if err4 != nil {
		warnings = append(warnings, "memory_usage: "+err4.Error())
	}

	cpuItems := wasteList("cpu", parseVector(cpuReq), parseVector(cpuUse), 0.3)
	memItems := wasteList("memory", parseVector(memReq), parseVector(memUse), 0.3)
	items := append(cpuItems, memItems...)

	writeJSON(w, http.StatusOK, envelope{
		"service":   appName,
		"namespace": ns,
		"source":    prom.server,
		"count":     len(items),
		"items":     items,
		"warnings":  warnings,
		"note":      "waste = request - usage when usage < 30% of request (FinOps heuristic)",
	})
}

func parseVector(payload map[string]any) map[string]float64 {
	out := map[string]float64{}
	if payload == nil {
		return out
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return out
	}
	result, _ := data["result"].([]any)
	for _, row := range result {
		m, ok := row.(map[string]any)
		if !ok {
			continue
		}
		metric, _ := m["metric"].(map[string]any)
		ns, _ := metric["namespace"].(string)
		pod, _ := metric["pod"].(string)
		container, _ := metric["container"].(string)
		key := ns + "|" + pod + "|" + container
		value := 0.0
		switch v := m["value"].(type) {
		case []any:
			if len(v) >= 2 {
				switch t := v[1].(type) {
				case string:
					value, _ = strconv.ParseFloat(t, 64)
				case float64:
					value = t
				}
			}
		}
		out[key] = value
	}
	return out
}

func wasteList(resource string, requests, usage map[string]float64, threshold float64) []resourceWasteItem {
	items := make([]resourceWasteItem, 0)
	for key, req := range requests {
		if req <= 0 {
			continue
		}
		use := usage[key]
		ratio := use / req
		if ratio >= threshold {
			continue
		}
		parts := strings.Split(key, "|")
		ns, pod, container := "", "", ""
		if len(parts) > 0 {
			ns = parts[0]
		}
		if len(parts) > 1 {
			pod = parts[1]
		}
		if len(parts) > 2 {
			container = parts[2]
		}
		waste := req - use
		if waste < 0 {
			waste = 0
		}
		items = append(items, resourceWasteItem{
			Namespace: ns,
			Pod:       pod,
			Container: container,
			Resource:  resource,
			Request:   req,
			Usage:     use,
			Waste:     waste,
			WastePct:  (1 - ratio) * 100,
			Reason:    "usage_below_30pct_of_request",
		})
	}
	return items
}

// silence unused import if context only used indirectly
var _ = context.Background
