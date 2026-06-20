# cloudops-web

`cloudops-web` 是 CloudOps AI 智能运维平台的最小前端服务。

当前阶段用于验证：

- Jenkins 从 GitHub 拉取 `cloudops-platform` 仓库
- Kaniko 在 Kubernetes Agent Pod 中构建镜像
- 镜像推送到 Harbor
- 后续由 Argo CD / GitOps 部署到 Kubernetes

## 本地目录

```text
services/cloudops-web
├── Dockerfile
├── README.md
└── index.html
```

## 镜像名称

```text
harbor-server.jianggan.cn/cloudops/cloudops-web:<tag>
```
