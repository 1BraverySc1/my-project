package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"creator-platform/internal/config"
	"creator-platform/internal/handler"
	"creator-platform/internal/middleware"
	"creator-platform/internal/repository"
	"creator-platform/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 1. 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 连接数据库
	db, err := gorm.Open(mysql.Open(cfg.Database.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("获取数据库连接失败: %v", err)
	}
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	// 3. 自动建表
	if err := db.AutoMigrate(
		// model 层会自动迁移
	); err != nil {
		log.Fatalf("自动建表失败: %v", err)
	}
	// 手动建表语句
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(64) NOT NULL UNIQUE,
		password_hash VARCHAR(256) NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	db.Exec(`CREATE TABLE IF NOT EXISTS files (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		file_id VARCHAR(64) NOT NULL,
		name VARCHAR(512) NOT NULL,
		size BIGINT NOT NULL,
		chunk_size INT NOT NULL,
		total_chunks INT NOT NULL,
		content_hash VARCHAR(64) NOT NULL,
		user_id BIGINT NOT NULL,
		status TINYINT DEFAULT 0,
		file_path VARCHAR(1024) DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_user_id (user_id),
		INDEX idx_content_hash (content_hash),
		UNIQUE INDEX idx_file_id (file_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	db.Exec(`CREATE TABLE IF NOT EXISTS upload_sessions (
		upload_id VARCHAR(64) PRIMARY KEY,
		file_id VARCHAR(64) NOT NULL,
		user_id BIGINT NOT NULL,
		file_name VARCHAR(512) NOT NULL,
		file_size BIGINT NOT NULL,
		content_hash VARCHAR(64) NOT NULL,
		chunk_size INT NOT NULL,
		total_chunks INT NOT NULL,
		received_chunks TEXT,
		status TINYINT DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_file_id (file_id),
		INDEX idx_user_id (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)

	// 4. 连接 Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// 5. 初始化各层
	// Repository
	userRepo := repository.NewUserRepository(db)
	fileRepo := repository.NewFileRepository(db)
	uploadRepo := repository.NewUploadRepository(db)

	// Service
	authService := service.NewAuthService(userRepo, &cfg.JWT)
	uploadService := service.NewUploadService(fileRepo, uploadRepo, rdb, &cfg.Upload)

	// 6. 初始化 Gin
	r := gin.Default()
	r.Use(middleware.CORSMiddleware())

	// 健康检查
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API 路由
	api := r.Group("/api")
	{
		// 无需鉴权
		authHandler := handler.NewAuthHandler(authService)
		authHandler.Register(api.Group("/auth"))

		// 需要 JWT 鉴权
		uploadHandler := handler.NewUploadHandler(uploadService)
		uploadHandler.Register(api.Group("", middleware.JWTAuth(cfg.JWT.Secret)))
	}

	// 7. 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		fmt.Printf("服务启动成功，监听 %s\n", cfg.Server.Addr)
		if err := r.Run(cfg.Server.Addr); err != nil {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	<-quit
	log.Println("正在关闭服务...")
	sqlDB.Close()
	rdb.Close()
	log.Println("服务已关闭")
}
