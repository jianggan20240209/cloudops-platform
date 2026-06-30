ARG NGINX_IMAGE=harbor-server.jianggan.cn/base/nginx:1.27-alpine
FROM ${NGINX_IMAGE}

LABEL org.opencontainers.image.title="cloudops-web"
LABEL org.opencontainers.image.description="CloudOps AI Platform minimal web service"

COPY index.html /usr/share/nginx/html/index.html
