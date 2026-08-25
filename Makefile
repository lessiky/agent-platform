.PHONY: run build test clean up down docker-up docker-down

# 运行 (本地开发)
run:
	cd backend && go run ./cmd/server

# 构建
build:
	cd backend && go build -o bin/server ./cmd/server

# 测试
test:
	cd backend && go test ./...

# 清理
clean:
	cd backend && rm -rf bin/

# 一键启动全部容器 (postgres/backend/frontend)
up:
	docker compose -f infra/docker-compose.yml up -d --build

# 停止全部容器
down:
	docker compose -f infra/docker-compose.yml down

# 仅启动依赖 (本地开发用, 后端/前端在宿主机运行)
docker-up:
	docker compose -f infra/docker-compose.yml up -d postgres

# 仅停止依赖
docker-down:
	docker compose -f infra/docker-compose.yml down postgres
