# AGENTS.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

## 5. Test-Driven Development (TDD)

**先写测试，再让测试逐步变绿。**

所有功能开发与 Bug 修复必须遵循 TDD 节奏：

1. **写失败测试**：针对目标行为编写测试，确认它在当前代码下失败（Red）。
2. **最小实现**：只写让测试通过所需的最少代码，不超出测试范围（Green）。
3. **重构**：在测试绿色的保护下清理代码，确保所有测试仍然通过（Refactor）。

**逐步推进，不要跳步：**

```
❌ 一次写完所有实现，然后补测试
✅ 写一个测试 → 让它变绿 → 写下一个测试 → 让它变绿 → ...
```

**测试应覆盖：**
- 正常路径（happy path）
- 边界条件（空值、零值、最大值）
- 错误路径（预期失败的 case）

**验证命令在提交前必须全部通过：**

```bash
go test ./...       # 后端全量测试
make code-check     # 静态分析
```

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.


## Git 提交规范

每次完成一个功能点开发或修复一个问题后，务必提交 Git commit , 禁止推送远程仓库。
遵循 Conventional Commits：`<type>(<scope>): <subject>`（例：`feat(auth): support email login`）。

## 务必阅读匹配的 Skill

| Skill | 何时使用 |
| :--- | :--- |
| `new-api` | 基于 Cordis 插件开发业务 HTTP API、通过 `ctx.Router()` 声明路由与挂载中间件 |
| `new-async-task` | 基于 Cordis 插件通过 `ctx.Task()` 与 `ctx.Schedule()` 注册 Asynq 异步任务与定时调度 |
| `new-setting` | 基于 Cordis 插件通过 `ctx.Settings()` 声明配置 Schema、绑定 YAML 配置或管理台热加载设置 |
| `database-guide` | 插件自包含 `embed.FS` 独立 Goose SQL 迁移（PG/SQLite 双方言、ClickHouse 分析库）；以及 ClickHouse 批量写入、`pkg/batchwriter` 接入、分析表异步 flush 与背压策略 |
| `cache-framework` | 基于 `ctx.Cache()` 与 `contracts.CacheService` 访问三层缓存（RAM L1 + Redis L2 + Pub/Sub 同步） |
| `logstore` | 日志/分析用途表、`plugins/domain/risk_control/logstore`、切换日志主库、PG/SQLite 回落 |
| `file-upload` | 业务上传文件、Worker 程序化摄取、`upload.Ingest` / `contracts.StorageService`、文件访问与统计 |
| `push-notification` | 系统通知推送事件、统一触发器投递、带消息推送的业务功能 |
| `go-logging` | 选择日志方案、配置 slog、编写结构化日志语句、决定日志级别或为日志添加请求上下文 |
| `release-guide` | 根据自上一正式版本 Tag 以来的提交整理 Version Bump 提交信息以触发双语 Release |
| `code-review-skill` | 进行代码审查（Code Review）、PR 评审、代码质量与安全性审查、检查代码坏味道 |
| `shadcn` | 添加、修改或组合 shadcn/ui 组件 |

## 严格遵循事项 (Guardrails)

### 上游优先与下游合并规范 (Upstream-First)

- Wavelet是一个 Cordis 微内核插件化架构的开源项目，所有框架层改动必须在上游修改并合并到下游，禁止直接在下游修改框架层代码。
- **归属判定**：改动位于框架层（`backend/core/`、`backend/pkg/`、通用 `backend/plugins/drivers|infra|domain/`、Cordis/微内核机制）→ **必须先在上游修改**；仅下游业务（`backend/downstream/`、产品页面与业务插件）可直接在下游修改。
- **标准流程**：上游改代码 + 补测试 + 本地 commit（禁止 push）→ 下游 `git fetch wavelet` 确认带入范围（避免拖入无关提交）→ `git merge wavelet/main` → 下游重跑相关测试验证。
- **严禁**直接在下游修改框架层代码（会造成双源分叉、合并冲突）。若已误改，先 `git revert` 撤销下游改动，再按标准流程合并上游。

