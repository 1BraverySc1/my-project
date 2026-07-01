# CloudRaft Drive（Go + 自研 Raft + gRPC 分布式存储）

项目目录：`/root/projects/webdownld_go.dir`

本项目已将自研 Raft 实现迁入当前工程：

- 网盘服务：`cmd/server`
- Raft KV 集群：`raftImpl/cmd/raftnode`
- Raft 内核：`raftImpl/internal/raft`

## 功能总览

### 用户系统（新增）
- 用户注册与登录：白底轻蓝渐变 UI，JWT 双令牌鉴权（Access Token 2h + Refresh Token 7d）。
- 管理员账号：通过环境变量 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 配置，服务启动时自动创建，无需注册即可直接登录。
- 密码安全：bcrypt 哈希存储，`golang.org/x/crypto` 保证密码安全。
- MySQL 持久化：用户信息、订单记录、会员套餐均存储于 MySQL，自动建表。

### 分布式锁（新增）
- Redis 分布式锁：基于 RESP2 协议自研极简 Redis 客户端，支持 SET NX PX 原子获取锁 + Lua 脚本安全释放。
- 看门狗机制：每 ttl/3 通过 Lua 校验 token 后执行 PEXPIRE，防止旧持有者续期其他实例的新锁。
- 多实例部署：替代进程内 sync.Map，保证跨实例上传会话互斥。

### 会员充值（新增）
- 支付宝支付：对接支付宝电脑网站支付，支持沙箱/生产模式切换。
- 订单系统：pending → paid → expired/refunded 完整订单生命周期。
- RabbitMQ Topic Exchange：真实 RabbitMQ 持久化 Exchange/Queue、手动 ACK、失败重入队和 Publisher Confirm。

### 原有网盘功能
- 元数据强一致：通过内置 `raftImpl` 的 Raft KV 保存文件元数据、上传会话、分片状态和目录索引。
- Leader 读写：Raft HTTP 层仅允许 leader 处理 KV 读写，网盘侧在配置的 HTTP 节点中使用指数退避自动重试到 leader。
- 写入落盘确认：Raft 写请求等待日志提交并应用到 Pebble 状态机后再返回。
- 秒传：基于文件名哈希 + 文件大小建立索引，命中完整文件后直接返回。
- 断点续传：上传会话与每个 chunk 完成状态分别写入 Raft，失败后只补传缺失分片。
- 分片上传：服务端固定 worker pool + 有界队列处理 chunk 写入，并校验 chunk 大小；Worker 池支持优雅关闭；引入 sync.Pool 复用分片 buffer 与序列化缓冲区，降低 GC 压力。
- 分片下载：客户端获取 manifest 后并行下载 chunk 并在浏览器本地合并；服务端使用流式传输避免大文件 OOM。
- 块级去重：每个 chunk 计算 SHA256 内容哈希，已存在内容直接复用；引入 Bloom Filter 加速去重预判。
- 文件删除：支持通过 API 删除文件元数据、秒传索引和存储分片。
- 用户数据隔离：文件 ID、秒传索引、列表、下载、删除和上传会话均按 JWT 用户隔离；管理员可管理全部文件。
- 安全删除：通过 chunk hash 分布式锁检查完整文件和进行中上传的引用，仅清理无引用 chunk。
- 可观测性：结构化 JSON 日志（slog）、统一 INFO 日志宏、requestID 追踪、Prometheus 风格 Metrics 端点、pprof 性能分析。
- 健康检查：`/healthz` 探活、`/readyz` 就绪检查（含 Raft 连通性验证）。
- API 鉴权：网盘与支付业务接口统一要求 Access Token；Refresh Token 只能用于刷新访问令牌。
- 优雅关闭：捕获 SIGINT/SIGTERM，超时等待进行中的请求完成后退出。

## 关键修复

- 修复网盘 API 使用空鉴权中间件的问题，上传、下载、列表和删除接口现统一校验 JWT Access Token。
- 修复支付路由向 JWT 中间件传入 `nil` 导致请求 panic 的问题，支付业务接口复用主服务的 JWT 实例。
- JWT Claims 增加 `token_type`，严格区分 Access Token 与 Refresh Token，防止刷新令牌直接访问业务 API。
- 本次令牌格式升级后，旧版未包含 `token_type` 的 JWT 会被拒绝，客户端需要重新登录获取新令牌。
- 目录索引从 `catalog:files` 全量读改写改为 `catalog:file:<fileID>` 前缀扫描，避免多实例并发追加时丢更新。
- 上传分片状态从整份 `UploadSession` 覆盖写改为 `uploadchunk:<uploadID>:<index>` 单独写，降低并发 chunk 上传互相覆盖风险。
- 秒传索引升级为 `idx:ownername:<ownerHash>:<nameHash>:<size>`，避免跨用户秒传泄露及同名不同大小覆盖。
- Raft GET/PUT/DELETE 只由 leader 承担，客户端遇到 `409 not leader` 使用指数退避 + jitter 重试。
- Raft `Submit` 等待 Pebble apply 完成后返回，避免 PUT 成功后立刻 GET 读不到。
- 服务端限制 chunk 请求体大小，并校验每个分片实际大小与上传计划一致。
- 块存储落盘使用临时文件 + 原子硬链接，避免同 hash 分片并发写入时覆盖已有文件。
- ChunkWorkerPool：worker goroutine 检测 ctx 取消后跳过任务，避免 resultCh 无人接收导致泄漏；支持 `Shutdown()` 优雅关闭。
- 内存与 GC 优化：分片上传的 body buffer 通过 `sync.Pool` 复用，避免高并发下频繁分配 4MB 大块内存导致 GC 停顿；JSON 序列化路径以池化 `bytes.Buffer` 替代 `json.Marshal` 的临时分配，降低 Raft 写入链路上的 GC 开销。
- downloadChunk：从 `os.ReadFile` 全量读入内存改为流式 `io.ReadCloser` + `Content-Length` 传输。
- findChunk：使用内存 Bloom Filter 预判分片是否已存在，减少磁盘 `os.Stat` 扫描。
- RaftClient：从固定 8 次轮询改为指数退避（50ms→2s）+ 随机抖动，最多 10 次重试。
- requestID：每个请求注入唯一 ID 并写入响应头，贯穿日志链路。
- 支付回调校验签名、`app_id`、交易号和实付金额；待支付订单交易号使用 `NULL`。
- 存储读删严格使用元数据中的 `storage_id`；Raft 客户端按 `leader_id` 映射 HTTP Leader。

