package service

import (
	"context"
	"log"
	"sync"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
)

// ModelHealthChecker 模型连通性定时检测 (PRD 2.3.2 P0)
//
// 周期性对所有非停用模型执行连通性探测, 更新状态/延迟并记录历史。
// 手动停用 (inactive) 的模板不自动探测。
type ModelHealthChecker struct {
	svc       ModelTemplateService
	templates repository.ModelTemplateRepository
	interval  time.Duration

	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	started bool
}

func NewModelHealthChecker(svc ModelTemplateService, templates repository.ModelTemplateRepository, interval time.Duration) *ModelHealthChecker {
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	return &ModelHealthChecker{
		svc:       svc,
		templates: templates,
		interval:  interval,
	}
}

// Start 启动定时检测 (幂等)
func (h *ModelHealthChecker) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return
	}
	h.stopCh = make(chan struct{})
	h.doneCh = make(chan struct{})
	h.started = true
	go h.loop()
	log.Printf("model health checker started (interval=%s)", h.interval)
}

// Stop 停止定时检测 (服务退出时调用)
func (h *ModelHealthChecker) Stop() {
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
		log.Println("model health checker did not stop in time")
	}
}

func (h *ModelHealthChecker) loop() {
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

// checkAll 检测所有非停用模板 (逐个执行, 单个失败不影响其他)
func (h *ModelHealthChecker) checkAll() {
	templates, err := h.templates.ListForRoute(context.Background())
	if err != nil {
		log.Printf("model health check: list templates failed: %v", err)
		return
	}
	for i := range templates {
		t := templates[i]
		if t.Status == model.ModelStatusInactive {
			continue
		}
		h.svc.CheckHealth(context.Background(), &t)
	}
}
