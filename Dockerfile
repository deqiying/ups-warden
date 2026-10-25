# syntax=docker/dockerfile:1
# 多阶段构建：前端产物嵌入后端，运行镜像只含纯 Go 静态二进制。
# 版本号经 --build-arg VERSION=... 注入，与 mise run backend:release 一致。
# 工具版本与根目录 mise.toml 保持一致，升级时同步修改。

FROM --platform=$BUILDPLATFORM node:24.21.0-slim AS webui
RUN npm install -g pnpm@12.11.2
WORKDIR /src
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml frontend/
RUN pnpm --dir frontend install --frozen-lockfile
COPY frontend frontend
COPY scripts scripts
COPY backend/internal/webui/dist backend/internal/webui/dist
RUN pnpm --dir frontend build && node scripts/build-webui.mjs

FROM --platform=$BUILDPLATFORM golang:1.27.2 AS backend
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend .
COPY --from=webui /src/backend/internal/webui/dist internal/webui/dist
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/ups-warden ./cmd/ups-warden

FROM alpine:3
# ca-certificates 供 HTTPS Webhook / Telegram 与 HTTP 动作使用。
RUN apk add --no-cache ca-certificates \
    && adduser -D -H -u 10001 ups-warden \
    && mkdir /data && chown ups-warden:ups-warden /data && chmod 0700 /data
COPY --from=backend /out/ups-warden /usr/local/bin/ups-warden
USER ups-warden
ENV UPS_WARDEN_LISTEN=0.0.0.0:8080 UPS_WARDEN_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ups-warden"]