- 切勿删除 `frontend/node_modules`。
- 保持 `backend/pkg/` 绝对纯净，属于底层通用基础库，**严禁依赖项目上层包（如 `Wavelet/core/*`、`Wavelet/plugins/*`）**；保持 `backend/pkg/util/` 绝对纯净无状态，禁止导入 Gin、GORM、sessions 等 Web/数据库框架包。
- 测试用例禁止硬编码相对路径创建临时目录，统一使用 Go 内置 `t.TempDir()`。
- 修改 API Handler 后运行 `make swagger`，完成代码开发后必须依次运行 `make code-check` 与 `make format`。

### Cordis 架构核心防线与分层规范
- **微内核 (`backend/core/`)**：
  - 上下文总线（`Context`）、泛型依赖注入（`Container`）、生命周期编排（`Lifecycle`）、扩展点定义（`extpoints/`）与领域事件总线（`EventBus`）。
  - **严禁**包含任何具体业务逻辑，**严禁** import `gin`、`gorm`、`asynq` 等具体运行时依赖。
- **服务契约 (`backend/core/contracts/`)**：
  - 跨插件通信的统一公开 Go Interface（如 `AuthService`、`UserService`、`CacheService`、`DBService`、`StorageService`）与公共 DTO。
  - **严禁**包含任何具体业务实现或 SQL 操作。
- **自包含插件 (`backend/plugins/`)**：
  - 所有业务功能与驱动实现均以插件形式存在（`backend/plugins/drivers/`、`backend/plugins/infra/`、`backend/plugins/domain/` 或下游 `backend/downstream/`）。
  - 每个插件实现 `core.Plugin`（`Name() string` 与 `Apply(ctx *core.Context) error`）。
  - **统一插件分层架构与标准模板**：
    - **开发模板唯一基准**：所有插件统一以 `backend/downstream/plugins/custom_example` 为基准模板构建。
    - **物理子包隔离规范**：统一采用物理子包结构（`plugin.go`, `consts/`, `controller/`, `service/`, `dao/`, `model/` [含 `entity/`, `do/`], `migrations/` [含 `postgres/`, `sqlite/`]）。**严禁在根包平铺 `handlers_*`、`service_*`、`dao_*` 等前缀文件**，子包内文件直接按业务实体命名（如 `hello.go`, `user.go`），严格约束 `controller -> service -> dao -> model` 单向依赖。
- **插件通信与依赖隔离**：
  - **严禁跨包 import internal/私有实现**：插件之间严禁直接 import 对方具体实现包代码。
  - **单向服务契约调用**：调用方仅面向 `backend/core/contracts` 编程，在 `Apply` 中通过 `core.Provide[contracts.XxxService](ctx, svc)` 注册服务，通过 `core.Inject[contracts.XxxService](ctx)` 或 `ctx.Using(func(svc contracts.XxxService) { ... })` 声明式解析。
  - **事件总线广播**：状态联动与解耦通信统一通过强类型事件 `ctx.Events().Emit()` 广播，由感兴趣的插件通过 `ctx.Events().On()` 订阅，消除双向依赖与循环引用。
