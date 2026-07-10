package service

import (
	"errors"
	"time"

	"creator-platform/internal/config"
	"creator-platform/internal/model"
	"creator-platform/internal/repository"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	userRepo *repository.UserRepository
	cfg      *config.JWTConfig
}

func NewAuthService(userRepo *repository.UserRepository, cfg *config.JWTConfig) *AuthService {
	return &AuthService{userRepo: userRepo, cfg: cfg}
}

func (s *AuthService) Register(username, password string) (*model.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Username:     username,
		PasswordHash: string(hash),
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *AuthService) Login(username, password string) (*model.User, string, string, error) {
	user, err := s.userRepo.FindByUsername(username)
	if err != nil {
		return nil, "", "", errors.New("用户名或密码错误")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, "", "", errors.New("用户名或密码错误")
	}

	accessToken, err := s.generateToken(user.ID, user.Username, s.cfg.AccessTTL)
	if err != nil {
		return nil, "", "", err
	}

	refreshToken, err := s.generateToken(user.ID, user.Username, s.cfg.RefreshTTL)
	if err != nil {
		return nil, "", "", err
	}

	return user, accessToken, refreshToken, nil
}

func (s *AuthService) RefreshToken(tokenStr string) (string, error) {
	claims := &middlewareClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(s.cfg.Secret), nil
	})
	if err != nil || !token.Valid {
		return "", errors.New("刷新令牌无效")
	}

	return s.generateToken(claims.UserID, claims.Username, s.cfg.AccessTTL)
}

func (s *AuthService) generateToken(userID int64, username string, ttl time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"exp":      time.Now().Add(ttl).Unix(),
		"iat":      time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.Secret))
}

type middlewareClaims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}
