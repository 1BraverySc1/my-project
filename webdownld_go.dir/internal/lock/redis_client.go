package lock

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// redisClient 极简 Redis 客户端，基于 RESP2 协议直接通过 TCP 通信。
// 仅实现分布式锁所需的 PING / SET NX PX / EVAL / EXPIRE 四个命令。
type redisClient struct {
	mu     sync.Mutex // mu 保护 conn 的并发写入。
	conn   net.Conn   // conn 到 Redis 的 TCP 连接。
	reader *bufio.Reader // reader 缓冲读取 Redis 响应。
}

// newRedisClient 连接 Redis 服务器并验证连通性。
// addr 为 host:port，password 为空时不发送 AUTH，db 指定数据库编号。
func newRedisClient(addr, password string, db int) (*redisClient, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("Redis 连接失败 %s: %w", addr, err)
	}

	c := new(redisClient)
	c.conn = conn
	c.reader = bufio.NewReader(conn)

	// 认证。
	if password != "" {
		if _, err := c.do("AUTH", password); err != nil {
			conn.Close()
			return nil, fmt.Errorf("Redis AUTH 失败: %w", err)
		}
	}

	// 选择数据库。
	if db != 0 {
		if _, err := c.do("SELECT", strconv.Itoa(db)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("Redis SELECT 失败: %w", err)
		}
	}

	// 连通性验证。
	if _, err := c.do("PING"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("Redis PING 失败: %w", err)
	}

	return c, nil
}

// do 发送 RESP 命令并解析响应。
// args 为命令参数，第一个参数为命令名，后续为参数值。
func (c *redisClient) do(args ...string) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.writeCommand(args...); err != nil {
		return nil, err
	}
	return c.readResponse()
}

// writeCommand 将命令编码为 RESP 格式写入连接。
// 格式: *<参数数量>\r\n$<参数1长度>\r\n<参数1>\r\n...
func (c *redisClient) writeCommand(args ...string) error {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, a := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(a)))
		b.WriteString("\r\n")
		b.WriteString(a)
		b.WriteString("\r\n")
	}
	_, err := c.conn.Write([]byte(b.String()))
	return err
}

// readResponse 从连接读取并解析 RESP 响应。
func (c *redisClient) readResponse() (any, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("读取 Redis 响应失败: %w", err)
	}
	line = strings.TrimSuffix(line, "\r\n")

	switch line[0] {
	case '+':
		// 简单字符串: +OK
		return line[1:], nil
	case '-':
		// 错误: -ERR ...
		return nil, errors.New(line[1:])
	case ':':
		// 整数: :1
		n, err := strconv.ParseInt(line[1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("解析 Redis 整数响应失败: %s", line)
		}
		return n, nil
	case '$':
		// 批量字符串: $<长度>\r\n<数据>\r\n  或  $-1\r\n 表示 nil
		length, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("解析 Redis 批量字符串长度失败: %s", line)
		}
		if length < 0 {
			return nil, nil // nil 响应
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(c.reader, data); err != nil {
			return nil, fmt.Errorf("读取 Redis 批量字符串数据失败: %w", err)
		}
		// 消耗结尾的 \r\n
		if _, err := c.reader.Discard(2); err != nil {
			return nil, fmt.Errorf("跳过 Redis 响应结尾失败: %w", err)
		}
		return string(data), nil
	case '*':
		// 数组: *<数量>\r\n<元素>...
		count, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("解析 Redis 数组长度失败: %s", line)
		}
		if count < 0 {
			return nil, nil
		}
		result := make([]any, count)
		for i := 0; i < count; i++ {
			result[i], err = c.readResponse()
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("未知 Redis 响应类型: %s", line)
	}
}

// close 关闭与 Redis 的连接。
func (c *redisClient) close() error {
	return c.conn.Close()
}