- **扩展点自包含注册**：
  - **HTTP 路由与白名单机制**：
    - 插件自包含在 `Apply` 中通过 `ctx.Router().Group(...)` 挂载路由与中间件，禁止跨插件散落注册。
    - **白名单机制**：`driver_http` 与微内核扩展点提供路由白名单支持（`ctx.Router().RegisterWhitelist(patterns...)`），支持精确路径与通配符（如 `/api/v1/oauth/*`）。
    - **所有权主动声明**：认证域（`auth` 插件）与各业务插件必须在 `Apply` 中主动注册其公开/免鉴权接口（如 `/api/v1/user/login`、`/api/v1/oauth/callback`、`/api/v1/cap/*` 等）。
    - **鉴权中间件放行防线**：`auth` 提供的登录鉴权中间件（`LoginRequired`）必须先执行白名单匹配并自动放行，彻底杜绝免鉴权接口被全局或组级鉴权中间件误拦截（返回 401 Unauthorized）。
  - **异步与定时任务**：插件自包含在 `Apply` 中通过 `ctx.Task().Register(...)` 与 `ctx.Schedule().RegisterCron(...)` 声明。
  - **静态启动配置**：插件自包含在 `Apply` 中通过 `ctx.Config().Bind("<prefix>", &cfg)` 读取**自己声明**的配置，字段以 tag 表达来源：`config`（yaml 路径）、`env`（覆盖变量名）、`default`、`autoEnable`（该变量存在即置真）、`secret`（导出脱敏）。需要在 `Apply` 之前被门禁求值的键，必须在 `DeclareConfig()` 中提前声明并实现 `core.ConfigGatedPlugin`。新增基础设施 key 保持顶层命名（`redis.*`），插件私有配置归 `plugins.<name>.*`。**严禁**再造全局配置单例或在 `backend/pkg/` 读取配置。
  - **动态设置**：插件自包含在 `Apply` 中通过 `ctx.Settings().Register(core.SettingSchema{...})` 声明可热更新的管理台设置模式（与上面的静态启动配置分属两层）。
  - **数据迁移**：插件自包含在内部维护 `migrations/*.sql`，通过 `//go:embed` 打包并在 `Apply` 中通过 `ctx.Migrations().Register(pluginID, embedFS)` 注入。
- **表单一所有者原则 (Single Owner Principle)**：
  - 每张数据表有且仅由一个所有者插件声明与维护（表名使用插件前缀如 `w_order_*`）。
  - 严禁插件 B 跨过所有者插件 A 直接 DDL/DML 旁路读写表 A，必须调用插件 A 暴露的 `contracts` 接口或订阅事件。
- **平台服务复用**：
  - 文件摄取统一使用 `upload.Ingest` / `contracts.StorageService`，禁止绕过存储域直接操作底层 Bucket 或直写文件表。
  - 业务缓存统一使用 `ctx.Cache()`（`contracts.CacheService`）或标准缓存框架，禁止自研不带失效广播的本地 map。
  - 数据库操作通过 `ctx.DB()`（`contracts.DBService`）获取受事务与 Trace 保护的连接。

## 后端开发规范

### API 响应规范
- **统一信封**：`{ "error_msg": "", "data": ... }`
- **成功**：HTTP 200，写出 `c.JSON(http.StatusOK, response.OK(data))` 或 `response.OKNil()`。
- **失败**：使用 `backend/pkg/response` 的 `Abort*` 系列函数（如 `AbortBadRequest`、`AbortUnauthorized`、`AbortNotFound`、`AbortInternal`）中断请求。
- **错误文案**：使用模块内 `errs.go` 中的 camelCase 字符串常量（如 `errBindParamsFailed`），禁止暴露底层数据库/系统错误细节给客户端。
- **Service/Logics 分工**：业务逻辑层只接受 `context.Context`，返回 `(result, error)`，严禁依赖 `*gin.Context` 或调用 `c.JSON`/`Abort*`。
- **错误日志**：底层错误在 Handler/Logic 边界用 `backend/pkg/logger` 打印日志，禁止使用 `_ = ...` 静默吞掉关键错误。

### 数据库操作
- 插件数据库表结构严禁使用 GORM AutoMigrate，统一编写 Goose SQL 迁移并嵌入二进制。
- 不创建物理外键（显式建索引）；Go 模型零值需与数据库默认值匹配。
- **SQL LIKE 查询防注入与转义**：所有含用户输入的模糊查询必须调用 `backend/pkg/util.EscapeLike` 转义通配符，并显式指定 `ESCAPE '\\'` 语法（如 `Where("username LIKE ? ESCAPE '\\'", util.EscapeLike(keyword)+"%")`），同时兼容 PostgreSQL 与 SQLite 方言并杜绝通配符注入攻击。

