---
name: "database-guide"
description: "Wavelet 项目数据库使用规范：当新增或修改数据库表结构、索引、初始化数据、插件自包含 Goose SQL 迁移、embed.FS 注册、PG/SQLite 双方言支持、ClickHouse 分析库 DDL，或接入 batchwriter 批量写入 ClickHouse 时必须使用。"
---

# 数据库迁移与 ClickHouse 批量写入开发规范 (Cordis 插件化架构)

本技能覆盖 Wavelet 在 Cordis 微内核与插件化架构下的两个紧密关联的主题：

1. **数据库表结构**：Goose SQL 迁移与插件嵌入式注册（PG / SQLite 双方言，ClickHouse 单方言）
2. **ClickHouse 批量写入**：运行时写入架构，使用 `pkg/batchwriter` 泛型队列框架

日志/分析用途表的判定、三库回落与切换见 `logstore` 技能。

---

## 第一部分：数据库迁移规范

### 1. 核心架构：插件自包含迁移 (Self-Contained Migrations)

在 Cordis 架构中，**彻底告别集中式单体大迁移目录**。
每个插件在自身包内维护专属的 `migrations/` 目录，通过 Go 语言内置 `//go:embed` 打包为嵌入式文件系统，并在 `Apply(ctx *core.Context)` 时通过微内核扩展点 `ctx.Migrations().Register(...)` 自主注入。

```
backend/plugins/domain/order/
├── plugin.go
├── migrations/
│   ├── postgres/
│   │   └── 00001_initial.sql   ← PG 方言
│   └── sqlite/
│       └── 00001_initial.sql   ← SQLite 方言
└── migrations-clickhouse/      ← 仅含 OLAP 分析表时才创建
    └── 00001_initial.sql
```

> **重要**：迁移目录采用 `migrations/postgres/` + `migrations/sqlite/` 双方言子目录结构，**不是**单层 `migrations/*.sql`。`//go:embed` 指令必须使用 `migrations/*/*.sql` 匹配双层路径。

---

### 2. 插件迁移代码集成标准

#### 步骤 1：在插件内嵌入并注册迁移

```go
package order

import (
    "embed"
    "Wavelet/core"
)

//go:embed migrations/*/*.sql
var orderMigrations embed.FS

func (p *Plugin) Apply(ctx *core.Context) error {
    // 注册本插件的专属迁移（系统启动时由微内核统一收集并按版本执行）
    ctx.Migrations().Register("order", orderMigrations)
    return nil
}
```

#### 步骤 2：编写 Goose SQL 脚本

每个插件只需维护一个 `00001_initial.sql`（含全部建表与种子数据），未来追加 DDL 则新增 `00002_xxx.sql`。

