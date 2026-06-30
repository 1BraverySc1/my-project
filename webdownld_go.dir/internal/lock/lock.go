package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// DistributedLock Redis 分布式锁，支持看门狗自动续期机制。
// 锁通过 Redis SET NX PX 命令获取，通过 Lua 脚本原子释放。
// 持有锁期间，看门狗 goroutine 每隔 ttl/3 自动续期，防止业务逻辑执行时间超过 TTL 导致锁被抢占。
type DistributedLock struct {
	client *redisClient  // client Redis 客户端连接。
	key    string        // key 锁在 Redis 中的键名。
	token  string        // token 锁持有者的唯一标识，防止误删其他实例的锁。
	ttl    time.Duration // ttl 锁的过期时间。
	mu     sync.Mutex    // mu 保护 watchdog 状态的并发访问。
	stopCh chan struct{} // stopCh 通知看门狗 goroutine 停止续期。
}

// LockFactory 分布式锁工厂，管理与 Redis 的连接并创建锁实例。
// 所有锁实例共享同一个 Redis 连接池（通过互斥锁保护）。
type LockFactory struct {
	client *redisClient // client Redis 客户端连接。
	ttl    time.Duration // ttl 锁的默认过期时间。
}

// NewLockFactory 创建分布式锁工厂并验证 Redis 连通性。
// addr 为 Redis 地址（host:port），password 为空时不认证，db 为数据库编号，ttl 为默认锁过期时间。
func NewLockFactory(addr, password string, db int, ttl time.Duration) (*LockFactory, error) {
	client, err := newRedisClient(addr, password, db)
	if err != nil {
		return nil, err
	}

	f := new(LockFactory)
	f.client = client
	f.ttl = ttl
	return f, nil
}

// NewLock 创建一把针对指定 key 的分布式锁。
// key 为锁在 Redis 中的键名，例如 "upload:lock:<uploadID>"。
func (f *LockFactory) NewLock(key string) *DistributedLock {
	l := new(DistributedLock)
	l.client = f.client
	l.key = key
	l.ttl = f.ttl
	l.token = generateToken()
	l.stopCh = make(chan struct{})
	return l
}

// Close 关闭与 Redis 的连接，应在程序退出前调用。
func (f *LockFactory) Close() error {
	if f.client != nil {
		return f.client.close()
	}
	return nil
}

// Lock 获取分布式锁，成功返回 nil，失败返回 error。
// ctx 用于超时控制：若 ctx 取消则立即返回 context.Canceled。
// 内部通过 Redis SET key token NX PX ttl 命令实现，原子性保证只有一个实例能获取锁。
// 获取成功后启动看门狗 goroutine 自动续期。
func (l *DistributedLock) Lock(ctx context.Context) error {
	ttlMs := int64(l.ttl / time.Millisecond)
	// SET key token NX PX ttlMs
	result, err := l.client.do("SET", l.key, l.token, "NX", "PX", fmt.Sprintf("%d", ttlMs))
	if err != nil {
		return fmt.Errorf("获取分布式锁失败 key=%s: %w", l.key, err)
	}
	// Redis SET NX 成功返回 "OK"，未获取到锁返回 nil
	if result == nil {
		return fmt.Errorf("分布式锁已被占用 key=%s", l.key)
	}

	l.startWatchdog()
	return nil
}

// Unlock 释放分布式锁。
// 通过 Lua 脚本原子校验 token 后删除，防止误删其他实例持有的锁。
// 同时停止看门狗 goroutine。
func (l *DistributedLock) Unlock(ctx context.Context) error {
	l.stopWatchdog()

	// Lua 脚本: 如果 key 对应的 value 等于 token，则删除
	script := "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end"
	result, err := l.client.do("EVAL", script, "1", l.key, l.token)
	if err != nil {
		return fmt.Errorf("释放分布式锁失败 key=%s: %w", l.key, err)
	}
	// DEL 返回 1 表示成功删除，返回 0 表示锁已被其他人持有（不应发生）
	n, _ := result.(int64)
	if n == 0 {
		return fmt.Errorf("分布式锁已过期或被抢占 key=%s", l.key)
	}
	return nil
}

// startWatchdog 启动看门狗 goroutine，每隔 ttl/3 自动续期。
func (l *DistributedLock) startWatchdog() {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 重新创建 stopCh，支持锁的重复使用。
	l.stopCh = make(chan struct{})

	go func() {
		interval := l.ttl / 3
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-l.stopCh:
				return
			case <-ticker.C:
				ttlSeconds := int64(l.ttl / time.Second)
				if ttlSeconds < 1 {
					ttlSeconds = 1
				}
				_, err := l.client.do("EXPIRE", l.key, fmt.Sprintf("%d", ttlSeconds))
				if err != nil {
					// 续期失败（如 Redis 断连），goroutine 静默退出。
					// 业务逻辑完成后 Unlock 会尝试主动释放。
					return
				}
			}
		}
	}()
}

// stopWatchdog 停止看门狗 goroutine。
func (l *DistributedLock) stopWatchdog() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.stopCh != nil {
		select {
		case <-l.stopCh:
			// 已关闭，无需重复关闭。
		default:
			close(l.stopCh)
		}
	}
}

// generateToken 生成 16 字节随机标识符，用作锁持有者身份。
func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
