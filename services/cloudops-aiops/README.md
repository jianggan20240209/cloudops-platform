# cloudops-aiops

Week 18–22：本地 RAG 问答、MCP 风格只读工具调用、诊断草稿、修复建议（不执行）、备份任务展示、复盘草稿。

## 接口

| Path | 说明 |
|------|------|
| `POST /api/v1/aiops/ask` | 本地知识库问答 |
| `POST /api/v1/aiops/diagnose` | 告警诊断草稿（可拉取 observe detail） |
| `GET /api/v1/aiops/tools` | 工具列表 |
| `POST /api/v1/aiops/tools/{name}/invoke` | 只读工具调用 |
| `POST /api/v1/aiops/remediation/suggest` | 修复建议（永不执行） |
| `GET /api/v1/aiops/backup/jobs` | 备份任务（fixture + 计划） |
| `POST /api/v1/aiops/retro/draft` | 复盘 Markdown 草稿 |

## 环境变量

| 变量 | 默认 |
|------|------|
| `HTTP_ADDR` | `:8080` |
| `OBSERVE_BASE_URL` | `http://cloudops-observe.cloudops-dev.svc.cluster.local` |
| `CICD_BASE_URL` | `http://cloudops-cicd.cloudops-dev.svc.cluster.local` |
