package middleware

import (

    "strings"
    "time"

    "agent-platform/pkg/response"

    "github.com/gin-gonic/gin"
    "github.com/golang-jwt/jwt/v5"
)

var jwtSecret []byte

// InitJWT 设置 JWT 签名密钥
func InitJWT(secret string) {
    jwtSecret = []byte(secret)
}

type claims struct {
    UserID   string `json:"user_id"`
    Username string `json:"username"`
    Roles    []string `json:"roles"`
    jwt.RegisteredClaims
}

// AuthSkip 跳过认证的公开路由
func AuthSkip() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Next()
    }
}

// Auth JWT 认证中间件
func Auth() gin.HandlerFunc {
    return func(c *gin.Context) {
        tokenStr := extractToken(c)
        if tokenStr == "" {
            response.Unauthorized(c, "missing token")
            c.Abort()
            return
        }

        claims, err := parseToken(tokenStr)
        if err != nil {
            response.Unauthorized(c, "invalid token")
            c.Abort()
            return
        }

        c.Set("user_id", claims.UserID)
        c.Set("username", claims.Username)
        c.Set("roles", claims.Roles)
        c.Next()
    }
}

// AuthCheck 认证并检查权限
func AuthCheck(requiredPermissions ...string) gin.HandlerFunc {
    return func(c *gin.Context) {
        tokenStr := extractToken(c)
        if tokenStr == "" {
            response.Unauthorized(c, "missing token")
            c.Abort()
            return
        }

        claims, err := parseToken(tokenStr)
        if err != nil {
            response.Unauthorized(c, "invalid token")
            c.Abort()
            return
        }

        c.Set("user_id", claims.UserID)
        c.Set("username", claims.Username)
        c.Set("roles", claims.Roles)

        // 检查权限
        if len(requiredPermissions) > 0 {
            userPerms := GetUserPermissions(c.Request.Context(), claims.UserID)
            if !hasPermission(userPerms, requiredPermissions...) {
                response.Forbidden(c, "permission denied")
                c.Abort()
                return
            }
        }

        c.Next()
    }
}

func extractToken(c *gin.Context) string {
    auth := c.GetHeader("Authorization")
    if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
        return ""
    }
    return strings.TrimPrefix(auth, "Bearer ")
}

func parseToken(tokenStr string) (*claims, error) {
    token, err := jwt.ParseWithClaims(tokenStr, &claims{}, func(token *jwt.Token) (interface{}, error) {
        return jwtSecret, nil
    })
    if err != nil {
        return nil, err
    }

    if claims, ok := token.Claims.(*claims); ok && token.Valid {
        return claims, nil
    }

    return nil, jwt.ErrTokenInvalidClaims
}

func GenerateToken(userID, username string, roles []string) (string, error) {
    now := time.Now()
    exp := now.Add(24 * time.Hour)

    c := claims{
        UserID:   userID,
        Username: username,
        Roles:    roles,
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(exp),
            IssuedAt:  jwt.NewNumericDate(now),
            Issuer:    "agent-platform",
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
    return token.SignedString(jwtSecret)
}

func hasPermission(userPerms []string, required ...string) bool {
    permMap := make(map[string]bool)
    for _, p := range userPerms {
        permMap[p] = true
    }

    for _, r := range required {
        if !permMap[r] {
            return false
        }
    }
    return true
}