## 启动 Raft 集群

在当前项目内启动 3 个 Raft 节点：

```bash
cd /root/projects/webdownld_go.dir/raftImpl
go build -o bin/raftnode ./cmd/raftnode

./bin/raftnode -id=1 -raft=127.0.0.1:9000 -http=127.0.0.1:8080 -bootstrap -persist=./data/n1/state.json -db=./data/n1/pebble
./bin/raftnode -id=2 -raft=127.0.0.1:9001 -http=127.0.0.1:8081 -join=127.0.0.1:8080 -persist=./data/n2/state.json -db=./data/n2/pebble
./bin/raftnode -id=3 -raft=127.0.0.1:9002 -http=127.0.0.1:8082 -join=127.0.0.1:8080 -persist=./data/n3/state.json -db=./data/n3/pebble
```

Raft HTTP 接口：

- `GET /status`：查看节点状态和 leader hint。
- `PUT /kv/:key`：写入 key/value，仅 leader 成功。
- `GET /kv/:key`：读取 key/value，仅 leader 成功。
- `GET /kv-prefix/*prefix`：按前缀扫描 KV，仅 leader 成功。
- `DELETE /kv/:key`：删除 key，仅 leader 成功。
- `POST /cluster/join`：动态加入节点。

## 启动网盘服务

服务启动前需准备 MySQL、Redis 与 RabbitMQ。例如：

```bash
docker run -d --name cloudraft-rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:management
```

```bash
cd /root/projects/webdownld_go.dir
go mod tidy
go run ./cmd/server
```

默认访问：`http://127.0.0.1:8188`

### 生产部署（Nginx 前置）

```bash
# 使用内置 nginx 配置（TLS / 限流 / 负载均衡 / 静态文件直出）
cp nginx.conf /etc/nginx/nginx.conf
nginx -s reload
```

Nginx 职责：TLS 卸载、静态资源直出（`/assets/*` 不经过 Go）、限流、负载均衡到多网盘实例。

## 启动存储节点

```bash
cd /root/projects/webdownld_go.dir
go run ./cmd/storagenode -grpc=:9001 -data=./data/storage-n1 &
go run ./cmd/storagenode -grpc=:9002 -data=./data/storage-n2 &
go run ./cmd/storagenode -grpc=:9003 -data=./data/storage-n3 &
```

## 环境变量

### 服务基础配置
- `APP_ADDR`：默认 `:8188`
- `DATA_ROOT`：默认 `./data`
- `STORAGE_NODE_IDS`：默认 `node-a,node-b,node-c`
- `RAFT_HTTP_NODES`：默认 `127.0.0.1:8080,127.0.0.1:8081,127.0.0.1:8082`
- `STORAGE_NODE_ADDRS`：默认 `127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003`
- `CHUNK_SIZE_MB`：默认 `4`
- `MAX_CONCURRENT_CHUNK_WRITES`：默认 `64`

### 用户系统配置
- `MYSQL_DSN`：MySQL 连接字符串，默认 `root:root@tcp(127.0.0.1:3306)/cloudraft?parseTime=true&charset=utf8mb4`
- `ADMIN_USERNAME`：管理员登录用户名，默认 `admin`（服务启动时自动创建，无需注册）
- `ADMIN_PASSWORD`：管理员登录密码，默认 `admin123`（生产环境务必修改）
- `JWT_SECRET`：JWT 签名密钥，生产环境必须修改

### 分布式锁配置
- `REDIS_ADDR`：Redis 服务器地址，默认 `127.0.0.1:6379`
- `REDIS_PASSWORD`：Redis 认证密码，默认空
- `REDIS_DB`：Redis 数据库编号，默认 `0`
- `LOCK_TTL_SECONDS`：分布式锁 TTL 秒数，默认 `30`（看门狗每 TTL/3 秒自动续期）
- `JWT_ACCESS_TTL_HOURS`：访问令牌有效期（小时），默认 `2`
- `JWT_REFRESH_TTL_DAYS`：刷新令牌有效期（天），默认 `7`

### 消息队列配置
- `RABBITMQ_URL`：RabbitMQ AMQP 连接地址，默认 `amqp://guest:guest@127.0.0.1:5672/`

