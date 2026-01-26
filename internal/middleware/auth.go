package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nubro999/worldland-gpu/internal/auth"
)

// AuthMiddleware는 JWT 토큰을 검증하는 미들웨어입니다.
func AuthMiddleware(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Authorization 헤더에서 토큰 추출
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Authorization 헤더가 필요합니다",
			})
			return
		}

		// "Bearer " 접두사 제거
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "잘못된 Authorization 헤더 형식입니다",
			})
			return
		}

		tokenString := parts[1]

		// 토큰 검증
		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "유효하지 않은 토큰입니다",
			})
			return
		}

		// Context에 claims 저장
		c.Set("claims", claims)
		c.Set("userID", claims.Sub)
		c.Next()
	}
}

// DevAuthMiddleware는 개발/테스트용 인증 미들웨어입니다.
// X-User-ID 헤더로 간단히 사용자를 식별합니다.
// 운영 환경에서는 AuthMiddleware로 교체해야 합니다.
func DevAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. X-User-ID 헤더 확인 (테스트용)
		userID := c.GetHeader("X-User-ID")

		// 2. 없으면 anonymous로 설정
		if userID == "" {
			userID = "anonymous"
		}

		// 3. Context에 저장
		c.Set("userID", userID)
		c.Next()
	}
}

// OptionalAuthMiddleware는 토큰이 있으면 검증하고, 없으면 그냥 통과합니다.
// 공개 API에서 사용자 정보를 선택적으로 사용할 때 유용합니다.
func OptionalAuthMiddleware(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		// 헤더가 없으면 그냥 통과
		if authHeader == "" {
			// X-User-ID fallback (개발용)
			if userID := c.GetHeader("X-User-ID"); userID != "" {
				c.Set("userID", userID)
			}
			c.Next()
			return
		}

		// Bearer 토큰 추출
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.Next()
			return
		}

		// 토큰 검증 시도
		if claims, err := jwtManager.ValidateToken(parts[1]); err == nil {
			c.Set("claims", claims)
			c.Set("userID", claims.Sub)
		}

		c.Next()
	}
}
