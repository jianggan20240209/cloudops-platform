# cloudops-cicd

`cloudops-cicd` 是 CloudOps AI 智能运维平台的 CI/CD 发布中心服务。

第一版先提供静态 API，用于表达当前已经跑通的发布对象模型：

- Jenkins 构建
- Harbor 镜像 tag
- Argo CD Application
- Helm imageTag
- 发布健康状态

后续再逐步接入真实的 Jenkins API、Argo CD API、Harbor API 和 Prometheus API。

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
| `/metrics` | Prometheus 指标 |

## 镜像名称

```text
harbor-server.jianggan.cn/cloudops/cloudops-cicd:<tag>
```

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
curl http://127.0.0.1:8080/metrics
```