### 支付宝支付配置
- `ALIPAY_APP_ID`：支付宝应用 ID
- `ALIPAY_PRIVATE_KEY`：应用私钥（PEM 格式）
- `ALIPAY_PUBLIC_KEY`：支付宝公钥（PEM 格式）
- `ALIPAY_NOTIFY_URL`：异步通知回调地址
- `ALIPAY_RETURN_URL`：支付完成同步跳转地址
- `ALIPAY_IS_PRODUCTION`：是否生产模式（`false` 为沙箱）

## API 接口

除注册、登录、刷新令牌、支付宝回调和健康检查外，业务 API 请求必须携带：

```http
Authorization: Bearer <access_token>
```

Refresh Token 仅可提交到 `/api/auth/refresh`，不能访问网盘或支付业务接口。

### 用户认证
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/auth/register` | 用户注册 |
| POST | `/api/auth/login` | 登录获取 JWT 令牌 |
| GET | `/api/auth/me` | 获取当前用户信息 |
| POST | `/api/auth/refresh` | 刷新访问令牌 |

### 会员充值
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/payment/plans` | 获取会员套餐列表（需要 Access Token） |
| POST | `/api/payment/order` | 创建充值订单（需要 Access Token，返回支付链接） |
| POST | `/api/payment/notify` | 支付宝异步通知回调 |
| GET | `/api/payment/return` | 支付完成同步跳转 |

### 网盘操作
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/files` | 列出全部文件（需要 Access Token） |
| DELETE | `/api/files/:fileID` | 删除文件及分片（需要 Access Token） |
| POST | `/api/uploads/init` | 初始化上传会话（需要 Access Token） |
| GET | `/api/uploads/:uploadID/status` | 查询上传进度（需要 Access Token） |
| POST | `/api/uploads/:uploadID/chunks/:index` | 上传分片（需要 Access Token） |
| POST | `/api/uploads/:uploadID/complete` | 完成上传（需要 Access Token） |
| GET | `/api/files/:fileID/manifest` | 获取下载清单（需要 Access Token） |
| GET | `/api/files/:fileID/chunks/:index` | 流式下载分片（需要 Access Token） |

### 健康检查与观测
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/healthz` | 探活检查 |
| GET | `/readyz` | 就绪检查（含 Raft 连通） |
| GET | `/metrics` | 累积请求指标 |
| GET | `/debug/pprof/` | 性能分析 |

## 元数据键空间
- `uploadchunk:<uploadID>:<index>`：单个分片完成状态。
- `file:<fileID>`：完整文件元数据。
- `idx:ownername:<ownerHash>:<nameHash>:<size>`：按用户隔离的秒传索引。
- `catalog:file:<fileID>`：目录索引项。

## 验证

所有主项目测试统一存放在 `test/`；独立 Raft module 的测试存放在 `raftImpl/test/`。生产源码目录不放置 `_test.go` 文件。

```bash
cd /root/projects/webdownld_go.dir
go test ./...

cd /root/projects/webdownld_go.dir/raftImpl
go test ./...
```

## 简历项目描述

**项目名称：CloudRaft Drive — 基于自研 Raft 的全栈分布式云盘系统**

项目介绍：独立设计与实现基于 Go + Gin 的全栈分布式云盘系统，从零实现 Raft 分布式 KV（gRPC + Pebble）作为元数据一致性引擎，集成 JWT 用户认证、支付宝会员充值、Topic 事件总线与 MySQL 持久化，构建”浏览器（白底轻蓝渐变 UI）→ Nginx → 网盘服务 → gRPC 存储集群”多层级架构，完整覆盖用户注册登录、会员支付、分片上传/下载、秒传去重、断点续传、内容寻址路由、流式传输与全链路可观测性。

主要工作：

自研 Raft 共识引擎与 KV 层：从零实现 Raft 共识协议，包含随机超时 Leader 选举、日志复制、多数派提交、动态 Join 及 Leader Hint 透明重定向。引入”冲突任期 + 冲突首索引”回退机制，将日志追齐复杂度从 O(N) 降至 O(log N)；以 Pebble LSM 作为状态机实现高性能 KV 读写，所有元数据（文件、上传会话、分片状态、秒传索引、目录索引）通过 Raft 达成强一致，仅 Leader 承担读写避免脏读。

JWT 用户认证与会员系统：自实现 HMAC-SHA256 签名 JWT 双令牌机制（Access Token 2h + Refresh Token 7d），bcrypt 密码哈希存储；MySQL 持久化用户、订单与会员套餐数据，支持注册、登录、令牌刷新、会员状态查询。前端白底轻蓝渐变主题，纯白圆角卡片 UI，登录/注册独立页面。

支付宝支付与 Topic 事件总线：对接支付宝电脑网站支付 API，自实现 RSA-SHA256 请求签名与回调验签，并校验应用 ID 与实付金额。接入真实 RabbitMQ Topic Exchange，以持久化消息、手动 ACK 和 Publisher Confirm 驱动订单事件流。

内容寻址存储集群：设计 gRPC 流式存储节点服务（SaveChunk / ReadChunk / DeleteChunk），基于 FNV-1a 哈希实现内容寻址路由，将去重与定位收敛为 O(1)；上传时分片流式转发至目标节点，以临时文件 + 原子硬链接安全落盘并实时校验 SHA256；下载时 gRPC 流式回传适配为 io.ReadCloser，实现零拷贝流式响应。

秒传、断点续传与分片并行传输：基于文件名哈希 + 文件大小实现秒传索引，命中即可跳过上传。分片上传以独立键写入 Raft 追踪完成状态，支持断点续传与幂等重试；sync.Pool 复用分片 buffer 与序列化缓冲区，降低高并发 GC 压力。前端采用并行 Worker 并发拉取分片并触发浏览器流式下载，配合进度条实时反馈。

