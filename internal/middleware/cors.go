package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig는 CORS 미들웨어 설정입니다.
type CORSConfig struct {
	AllowedOrigins string // 허용할 Origin (쉼표 구분, "*"는 모두 허용)
}

// CORS는 CORS 헤더를 추가하는 Gin 미들웨어입니다.
// 기본값으로 모든 Origin을 허용합니다.
func CORS() gin.HandlerFunc {
	return CORSWithConfig(CORSConfig{AllowedOrigins: "*"})
}

// CORSWithConfig는 설정을 받아 CORS 미들웨어를 생성합니다.
func CORSWithConfig(config CORSConfig) gin.HandlerFunc {
	allowedOrigins := parseOrigins(config.AllowedOrigins)

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Origin 검증
		allowOrigin := "*"
		if config.AllowedOrigins != "*" {
			if isOriginAllowed(origin, allowedOrigins) {
				allowOrigin = origin
			} else {
				// 허용되지 않은 Origin
				c.AbortWithStatus(403)
				return
			}
		}

		// CORS 헤더 설정
		c.Writer.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400") // 24시간 캐시

		// Preflight 요청 처리
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// parseOrigins는 쉼표로 구분된 Origin 문자열을 파싱합니다.
func parseOrigins(origins string) []string {
	if origins == "*" || origins == "" {
		return nil
	}
	result := strings.Split(origins, ",")
	for i, o := range result {
		result[i] = strings.TrimSpace(o)
	}
	return result
}

// isOriginAllowed는 주어진 Origin이 허용 목록에 있는지 확인합니다.
func isOriginAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == origin {
			return true
		}
	}
	return false
}
