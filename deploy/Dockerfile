# 多阶段构建：前端 → 后端 → 精简运行镜像。支持 amd64 / arm64（常见 NAS 架构）。
# 在仓库根目录执行：docker build -f deploy/Dockerfile -t cc-lemon .

FROM node:22-alpine AS web
# 国内服务器构建时默认走阿里云 npm 镜像；需要官方源可传 --build-arg NPM_REGISTRY=https://registry.npmjs.org
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm config set registry "$NPM_REGISTRY" && npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS server
# 同理：默认走 goproxy.cn，避免国内构建时拉不到依赖
ARG GOPROXY=https://goproxy.cn,direct
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
