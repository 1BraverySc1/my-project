package handler

import (
	"net/http"

	"creator-platform/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	authService *service.AuthService
}

func NewAuthHandler(authService *service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

func (h *AuthHandler) Register(r *gin.RouterGroup) {
	r.POST("/register", h.register)
	r.POST("/login", h.login)
	r.POST("/refresh", h.refresh)
	r.GET("/me", h.me)
}

type authReq struct {
	Username string `json:"username" binding:"required,min=2,max=64"`
	Password string `json:"password" binding:"required,min=6,max=128"`
}

func (h *AuthHandler) register(c *gin.Context) {
	var req authReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}

	user, err := h.authService.Register(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "user_id": user.ID, "username": user.Username})
}

func (h *AuthHandler) login(c *gin.Context) {
	var req authReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	user, accessToken, refreshToken, err := h.authService.Login(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"user_id":       user.ID,
		"username":      user.Username,
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

func (h *AuthHandler) refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	token, err := h.authService.RefreshToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "access_token": token})
}

func (h *AuthHandler) me(c *gin.Context) {
	userID := c.GetInt64("user_id")
	username := c.GetString("username")

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"user_id":  userID,
		"username": username,
	})
}
