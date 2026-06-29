# cloudops-cicd

`cloudops-cicd` 是 CloudOps AI 智能运维平台的 CI/CD 发布中心服务。

第一版提供发布中心 API，用于表达当前已经跑通的发布对象模型：

- Jenkins 构建
- Harbor 镜像 tag
- Argo CD Application
- Helm imageTag
- 发布健康状态

当前版本会优先从 Argo CD API 读取实时 Application 状态，可从 Harbor API 查询镜像 tag 列表，并可从 Prometheus API 查询基础运行指标；如果没有配置对应依赖或调用失败，会回退到静态示例数据。后续再逐步接入 Jenkins API。

## 本地目录

```text
services/cloudops-cicd
├── Dockerfile
├── README.md
├── go.mod
└── main.go
```

## HTTP 接口

| Path | 说明 |
|---|---|
| `/healthz` | 存活检查 |
| `/readyz` | 就绪检查 |
| `/api/healthz` | Ingress `/api` 前缀下的存活检查 |
| `/api/readyz` | Ingress `/api` 前缀下的就绪检查 |
| `/api/v1/version` | 服务版本信息 |
| `/api/v1/cicd/apps` | 应用发布列表 |
| `/api/v1/cicd/apps/{name}` | 应用发布详情 |
| `/api/v1/cicd/apps/{name}/status` | 应用当前发布状态 |
| `/api/v1/cicd/apps/{name}/releases` | 应用发布历史 |
| `/api/v1/cicd/apps/{name}/images` | 应用 Harbor 镜像 tag 列表 |
| `/api/v1/cicd/apps/{name}/metrics` | 应用 Prometheus 基础运行指标 |
| `/api/v1/cicd/apps/{name}/release` | 聚合发布详情，合并 Argo CD、Harbor、Prometheus 数据 |
| `/api/v1/cicd/apps/{name}/health` | 发布健康判断，只返回健康结论和检查项 |
| `/api/v1/cicd/apps/{name}/verify` | 发布后验证结果，只返回镜像、tag、检查项和验证结论 |
| `/api/v1/cicd/apps/{name}/records` | 发布批次记录列表 |
| `/api/v1/cicd/apps/{name}/records/latest` | 最新发布批次记录 |
| `/api/v1/cicd/apps/{name}/records/{id}` | 指定发布批次记录 |
| `POST /api/v1/cicd/apps/{name}/records/snapshot` | 保存当前应用聚合结果为发布记录快照 |
| `POST /api/v1/cicd/releases/records` | 写入发布批次记录 |
| `/api/v1/cicd/apps/{name}/rollback-candidates` | 查询可回滚候选版本 |
| `/api/v1/cicd/apps/{name}/rollout` | 查询应用对应 Argo Rollout 状态 |
| `/api/v1/cicd/apps/{name}/analysisruns` | 查询应用对应 AnalysisRun 列表 |
| `/api/v1/cicd/apps/{name}/traffic` | 查询应用对应 Istio VirtualService / DestinationRule 摘要 |
| `/api/v1/cicd/apps/{name}/observability` | 关联灰度阶段与 Istio 指标，用于故障复盘 |
| `/metrics` | Prometheus 指标 |

## 镜像名称

```text
harbor-server.jianggan.cn/cloudops/cloudops-cicd:<tag>
```

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `ARGOCD_SERVER` | 空 | Argo CD API 地址，例如 `https://argocd.jianggan.cn` |
| `ARGOCD_AUTH_TOKEN` | 空 | Argo CD API Token |
| `ARGOCD_INSECURE` | `true` | 是否跳过 Argo CD HTTPS 证书校验 |
| `HARBOR_SERVER` | 空 | Harbor API 地址，例如 `https://harbor-server.jianggan.cn` |
| `HARBOR_USERNAME` | 空 | Harbor 用户名或 Robot 账号 |
| `HARBOR_PASSWORD` | 空 | Harbor 密码或 Robot Token |
| `HARBOR_INSECURE` | `true` | 是否跳过 Harbor HTTPS 证书校验 |
| `PROMETHEUS_SERVER` | 空 | Prometheus API 地址，例如 `http://kube-prometheus-stack-prometheus.monitoring.svc:9090` |
| `RELEASE_RECORD_DATABASE_URL` | 空 | PostgreSQL DSN，未配置时使用内存存储 |
| `POSTGRES_DSN` | 空 | PostgreSQL DSN 兼容变量，优先级低于 `RELEASE_RECORD_DATABASE_URL` |
| `RELEASE_RECORD_WRITE_TOKEN` | 空 | 发布记录写入 token；为空时不校验写入 token |
| `KUBERNETES_API_SERVER` | 空 | Kubernetes API 地址；为空时自动使用 Pod 内 `KUBERNETES_SERVICE_HOST` |

未设置 `ARGOCD_SERVER` 或 `ARGOCD_AUTH_TOKEN` 时，接口会返回静态数据，并在响应中标记：

```json
{
  "source": "static"
}
```

成功读取 Argo CD 时，响应中会标记：

```json
{
  "source": "argocd"
}
```

## 发布详情聚合

`/api/v1/cicd/apps/{name}/release` 会聚合三类数据：

