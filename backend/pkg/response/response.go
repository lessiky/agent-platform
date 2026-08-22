package response

import (
    "net/http"

    "agent-platform/pkg/errors"

    "github.com/gin-gonic/gin"
)

// Response 统一响应格式
type Response struct {
    Code    string      `json:"code"`
    Message string      `json:"message"`
    Data    interface{} `json:"data,omitempty"`
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
    c.JSON(http.StatusOK, Response{
        Code:    "success",
        Message: "ok",
        Data:    data,
    })
}

// Created 创建成功响应
func Created(c *gin.Context, data interface{}) {
    c.JSON(http.StatusCreated, Response{
        Code:    "success",
        Message: "created",
        Data:    data,
    })
}

// Accepted 已受理 (202): 异步处理中, 如等待人工审核 (M4.5)
func Accepted(c *gin.Context, data interface{}) {
    c.JSON(http.StatusAccepted, Response{
        Code:    "pending_approval",
        Message: "已受理, 等待人工审核",
        Data:    data,
    })
}

// Error 错误响应
func Error(c *gin.Context, err error) {
    appErr, ok := err.(*errors.AppError)
    if !ok {
        appErr = errors.ErrInternal
    }
    c.JSON(appErr.HTTPCode, Response{
        Code:    appErr.Code,
        Message: appErr.Message,
    })
}

// BadRequest 参数错误
func BadRequest(c *gin.Context, msg string) {
    c.JSON(http.StatusBadRequest, Response{
        Code:    "validation_error",
        Message: msg,
    })
}

// Unauthorized 未授权
func Unauthorized(c *gin.Context, msg string) {
    c.JSON(http.StatusUnauthorized, Response{
        Code:    "unauthorized",
        Message: msg,
    })
}

// Forbidden 权限不足
func Forbidden(c *gin.Context, msg string) {
    c.JSON(http.StatusForbidden, Response{
        Code:    "forbidden",
        Message: msg,
    })
}

// NotFound 资源不存在
func NotFound(c *gin.Context, msg string) {
    c.JSON(http.StatusNotFound, Response{
        Code:    "not_found",
        Message: msg,
    })
}