并发治理与数据一致性：以固定 Worker Pool + 有界队列控制分片写入并发，context 取消检测防止协程泄漏；上传会话加锁协作分片独立键写入，消除多客户端并发状态覆盖；目录索引以 Raft 前缀扫描替代全量读改写，避免并发追加丢失更新。

可观测性与生产化部署：统一 INFO 日志宏 + 结构化 JSON 日志 + 请求级 Trace ID 实现全链路追踪；暴露 Prometheus 指标端点并集成 pprof 在线性能分析；提供 /healthz 探活与 /readyz 就绪检查（含 Raft 集群连通性验证）；引入内存布隆过滤器加速分片去重预判；Raft 客户端采用指数退避 + 随机抖动重试；Nginx 前置提供 TLS 卸载、限流、静态资源直出与负载均衡；信号驱动的优雅关闭，超时等待进行中请求完成后退出。




## 面试题集

### 一、Go 语言基础

<details>
<summary><b>Q1: Go 的 slice 底层结构是什么？append 扩容机制是怎样的？</b></summary>

**答：** slice 底层是一个 `reflect.SliceHeader` 结构体，包含三个字段：`Data`（指向底层数组的指针）、`Len`（当前长度）、`Cap`（容量）。

append 扩容规则（Go 1.18+）：
- 如果 `cap < 256`，新容量约为 `oldCap * 2`
- 如果 `cap >= 256`，以约 12.5% 的速率递增，逐步收敛到约 1.25 倍
- 如果期望容量大于计算出的新容量，则直接用期望容量
- 分配新底层数组后，把旧数据 copy 过去，返回的新 slice 指向新数组

**项目关联：** `uploadStatus` 中 `make([]int, 0, len(ss.Received))` 预分配了容量，避免多次扩容。
</details>

<details>
<summary><b>Q2: Go 的 map 是并发安全的吗？不安全的 map 并发写会怎样？</b></summary>

**答：** 不是。Go 内置 map 在并发写（包括一个写 + 一个读）时会导致 `fatal error: concurrent map writes`，直接 crash 进程。

解决方案：
- `sync.RWMutex` 保护 map 访问
- `sync.Map`（适合读多写少 / key 集合稳定的场景）
- 用 channel 将写操作串行化到单个 goroutine

**项目关联：** 本项目的 `uploadLocks` 用 `sync.Map` 存储每个上传会话的互斥锁，利用其 `LoadOrStore` 实现无锁创建；Bloom Filter 的 `bits` 用 `sync.RWMutex` 保护。
</details>

<details>
<summary><b>Q3: defer 的执行顺序是怎样的？defer 和 return 的先后顺序？</b></summary>

**答：** defer 是 LIFO 栈顺序（后进先出）。执行顺序：

1. 先执行 `return` 表达式（计算返回值）
2. 然后按 LIFO 顺序执行所有 defer
3. 最后函数返回

如果 defer 中修改了**命名返回值**，会影响最终返回值；但匿名返回值不受影响。defer 的参数在 defer 语句执行时就已求值，而非在延迟调用时。

**项目关联：** 本项目多处用 `defer` 释放资源，如 `defer rc.Close()`、`defer lock.Unlock()`。`AccessLogMiddleware` 原本用 `c.Next()` 之后记录日志（panic 时丢日志），修复后用 `defer` 保证无论正常还是 panic 都会记录。
</details>

<details>
<summary><b>Q4: Go 的 interface 底层是怎么实现的？空 interface 和非空 interface 有什么区别？</b></summary>

**答：**

interface 底层是两个指针：`(type, data)`。
- `type`：指向类型元数据的指针（`_type` 结构体，包含类型大小、哈希、方法集等）
- `data`：指向实际值的指针

**空 interface（`interface{}` / `any`）：** 只存储 `(dynamicType, data)`，没有任何方法约束。

**非空 interface（如 `io.Reader`）：** `type` 部分指向 `itab` 结构体，包含 `_type`（具体类型）+ `interfacetype`（接口类型）+ 方法表（fun 数组）。将具体类型赋值给 interface 时，编译器生成 `itab`，检查具体类型是否实现了接口所有方法。

**nil 判断陷阱：** `var r io.Reader = (*os.File)(nil)`，此时 `r != nil`，因为 interface 的 type 字段不为 nil，只是 data 为 nil。

**项目关联：** 项目中 `sync.Pool` 的 `New` 返回 `any`（空 interface）；`chunkBufPool.Put(&buf)` 传入 `*[]byte` 在取出时需要类型断言 `.(*[]byte)`。
</details>

<details>
<summary><b>Q5: string 和 []byte 互转会发生什么？如何零拷贝转换？</b></summary>

**答：** Go 中 `string` 是不可变的（immutable），底层是 `(Data *byte, Len int)`，`[]byte` 底层是 `(Data *byte, Len int, Cap int)`。

标准互转 `string(b)` 和 `[]byte(s)` 会**发生内存拷贝**，因为 string 不可变，编译器必须确保转换后的 []byte 的修改不会影响原 string。

零拷贝转换可用 `unsafe.Pointer`，但这是**不安全**的——修改转换后的 []byte 会破坏 string 的不可变性，可能导致运行时 panic 或数据竞态。只应在只读场景下使用（如 `strings.Builder` 内部）。

