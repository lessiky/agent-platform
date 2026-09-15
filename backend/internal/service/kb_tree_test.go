package service

// kb_tree_test.go — M11.5 分类树单测 (事项 1: 两级层级 / 同级唯一 / 移动 / 删除保护 / 审计)

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apperrors "agent-platform/pkg/errors"
)

func TestKBCategoryTreeCreate(t *testing.T) {
	svc, _, _, _, _, _ := newTestKBService()
	ctx := context.Background()

	top, err := svc.CreateCategory(ctx, "", "运维", "", "op", "op", "ip")
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	child, err := svc.CreateCategory(ctx, top.ID, "数据库", "", "op", "op", "ip")
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != top.ID {
		t.Fatalf("child.ParentID = %v, want %s", child.ParentID, top.ID)
	}
	// 同级重名 → 400
	if _, err := svc.CreateCategory(ctx, top.ID, "数据库", "", "op", "op", "ip"); err == nil || !strings.Contains(err.Error(), "同级分类名已存在") {
		t.Fatalf("dup sibling name: err = %v, want 同级分类名已存在", err)
	}
	// 大小写不敏感
	if _, err := svc.CreateCategory(ctx, top.ID, "数据库", "", "op", "op", "ip"); err == nil {
		t.Fatal("dup sibling name (case) should be rejected")
	}
	// 不同父下同名 (顶级 vs 子级) → 允许
	if _, err := svc.CreateCategory(ctx, "", "数据库", "", "op", "op", "ip"); err != nil {
		t.Fatalf("same name under different parent should pass: %v", err)
	}
	// 父级须为顶级 → 400
	if _, err := svc.CreateCategory(ctx, child.ID, "孙级", "", "op", "op", "ip"); err == nil || !strings.Contains(err.Error(), "顶级分类") {
		t.Fatalf("create under child: err = %v, want 顶级分类", err)
	}
	// 父级不存在 → 400
	if _, err := svc.CreateCategory(ctx, "cat-missing", "XY", "", "op", "op", "ip"); err == nil || !strings.Contains(err.Error(), "父级分类不存在") {
		t.Fatalf("missing parent: err = %v, want 父级分类不存在", err)
	}
}

func TestKBCategoryMove(t *testing.T) {
	svc, _, _, _, _, audits := newTestKBService()
	ctx := context.Background()

	topA, _ := svc.CreateCategory(ctx, "", "AA", "", "op", "op", "ip")
	topB, _ := svc.CreateCategory(ctx, "", "BB", "", "op", "op", "ip")
	subA, _ := svc.CreateCategory(ctx, topA.ID, "S1", "", "op", "op", "ip")
	subB, _ := svc.CreateCategory(ctx, topB.ID, "S2", "", "op", "op", "ip")

	// subB (无子级) 移到 topA 下 → 成功
	moved, err := svc.UpdateCategory(ctx, subB.ID, strPtr(topA.ID), "S2", "", "op", "op", "ip")
	if err != nil {
		t.Fatalf("move subB: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != topA.ID {
		t.Fatalf("moved parent = %v, want %s", moved.ParentID, topA.ID)
	}
	// topA 已有子级 (subA/subB) → 降为子级 400
	if _, err := svc.UpdateCategory(ctx, topA.ID, strPtr(topB.ID), "AA", "", "op", "op", "ip"); err == nil || !strings.Contains(err.Error(), "不可降为子级") {
		t.Fatalf("demote with children: err = %v, want 不可降为子级", err)
	}
	// 移动时同级重名 → 400 (topB 下已有 S2? 不, subB 已移走; 用改名触发: subA 改名 S2 再移 topB — topB 无 S2 了, 改为在 topA 下 subA 改名 S2 → 与 subB 同级重名)
	if _, err := svc.UpdateCategory(ctx, subA.ID, nil, "S2", "", "op", "op", "ip"); err == nil || !strings.Contains(err.Error(), "同级分类名已存在") {
		t.Fatalf("rename to sibling name: err = %v, want 同级分类名已存在", err)
	}
	// subA 升顶级 (parent_id = "" 显式空串指针; nil 表示不变) → 成功
	empty := ""
	asc, err := svc.UpdateCategory(ctx, subA.ID, &empty, "S1", "", "op", "op", "ip")
	if err != nil {
		t.Fatalf("promote subA: %v", err)
	}
	if asc.ParentID != nil {
		t.Fatalf("promoted parent = %v, want nil", asc.ParentID)
	}
	// 审计: 移动的记录含 parent_id / old_parent_id 前后值
	var moveDetail map[string]interface{}
	for i := len(audits.entries) - 1; i >= 0; i-- {
		if audits.entries[i].Action != "kb.category_updated" {
			continue
		}
		if err := json.Unmarshal(audits.entries[i].Detail, &moveDetail); err != nil {
			t.Fatalf("unmarshal audit detail: %v", err)
		}
		if moveDetail["name"] != "S1" {
			continue
		}
		if moveDetail["parent_id"] != nil || moveDetail["old_parent_id"] == nil {
			t.Fatalf("move audit detail = %v, want parent_id nil + old_parent_id set", moveDetail)
		}
		break
	}
	if moveDetail == nil {
		t.Fatal("no kb.category_updated audit with name S1 found")
	}
}

func TestKBCategoryDeleteWithChildren(t *testing.T) {
	svc, _, _, _, _, _ := newTestKBService()
	ctx := context.Background()

	top, _ := svc.CreateCategory(ctx, "", "父级", "", "op", "op", "ip")
	child, _ := svc.CreateCategory(ctx, top.ID, "子级", "", "op", "op", "ip")

	// 有子级 → 409
	err := svc.DeleteCategory(ctx, top.ID, "op", "op", "ip")
	if err == nil || err != apperrors.ErrKBCategoryHasChildren {
		t.Fatalf("delete with children: err = %v, want ErrKBCategoryHasChildren", err)
	}
	// 子级清空后可删
	if err := svc.DeleteCategory(ctx, child.ID, "op", "op", "ip"); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if err := svc.DeleteCategory(ctx, top.ID, "op", "op", "ip"); err != nil {
		t.Fatalf("delete top after children cleared: %v", err)
	}
}

func TestKBCategoryCreateAudit(t *testing.T) {
	svc, _, _, _, _, audits := newTestKBService()
	ctx := context.Background()

	top, _ := svc.CreateCategory(ctx, "", "父级", "", "op", "op", "ip")
	_, err := svc.CreateCategory(ctx, top.ID, "子级", "", "op", "op", "ip")
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	var detail map[string]interface{}
	for i := len(audits.entries) - 1; i >= 0; i-- {
		if audits.entries[i].Action == "kb.category_created" {
			_ = json.Unmarshal(audits.entries[i].Detail, &detail)
			break
		}
	}
	if detail == nil {
		t.Fatal("no kb.category_created audit")
	}
	if detail["name"] != "子级" {
		t.Fatalf("audit name = %v, want 子级", detail["name"])
	}
	if pid, ok := detail["parent_id"].(string); !ok || pid != top.ID {
		t.Fatalf("audit parent_id = %v, want %s", detail["parent_id"], top.ID)
	}
}