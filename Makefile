# 开发常用命令。数据库连接用环境变量 LEMON_DATABASE_DSN / LEMON_TEST_DSN 指定。
.PHONY: dev-server dev-web build test lint demo-data

dev-server:        ## 启动后端（:8080）
	go run ./cmd/lemon

dev-web:           ## 启动前端开发服务器（/api 代理到后端）
	cd web && npm install && npm run dev

build:             ## 构建前后端到 bin/ 与 web/dist/
	cd web && npm ci && npm run build
	CGO_ENABLED=0 go build -trimpath -o bin/lemon ./cmd/lemon

test:              ## 后端测试（端到端测试需 LEMON_TEST_DSN 指向可清空的测试库）
	go test ./...

lint:
	go vet ./...
	cd web && npx vue-tsc --noEmit

demo-data:         ## 向正在运行的服务写入演示账套（LEMON_URL 默认 http://localhost:8080，登录 admin / lemon-demo）
	node scripts/seed-demo.mjs