**项目关联：** `chunkBufPool` 中 `buf := GetChunkBuf()[:0]`，通过 slice 表达式 `[:0]` 重置长度但保留底层数组容量，避免重新分配。
</details>

### 二、Go 并发与内存

<details>
<summary><b>Q6: goroutine 的调度模型（GMP）是什么？</b></summary>

**答：**

**G（Goroutine）：** 用户态轻量级协程，初始栈 2KB，可动态扩缩。

**M（Machine / OS Thread）：** 操作系统线程，真正执行计算。

**P（Processor）：** 逻辑处理器，持有本地运行队列（LRQ），是 G 和 M 之间的调度中介。P 的数量由 `GOMAXPROCS` 决定，默认等于 CPU 核数。

调度流程：
1. 每个 P 有一个本地 runqueue（最多 256 个 G）
2. M 绑定 P 后从 P 的本地队列取 G 执行
3. 本地队列空时，从全局队列取（加锁），或从其他 P 的队列**偷一半**（work-stealing）
4. G 阻塞在 syscall 时，M 被挂起，P 会绑定到新 M；G 阻塞在 channel/网络 IO 时（用户态阻塞），G 被挂起到等待队列，M 继续执行其他 G

**项目关联：** `ChunkWorkerPool` 启动了固定数量的 worker goroutine，通过有界 channel 传递任务。Worker 数量默认 64，超过 P 数量时会产生额外调度开销。
</details>

<details>
<summary><b>Q7: channel 的底层结构是什么？无缓冲和有缓冲 channel 的区别？</b></summary>

**答：**

channel 底层是 `hchan` 结构体，包含：
- `buf`：环形队列（有缓冲时使用）
- `sendx` / `recvx`：发送和接收的索引
- `recvq` / `sendq`：等待接收 / 等待发送的 goroutine 队列（sudog 链表）
- `lock`：互斥锁保护

**无缓冲 channel：** 发送方必须等接收方准备好（同步），发送的 G 会阻塞在 `sendq`，直到有 G 来接收。

**有缓冲 channel：** 缓冲区未满时发送直接写入环形缓冲区并返回；满时发送方阻塞。接收方在缓冲区非空时直接取，空时阻塞。

**已关闭的 channel：** 从已关闭的 channel 读取会得到零值 + `ok=false`；向已关闭的 channel 写会 panic。

**项目关联：** `ChunkWorkerPool.jobs` 是有缓冲 channel（`workers * 4`），`Shutdown()` 中 `close(p.jobs)` 通知所有 worker 退出，worker 通过 `for job := range pool.jobs` 在 channel 关闭后自动结束循环。
</details>

<details>
<summary><b>Q8: sync.Mutex 的底层实现是怎样的？正常模式和饥饿模式有什么区别？</b></summary>

**答：**

Go 的 `sync.Mutex` 基于 CAS + 信号量实现：

**正常模式（Normal）：** 新到达的 G 与等待队列的 G 一起竞争锁。等待者先入 FIFO 队列（`sema` 信号量）。新到达的 G 具有优势（自旋 + 可能先抢到），避免频繁上下文切换。

**饥饿模式（Starvation）：** 等待超过 1ms 的 G 触发饥饿模式。锁直接交给队列头的 G，新到达的 G 不自旋，直接入队尾。当队列头的 G 是最后一个等待者或其等待时间 < 1ms 时切回正常模式。

**自旋条件：** 多核 + P 的本地队列空 + 锁已被持有 + 自旋次数 < 4。

**项目关联：** 大量使用 `sync.Mutex`（`raftClient.mu`、`storage.Service.mu`、Bloom Filter 的 `mu`）。`getOrDial` 原先持写锁时拨号（阻塞 3s），修复后在锁外拨号再持锁写 map，减少了锁持有时间。
</details>

<details>
<summary><b>Q9: sync.Pool 的作用和原理？什么场景适合用？</b></summary>

**答：**

**作用：** 复用临时对象，减少 GC 压力。Pool 里的对象在每次 GC 时会被清理。

**原理：** 每个 P 有私有的 poolLocal（无锁访问），包含一个 private（单个对象）和一个 shared（双向链表）。Get 时先从 private 取 → 再从 shared 头部取 → 再从其他 P 的 shared 尾部偷 → 最后调用 New 创建。

**适用场景：** 高频分配 + 短期使用 + 对象大小较一致。不适合长期持有或需要池中对象持久化的场景。

**项目关联：** `chunkBufPool` 复用分片上传的 4MB buffer，避免高并发下频繁分配大块内存触发 GC；`jsonBufPool` 复用 JSON 序列化的 `bytes.Buffer`，降低 Raft 写入路径上的 GC 分配。`PutChunkBuf` 检查 `cap(buf) >= chunkSize`，只归还足够大的 buffer。
</details>

<details>
<summary><b>Q10: context 包的设计目的？取消是如何传播的？</b></summary>

**答：**

**设计目的：**
1. **超时控制：** `context.WithTimeout` / `context.WithDeadline`
2. **取消传播：** `context.WithCancel`，父 cancel 时所有子 context 都会被取消
3. **值传递：** `context.WithValue`，跨调用链传递 request-scope 的数据（如 traceID）

**取消传播机制：** context 形成树状结构。当父 context 取消时，递归关闭所有子 context 的 `Done()` channel。`ctx.Done()` 返回一个只读 channel，关闭后 `<-ctx.Done()` 立即返回。

**最佳实践：** context 应作为函数的第一个参数；不要把 context 存到 struct 中长期持有；不要用 context 传业务参数。

