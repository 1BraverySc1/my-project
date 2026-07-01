package tests

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"webdownld_go/internal/api"
	"webdownld_go/internal/auth"

	"github.com/gin-gonic/gin"
)

func TestJWTTokenTypes(t *testing.T) {
	svc := auth.NewJWTService("test-secret", time.Hour, 24*time.Hour)

	accessToken, err := svc.GenerateAccessToken(7, "alice", true, true)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}
	accessClaims, err := svc.ValidateAccessToken(accessToken)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error = %v", err)
	}
	if accessClaims.TokenType != auth.TokenTypeAccess || !accessClaims.IsAdmin || !accessClaims.IsMember {
		t.Fatalf("unexpected access claims: %+v", accessClaims)
	}
	if _, err := svc.ValidateRefreshToken(accessToken); err == nil {
		t.Fatal("access token must not pass refresh-token validation")
	}

	refreshToken, err := svc.GenerateRefreshToken(7, "alice", true)
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error = %v", err)
	}
	if _, err := svc.ValidateAccessToken(refreshToken); err == nil {
		t.Fatal("refresh token must not pass access-token validation")
	}
}

func TestJWTRejectsInvalidClaims(t *testing.T) {
	const secret = "test-secret"
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":0,"username":"","token_type":"access","exp":4102444800}`))
	input := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	token := input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	svc := auth.NewJWTService(secret, time.Hour, 24*time.Hour)
	if _, err := svc.ValidateAccessToken(token); err == nil {
		t.Fatal("token with invalid identity claims must be rejected")
	}
}

func TestJWTAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := auth.NewJWTService("test-secret", time.Hour, 24*time.Hour)
	accessToken, _ := svc.GenerateAccessToken(9, "alice", false, true)
	refreshToken, _ := svc.GenerateRefreshToken(9, "alice", false)

	tests := []struct {
		name       string
		service    *auth.JWTService
		token      string
		wantStatus int
	}{
		{name: "access token", service: svc, token: accessToken, wantStatus: http.StatusOK},
		{name: "refresh token", service: svc, token: refreshToken, wantStatus: http.StatusUnauthorized},
		{name: "missing token", service: svc, wantStatus: http.StatusUnauthorized},
		{name: "missing service", token: accessToken, wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/protected", api.JWTAuthMiddleware(tt.service), func(c *gin.Context) {
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			if resp.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, tt.wantStatus, resp.Body.String())
			}
		})
	}
}
