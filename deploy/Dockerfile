# 多阶段构建：前端 → 后端 → 精简运行镜像。支持 amd64 / arm64（常见 NAS 架构）。
# 在仓库根目录执行：docker build -f deploy/Dockerfile -t cc-lemon .

FROM node:22-alpine AS web
# 默认走官方源（GitHub Actions 构建用）；国内服务器在 compose 里覆盖为 npmmirror
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm config set registry "$NPM_REGISTRY" && npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS server
# 同理：默认官方代理，国内部署时用 compose 传入 goproxy.cn
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lemon ./cmd/lemon

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 lemon
WORKDIR /app
COPY --from=server /out/lemon /app/lemon
COPY --from=web /src/web/dist /app/web
ENV LEMON_SERVER_ADDR=:8080 \
    LEMON_SERVER_STATIC_DIR=/app/web
USER lemon
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/lemon"]
