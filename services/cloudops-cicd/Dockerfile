ARG GO_BUILD_IMAGE=harbor-server.jianggan.cn/base/golang:1.23-alpine
FROM ${GO_BUILD_IMAGE} AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ARG http_proxy
ARG https_proxy
ARG no_proxy

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
    -o /out/cloudops-cicd .

FROM scratch

LABEL org.opencontainers.image.title="cloudops-cicd"
LABEL org.opencontainers.image.description="CloudOps AI Platform minimal CI/CD service"

COPY --from=builder /out/cloudops-cicd /cloudops-cicd

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/cloudops-cicd"]