**`migrations/postgres/00001_initial.sql`**：

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS w_orders (
    id          BIGINT NOT NULL,
    user_id     BIGINT NOT NULL DEFAULT 0,
    amount      BIGINT NOT NULL DEFAULT 0,
    status      VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_w_orders_user_id ON w_orders(user_id, created_at DESC);

-- 种子数据
INSERT INTO w_orders (id, user_id, amount, status)
VALUES (1, 0, 0, 'completed')
ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS w_orders;
-- +goose StatementEnd
```

**`migrations/sqlite/00001_initial.sql`**（方言差异见下表）：

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS w_orders (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL DEFAULT 0,
    amount      INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'pending',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_w_orders_user_id ON w_orders(user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS w_orders;
-- +goose StatementEnd
```

---

### 3. 版本管理与升级机制

#### 3.1 版本表结构

所有插件共享一张 `w_schema_versions` 表，以 `plugin_id` 为区分：

```sql
w_schema_versions (
    plugin_id   VARCHAR(64)  NOT NULL,   -- 如 "auth", "user", "admin"
    version_id  BIGINT       NOT NULL,   -- 迁移文件版本号 (00001 → 1)
    applied_at  TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, version_id)
)
```

#### 3.2 升级判定逻辑

| 场景 | 例子 | 是否升级 |
|------|------|---------|
| 首次部署，插件第一次运行 | auth 插件，表不存在 | ✅ 执行 `00001_initial.sql` |
| 第二次启动，无变化 | 文件未变，版本已记录 | ❌ 跳过 |
| 追加新迁移文件 | 新增 `00002_add_index.sql` | ✅ 执行 `00002_*` |
| 移除一个插件 | 该插件不再注册 | ❌ 其记录在表中被忽略 |
| 新增一个插件 | 新插件有 `00001_initial.sql` | ✅ 执行 |

#### 3.3 查看全局迁移状态

```sql
SELECT * FROM w_schema_versions ORDER BY plugin_id, version_id;
```

输出示例：

```
plugin_id            | version_id | applied_at
---------------------+------------+---------------------------
admin                |          1 | 2026-08-28 10:00:00+00
auth                 |          1 | 2026-08-28 10:00:00+00
user                 |          1 | 2026-08-28 10:00:00+00
upload               |          1 | 2026-08-28 10:00:00+00
risk_control/logstore|          1 | 2026-08-28 10:00:00+00
```

---

### 4. 核心设计与防线原则 (Guardrails)

1. **表单一所有者原则**：每张数据表归属且仅归属于一个所有者插件（如 `w_orders` 归 `order` 插件）。**严禁**插件 B 跨包编写 SQL 直接读写插件 A 拥有的表；必须通过插件 A 暴露的 `contracts` 接口或事件总线进行交互。

2. **表名前缀规范**：所有表名必须带有前缀（如 `w_orders`、`w_auth_users`），杜绝跨插件表名冲突。

3. **单文件初始迁移**：每个插件只维护一个 `00001_initial.sql`，包含该插件所有表的建表语句与初始种子数据。未来如需追加 DDL，新增 `00002_xxx.sql`，Goose 会根据 `w_schema_versions` 判断增量执行。

4. **禁止物理外键**：关系字段统一显式建立单列或联合索引，禁止在数据库中创建物理外键约束。

5. **双方言兼容性（PostgreSQL & SQLite）**：

   | 特性 | PostgreSQL | SQLite |
   |------|-----------|--------|
   | 自增主键 | `BIGSERIAL` 或 `BIGINT NOT NULL` | `INTEGER PRIMARY KEY AUTOINCREMENT` |
   | 时间类型 | `TIMESTAMPTZ` | `DATETIME` |
   | JSON 类型 | `JSONB` | `JSON` 或 `TEXT` |

6. **幂等性要求**：
   - 所有 `CREATE TABLE` 必须使用 `IF NOT EXISTS`。
   - 所有 `INSERT` 种子数据必须使用 `ON CONFLICT DO NOTHING`。
   - 所有 `ALTER TABLE ADD COLUMN` 必须使用 `IF NOT EXISTS`（如果数据库方言支持）。

---

### 5. ClickHouse 分析库迁移规则 (辅助 OLAP)

ClickHouse 作为辅助 OLAP 分析存储，采用独立迁移通道：

- 迁移文件位于插件自身的 `migrations-clickhouse/` 目录（仅单方言 DDL，不创建 SQLite 镜像）。
- ClickHouse 迁移**不通过** `ctx.Migrations().Register()` 注入，由 `database` infra 插件的 ClickHouse 初始化流程单独执行。
- 日志/分析用途表必须同时在关系型主库建回落表并接入 `logstore` 门面。
- 分析表高频写入统一接入 `batchwriter` 进行异步批量刷盘（见第二部分）。

**实际示例**（`plugins/domain/risk_control/logstore/migrations-clickhouse/00001_initial.sql`）：

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS w_user_access_logs
(
    id          UInt64,
    user_id     UInt64,
    path        String,
    method      String,
    ip          String,
    user_agent  String,
    headers     String,
    status      Int32,
    latency     Int64,
    created_at  DateTime
)
ENGINE = MergeTree()
PARTITION BY toYYYYMM(created_at)
ORDER BY (created_at, ip, user_id)
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE IF EXISTS w_user_access_logs;
```

---

### 6. 质量与验证门禁

```bash
make format
make code-check
go test ./backend/plugins/...
```

验证迁移注册完整性：

```bash
# 检查每个有 migrations/ 目录的插件是否同时有 go:embed + Register()
grep -rn 'go:embed.*migrations' backend/plugins/domain/*/plugin.go backend/plugins/drivers/*/plugin.go
grep -rn 'Migrations()\.Register' backend/plugins/domain/*/plugin.go backend/plugins/drivers/*/plugin.go
```

---

## 第二部分：ClickHouse 批量写入规范

开始前阅读根目录 `AGENTS.md`。ClickHouse 是辅助 OLAP 存储，**厌恶高频单条写入**（过多小 part）；写入路径必须优先批量或异步聚合。

### 1. 分层职责

| 层级 | 路径 | 职责 |
| :--- | :--- | :--- |
| 连接 | `backend/plugins/infra/database/clickhouse.go` | `ChConn`（原生批量写）、`ChDB(ctx)`（GORM 查询）；禁止在业务包直接 `clickhouse.Open` |
| 批量框架 | `backend/pkg/batchwriter/` | 泛型队列 + 按条数/时间 flush + 非阻塞入队 + 优雅停机；**各业务域独立实例** |
| Model | 插件内 logstore 包（如 `plugins/domain/risk_control/logstore/models.go`） | 列定义、`TableName()`、`BatchInsertSQL()`、`InsertColumns()` |
| BatchInsert | 插件内 logstore 包（如 `logstore/access_log_writer.go`） | `PrepareBatch` + 多行 `Append` + 一次 `Send` |
| 业务入队 | 插件服务层（如 `plugins/domain/risk_control/service.go`） | 采集、入队、背压；FlushFunc 调 logstore，不写 SQL、不 `PrepareBatch` |
| 装配 | 插件 `Apply()` 方法 | 调用 `InitLogWriter`；通过 `ctx.OnDispose()` 挂载停机钩子 |

**禁止**在 Handler / middleware 内直接调 `ChConn.PrepareBatch`；**禁止**在 logstore 内启动 goroutine 或维护全局 channel（队列生命周期由业务插件 service 层负责）。

### 2. batchwriter 框架契约

```go
// backend/pkg/batchwriter/writer.go
writer, err := batchwriter.New[YourType](cfg, flushFunc, opts...)
writer.Start(ctx)       // 启动后台 worker goroutine（内部用 context.WithoutCancel）
writer.TryEnqueue(item) // 非阻塞；满则 false
writer.IsFull()         // 背压探测
writer.Len()            // 当前队列深度
writer.Stop(stopCtx)    // close 队列 + drain + 最终 flush
```

#### Config 默认值（`batchwriter.DefaultConfig()`）

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `QueueSize` | 10,000 | 缓冲 channel 容量 |
| `MaxBatchSize` | 1,000 | 达到此条数立即 flush |
| `MinBatchSize` | 50 | 时间触发 flush 的最小批次；未达到则跳过（除非 `MaxFlushWait` 到期） |
| `FlushInterval` | 1s | worker 检查时间触发 flush 的频率 |
| `MaxFlushWait` | 0（禁用） | 强制 flush 小批次的最大等待时间 |

各域可独立覆盖；可观测低频指标可用更小 `MaxBatchSize`（如 100）与更长 `FlushInterval`（如 2–5s），但**不要**退化为逐条 `Send`。

#### 可选回调

- `batchwriter.WithFlushErrorHandler[T]`：flush 失败时记录日志；批次丢弃后 worker 继续
- `batchwriter.WithDropHandler[T]`：队列满或未 `Start` 时处理丢弃项

#### FlushFunc 规范

- 签名：`func(ctx context.Context, items []T) error`
- **日志/分析用途表**：FlushFunc 调对应 logstore 的 `BatchInsert`（内部再调 `ChConn.PrepareBatch`）。禁止跨过 logstore 直连 `ChConn`。
- 在 flush 边界记录一次错误日志，不要把 DB 驱动错误直接暴露给 HTTP 客户端。

### 3. 生命周期管理（Cordis 插件装配）

**装配示例**（`plugins/domain/risk_control/plugin.go`）：

```go
func (p *Plugin) Apply(ctx *core.Context) error {
    // 初始化 log writer（内含 Start，幂等）
    InitLogWriter(ctx.GoContext())

    // 通过 ctx.OnDispose() 注册停机钩子（Cordis 架构，禁止用 lifecycle.OnShutdown）
    ctx.OnDispose(func() error {
        return StopLogWriter(context.Background())
    })
    return nil
}
```

**InitLogWriter 示例**（`plugins/domain/risk_control/service.go`）：

```go
var (
    logWriterMu sync.RWMutex
    logWriter   *batchwriter.Writer[*logstore.UserAccessLog]
)

func InitLogWriter(ctx context.Context) {
    logWriterMu.Lock()
    defer logWriterMu.Unlock()
    if logWriter != nil { // 幂等：Apply 多次调用安全
        return
    }
    cfg := batchwriter.DefaultConfig()
    cfg.MaxFlushWait = 2 * time.Second
    writer, err := batchwriter.New[*logstore.UserAccessLog](cfg, writeAccessLogBatch,
        batchwriter.WithDropHandler[*logstore.UserAccessLog](func(item *logstore.UserAccessLog) {
            logger.WarnF(context.Background(), "[RiskControl] Log queue full, dropping: %s", item.Path)
        }),
        batchwriter.WithFlushErrorHandler[*logstore.UserAccessLog](func(ctx context.Context, items []*logstore.UserAccessLog, err error) {
            logger.ErrorF(ctx, "[RiskControl] flush batch failed (batch=%d): %v", len(items), err)
        }),
    )
    if err != nil {
        logger.ErrorF(ctx, "[RiskControl] init log writer failed: %v", err)
        return
    }
    writer.Start(ctx)
    logWriter = writer
}
```

> **严禁**：不要使用 `lifecycle.OnShutdown` 或 `lifecycle.Stop()`——这些在旧架构中已废弃。Cordis 架构统一使用 `ctx.OnDispose()` 注册停机回调。

### 4. 各域独立实例（不共享队列）

每个业务域拥有自己的 `Writer`、配置与 `FlushFunc`：

| 域 | 表 | 写入路径 |
| :--- | :--- | :--- |
| 管理端审计 | `w_user_access_logs` | `risk_control` service → `batchwriter` → logstore `BatchInsert` → `ChConn.PrepareBatch` |

**不要**把不同日志域并入同一 channel。新日志表先按 `logstore` skill 判定，再为本域建独立 writer。

### 5. 新增 ClickHouse 写入工作流

1. **DDL**：在插件内新增 `migrations-clickhouse/00001_initial.sql`（使用 `MergeTree`，按时间分区）。
2. **Model**：在 logstore 包内定义 struct，实现 `TableName()`、`BatchInsertSQL()`、`InsertColumns()`，列顺序与 DDL 一致。
3. **BatchInsert**：在 logstore 包内实现 `BatchInsert(ctx, []T) error`：
   - `len(items)==0` 直接返回
   - `conn == nil` 返回明确错误
   - 一次 `PrepareBatch` → 循环 `Append` → 一次 `Send`
4. **Writer 胶水**（插件 service 层）：
   - `InitXxxWriter`（内含 `New` + `Start`）；检查 `if logWriter != nil { return }` 保证幂等
   - 日志表 FlushFunc 调 logstore `BatchInsert`
   - 业务路径调 `TryEnqueue`；HTTP 背压用 `IsFull()`
5. **装配**：插件 `Apply()` 调 `InitXxxWriter`；`ctx.OnDispose()` 挂载 `StopXxxWriter`。
6. **测试**：logstore BatchInsert 用 mock conn 验证 SQL 与 append 列数；`go test ./backend/pkg/batchwriter/...`。
7. 运行 `make code-check`；有 API 变更时 `make swagger`。

### 6. 背压与丢弃策略

| 场景 | 推荐策略 |
| :--- | :--- |
| 管理端 API 审计 | 队列满 → `IsFull()` 触发 429（见 `risk_control` middleware） |
| 可丢弃的高频日志 | 队列满 → `WithDropHandler` 记 warn；不阻塞请求 |

### 7. 禁止写法

```go
// ❌ 单条伪批量：每条都 PrepareBatch + Send
batch.Append(oneRow)
batch.Send()

// ❌ 写前 OLTP 式去重（高 RTT + 仍产生小 part）
SELECT count() FROM ... WHERE id = ? AND created_at = ?

// ❌ Handler 内直接写 ClickHouse
database.ChConn.PrepareBatch(...)

// ❌ 全局单队列承载所有分析表
var globalChan chan any

// ❌ 用 lifecycle.OnShutdown（Cordis 架构下已废弃）
lifecycle.OnShutdown("writer", writer.Stop)

// ❌ 在业务包直接 clickhouse.Open
conn, _ := clickhouse.Open(opts)
```

去重应使用：`ReplacingMergeTree`、查询侧 `argMax`、或进程内短 TTL 去重缓存——**不要**在每次 insert 前 `SELECT count()`。

### 8. async_insert（补充，非主方案）

可在 `backend/plugins/infra/database/clickhouse.go` 的 `Settings` 增加服务端异步写入作为第二层防护：

```go
"async_insert": 1,
"wait_for_async_insert": 1,
```

**不能替代**应用层批量；接入前需评估丢失可观测性与服务端负载。优先完成 `batchwriter` 接入后再考虑。

### 9. 验证清单

```bash
go test ./backend/pkg/batchwriter/...
make code-check
```

- flush 按 `MaxBatchSize` 与 `FlushInterval` 触发
- `Stop` 能 drain 队列内剩余项
- logstore 层无 goroutine、无 channel
- 日志表：`clickhouse.enabled: false` 时 writer 仍 `Start`，flush 走主库 logstore
- 仅 CH 的分析表：未启用 CH 时不要 `Start`、不要入队

---

## 相关文件速查

- **batchwriter 框架**：`backend/pkg/batchwriter/{config,writer,errs}.go`
- **ClickHouse 连接**：`backend/plugins/infra/database/clickhouse.go`
- **审计写入示例**：`backend/plugins/domain/risk_control/service.go`（InitLogWriter / QueueAccessLog）
- **BatchInsert 实现**：`backend/plugins/domain/risk_control/logstore/access_log_writer.go`
- **logstore 抽象**：`backend/plugins/domain/risk_control/logstore/`
- **CH 迁移示例**：`backend/plugins/domain/risk_control/logstore/migrations-clickhouse/00001_initial.sql`
