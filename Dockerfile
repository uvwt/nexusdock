FROM node:26-alpine AS web-builder
# 前端契约类型由仓库内 Python OpenAPI 源生成；Python 仅存在于构建阶段，不进入最终镜像。
RUN apk add --no-cache python3
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web ./
COPY scripts/generate-contracts.py /src/scripts/generate-contracts.py
COPY internal/httpx/web_dist ../internal/httpx/web_dist
RUN npm run build

FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/httpx/web_dist ./internal/httpx/web_dist
# 构建参数缺省值与 internal/buildinfo 的缺省值一致，本地直接 docker build 不受影响；
# CI 或发布流程注入这三个参数后，镜像内二进制与 OCI labels 保持同一版本信息。
ARG NEXUS_VERSION=dev
ARG NEXUS_REVISION=unknown
ARG NEXUS_SOURCE=unknown
RUN go build -trimpath \
    -ldflags "-X github.com/uvwt/nexusdock/internal/buildinfo.Version=${NEXUS_VERSION} -X github.com/uvwt/nexusdock/internal/buildinfo.Revision=${NEXUS_REVISION} -X github.com/uvwt/nexusdock/internal/buildinfo.Source=${NEXUS_SOURCE}" \
    -o /out/nexusdock ./cmd/nexusdock

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates \
    && addgroup -S -g 10001 nexus \
    && adduser -S -D -H -u 10001 -G nexus nexus \
    && mkdir -p /var/lib/nexus /recall \
    && chown -R 10001:10001 /var/lib/nexus /recall
WORKDIR /app
COPY --from=go-builder /out/nexusdock /usr/local/bin/nexusdock
ENV NEXUS_HOST=0.0.0.0 \
    NEXUS_PORT=18777 \
    NEXUS_DATA_DIR=/var/lib/nexus \
    RECALL_REPO_DIR=/recall \
    HOME=/tmp
EXPOSE 18777
VOLUME ["/var/lib/nexus", "/recall"]
# OCI 标准镜像标签：ARG 在每个 stage 独立作用域，最终阶段需要重新声明才能用于 LABEL。
ARG NEXUS_VERSION=dev
ARG NEXUS_REVISION=unknown
ARG NEXUS_SOURCE=unknown
LABEL org.opencontainers.image.version=${NEXUS_VERSION} \
      org.opencontainers.image.revision=${NEXUS_REVISION} \
      org.opencontainers.image.source=${NEXUS_SOURCE}
USER 10001:10001
# HEALTHCHECK 打 /ready（readiness）：控制库可查询且 Recall 根目录可访问才算健康；
# /health 保持为极轻量 liveness，不做依赖检查，不能反映数据面是否可用。
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -q -T 2 -O /dev/null http://127.0.0.1:18777/ready || exit 1
ENTRYPOINT ["nexusdock"]
