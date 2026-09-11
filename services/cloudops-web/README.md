# cloudops-web

CloudOps AI 智能运维平台前端（静态页）。

## Day 47 · 日志查询

首页提供 **Pod 日志查询** 面板，调用：

`GET /api/v1/observe/logs?namespace=&pod=&container=&q=&trace_id=&limit=`

深链示例：

`https://cloudops.jianggan.cn/?namespace=cloudops-dev&pod=cloudops-gateway-*&container=cloudops-gateway&trace_id=day46-trace-001&auto=1`

## Day 55 · 观测跳转

页面顶部增加观测入口：

| 入口 | 行为 |
|------|------|
| Grafana | `https://grafana.jianggan.cn` |
| Tempo（Explore） | Grafana Explore，datasource uid=`tempo`；带当前 `trace_id` |
| Hubble UI | `http://127.0.0.1:12000`（需本机 `kubectl port-forward`） |

日志结果中带 `trace_id` 的行可点 **→ Tempo**。

Hubble port-forward：

```bash
kubectl -n kube-system port-forward svc/hubble-ui 12000:80
```
