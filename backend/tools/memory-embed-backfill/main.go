// memory-embed-backfill 记忆向量历史回填工具 (M10.3 语义检索)
//
// 用法 (在 backend 目录下运行, 读取 .env):
//
//	go run ./tools/memory-embed-backfill [-batch 64] [-limit 0]
//
// 行为:
//   - 要求配置 MEMORY_EMBED_MODEL (向量专用 ModelTemplate 名称, OpenAI 兼容 /embeddings 端点);
//   - 分批回填 "活跃但无向量" 的记忆 (每批一次 /embeddings 批量请求);
//   - 用量经 ModelTemplateService 计入 ModelUsageLog / 配额 (与对话调用同路径计量);
//   - 幂等, 可重复运行 (仅处理向量列为空的行); 批内全部失败时中止, 避免死循环。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"agent-platform/internal/config"
	"agent-platform/internal/crypto"
	"agent-platform/internal/database"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
)

func main() {
	batch := flag.Int("batch", 64, "每批条数 (一次 /embeddings 批量请求)")
	limit := flag.Int("limit", 0, "最多回填条数 (0 = 全部)")
	modelFlag := flag.String("model", "", "embedding 模型模板名 (覆盖 MEMORY_EMBED_MODEL)")
	flag.Parse()

	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	embedName := *modelFlag
	if embedName == "" {
		embedName = cfg.Memory.EmbedModel
	}
	if _, err := database.Init(cfg.Database); err != nil {
		log.Fatalf("database: %v", err)
	}
	// 与在线服务一致: 向量模型名优先级 -model flag > 平台设置 (platform_settings.memory_embed_model) > MEMORY_EMBED_MODEL env
	if embedName == "" {
		ctx0 := context.Background()
		if s, err := repository.NewPlatformSettingsRepository().Get(ctx0); err == nil && s.MemoryEmbedModel != "" {
			embedName = s.MemoryEmbedModel
		}
	}
	if embedName == "" {
		log.Fatalf("MEMORY_EMBED_MODEL is required (向量专用 ModelTemplate 名称; 亦可在平台设置页配置)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cipher, err := crypto.NewAesGCM(cfg.Model.CredentialsKey)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	modelSvc := service.NewModelTemplateService(
		repository.NewModelTemplateRepository(),
		repository.NewModelQuotaRepository(),
		repository.NewModelUsageLogRepository(),
		repository.NewModelHealthLogRepository(),
		repository.NewAgentRepository(),
		cipher,
		cfg.Model.CheckTimeout,
		cfg.Model.ChatTimeout,
		cfg.Memory.EmbedTimeout,
		service.StaticTemplateSource(embedName),
	)
	memRepo := repository.NewMemoryRepository()

	remaining := *limit
	backfilled, failed := 0, 0
	for {
		n := *batch
		if remaining > 0 && remaining < n {
			n = remaining
		}
		items, err := memRepo.ListMissingEmbedding(ctx, n, 0)
		if err != nil {
			log.Fatalf("list missing embedding: %v", err)
		}
		if len(items) == 0 {
			break
		}
		texts := make([]string, len(items))
		for i := range items {
			texts[i] = items[i].Content
		}
		vecs, err := modelSvc.EmbedForMemory(ctx, embedName, texts)
		if err != nil {
			log.Fatalf("embed batch: %v", err)
		}
		if len(vecs) != len(items) {
			log.Fatalf("embed batch: vector count mismatch (got %d want %d)", len(vecs), len(items))
		}
		updatedInBatch := 0
		for i := range items {
			raw, mErr := json.Marshal(vecs[i])
			if mErr != nil {
				log.Printf("marshal vector failed id=%s: %v", items[i].ID, mErr)
				failed++
				continue
			}
			if uErr := memRepo.UpdateEmbedding(ctx, items[i].AgentID, items[i].ID, raw); uErr != nil {
				log.Printf("update embedding failed id=%s: %v", items[i].ID, uErr)
				failed++
				continue
			}
			updatedInBatch++
			backfilled++
			if remaining > 0 {
				remaining--
			}
		}
		fmt.Printf("batch done: rows=%d updated=%d backfilled_total=%d failed_total=%d\n",
			len(items), updatedInBatch, backfilled, failed)
		if updatedInBatch == 0 {
			log.Fatalf("no progress in batch (all updates failed), aborting to avoid infinite loop")
		}
		if *limit > 0 && remaining <= 0 {
			break
		}
	}
	fmt.Printf("backfill complete: backfilled=%d failed=%d\n", backfilled, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
