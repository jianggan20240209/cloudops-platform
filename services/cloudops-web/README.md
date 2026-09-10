# cloudops-web

CloudOps AI 智能运维平台前端（静态页）。

## Day 47

首页提供 **Pod 日志查询** 面板，调用：

`GET /api/v1/observe/logs?namespace=&pod=&container=&q=&trace_id=&limit=`

深链示例：

`https://cloudops.jianggan.cn/?namespace=cloudops-dev&pod=cloudops-gateway-*&container=cloudops-gateway&trace_id=day46-trace-001&auto=1`
