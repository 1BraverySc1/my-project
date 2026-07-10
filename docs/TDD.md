# 创作者素材管理平台 — 技术方案设计 (TDD)

| 版本 | 日期 | 作者 | 变更 |
|------|------|------|------|
| v1.0 | 2026-07-10 | 老张 | 初始版本 |

---

## 1. 技术选型

| 层级 | 选型 | 说明 |
|------|------|------|
| 开发语言 | Go 1.26 | 高并发、部署简单、编译为单二进制 |
| Web 框架 | Gin v1.12 | 轻量高性能，社区活跃 |
| ORM | GORM v1.31 | Go 生态主流 ORM，支持自动迁移 |
| 数据库 | MySQL 8.0 | Docker 部署，关系型数据存储 |
| 缓存 | Redis 7 | Docker 部署，用于后续限流/分布式锁 |
| 认证 | JWT (golang-jwt v5) | 无状态认证，无需服务端存储 Session |
| 配置管理 | Viper v1.19 | 支持 YAML 配置文件 |
| 加密 | bcrypt + SHA256 | 密码哈希 + 文件内容校验 |

---

## 2. 系统架构

```
┌─────────────────────────────────────────────────┐
│                 客户端 (Client)                   │
│     (CLI / Postman / Web / Mobile)              │
└──────────────────┬──────────────────────────────┘
                   │ HTTP/JSON
                   ▼
┌─────────────────────────────────────────────────┐
│              Gin Router / Middleware              │
│    ┌───────────┐  ┌──────────┐  ┌───────────┐   │
│    │ JWT Auth  │  │   CORS   │  │   Logger  │   │
│    └───────────┘  └──────────┘  └───────────┘   │
├─────────────────────────────────────────────────┤
│                  Handler 层                       │
│    ┌───────────┐  ┌─────────────────────────┐   │
│    │ Auth      │  │   Upload / File         │   │
│    │ Handler   │  │   Handler               │   │
│    └─────┬─────┘  └──────────┬──────────────┘   │
├──────────┼───────────────────┼──────────────────┤
│          ▼                   ▼                    │
│                Service 层                         │
│    ┌───────────┐  ┌─────────────────────────┐   │
│    │ Auth      │  │   Upload                │   │
│    │ Service   │  │   Service               │   │
│    │ (JWT签发)  │  │ (分片/秒传/合并/文件管理) │   │
│    └─────┬─────┘  └──────────┬──────────────┘   │
├──────────┼───────────────────┼──────────────────┤
│          ▼                   ▼                    │
│               Repository 层                       │
│    ┌───────────┐  ┌──────────┐  ┌───────────┐   │
│    │ User Repo │  │ File Repo│  │Upload Repo│   │
│    └─────┬─────┘  └────┬─────┘  └─────┬─────┘   │
├──────────┼──────────────┼──────────────┼────────┤
│          ▼              ▼              ▼          │
│              MySQL (GORM) + Redis                 │
└──────────────────────────────────────────────────┘
```

### 分层职责

| 层 | 职责 | 备注 |
|----|------|------|
| **Handler** | 参数校验、HTTP 响应、路由注册 | 不包含业务逻辑 |
| **Service** | 核心业务逻辑、事务控制 | 可测试，不依赖 HTTP |
| **Repository** | 数据访问、ORM 操作 | 单一职责，只做 CRUD |
| **Model** | 数据模型定义 | 与数据库表一一对应 |

---

## 3. 数据库设计

### 3.1 ER 图（文字描述）