**项目关联：** `chunkPool.Submit(ctx, task)` 将请求的 ctx 传入 worker，worker 通过 `job.ctx.Err() != nil` 检测取消并跳过任务；`raftClient.Get(ctx, key)` 在重试等待时 select 在 `ctx.Done()` 上，客户端断开时立即返回。
</details>

### 三、Raft 与分布式一致性

<details>
<summary><b>Q11: Raft 的 Leader 选举流程是怎样的？为什么要随机超时？</b></summary>

**答：**

**选举流程：**
1. Follower 在 election timeout 内未收到 Leader 心跳，转为 Candidate
2. Candidate 递增 term，给自己投票，向所有节点并发发 `RequestVote`
3. 获得多数派投票 → 成为 Leader，立刻发送心跳（空的 AppendEntries）
4. 发现更高 term → 退为 Follower
5. 选举超时（split vote）→ 递增 term 重新选举

**随机超时的作用：** 避免多个 Follower 同时超时、同时发起选举导致 split vote。本项目使用 `300 + rand.Intn(250)` ms 的随机超时。

**项目关联：** `raft.go` 的 `ticker_loop` 每 30ms tick 一次，`start_election` 中并发向所有 peer 发 `RequestVote` RPC，`become_leader` 后立即发心跳广播。
</details>

<details>
<summary><b>Q12: Raft 的日志复制流程？Leader 如何保证 Follower 和自己的日志一致？</b></summary>

**答：**

1. Leader 收到客户端请求，追加本地日志
2. Leader 并发向所有 Follower 发 `AppendEntries`（包含 `prevLogIndex` + `prevLogTerm` + `entries`）
3. Follower 校验 `prevLogIndex` 处日志的 term 是否匹配：
   - **匹配：** 追加 entries，返回 success
   - **不匹配：** 返回 conflict 信息（`conflictTerm` + `conflictIndex`）
4. Leader 收到多数派确认后，推进 `commitIndex`，应用到状态机

**日志不一致修复：** Leader 维护 `nextIndex[follower]`。被拒绝时根据 conflict 信息回退：
- 有 `conflictTerm`：跳到 Leader 日志中该 term 的最后一条之后
- 无 `conflictTerm`：直接跳到 `conflictIndex`

这比逐条回退的 O(N) 效率高，本项目实现了这个优化（`adjust_send_idx_after_append_rejected_locked`）。
</details>

<details>
<summary><b>Q13: 为什么要等到日志 apply 到状态机才返回客户端？读操作为什么也走 Leader？</b></summary>

**答：**

**写等 apply：** 如果只等 commit 不等 apply 就返回，客户端后续立即读可能读到旧数据（commit 了但还没 apply）。本项目 `submit_and_wait_applied` 用 `pending` channel 模式——Leader append + commit 后等待 apply 协程发 signal，确保线性一致性。

**读走 Leader：** Follower 的 commit 可能滞后于 Leader（网络分区 / 慢节点），读到 stale data。走 Leader 保证读到最新已 commit 的数据。

**项目关联：** HTTP 层每个 GET/PUT handler 都检查 `rf.IsLeader()`，非 Leader 返回 409 + leader hint，客户端通过 `RaftClient.promote` 将 leader 提升到节点列表首位。
</details>

<details>
<summary><b>Q14: 这个项目的 Raft 客户端为什么需要指数退避重试？</b></summary>

**答：**

1. Raft 集群中只有 Leader 处理请求，客户端可能打到 Follower（返回 409），或 Leader 刚 crash 正在选举
2. 如果固定间隔重试，所有客户端同时重试会导致**惊群效应**（Thundering Herd）——Leader 刚当选瞬间被大量请求打满
3. 指数退避 + 随机抖动（jitter）将重试时间分散，平滑流量

**本项目实现：** 退避公式 `50ms * 2^attempt`，上限 2s；±25% 随机抖动；最多 10 次重试。
</details>

### 四、存储与上传架构

<details>
<summary><b>Q15: 内容寻址路由是什么？为什么用 FNV-1a 而不是一致性哈希？</b></summary>

**答：**

**内容寻址路由：** 根据数据内容的哈希值决定存放位置。同一个 `chunkHash` 永远路由到同一个存储节点，天然支持去重（不需要查全局索引就知道该去哪找）。

**为什么用 FNV-1a：** 简单快速，非加密哈希，适合做分片路由。输入已经过 SHA256 处理（均匀分布），路由哈希不需要密码学安全性。一致性哈希解决的是增减节点时的数据迁移问题，但本项目的存储节点数量由配置固定，不需要在线 rebalance。

**项目实现：** `fnv1a(chunkHash) % len(nodes)`，O(1) 定位，无中心元数据依赖。
</details>

<details>
<summary><b>Q16: 大文件上传如何做到断点续传？上传会话和分片状态分别怎么存的？</b></summary>

**答：**

分两步：
1. `POST /api/uploads/init` 创建 `UploadSession`（总分片数、名称、大小），以**单键**存入 Raft
2. 每上传一个分片，分片完成状态以**独立键** `uploadchunk:<uploadID>:<index>` 写入 Raft

**为什么分片状态要独立存：** 如果每次更新整个 `UploadSession` 写入 Raft，4 个并发 worker 可能互相覆盖。独立键写入使并发分片上传互不冲突。

**断点续传：** 客户端调用 `GET /api/uploads/:id/status` 拿 `received` 数组，只上传缺失分片。服务端检查 `ss.Received[index]`，已上传的返回 `already_uploaded: true`（幂等）。
</details>

