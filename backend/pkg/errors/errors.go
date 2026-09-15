package errors

import "fmt"

// AppError 应用层错误
type AppError struct {
	Code     string
	Message  string
	HTTPCode int
}

func (e *AppError) Error() string {
	return e.Message
}

// 预定义错误
var (
	ErrNotFound        = &AppError{Code: "not_found", Message: "资源不存在", HTTPCode: 404}
	ErrUnauthorized    = &AppError{Code: "unauthorized", Message: "未授权", HTTPCode: 401}
	ErrForbidden       = &AppError{Code: "forbidden", Message: "权限不足", HTTPCode: 403}
	ErrValidation      = &AppError{Code: "validation_error", Message: "参数校验失败", HTTPCode: 400}
	ErrInternal        = &AppError{Code: "internal_error", Message: "服务器内部错误", HTTPCode: 500}
	ErrRoleInUse       = &AppError{Code: "role_in_use", Message: "角色已分配给用户, 请先解除分配", HTTPCode: 400}
	ErrSkillConflict   = &AppError{Code: "skill_conflict", Message: "技能名已存在, 如需覆盖请使用强制升级", HTTPCode: 409}
	ErrSkillInUse      = &AppError{Code: "skill_in_use", Message: "技能已被 Agent 关联, 请先解除关联或使用 force 删除", HTTPCode: 409}
	ErrKBCategoryInUse = &AppError{Code: "kb_category_in_use", Message: "分类下存在条目, 请先迁移或删除条目后再删除分类", HTTPCode: 409}
	ErrKBCategoryHasChildren = &AppError{Code: "kb_category_has_children", Message: "分类下存在子级分类, 请先删除或迁移子级分类", HTTPCode: 409}
	ErrKBTaskActive          = &AppError{Code: "kb_task_active", Message: "已有运行中的向量回填任务, 请等待完成或取消后再启动", HTTPCode: 409}
	ErrKBTaskNotFound        = &AppError{Code: "kb_task_not_found", Message: "向量回填任务不存在", HTTPCode: 404}
	ErrKBTaskNotCancellable  = &AppError{Code: "kb_task_not_cancellable", Message: "任务已结束, 不可取消", HTTPCode: 400}
	ErrKBBackfillGone        = &AppError{Code: "gone", Message: "端点已弃用 (410): 请改用 POST /kb/backfill-tasks 启动向量回填任务, 进度经 GET /kb/backfill-tasks/:id 查询", HTTPCode: 410}
	ErrBuiltinRole     = &AppError{Code: "builtin_role", Message: "内置角色不可执行此操作", HTTPCode: 400}
)

// NewValidationError 创建校验错误
func NewValidationError(msg string) *AppError {
	return &AppError{
		Code:     "validation_error",
		Message:  msg,
		HTTPCode: 400,
	}
}

// Wrap 包装错误
func Wrap(err error, msg string) *AppError {
	return &AppError{
		Code:     "wrapped_error",
		Message:  fmt.Sprintf("%s: %v", msg, err),
		HTTPCode: 500,
	}
}
