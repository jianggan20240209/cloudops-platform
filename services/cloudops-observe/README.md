# cloudops-observe

Day 47：VictoriaLogs LogsQL。  
Day 59–60：Alertmanager 告警列表与详情聚合。

## Endpoints

| Path | 说明 |
|------|------|
| `GET /healthz` / `GET /readyz` | 探针 |
| `GET /api/v1/version` | 版本 |
| `GET /api/v1/observe/logs` | LogsQL |
| `GET /api/v1/observe/alerts` | Alertmanager `/api/v2/alerts` |
| `GET /api/v1/observe/alerts/detail` | 告警 + metrics + logs + runbook |
| `GET /metrics` | Prometheus 文本指标 |

## 环境变量

| 变量 | 默认 |
|------|------|
| `HTTP_ADDR` | `:8080` |
| `VICTORIA_LOGS_URL` | VictoriaLogs |
| `ALERTMANAGER_URL` | `http://kube-prometheus-stack-alertmanager.monitoring.svc:9093` |
| `PROMETHEUS_SERVER` | `http://kube-prometheus-stack-prometheus.monitoring.svc:9090` |