<details>
<summary><b>Q17: 秒传（instant upload）是怎么实现的？</b></summary>

**答：**

- `FileID` = `SHA256(NameHash(name) + ":" + size)`
- 秒传索引键：`idx:ownername:<ownerHash>:<nameHash>:<size>` → value = `fileID`
- `initUpload` 查这个索引，命中且文件 `Complete=true` 就跳过上传直接返回

**边界情况修复：** 早期版本秒传索引只用 `nameHash`，同名不同大小的文件会互相覆盖。修复后加入 `size` 维度。即使命中也要检查 `Complete` 标志，防止文件正在上传中但未完成。

**局限性：** 只检查名称+大小匹配，不是内容级去重。内容级去重由分片的 SHA256 哈希在存储层完成。
</details>

<details>
<summary><b>Q18: ChunkWorkerPool 的设计有什么要点？为什么用有界队列？</b></summary>

**答：**

**设计要点：**
- **固定 worker 数量：** 默认 64 个 goroutine，避免无限制创建
- **有界队列：** `workers * 4` 容量。满时 `Submit` 阻塞形成**背压**（backpressure），防止任务无限堆积 OOM
- **ctx 取消检测：** worker 检查 `job.ctx.Err()`，请求已取消则跳过任务并归还 buffer，防止 resultCh 无人接收导致 goroutine 泄漏
- **resultCh 带 default：** `select { case resultCh <- result: default: }`，提交方已超时时丢弃结果但不阻塞 worker
- **优雅关闭：** `Shutdown()` 先 `close(jobs)` 再 `wg.Wait()`

**为什么有界队列：** 无界队列在流量突发时无限增长，耗尽内存。有界队列在满时迫使调用方等待或超时，自然形成限流。
</details>

<details>
<summary><b>Q19: 存储层临时文件 + 原子硬链接解决什么问题？</b></summary>

**答：**

```go
tmpFile, _ = os.CreateTemp(tmpDir, "*.part")
// ... 写入所有数据 ...
os.Link(tmpPath, finalPath)  // 硬链接
defer os.Remove(tmpPath)
```

1. **部分写入问题：** 先写临时文件再 link，只有完整分片才出现在 chunks 目录，crash 不留损坏文件
2. **并发去重：** 两个请求同时上传相同内容，第一个 `os.Link` 成功，第二个遇到 `os.ErrExist`，直接返回 `Reused: true`。硬链接在文件系统层面是原子操作
3. **无需应用层加锁：** 利用文件系统原子语义

**硬链接 vs 重命名：** 用 `Link` 让临时文件可以在不同目录，`Rename` 则要求同一文件系统且更简单。
</details>

### 五、性能与可观测性

<details>
<summary><b>Q20: 这个项目做了哪些 GC 优化？为什么 4MB chunk buffer 对 GC 压力大？</b></summary>

**答：**

**大对象对 GC 的影响：** >32KB 的对象直接分配在堆上。高并发下每秒创建数百个 4MB byte slice，GC 需要频繁扫描大量内存，STW 时间变长。

**优化手段：**
1. **sync.Pool 复用 chunk buffer：** `chunkBufPool` 复用 4MB 数组，`PutChunkBuf` 检查 `cap` 只归还合格 buffer
2. **sync.Pool 复用 JSON 序列化 buffer：** `jsonBufPool` 用 `buf.Reset()` 清空后归还，避免 `json.Marshal` 的临时分配
3. **Worker Pool 限流：** 64 个固定 worker 限制同时存在的 4MB buffer 数量
4. **流式下载：** `downloadChunk` 从全量读改为 gRPC stream → `io.ReadCloser` → HTTP 流式响应

**效果：** 上传 1GB 文件（256 个 chunk），没有 sync.Pool 时可能同时存在 64*4=256MB 待处理数据，sync.Pool 将分配次数从 256+ 降到 64+，GC 频率显著下降。
</details>

<details>
<summary><b>Q21: Bloom Filter 在这个项目中的作用？假阳性会有什么影响？</b></summary>

**答：**

**作用：** 分片去重的**预判过滤器**。查 Bloom Filter 判断 chunkHash 是否"可能已存在"：返回 false 一定不存在，跳过 gRPC 远程检查；返回 true 可能已存在，需远程确认。

**假阳性的影响：** 多做一次无用的 gRPC `ChunkExists` 调用——纯性能开销，不影响正确性，远程检查才是最终裁决。

**参数：** 64M bits（8MB 内存）+ 7 次哈希，假阳性率约 1%。对于命中率高的场景可忽略。

**局限：** 纯内存，进程重启后丢失。重启后需逐步重建。
</details>

<details>
<summary><b>Q22: Request ID 全链路追踪怎么实现的？</b></summary>

**答：**

**中间件注入：** `RequestIDMiddleware` 检查 `X-Request-ID` 请求头（Nginx 可预先生成），没有则用 `crypto/rand` 生成 16 字符 hex ID，通过 `c.Set` 写入 Gin context 并写响应头。

**日志关联：** `AccessLogMiddleware` 在 defer 中记录日志时携带 `req_id` 字段。

**当前局限：** 只覆盖 HTTP 层。gRPC 调用和 Raft HTTP 调用没用 gRPC metadata / HTTP header 透传 traceID。完善方案是在 interceptor 和 RaftClient 中注入/提取。
</details>

### 六、系统设计

