package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// UserClaims는 토큰 생성에 사용되는 사용자 정보입니다.
type UserClaims struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	Sub     string `json:"sub"` // 사용자 고유 ID
}

// JWTClaims는 JWT 토큰에 포함될 클레임입니다.
type JWTClaims struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	Sub     string `json:"sub"` // 사용자 ID
	jwt.RegisteredClaims
}

// JWTManager는 JWT 토큰 생성 및 검증을 담당합니다.
type JWTManager struct {
	secret        []byte
	tokenDuration time.Duration
}

// NewJWTManager는 새 JWT 매니저를 생성합니다.
func NewJWTManager(secret string, tokenDuration time.Duration) *JWTManager {
	return &JWTManager{
		secret:        []byte(secret),
		tokenDuration: tokenDuration,
	}
}

// GenerateToken은 사용자 정보로 JWT 토큰을 생성합니다.
func (j *JWTManager) GenerateToken(claims *UserClaims) (string, error) {
	now := time.Now()

	jwtClaims := JWTClaims{
		Email:   claims.Email,
		Name:    claims.Name,
		Picture: claims.Picture,
		Sub:     claims.Sub,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(j.tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "k8s-proxy-server",
			Subject:   claims.Sub,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims)
	signedToken, err := token.SignedString(j.secret)
	if err != nil {
		return "", fmt.Errorf("토큰 서명 실패: %w", err)
	}

	return signedToken, nil
}

// ValidateToken은 JWT 토큰을 검증하고 클레임을 반환합니다.
func (j *JWTManager) ValidateToken(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// 서명 방식 확인
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("잘못된 서명 방식: %v", token.Header["alg"])
		}
		return j.secret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("토큰 파싱 실패: %w", err)
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("유효하지 않은 토큰")
	}

	return claims, nil
}
