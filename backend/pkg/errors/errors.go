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
	ErrNotFound      = &AppError{Code: "not_found", Message: "资源不存在", HTTPCode: 404}
	ErrUnauthorized  = &AppError{Code: "unauthorized", Message: "未授权", HTTPCode: 401}
	ErrForbidden     = &AppError{Code: "forbidden", Message: "权限不足", HTTPCode: 403}
	ErrValidation    = &AppError{Code: "validation_error", Message: "参数校验失败", HTTPCode: 400}
	ErrInternal      = &AppError{Code: "internal_error", Message: "服务器内部错误", HTTPCode: 500}
	ErrRoleInUse     = &AppError{Code: "role_in_use", Message: "角色已分配给用户, 请先解除分配", HTTPCode: 400}
	ErrSkillConflict = &AppError{Code: "skill_conflict", Message: "技能名已存在, 如需覆盖请使用强制升级", HTTPCode: 409}
	ErrSkillInUse    = &AppError{Code: "skill_in_use", Message: "技能已被 Agent 关联, 请先解除关联或使用 force 删除", HTTPCode: 409}
	ErrBuiltinRole   = &AppError{Code: "builtin_role", Message: "内置角色不可执行此操作", HTTPCode: 400}
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
