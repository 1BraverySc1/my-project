# Raft 算法原理（简明版）

Raft 是一种**复制状态机**共识算法：多个节点维护同一份**按顺序追加的日志**，日志应用到状态机后，各节点对外表现一致。下面按「任期 → 选举 → 复制 → 提交 → 异常」的顺序说明核心机制。

---

## 1. 三种角色

| 角色 | 职责（直觉） |
|------|----------------|
| **Follower** | 被动接收来自 Leader 的心跳与日志；超时则参与选举。 |
| **Candidate** | 发起一轮选举，向其他节点拉票。 |
| **Leader** | 唯一处理客户端写请求（演示里也可约定读）；向 Follower **复制日志**并维护心跳。 |

任意时刻，**一个稳定集群里通常只有一个 Leader**（少数派分区里可能短暂存在「自以为的 Leader」，见下文脑裂）。

---

## 2. 任期（Term）

- **Term** 是单调递增的整数，整段逻辑时间切成一届一届。
- 每次**新一轮选举**，Term **至少 +1**（具体实现可在成为 Candidate 时递增）。
- **规则要点**：
  - 节点发现「对方 RPC 里带的 Term 更大」→ 自己立刻**退回到 Follower**并更新本地 Term。
  - **每个 Term 至多一个节点被选为 Leader**（靠多数派投票保证）。
- **Term 的作用**：识别过期的 Leader、防止旧 Leader 在分区恢复后继续误写；也是 RequestVote / AppendEntries 里**拒绝陈旧消息**的依据。

---

## 3. Leader 选举（怎么处理「没有主」）

1. Follower **选举定时器**到期（一般带**随机抖动**，避免多节点同时抢选导致长期平票）。
2. 转为 **Candidate**，Term+1，**先投自己一票**，并并行向其他节点发 **RequestVote**。
3. **赢得选举**：本 Term 内获得**超过半数**节点的赞成票 → 成为 **Leader**，立刻发心跳（空的 AppendEntries）宣告主权。
4. **RequestVote 的日志约束（选举安全性）**：  
   若 Candidate 的日志**不比投票者更新**（最后一条日志的 Term 更旧，或同 Term 但更短），投票者**拒绝投票**。  
   这样能保证：被选上的 Leader **一定包含所有已提交条目**（在 Raft 假设下）。

**平票 / 超时**：本轮无人过半 → 超时后再来一轮（Term 继续推进），随机抖动降低再次同时开选的概率。

---

## 4. 日志复制与「不一致」怎么处理

Leader 为每个 Follower 维护 **`nextIndex`**（下一条要发的日志下标）和 **`matchIndex`**（已确认复制的最大下标）。

- 发 **AppendEntries** 时带上 **`prevLogIndex` / `prevLogTerm`**：要求 Follower 在该位置上的日志与 Leader **一致**，才追加新条目。
- **Follower 校验失败**（前缀对不上）→ 回复 `success=false`，并附带 **加速回滚提示**（与 etcd / TiKV 等一致）：若本地在 `prevLogIndex` 处**还没有日志**，则返回 `conflict_term=0`、`conflict_index=len(本地 log)`，Leader 直接把 `nextIndex` 跳到 Follower 缺口处；若**任期不一致**，则返回冲突位置的**任期**以及该任期在本地日志中的**首条索引**，Leader 在自己日志里查找该任期的**最后一条**，将 `nextIndex` 设为「其后一条」，否则退化为跳到 Follower 给出的首索引。这样可按「任期」大步回退，避免每次只减 1 的千次 RPC。

**心跳**：没有新命令时，Leader 仍发 AppendEntries（entries 为空）刷新 Follower 的「跟主关系」，防止 Follower 误以为主挂了又去选举。

---

## 5. 提交（Commit）与 Figure 8

- 只有日志被**复制到多数派**后，才可能**提交**（推进 `commitIndex`）。
- **关键安全规则**：Leader **不能**仅凭「多数派复制了某下标」就提交**旧 Term** 里写的条目，除非**同一条日志链上已经有本 Term 的条目被多数派复制**（否则可能出现论文 **Figure 8** 里的错误提交）。  
  本仓库实现采用常见写法：**仅当 `log[N].Term == currentTerm` 时**，才用多数派 `matchIndex` 推进 `commitIndex`。

Follower 在 `commitIndex` 推进后，按序**应用到状态机**（`apply`），对外可见状态才一致。

---

## 6. 脑裂（网络分区）直觉

- **多数派所在分区**：能选出（或保留）合法 Leader，**能复制、能提交**（在满足上述提交规则前提下）。
- **少数派分区**：要么**选不出 Leader**（得票不过半），要么即使短暂自举出「Leader」，**无法得到多数派复制**，**无法提交新条目**；客户端通常会超时或失败。
- 分区恢复后，**Term 更高的一方**成为真相来源；旧 Term 的未提交条目可能被**覆盖**（在多数派从未确认的前提下），从而保证全局不会有两套已提交的冲突历史。

因此：**脑裂不是靠「禁止两个主」硬件解决，而是靠「只有多数派能提交」+ Term 与日志规则**在逻辑上消冲突。

---

## 7. 和本仓库的对应关系（便于对照代码）

| 概念 | 代码中大致位置 |
|------|----------------|
| 角色、Term、`voted_for` | `internal/raft/raft.go` 状态字段与 RPC 入口 |
| 选举、随机超时 | `ticker_loop`、`start_election`、RequestVote |
| `leader_last_idx` / `follower_matched_idx`（论文常称 nextIndex/matchIndex）、冲突加速回退 | `run_append_entries_for_follower`、`adjust_send_idx_after_append_rejected_locked`、`handle_append_entries` |
| 提交规则、应用到状态机 | `try_commit`、`apply_committed_entries_loop`、`apply_ch` |
| 节点间 RPC | gRPC：`api/raft.proto`、`internal/raft/grpc.go` |

更细的面试问答见同目录 `INTERVIEW_QA.md`。

## 8. 当前工程边界

- 网盘客户端收到 409 后使用 `leader_id` 映射 HTTP 节点；`leader_raft_addr` 仅供节点间 gRPC 使用。
- `Submit` 会等待日志提交并应用到 Pebble，但读取尚未实现 ReadIndex，因此不能宣称具备完整的线性一致读。
- 当前未实现快照、日志压缩、联合一致成员变更，也未持久化 `commitIndex/lastApplied`。
- Raft 黑盒测试统一位于 `raftImpl/test/`。