```
┌───────────────┐     ┌──────────────────┐     ┌───────────────────┐
│     users     │     │  upload_sessions │     │      files       │
├───────────────┤     ├──────────────────┤     ├───────────────────┤
│ PK id         │◄────┤ FK user_id       │     │ PK id            │
│ username      │     │ PK upload_id     │◄────┤ FK user_id       │
│ password_hash │     │ file_id          │     │ file_id (唯一)    │
│ created_at    │     │ file_name        │     │ name             │
│ updated_at    │     │ file_size        │     │ size             │
└───────────────┘     │ content_hash     │     │ content_hash(唯一)│
                      │ chunk_size       │     │ chunk_size       │
                      │ total_chunks     │     │ total_chunks     │
                      │ received_chunks  │     │ status           │
                      │ status           │     │ file_path        │
                      │ created_at       │     │ created_at       │
                      │ updated_at       │     │ updated_at       │
                      └──────────────────┘     └───────────────────┘
```

### 3.2 users 表

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | BIGINT | PK, AUTO_INCREMENT | 用户 ID |
| username | VARCHAR(64) | UNIQUE, NOT NULL | 用户名 |
| password_hash | VARCHAR(256) | NOT NULL | bcrypt 哈希 |
| created_at | DATETIME | DEFAULT CURRENT_TIMESTAMP | 创建时间 |
| updated_at | DATETIME | ON UPDATE | 更新时间 |

索引：`username` 唯一索引

### 3.3 upload_sessions 表

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| upload_id | VARCHAR(64) | PK | 上传会话 ID |
| file_id | VARCHAR(64) | NOT NULL | 文件最终 ID |
| user_id | BIGINT | NOT NULL, INDEX | 所属用户 |
| file_name | VARCHAR(512) | NOT NULL | 原始文件名 |
| file_size | BIGINT | NOT NULL | 文件总大小 |
| content_hash | VARCHAR(64) | NOT NULL | SHA256 哈希 |
| chunk_size | INT | NOT NULL | 分片大小 |
| total_chunks | INT | NOT NULL | 总分片数 |
| received_chunks | TEXT | | JSON 数组，如 "[0,1,2]" |
| status | TINYINT | DEFAULT 0 | 0=上传中 1=已完成 2=已取消 |
| created_at | DATETIME | | 创建时间 |
| updated_at | DATETIME | | 更新时间 |

索引：`user_id`、`file_id`

### 3.4 files 表

| 字段 | 类型 | 约束 | 说明 |
|------|------|------|------|
| id | BIGINT | PK, AUTO_INCREMENT | 自增 ID |
| file_id | VARCHAR(64) | UNIQUE, NOT NULL | 文件唯一标识 |
| name | VARCHAR(512) | NOT NULL | 文件名 |
| size | BIGINT | NOT NULL | 文件大小 |
| chunk_size | INT | NOT NULL | 分片大小 |
| total_chunks | INT | NOT NULL | 总分片数 |
| content_hash | VARCHAR(64) | UNIQUE, NOT NULL | 文件哈希（用于秒传） |
| user_id | BIGINT | NOT NULL, INDEX | 所属用户 |
| status | TINYINT | DEFAULT 0 | 0=上传中 1=正常 |
| file_path | VARCHAR(1024) | | 文件存储路径 |
| created_at | DATETIME | | 创建时间 |
| updated_at | DATETIME | | 更新时间 |

索引：`user_id`、`content_hash`（唯一）、`file_id`（唯一）

---

## 4. API 接口契约

### 4.1 健康检查

```
GET /healthz

Response 200:
{
    "status": "ok"
}
```

### 4.2 用户注册

```
POST /api/auth/register
Content-Type: application/json

Request:
{
    "username": "zhangsan",
    "password": "123456"
}

Response 200:
{
    "ok": true,
    "user_id": 1,
    "username": "zhangsan"
}

Response 409:
{
    "error": "用户名已存在"
}
```

### 4.3 用户登录

```
POST /api/auth/login
Content-Type: application/json

Request:
{
    "username": "zhangsan",
    "password": "123456"
}

Response 200:
{
    "ok": true,
    "user_id": 1,
    "username": "zhangsan",
    "access_token": "eyJ...",
    "refresh_token": "eyJ..."
}
```

### 4.4 刷新令牌

