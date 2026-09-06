---
name: "logstore"
description: "Wavelet 项目专用：当新增或修改日志/分析用途表（访问日志、审计流水、可观测时序）、接入 plugins/domain/risk_control/logstore、切换日志主库、实现 PG/SQLite 回落，或判断一张表该走业务主库还是日志库时必须使用。"
---

# 日志用途表开发

开始前阅读根目录 `AGENTS.md`。DDL 用 `database-migration`；高频写入队列用 `clickhouse-batchwriter`；切换任务用 `new-async-task`。本技能只回答：**这张表是不是日志表，以及如何接入可切换的日志主库。**

## 先判定

日志表同时满足：

- 追加写入、几乎不更新单行
- 按时间查询/聚合，允许按保留天数删除
- 关闭 ClickHouse 后仍要能写、能查
- 不参与用户/配置/任务等事务一致性

**不要**做成日志表：用户、配置、任务执行、上传元数据、需要事务或强一致的业务实体。这些走主库业务插件 `repository`，不要进 `logstore`。

当前框架已接入的日志表：`w_user_access_logs`（管理端 API 访问审计），实现位于 `plugins/domain/risk_control/logstore/`。

## 分层

| 层级 | 路径 | 职责 |
| :--- | :--- | :--- |
| 抽象接口 | `plugins/domain/risk_control/logstore/logstore.go` | `Store`、`UserAccessLogStore`、`StatusStore` 接口定义 |
| CH 实现 | `logstore/clickhouse.go` | 原生 `PrepareBatch` / GORM ChDB 查询 |
| 主库实现 | `logstore/gorm.go` | GORM，PG（按月分区）与 SQLite（普通表），共用一套 |
| Model | `logstore/models.go` | `UserAccessLog` 实体、`TableName`、`InsertColumns`、`BatchInsertSQL` |
| 迁移 DDL | `logstore/migrations/{postgres,sqlite}/` + `migrations-clickhouse/` | embed.FS 注入迁移 |
| batchwriter | `risk_control/service.go` 的 `InitLogWriter` | `batchwriter.Writer[*logstore.UserAccessLog]`，`FlushFunc` 调 `logstore.Active().UserAccessLogs.BatchInsert` |
| 切换任务 | `plugins/domain/admin/service/log_switch.go` 的 `LogDBSwitchHandler` | 冻结写入 → Drain → 复制 → 翻转 `log_database` |
| 清理 | `logstore/cleanup.go` 的 `CleanupExpired`，由 `system:cleanup` 调用 | 按库读取保留天数后 `DeleteBefore` |
| DB 注入 | `logstore/db_helper.go` 的 `SetDBService` | 在 `risk_control/plugin.go` 中通过 `core.Bind` 注入 |
| 配置解析 | `logstore/provider.go` 的 `SetConfigReader` | bootstrap 注入，避免 logstore ↔ repository 循环依赖 |

`log_database` ∈ {`postgres`,`sqlite`,`clickhouse`}，且只能是「随主库」或 ClickHouse：主库为 PG 时日志不能是 SQLite，反之亦然。`log_database` / `log_db_migration` 受保护，禁止管理端手动改。

## 新增一张日志表

按顺序做，列名三库必须一致。

1. **Model**
   在 `logstore/models.go`（或新域自建同级 `models.go`）定义 struct；实现 `TableName()`；批量写再提供 `InsertColumns()` / `BatchInsertSQL()`。

2. **三套 DDL**（`database-migration`）
   - ClickHouse：`logstore/migrations-clickhouse/`，`MergeTree`，`PARTITION BY toYYYYMM(时间列)`。
   - PostgreSQL：`logstore/migrations/postgres/`，高频表用 `PARTITION BY RANGE (时间列)`，复合主键必须包含分区键。
   - SQLite：`logstore/migrations/sqlite/`，普通表 + 时间/过滤列索引。
   不要在 PG/SQLite 上复制 CH 物化视图；聚合在查询时实时算。

3. **logstore 接口**
   在 `logstore/logstore.go` 的对应 Store 接口补齐至少：
   - 写入：`BatchInsert`（flush 目标；内调 `ensureWritable`）
   - 查询：业务需要的 List/Count/聚合
   - 迁移：`ListForMigration(afterID, limit)`、`MigrationRange`、`DeleteAll`、`EnsurePartitions`（PG 按月预建，CH/SQLite no-op）
   - 清理：`DeleteBefore(cutoff)`、`DropEmptyPartitions`、`DropExpiredPartitions`（仅 PG；CH/SQLite no-op）
   并挂入 `Store` struct。

4. **双实现**
   - CH：在 `logstore/clickhouse.go` 或新域 clickhouse 文件中委托 `getChConn()`/`getChDB()` 实现，零额外查询路径。
   - GORM：在 `logstore/gorm.go` 或新域 gorm 文件中，PG/SQLite 共用一套；方言 SQL 只放小函数（如按日 `to_char` / `strftime`）。零值 `id` 落库前用 `idgen.NextUint64ID()`。

5. **`buildStore`**
   在 `logstore/provider.go` 的 `buildStore` 函数中，CH / GORM 分支同时挂上新域 Store。

6. **写入**
   在所属域插件（如 `risk_control/service.go`）用独立 `batchwriter.Writer` 实例；`FlushFunc` → `logstore.Active(ctx)` → 对应 `BatchInsert`。禁止直接调 `getChConn()` / `getChDB()` 进行写入。迁移任务调用域的 `Drain`（等队列空一个 flush 周期，不要 `Stop` writer）。

7. **切换任务**
   在 `plugins/domain/admin/service/log_switch.go` 的迁移流程（`MigrateAndSwitchEngine` → `copyAccessLogs`）增加该表：`DeleteAll` 目标 → `MigrationRange` + `EnsurePartitions` → 按 id 分页复制。不要改切换协议（仍冻结写入、源数据不删、成功才翻转）。

8. **清理**
   `logstore/cleanup.go` 的 `CleanupExpired`：PG 先 `DropExpiredPartitions`（整月过期分区），再 `DeleteBefore`（边界月），最后 `DropEmptyPartitions`。保留天数用已有 `log_retention_days_*`（`log_retention_days_postgres`、`log_retention_days_sqlite`、`log_retention_days_clickhouse`）。`plugins/domain` 下禁止 import `internal`（`logstore/imports_test.go` 守护）。

## 禁止

- 直接调用 `logstore.getChConn()` / `getChDB()` 进行日志写入（仅由 logstore 内部实现调用）
- 只建 CH 表、不建 PG/SQLite 回落
- 在 Handler 里逐条 `PrepareBatch` + `Send`
- 把业务表「顺便」放进 logstore 以便关 CH
- 管理端 API 改 `log_database` / `log_db_migration`（这两个 key 受保护）
- `plugins/domain` 下直接 import `Wavelet/internal`（守护测试会报错）

## 验证

```bash
go test ./plugins/domain/risk_control/logstore/...
go test ./plugins/domain/admin/...
make swagger   # 若改了状态/查询 API
make code-check
```

对照：`w_user_access_logs` 的 model（`logstore/models.go`）、三库 DDL（`logstore/migrations/`、`logstore/migrations-clickhouse/`）、GORM 实现（`logstore/gorm.go`）、CH 实现（`logstore/clickhouse.go`）、`risk_control.InitLogWriter`（`service.go`）、`LogDBSwitchHandler`（`admin/service/log_switch.go`）、`CleanupExpired`（`logstore/cleanup.go`）。
