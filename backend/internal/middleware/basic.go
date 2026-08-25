package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"log"
	"time"

	"github.com/gin-contrib/cors"
)

// Logger 请求日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		cost := time.Since(start).Milliseconds()
		log.Printf("%s %s status=%d cost=%dms", c.Request.Method, path, c.Writer.Status(), cost)
	}
}

// TraceID 生成/传递 trace ID
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader("X-Trace-Id")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		c.Set("trace_id", traceID)
		c.Header("X-Trace-Id", traceID)
		c.Next()
	}
}

// CORS 跨域处理
func CORS() gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}
