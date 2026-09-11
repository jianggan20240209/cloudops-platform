# cloudops-gateway

`cloudops-gateway` 是 CloudOps AI 智能运维平台的最小后端入口服务。

当前阶段用于验证：

- Go 后端服务基础骨架
- Jenkins + Kaniko 构建后端镜像
- Harbor 镜像推送
- Argo CD / GitOps 部署到 `cloudops-dev`
- Ingress `/api` 路由到后端服务
- Prometheus 通过 ServiceMonitor 抓取 `/metrics`
- 结构化 JSON 访问日志（`trace_id` / `request_id`，对齐 Day 45 / ADR-002）
- 少量 OTel SDK（`otelhttp` → Collector → Tempo；Resource 含 `service.version` / `deployment.id`，Day 52）

## 本地目录

```text
services/cloudops-gateway
├── Dockerfile
├── README.md
├── go.mod
├── go.sum
├── main.go
└── otel.go
```

## HTTP 接口

| Path | 说明 |
|---|---|
| `/healthz` | 存活检查 |
| `/readyz` | 就绪检查 |
| `/api/healthz` | Ingress `/api` 前缀下的存活检查 |
| `/api/readyz` | Ingress `/api` 前缀下的就绪检查 |
| `/api/v1/version` | 服务版本信息 |
| `/metrics` | Prometheus 指标 |

## 日志约定（Day 45）

- 访问日志一行一条 JSON，字段含：`level`、`msg`、`app`、`trace_id`、`request_id`、`method`、`path`、`status`、`duration_ms`
- 优先透传请求头：`X-Request-Id`、`X-Trace-Id`、`traceparent`；缺失则服务端生成
- 响应回写：`X-Request-Id`、`X-Trace-Id`
- `/healthz`、`/readyz`、`/metrics` 不打访问日志（减探针噪声）

## 镜像名称

```text
harbor-server.jianggan.cn/cloudops/cloudops-gateway:<tag>
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | （空=关闭） | OTLP HTTP，如 `http://otel-collector.tracing.svc.cluster.local:4318` |
| `OTEL_SERVICE_NAME` | `cloudops-gateway` | `service.name` |
| `SERVICE_VERSION` | ldflags `version` | `service.version` |
| `DEPLOYMENT_ENVIRONMENT` | `dev` | `deployment.environment` |
| `DEPLOYMENT_ID` | `unknown` | `deployment.id`（集群内为 Pod UID） |

## 本地运行

```bash
go run .
```

验证：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/api/healthz
curl http://127.0.0.1:8080/api/readyz
curl http://127.0.0.1:8080/api/v1/version
curl http://127.0.0.1:8080/metrics
```