### 并发与安全防护规范
- **Goroutine 安全**：禁止直接使用裸 `go func()`；统一使用 `backend/pkg/util.Go`，确保具备未捕获 panic 恢复和调用栈日志记录能力。
- **Pub/Sub 监听并发安全**：启动 Redis Pub/Sub 订阅监听前，必须捕获局部客户端实例，禁止在 goroutine 闭包中直读可变全局变量；提供停止监听接口时必须维护 `done` 通道等待 goroutine 完整退出后再重置状态，消除数据竞争。
- **Session 固定攻击防御**：用户登录/授权成功后，必须调用 Session 轮换逻辑，防止 Session 固定攻击。
- **防账户枚举与时序攻击**：
  - 登录失败统一返回模糊报错；当查询用户不存在时，必须调用 `pkg/util.DummyCheckPassword` 执行同等开销的 bcrypt 哈希计算，彻底消除时序侧信道攻击。
  - 验证码、签名 Token 等敏感字符串比对必须使用 `crypto/subtle.ConstantTimeCompare` 常量时间比对。
- **敏感端点限流**：登录尝试、OAuth 授权发起等敏感接口必须接入基于 Redis 的滑动窗口限流机制，防止暴力破解与缓存资源耗尽。

## 前端开发规范

- 新特性开发前参考 Next.js 文档与 `frontend/app/(main)/admin/demo` 示例代码。
- **页面容器与标题栏**：
    - 页面根容器统一使用全宽 `w-full`，最外层统一用 `py-6` 或 `py-6 px-1` 对齐边距。
    - 标题容器统一 `flex items-center gap-2`（带操作按钮用 `justify-between`）。
    - 图标直接使用 Lucide 组件（`size-5 text-primary`），禁止包裹背景小卡片或装饰边框。
    - 标题文字统一使用 `<h1 className="text-2xl font-semibold tracking-tight">`。
- **无障碍语义与色彩规范 (a11y & WCAG)**：
    - **标题层级规范 (Heading Hierarchy)**：页面中非顶级结构化标题（如空状态提示、加载提示、卡片眉题/卡片标题、抽屉区块名）严禁滥用 `<h3>`/`<h4>`，统一使用 `<p>` 配合样式，保证屏幕阅读器感知的标题层级连续。
    - **无文本控件无障碍**：所有仅包含图标的按钮（如仅有 Icon 的 Button、Switch、无文本的 SelectTrigger）必须显式添加 `aria-label`。
    - **色彩对比度**：正文、提示、徽章等小字颜色在亮色/暗色模式下必须满足 WCAG AA（对比度 ≥ 4.5:1）。
- **组件拆分与维护**：
    - 物理路由页面 `page.tsx` 仅维护高级骨架与布局。
    - 单文件超过 600 行或含多 Tab/大复杂区块时，必须按就近原则拆分为子组件存放在路由同级的 `components/` 局部目录中。
- **样式与服务**：
    - 优先使用 shadcn/ui 的 `variant` 和全局 CSS 变量，不要在业务代码中硬编码颜色/背景。
    - 前端请求统一在 `frontend/lib/services/<name>/` 中继承 `BaseService` 编写并在 `index.ts` 注册。
- **国际化 (i18n)**：
    - 使用 `next-intl`（**无 URL locale 前缀** / non-routing provider 模式），兼容 `NEXT_STANDALONE_EXPORT` 静态导出。
    - 支持语言：`zh-CN`、`en`；默认 `zh-CN`。
    - 解析优先级：cookie `NEXT_LOCALE`（用户显式选择）→ 浏览器语言 → 默认 `zh-CN`。
    - 文案统一放在 `frontend/messages/{locale}.json`，按命名空间嵌套（`common` / `layout` / `auth` / `settings` / 业务域）。
    - 组件内用户可见文案必须通过 `useTranslations()` / `getTranslations()` 读取；**禁止**新增中英硬编码 UI 字符串（后端返回的 `error_msg`、日志、调试信息除外）。
    - key 使用 camelCase 分层（如 `auth.login.submit`）；完整短语作为 value，禁止在组件内拼接句子。
    - 新增或修改文案时必须**同步**更新 `zh-CN.json` 与 `en.json`，保持 key 树一致。
    - 语言选项展示用自称：`中文` / `English`（不随当前 UI 语言翻译）。
    - 日期/数字格式化使用 locale 感知 helper（如 `formatDateTime`），禁止写死 `'zh-CN'` / `date-fns` 的 `zhCN`。
