.PHONY: run build test clean docker-up docker-down

# 运行
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

# Docker 启动依赖
docker-up:
	docker-compose -f infra/docker-compose.yml up -d

# Docker 停止依赖
docker-down:
	docker-compose -f infra/docker-compose.yml down

# 初始化数据库
init-db:
	cd backend && go run cmd/migrate/main.go