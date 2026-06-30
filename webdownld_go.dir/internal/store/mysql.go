package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

// MySQLStore 封装 MySQL 数据库连接池，提供用户、订单、套餐数据的持久化访问。
type MySQLStore struct {
	DB *sql.DB // DB 数据库连接池。
}

// NewMySQLStore 创建 MySQL 存储实例，建立连接池、执行自动建表并播种管理员账号。
// dsn 为 MySQL 连接字符串，adminUser/adminPass 为默认管理员账号凭据。
func NewMySQLStore(dsn, adminUser, adminPass string) (*MySQLStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 连接失败: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("MySQL 连接探测失败: %w", err)
	}

	store := new(MySQLStore)
	store.DB = db

	if err := store.autoMigrate(); err != nil {
		return nil, fmt.Errorf("自动建表失败: %w", err)
	}

	if err := store.seedAdmin(adminUser, adminPass); err != nil {
		return nil, fmt.Errorf("播种管理员账号失败: %w", err)
	}

	slog.Info("MySQL 连接已建立并完成建表")
	return store, nil
}

// autoMigrate 创建用户、会员套餐和订单表（如不存在）。
func (s *MySQLStore) autoMigrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL UNIQUE,
			password_hash VARCHAR(256) NOT NULL,
			is_admin TINYINT DEFAULT 0,
			is_member TINYINT DEFAULT 0,
			member_expire DATETIME NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS member_plans (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			plan_name VARCHAR(32) NOT NULL,
			price_cent BIGINT NOT NULL,
			duration_days INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS orders (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_id BIGINT NOT NULL,
			plan_id BIGINT NOT NULL,
			amount_cent BIGINT NOT NULL,
			status VARCHAR(16) DEFAULT 'pending',
			alipay_trade_no VARCHAR(64) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			paid_at DATETIME NULL,
			INDEX idx_user_id (user_id),
			INDEX idx_status (status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}

	for _, q := range queries {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("执行建表语句失败: %w\n语句: %s", err, q)
		}
	}
	return nil
}

// seedAdmin 播种系统管理员账号，若管理员已存在则跳过。
func (s *MySQLStore) seedAdmin(username, password string) error {
	if username == "" || password == "" {
		slog.Warn("管理员账号或密码为空，跳过播种")
		return nil
	}

	var existID int64
	err := s.DB.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&existID)
	if err == nil {
		slog.Info("管理员账号已存在，跳过播种", "username", username)
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("查询管理员账号失败: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("管理员密码哈希失败: %w", err)
	}

	now := time.Now()
	_, err = s.DB.Exec(
		"INSERT INTO users (username, password_hash, is_admin, is_member, created_at, updated_at) VALUES (?, ?, 1, 0, ?, ?)",
		username, string(hash), now, now,
	)
	if err != nil {
		return fmt.Errorf("创建管理员账号失败: %w", err)
	}

	slog.Info("管理员账号已创建", "username", username)
	return nil
}

// Close 关闭数据库连接池。
func (s *MySQLStore) Close() error {
	return s.DB.Close()
}