<details>
<summary><b>Q23: 如果要支持单文件 1TB 上传，当前设计有哪些瓶颈？</b></summary>

**答：**

1. **UploadSession 元数据膨胀：** 1TB / 4MB = 262,144 个分片，`Received` 和 `ChunkMap` JSON 序列化后约 10MB+，Raft 单次写入超时
2. **分片状态键数量：** 26 万个键写入 Raft，`ListPrefix` 扫描代价大
3. **Raft 日志压力：** 26 万次 Raft 提交
4. **Bloom Filter 饱和：** 26 万条目在 64M bits 中假阳性率急剧上升
5. **前端 Blob 拼接：** `new Blob(results)` 在浏览器内存中合并，1TB 直接 OOM

**改进方向：** 分片大小提升到 64MB+；用 bitmap 替代 `map[int]bool`；分片状态批量写入；下载改为服务端合并 + 流式响应。
</details>

<details>
<summary><b>Q24: 如果存储节点宕机会发生什么？如何做高可用？</b></summary>

**答：**

**当前行为：** 路由到该节点的 chunk 读写全部失败，数据暂时不可用。

**改进方案：**
1. **多副本存储：** 每个 chunk 写入多个节点（replica=3），可用 Raft 或简单的副本管理
2. **纠删码（Erasure Coding）：** 比多副本省空间（如 8+4 → 1.5x 冗余 vs 3x 冗余）
3. **健康检查 + 自动摘除：** 存储节点不可用时从路由表摘除
4. **Hinted Handoff：** 写入时目标节点不可用，先写替代节点，目标恢复后回传

元数据层（Raft）已有强一致性保证（3 副本，多数派写入），存储层当前是单副本。
</details>

<details>
<summary><b>Q25: 为什么不把文件元数据存 MySQL 而要用 Raft？</b></summary>

**答：**

**Raft 的优势：**
- **自包含：** 不需要额外部署数据库，降低运维复杂度
- **强一致性：** 每个写操作经多数派确认，不丢数据
- **无单点：** Leader 挂了自动选举
- **嵌入进程：** Pebble 作为嵌入式状态机，不需要独立数据库进程

**MySQL 的优势：** 成熟稳定、生态丰富、功能完善（索引、事务、SQL 查询）。

**本项目选择 Raft 的核心原因：** 这是一个**教学/展示项目**，目标是展示自研 Raft 的能力。生产环境中元数据量不大的系统用 Raft + Pebble 可行；大规模生产系统用 etcd/MySQL/TiKV 更合适。

**关键设计决策：** Raft 承载体量小的元数据（几十 KB JSON），不存大对象。真正的数据（chunk 内容）走 gRPC 存储节点，Raft 不参与数据面。
</details>

<details>
<summary><b>Q26: uploadLocks sync.Map 为什么要定期清理？不清理会怎样？</b></summary>

**答：**

**问题：** `getUploadLock` 为每个 `uploadID` 创建一个 `sync.Mutex` 并存入 `sync.Map`。上传完成后该锁不再被使用，但 `sync.Map` 中的条目永远不会被删除。

**后果：** 长期运行后，数百个已完成的上传会话对应的锁对象残留在内存中，造成**内存泄漏**。虽然每个 `sync.Mutex` 只有 8 字节，但 `sync.Map` 中的 entry 和被包装的 `uploadLockVal` 结构体会持续累积。

**修复方案：** 新增 `uploadLockVal` 包装结构体（含 `lastAt` 时间戳），后台协程每 5 分钟扫描一次，删除超过 10 分钟未使用的锁。扫描时需先 `Lock` 以确认当前没有请求在使用该锁。
</details>

<details>
<summary><b>Q27: gin.New() 和 gin.Default() 的区别？为什么本项目用 gin.New()？</b></summary>

**答：**

- `gin.Default()` = `gin.New()` + `gin.Logger()` + `gin.Recovery()`
- `gin.New()` 创建不带任何中间件的空白 Engine

**本项目用 `gin.New()` 的原因：**
1. `gin.Logger()` 的默认日志格式不包含 requestID，项目用自定义的 `AccessLogMiddleware` 替代，输出结构化 JSON 日志并携带 `req_id`
2. `gin.Recovery()` 依然需要（处理 panic），单独挂载
3. 整体中间件顺序是显式控制的：`Recovery → RequestID → AccessLog → Metrics`

**raftnode 中用 `gin.Default()`：** 因为 Raft HTTP 接口只是调试/管理用途，不需要自定义日志格式。
</details>

<details>
<summary><b>Q28: 项目中 split3 被替换为 strings.SplitN，为什么 strings.SplitN 更优？</b></summary>

**答：**

原始 `split3` 实现逐字符遍历：
```go
for _, ch := range s {
    cur += string(ch)  // 每次迭代一次内存分配
}
```
每个字符 `string(ch)` 都是一次堆分配，解析 `"abc|def|123"` 需要约 11 次分配。

`strings.SplitN(s, "|", 3)` 内部使用汇编优化的 `IndexByte` 扫描分隔符，只分配一次结果 slice（3 个元素），且 `string` 切片操作共享底层字节，无需逐字符拷贝。

**性能对比：** 对于 4MB chunk 的上传路径每秒可能处理数十个分片，每个分片的 ChunkMap 都经过 split3。虽然单次差异极小，但累积影响可观。更重要的是代码意图更清晰——`strings.SplitN` 一目了然，手写逐字符解析需要阅读才能理解。
</details>
