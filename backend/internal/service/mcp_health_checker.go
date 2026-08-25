package service

import (
	"context"
	"log"
	"sync"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
)

// MCPHealthChecker MCP 连通性定时检测 (PRD 2.2.2 P0)
//
// 周期性对所有非 stdio MCP 服务器执行连通性检测, 更新状态/延迟并记录历史,
// 健康监控看板 (前端) 基于 mcp_health_logs 展示。
type MCPHealthChecker struct {
	svc      MCPServerService
	servers  repository.MCPServerRepository
	interval time.Duration

	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	started bool
}

func NewMCPHealthChecker(svc MCPServerService, servers repository.MCPServerRepository, interval time.Duration) *MCPHealthChecker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &MCPHealthChecker{
		svc:      svc,
		servers:  servers,
		interval: interval,
	}
}

// Start 启动定时检测 (幂等)
func (h *MCPHealthChecker) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return
	}
	h.stopCh = make(chan struct{})
	h.doneCh = make(chan struct{})
	h.started = true
	go h.loop()
	log.Printf("mcp health checker started (interval=%s)", h.interval)
}

// Stop 停止定时检测 (服务退出时调用)
func (h *MCPHealthChecker) Stop() {
	h.mu.Lock()
	if !h.started {
		h.mu.Unlock()
		return
	}
	close(h.stopCh)
	done := h.doneCh
	h.started = false
	h.mu.Unlock()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Println("mcp health checker did not stop in time")
	}
}

func (h *MCPHealthChecker) loop() {
	defer close(h.doneCh)
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.checkAll()
		}
	}
}

// checkAll 检测所有非 stdio 服务器 (逐个执行, 单个失败不影响其他)
func (h *MCPHealthChecker) checkAll() {
	servers, err := h.servers.ListAll(context.Background())
	if err != nil {
		log.Printf("mcp health check: list servers failed: %v", err)
		return
	}
	for i := range servers {
		server := servers[i]
		if server.Transport == model.MCPTransportStdio {
			continue
		}
		h.svc.CheckHealth(context.Background(), &server)
	}
}
