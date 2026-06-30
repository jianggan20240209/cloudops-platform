ARG GO_BUILD_IMAGE=harbor-server.jianggan.cn/base/golang:1.23-alpine
FROM ${GO_BUILD_IMAGE} AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src

COPY go.mod ./
COPY main.go ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o /out/cloudops-gateway .

FROM scratch

LABEL org.opencontainers.image.title="cloudops-gateway"
LABEL org.opencontainers.image.description="CloudOps AI Platform minimal API gateway"

COPY --from=builder /out/cloudops-gateway /cloudops-gateway

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/cloudops-gateway"]
