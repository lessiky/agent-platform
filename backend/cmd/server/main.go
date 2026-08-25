package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agent-platform/internal/api/agent"
	"agent-platform/internal/api/auth"
	"agent-platform/internal/api/mcp"
	"agent-platform/internal/api/model"
	"agent-platform/internal/api/overview"
	"agent-platform/internal/api/platform"
	"agent-platform/internal/api/rbac"
	"agent-platform/internal/api/skill"
	"agent-platform/internal/api/workflow"
	"agent-platform/internal/config"
	"agent-platform/internal/crypto"
	"agent-platform/internal/database"
	"agent-platform/internal/middleware"
	domain "agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/internal/runtime"
	"agent-platform/internal/service"
	"agent-platform/pkg/logger"

	"github.com/gin-gonic/gin"
)

func main() {
	// 1. 加载配置
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. 初始化日志
	logger.Init(cfg.Server.Mode == "release")

	// 初始化 JWT 密钥
	middleware.InitJWT(cfg.JWT.Secret)

	// 3. 连接数据库
	db, err := database.Init(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	_ = db

	// 4. 自动迁移表结构
	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	// 4.1 权限/角色种子数据 (M4.5: mcp:approve 审批权限, admin 角色)
	if err := database.SeedPermissionsAndRoles(db); err != nil {
		log.Fatalf("Failed to seed RBAC: %v", err)
	}

	// 5. 初始化 Agent 域 (runtime; service 在 5.71 创建, /invoke 需复用对话链路)
	agentRuntime := runtime.New(
		repository.NewAgentInstanceRepository(),
		repository.NewAgentLogRepository(),
		repository.NewAgentCallStatRepository(),
	)
	// 5.5 初始化 MCP 域 (M3): 凭证加密 -> 服务 -> 路由 + 健康检查
	mcpCipher, err := crypto.NewAesGCM(cfg.MCP.CredentialsKey)
	if err != nil {
		log.Fatalf("Failed to init MCP credential cipher: %v", err)
	}
	mcpService := service.NewMCPServerService(
		repository.NewMCPServerRepository(),
		repository.NewMCPHealthLogRepository(),
		repository.NewMCPAgentBindingRepository(),
		repository.NewAgentRepository(),
		repository.NewToolApprovalRepository(),
		mcpCipher,
		cfg.MCP.CheckTimeout,
	)
	// 运行时模拟流量中, 已绑定 MCP 的 Agent 会真实调用 MCP 工具 (6.6)
	agentRuntime.SetMCPInvoker(mcpService)

	// 5.51 初始化审核域 (M4.5): 审核配置 + 生命周期 + 超时扫描; MCP 服务获得审核门禁能力
	approvalService := service.NewApprovalService(
		repository.NewToolApprovalRepository(),
		repository.NewApprovalSettingsRepository(),
		repository.NewAuditLogRepository(),
		repository.NewMCPServerRepository(),
		repository.NewAgentRepository(),
		mcpService,
	)
	mcpService.SetApprovalRequester(approvalService)
	approvalTimeoutChecker := service.NewApprovalTimeoutChecker(approvalService, service.ApprovalTimeoutScanInterval)
	approvalTimeoutChecker.Start()

	mcpHealthChecker := service.NewMCPHealthChecker(
		mcpService,
		repository.NewMCPServerRepository(),
		cfg.MCP.HealthCheckInterval,
	)
	mcpHealthChecker.Start()

	// 5.6 初始化模型域 (M4): API Key 加密 -> 服务 -> 路由 + 健康检查
	modelCipher, err := crypto.NewAesGCM(cfg.Model.CredentialsKey)
	if err != nil {
		log.Fatalf("Failed to init model credential cipher: %v", err)
	}
	modelService := service.NewModelTemplateService(
		repository.NewModelTemplateRepository(),
		repository.NewModelQuotaRepository(),
		repository.NewModelUsageLogRepository(),
		repository.NewModelHealthLogRepository(),
		repository.NewAgentRepository(),
		modelCipher,
		cfg.Model.CheckTimeout,
		cfg.Model.ChatTimeout,
	)
	// 运行时模拟流量中, Agent 调用按优先级路由到模型并消费配额 (6.5)
	agentRuntime.SetModelRouter(modelService)

	modelHealthChecker := service.NewModelHealthChecker(
		modelService,
		repository.NewModelTemplateRepository(),
		cfg.Model.HealthCheckInterval,
	)
	modelHealthChecker.Start()

	// 5.70 初始化技能域 (M9): 技能包导入/管理 + Agent 关联 + 运行时注入
	skillService := service.NewSkillService(
		repository.NewSkillRepository(),
		repository.NewSkillFileRepository(),
		repository.NewSkillAgentBindingRepository(),
		repository.NewAuditLogRepository(),
		service.DefaultSkillLimits(),
	)

	// 5.7 初始化对话域 (M2.5): 对话执行链 (模型调用 + 工具调用审核联动) + 会话持久化
	chatService := service.NewChatService(
		repository.NewAgentRepository(),
		repository.NewChatSessionRepository(),
		repository.NewChatMessageRepository(),
		repository.NewAgentLogRepository(),
		mcpService,
		modelService,
		repository.NewAgentCallStatRepository(),
		skillService,
		repository.NewAgentExecutionRepository(),
		cfg.Model.ChatTimeout,
		cfg.MCP.CheckTimeout,
	)
	// 5.715 Agent 执行任务 watchdog: /invoke 异步任务区分 "执行中" 与 "卡死" (心跳/deadline/等待审核兜底)
	executionWatchdog := service.NewExecutionWatchdog(chatService, repository.NewAgentExecutionRepository(), chatService.StallThreshold(), service.ExecutionWatchdogScanInterval)
	executionWatchdog.Start()

	// 5.71 初始化 Agent 服务 (依赖对话链路: API Key /invoke 复用对话执行链返回模型应答)
	agentService := service.NewAgentService(
		repository.NewAgentRepository(),
		repository.NewAgentVersionRepository(),
		repository.NewAgentInstanceRepository(),
		repository.NewAgentLogRepository(),
		repository.NewAgentAPIKeyRepository(),
		repository.NewAgentCallStatRepository(),
		agentRuntime,
		repository.NewMCPServerRepository(),
		repository.NewMCPAgentBindingRepository(),
		repository.NewSkillAgentBindingRepository(),
		repository.NewSkillRepository(),
		repository.NewChatSessionRepository(),
		chatService,
		repository.NewToolApprovalRepository(),
		modelService,
	)

	// 服务启动时, 对账上次进程遗留的活动实例
	if err := agentService.ReconcileInstances(context.Background()); err != nil {
		log.Printf("Failed to reconcile agent instances: %v", err)
	}
	// 实例模拟流量改为 Agent 级显式开关 (M2.5: 默认关闭, Agent config simulate_traffic=true 才生成)
	trafficAgentRepo := repository.NewAgentRepository()
	agentRuntime.SetTrafficCheck(func(ctx context.Context, agentID string) bool {
		ag, err := trafficAgentRepo.GetByID(ctx, agentID)
		if err != nil {
			return false
		}
		var cfg service.AgentConfig
		if json.Unmarshal(ag.Config, &cfg) != nil {
			return false
		}
		return cfg.SimulateTraffic
	})

	// 5.8 初始化工作流域 (M5): DAG 执行引擎 + 审核挂起恢复 (M4.5 联动) + cron 调度
	workflowEngine := service.NewWorkflowEngine(
		repository.NewWorkflowRepository(),
		repository.NewWorkflowExecutionRepository(),
		repository.NewWorkflowNodeExecutionRepository(),
		repository.NewToolApprovalRepository(),
		mcpService,
		approvalService,
		chatService,
	)
	workflowService := service.NewWorkflowService(
		repository.NewWorkflowRepository(),
		repository.NewWorkflowVersionRepository(),
		repository.NewWorkflowExecutionRepository(),
		repository.NewWorkflowNodeExecutionRepository(),
		workflowEngine,
	)
	workflowScheduler := service.NewWorkflowScheduler(workflowService, repository.NewWorkflowRepository())
	// M4.5 -> M5: 审核决策 (通过/驳回/超时) 后恢复/终止相关工作流执行;
	// M4.5: 对话/外部调用来源的决策恢复对话 (工具结果回灌模型继续应答)
	approvalService.SetDecisionHook(func(ctx context.Context, approval *domain.ToolApproval) {
		switch {
		case approval.Source == domain.ApprovalSourceWorkflow && approval.WorkflowExecutionID != nil:
			workflowEngine.ResumeAfterApproval(ctx, approval)
		case (approval.Source == domain.ApprovalSourceChat || approval.Source == domain.ApprovalSourceAPIInvoke) && approval.ChatSessionID != nil:
			chatService.ContinueAfterApproval(ctx, approval)
		}
	})
	workflowService.SetSchedulerRefresher(workflowScheduler)
	// 启动对账: 上次进程遗留的运行中执行置为失败 (审核挂起保留, 决策后可恢复)
	workflowEngine.ReconcileOnStartup(context.Background())
	// 对话执行任务对账: 遗留 running 任务置为失败 (等待审核保留, 决策后可恢复)
	if err := chatService.ReconcileOrphanExecutions(context.Background()); err != nil {
		log.Printf("Failed to reconcile orphan executions: %v", err)
	}
	workflowScheduler.Start()
	// 6. 初始化路由
	approvalHandler := mcp.NewApprovalHandler(approvalService)
	// M5 Phase 2: AI 自动生成工作流 (复用模型路由 + Agent/MCP 目录)
	workflowAIGenerator := service.NewWorkflowAIGenerator(modelService, repository.NewAgentRepository(), repository.NewMCPServerRepository())
	workflowHandler := workflow.NewHandler(workflowService, workflowAIGenerator)
	skillHandler := skill.NewHandler(skillService)
	overviewHandler := overview.NewHandler(service.NewOverviewService(repository.NewOverviewRepository()))
	// 5.8 初始化平台设置域: 平台名/图标 (登录页与侧边导航展示)
	platformHandler := platform.NewHandler(service.NewPlatformService(
		repository.NewPlatformSettingsRepository(),
		repository.NewAuditLogRepository(),
	))
	// 5.9 初始化 RBAC 管理域 (M1 遗留落地): 用户/角色/权限管理 + /auth/me
	rbacService := service.NewRBACService(
		repository.NewUserRepository(),
		repository.NewRoleRepository(),
		repository.NewAuditLogRepository(),
	)
	rbacHandler := rbac.NewHandler(rbacService)
	router := setupRouter(cfg, agent.NewHandler(agentService, chatService), mcp.NewHandler(mcpService, approvalService), model.NewHandler(modelService), approvalHandler, workflowHandler, skillHandler, rbacHandler, overviewHandler, platformHandler)

	// 7. 启动服务 (支持优雅退出)
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	server := &http.Server{Addr: addr, Handler: router}

	go func() {
		log.Printf("Server starting on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to run server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received signal %v, shutting down...", sig)

	// 先停止 MCP/模型健康检查、审核超时扫描、执行任务 watchdog 与所有 Agent 实例, 再关闭 HTTP 服务
	workflowScheduler.Stop()
	mcpHealthChecker.Stop()
	modelHealthChecker.Stop()
	approvalTimeoutChecker.Stop()
	executionWatchdog.Stop()
	agentRuntime.Shutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
	logger.Close()
}

func setupRouter(cfg *config.Config, agentHandler *agent.Handler, mcpHandler *mcp.Handler, modelHandler *model.Handler, approvalHandler *mcp.ApprovalHandler, workflowHandler *workflow.Handler, skillHandler *skill.Handler, rbacHandler *rbac.Handler, overviewHandler *overview.Handler, platformHandler *platform.Handler) *gin.Engine {
	// 设置模式
	gin.SetMode(cfg.Server.Mode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(middleware.Logger())
	router.Use(middleware.TraceID())
	router.Use(middleware.CORS())

	// 注册路由
	authHandler := auth.NewHandler()
	api := router.Group("/api/v1")
	authHandler.RegisterRoutes(api)
	agentHandler.RegisterRoutes(api)
	mcpHandler.RegisterRoutes(api)
	modelHandler.RegisterRoutes(api)
	approvalHandler.RegisterRoutes(api)
	workflowHandler.RegisterRoutes(api)
	skillHandler.RegisterRoutes(api)
	rbacHandler.RegisterRoutes(api)
	overviewHandler.RegisterRoutes(api)
	platformHandler.RegisterRoutes(api)

	// 公开 webhook 触发端点 (token 鉴权, 无 JWT)
	workflowHandler.RegisterWebhookRoutes(router)

	return router
}