```
POST /api/auth/refresh
Content-Type: application/json

Request:
{
    "refresh_token": "eyJ..."
}

Response 200:
{
    "ok": true,
    "access_token": "eyJ..."
}
```

### 4.5 获取当前用户

```
GET /api/auth/me
Authorization: Bearer <access_token>

Response 200:
{
    "ok": true,
    "user_id": 1,
    "username": "zhangsan"
}
```

### 4.6 初始化上传

```
POST /api/uploads/init
Authorization: Bearer <access_token>
Content-Type: application/json

Request:
{
    "name": "素材包.zip",
    "size": 104857600,
    "hash": "sha256hex..."
}

Response 200 (正常):
{
    "ok": true,
    "data": {
        "upload_id": "abc123...",
        "file_id": "def456...",
        "chunk_size": 4194304,
        "total_chunks": 25,
        "instant_upload": false
    }
}

Response 200 (秒传):
{
    "ok": true,
    "data": {
        "file_id": "def456...",
        "instant_upload": true
    }
}
```

### 4.7 查询上传进度

```
GET /api/uploads/:id/status
Authorization: Bearer <access_token>

Response 200:
{
    "ok": true,
    "received": [0, 1, 2, 5, 6],
    "total_chunks": 25
}
```

### 4.8 上传分片

```
POST /api/uploads/:id/chunks/:index
Authorization: Bearer <access_token>
Content-Type: application/octet-stream

Body: <分片二进制数据>

Response 200:
{
    "ok": true
}
```

### 4.9 完成上传

```
POST /api/uploads/:id/complete
Authorization: Bearer <access_token>

Response 200:
{
    "ok": true,
    "data": {
        "id": 1,
        "file_id": "def456...",
        "name": "素材包.zip",
        "size": 104857600,
        "chunk_size": 4194304,
        "total_chunks": 25,
        "content_hash": "sha256hex...",
        "user_id": 1,
        "status": 1,
        "created_at": "2026-07-10T12:00:00Z"
    }
}
```

### 4.10 文件列表

```
GET /api/files
Authorization: Bearer <access_token>

Response 200:
{
    "ok": true,
    "data": [
        {
            "id": 1,
            "file_id": "def456...",
            "name": "素材包.zip",
            "size": 104857600,
            "status": 1,
            "created_at": "2026-07-10T12:00:00Z"
        }
    ]
}
```

### 4.11 下载文件

```
GET /api/files/:id/download
Authorization: Bearer <access_token>

Response 200: <文件二进制流>
Content-Disposition: attachment; filename="素材包.zip"
```

### 4.12 删除文件

```
DELETE /api/files/:id
Authorization: Bearer <access_token>

Response 200:
{
    "ok": true
}
```

---

## 5. 核心流程时序图

### 5.1 分片上传正常流程

```
Client                Handler              Service              MySQL/Disk
  │                      │                    │                    │
  │  POST /init          │                    │                    │
  │  {name,size,hash}    │                    │                    │
  │─────────────────────►│  查content_hash    │                    │
  │                      │───────────────────►│───────────────────►│
  │                      │                    │◄──── 未命中 ──────┤
  │                      │  创建UploadSession │                    │
  │                      │───────────────────►│───────────────────►│
  │  {upload_id,         │◄───────────────────┤                    │
  │   chunk_size,        │◄───────────────────┤                    │
  │   total_chunks}      │                    │                    │
  │◄─────────────────────┤                    │                    │
  │                      │                    │                    │
  │── ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─│                    │
  │  循环上传每个分片      │                    │                    │
  │── ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─│                    │
  │                      │                    │                    │
  │  POST /chunks/0      │                    │                    │
  │  <binary>            │                    │                    │
  │─────────────────────►│  校验upload_id     │                    │
  │                      │───────────────────►│                    │
  │                      │                    │  写入分片文件       │
  │                      │                    │───────────────────►│ Disk
  │                      │                    │  更新received      │
  │                      │                    │───────────────────►│ MySQL
  │  {ok:true}           │◄───────────────────┤                    │
  │◄─────────────────────┤                    │                    │
  │                      │                    │                    │
  │  ...重复上传剩余分片...│                    │                    │
  │                      │                    │                    │
  │  POST /complete      │                    │                    │
  │─────────────────────►│  合并分片          │                    │
  │                      │───────────────────►│ 读取所有分片       │
  │                      │                    │───────────────────►│ Disk
  │                      │                    │ 写入完整文件       │
  │                      │                    │───────────────────►│ Disk
  │                      │                    │ SHA256校验         │
  │                      │                    │ 清理分片文件       │
  │                      │                    │───────────────────►│ Disk
  │                      │                    │ 创建File记录       │
  │                      │                    │───────────────────►│ MySQL
  │                      │                    │ 删除UploadSession  │
  │                      │                    │───────────────────►│ MySQL
  │  {ok:true, file}     │◄───────────────────┤                    │
  │◄─────────────────────┤                    │                    │
```

