# raftImpl - 手写高可用 Raft（动态节点 + Pebble）

本项目是纯手写 Raft（不依赖 HashiCorp/etcd Raft 库），当前已实现：

- 完整的领导者选举（随机超时 + RequestVote）。
- 心跳维持与日志复制（AppendEntries + 冲突回退）。
- 多数派提交与顺序应用（commitIndex / lastApplied）。
- 基于日志的动态节点加入（`join` 命令一致复制）。
- 状态机使用 [Pebble](https://github.com/cockroachdb/pebble)（纯 Go、KV 落盘，无需 cgo）。

## 代码结构

- `internal/raft/raft.go`：Raft 核心（选举、心跳、复制、提交、apply）。
- `internal/raft/grpc.go`：节点间 gRPC（RequestVote/AppendEntries），含 `start_raft_grpc`。
- `internal/raft/persist.go`：持久化 `term/vote/log/peers`。
- `cmd/raftnode/main.go`：节点启动、HTTP、Pebble、join 注册。
- `完整执行流程报告.md`：端到端说明。

## 编译与测试

Raft 测试统一存放在 `test/`，使用公开接口执行黑盒验证。

```bash
cd /root/projects/webdownld_go.dir/raftImpl
go test ./test -count=1
go build -o bin/raftnode ./cmd/raftnode
```

## 启动示例

**首节点：**

```bash
./bin/raftnode -id=1 -raft=127.0.0.1:9000 -http=127.0.0.1:8080 -bootstrap \
  -persist=./data/node1/state.json -db=./data/node1/pebble
```

**新节点加入：**

```bash
./bin/raftnode -id=2 -raft=127.0.0.1:9001 -http=127.0.0.1:8081 \
  -join=127.0.0.1:8080 -persist=./data/node2/state.json -db=./data/node2/pebble
```

## 接口

- `GET /status`：角色、term、leader hint。
- `PUT/DELETE /kv/:key`：写（仅 Leader）。
- `GET /kv/:key`：读本地已应用 Pebble。
- `POST /cluster/join`：向 Leader 注册节点（JSON：`node_id`, `node_addr`）。

## 生成 Proto

```bash
make proto
```

## 与网盘服务的集成边界

- 非 Leader HTTP 响应提供 `leader_id` 与 `leader_raft_addr`；网盘客户端使用 `leader_id` 映射 `RAFT_HTTP_NODES`，不会把内部 Raft RPC 端口误当成 HTTP 端口。
- 写请求等待日志提交并由 Pebble 状态机确认应用后返回。
- 当前实现仍未提供 ReadIndex、快照/日志压缩、联合一致成员变更，也未持久化 `commitIndex/lastApplied`，定位为教学与项目展示实现，而非生产级 Raft。
