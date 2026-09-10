# cloudops-observe

Day 47：对接 VictoriaLogs LogsQL，为 CloudOps 提供按 Pod / trace_id 查询日志的 API。

## Endpoints

| Path | 说明 |
|------|------|
| `GET /healthz` / `GET /readyz` | 探针 |
| `GET /api/v1/version` | 版本 |
| `GET /api/v1/observe/logs` | LogsQL 查询（namespace/pod/container/q/trace_id/limit） |
| `GET /metrics` | Prometheus 文本指标 |

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `HTTP_ADDR` | `:8080` | 监听地址 |
| `VICTORIA_LOGS_URL` | `http://vls-victoria-logs-single-server.logging.svc.cluster.local:9428` | VictoriaLogs 基址 |

## 本地联调示例

```bash
export VICTORIA_LOGS_URL=http://127.0.0.1:9428
go run .

curl -sS 'http://127.0.0.1:8080/api/v1/observe/logs?namespace=cloudops-dev&pod=cloudops-gateway&limit=5'
curl -sS 'http://127.0.0.1:8080/api/v1/observe/logs?namespace=cloudops-dev&trace_id=day46-trace-001&limit=5'
```