### 5.2 秒传流程

```
Client                Handler              Service              MySQL
  │                      │                    │                    │
  │  POST /init          │                    │                    │
  │  {name,size,hash}    │                    │                    │
  │─────────────────────►│  查content_hash    │                    │
  │                      │───────────────────►│───────────────────►│
  │                      │                    │◄── 命中 File ─────┤
  │  {instant_upload:    │◄───────────────────┤                    │
  │   true, file_id}     │                    │                    │
  │◄─────────────────────┤                    │                    │
```

### 5.3 断点续传流程

```
Client                Handler              Service              MySQL
  │                      │                    │                    │
  │  POST /init          │                    │                    │
  │  ...                  │                    │                    │
  │  (正常初始化上传)      │                    │                    │
  │                      │                    │                    │
  │── 上传到分片3中断 ────│                    │                    │
  │                      │                    │                    │
  │  GET /status         │                    │                    │
  │─────────────────────►│  查询received      │                    │
  │                      │───────────────────►│───────────────────►│
  │  {received:[0,1,2,3],│◄───────────────────┤                    │
  │   total_chunks:25}   │                    │                    │
  │◄─────────────────────┤                    │                    │
  │                      │                    │                    │
  │  POST /chunks/4      │  (跳过0-3，从4继续)│                    │
  │  ...                  │                    │                    │
```

---

## 6. 关键设计决策

### 6.1 分片大小
- 默认 **4MB**（4194304 字节）
- 4MB 是业界常用值（OSS、S3 等云存储的分片下限）
- 太小→请求次数太多，太大→断点续传粒度粗
- 可通过配置文件调整

### 6.2 文件存储路径
```
data/
├── chunks/{upload_id}/{index}    # 临时分片文件
└── files/{file_id}               # 合并后的完整文件
```
- 分片按 upload_id 隔离
- 合并后清理分片目录
- file_id 取 content_hash 前 32 位，天然防冲突

### 6.3 哈希校验
- 初始化时客户端提交文件 SHA256
- 合并完成后服务端重新计算完整文件 SHA256
- 不一致则删除已合并文件，返回错误
- 防止传输过程中数据损坏

### 6.4 JWT 无状态认证
- 服务端不存储 Token
- 用户信息编码在 JWT 中，中间件解析后注入 Context
- Refresh Token 用于静默续期，降低用户打扰

---

## 7. 风险 & 应对

| 风险 | 影响 | 应对措施 |
|------|------|----------|
| 磁盘空间不足 | 分片/文件无法写入 | 监控磁盘使用率，设定阈值告警 |
| 并发分片写入冲突 | received_chunks 更新覆盖 | 后续引入 Redis 分布式锁 |
| 合并时服务重启 | 分片丢失 | 合并操作为幂等设计，支持重试 |
| 大文件 hash 计算慢 | 客户端等待时间长 | 已纳入 PRD 共识，前端用 Web Worker 处理 |

---

*文档结束*