- Argo CD：应用同步状态、健康状态、当前镜像、Git revision。
- Harbor：镜像 tag 列表，并检查当前运行 tag 是否存在。
- Prometheus：优先查询 `up{job="<app-name>"}`；无 target 时回退查询同命名空间下 `service="<app-name>"` 和 `service=~"<app-name>-(stable|canary)"`，判断服务监控目标是否全部存活。

接口会生成统一的 `checks` 数组，`status` 取值为 `pass`、`warn`、`fail`。只有存在 `fail` 时，`ready` 才会返回 `false`。当 Harbor 或 Prometheus 未配置时，接口会降级为静态数据并在 `warnings` 中提示，便于本地开发和依赖异常时继续展示基础发布状态。

## 发布批次记录

`/api/v1/cicd/apps/{name}/records` 会把一次发布固化为 `ReleaseRecord` 视图，核心字段包括：

- Jenkins：`jenkins_job`、`jenkins_build`。
- 镜像：`image`、`image_tag`、`image_digest`。
- Argo CD：`argocd_app`、`argocd_revision`、`argocd_sync`、`argocd_health`。
- 验证结果：`verification.ready`、`verification.checks`、`verification.metrics`、`verification.verified_at`。

当前版本支持两类发布记录：

- 实时记录：由 Argo CD、Harbor、Prometheus 聚合结果生成。
- 写入记录：由 `POST /api/v1/cicd/releases/records` 写入，未配置数据库时保存在内存中，配置 PostgreSQL DSN 后持久化到 `release_records` 表。

内存存储只适合本地开发或单副本实验。多副本部署时，每个 Pod 都有独立内存，Jenkins 写入的记录只会保存在收到请求的那个 Pod 上；后续 GET 请求如果被 Service 转发到其他 Pod，可能看不到同一条写入记录。需要可靠发布审计、回滚候选和多副本一致查询时，必须启用 PostgreSQL。

GitOps Helm chart 已支持为 `cloudops-cicd` 启用内置 PostgreSQL StatefulSet。启用后，服务会通过 `RELEASE_RECORD_DATABASE_URL` 连接数据库，并自动创建 `release_records` 表。只要配置了 PostgreSQL DSN，服务就不会静默回退到内存存储；如果数据库不可用，服务会重试连接并失败退出，避免发布审计产生假一致性。

Jenkins 可以在镜像构建、Argo CD 同步、健康检查完成后写入一条真实发布记录。回滚候选接口会从发布记录中筛选 `status=succeeded` 且 `verification.ready=true` 的历史版本，并排除当前运行 tag。

`POST /api/v1/cicd/apps/{name}/records/snapshot` 会读取当前应用的 Argo CD、Harbor、Prometheus、Rollout、AnalysisRun、Istio traffic 和 observability 聚合结果，生成带时间戳的快照记录并写入 Release Record 存储。快照不会覆盖同一个 imageTag 的基础记录，适合在灰度完成、失败或人工检查后沉淀阶段结果。

## Rollout 状态

服务运行在 Kubernetes 内时，会通过 ServiceAccount token 读取同命名空间的 `Rollout` 和 `AnalysisRun` 资源。Helm chart 会为 `cloudops-cicd` 创建最小 RBAC：

- `argoproj.io/rollouts`: `get/list/watch`
- `argoproj.io/analysisruns`: `get/list/watch`
- `networking.istio.io/virtualservices`: `get/list/watch`
- `networking.istio.io/destinationrules`: `get/list/watch`

`/release` 和 `/records` 会在应用存在同名 Rollout 时附带 `rollout` 摘要，并将 Rollout 健康状态加入检查项。

`/traffic` 会返回同名 `VirtualService` 的 hosts、gateways、route 权重、timeout/retry 摘要，以及匹配当前应用的 `DestinationRule` connection pool / outlier detection 摘要。

`/observability` 会关联 Rollout 灰度阶段（phase、step、stable/canary 权重）与 Istio Prometheus 指标（请求速率、5xx 速率、P95 延迟、按 destination 拆分）。Istio 指标查询会依次尝试 `destination_service_name`、`destination_service`（FQDN）、`destination_workload` 和 ingress gateway 出站标签；响应中的 `matched_selector` 表示命中的查询策略。

## 本地运行

```bash
go run .
```

验证：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/api/v1/version
curl http://127.0.0.1:8080/api/v1/cicd/apps
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/status
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/releases
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/images
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/metrics
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/release
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/health
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/verify
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/records
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/records/latest
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/records/dev-cloudops-gateway-main-14
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway/rollback-candidates
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway-rollout/release
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway-rollout/rollout
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway-rollout/traffic
curl http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway-rollout/observability
curl -X POST http://127.0.0.1:8080/api/v1/cicd/apps/cloudops-gateway-rollout/records/snapshot
curl http://127.0.0.1:8080/api/v1/cicd/apps/rollouts-demo-istio/rollout
curl http://127.0.0.1:8080/api/v1/cicd/apps/rollouts-demo-istio/analysisruns
curl -X POST http://127.0.0.1:8080/api/v1/cicd/releases/records \
  -H 'Content-Type: application/json' \
  --data '{"app_name":"cloudops-gateway","env":"dev","namespace":"cloudops-dev","image":"harbor-server.jianggan.cn/cloudops/cloudops-gateway:main-15","image_tag":"main-15","argocd_app":"cloudops-gateway-dev","argocd_sync":"Synced","argocd_health":"Healthy","status":"succeeded","verification":{"ready":true}}'
curl http://127.0.0.1:8080/metrics
```
