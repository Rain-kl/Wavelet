---
name: "cache-framework"
description: "Wavelet 项目专用：当新增或修改基于 Cordis 插件的业务缓存、ctx.Cache() / contracts.CacheService 访问、三层读路径（RAM L1 + Redis L2 + DB L3）、多节点 Pub/Sub 失效同步时必须使用。"
---

# 系统三层缓存框架与开发规范 (Cordis 插件化架构)

本技能指导 Wavelet 在 Cordis 架构下，如何使用平台统一提供的三层缓存服务（`contracts.CacheService`）进行高性能缓存读写与分布式失效同步。

---

## 1. 双插件架构与选择逻辑

平台提供两个互斥缓存插件，由配置门禁自动选择一个激活：

| 插件 | 包路径 | 激活条件 | 特性 |
| :--- | :--- | :--- | :--- |
| `cache` | `plugins/infra/cache` | `redis.enabled = true` | RAM L1 + Redis L2 + Pub/Sub 跨节点失效 |
| `cache_memory` | `plugins/infra/cache_memory` | `redis.enabled = false`（默认）| 纯进程内 RAM，EventBus 通知 |

两者都实现 `contracts.CacheService`，业务代码无感知差异，**不要在业务代码中判断使用的是哪个插件**。

---

## 2. 三层读路径与标准契约（Redis 模式）

Redis 模式下标准读路径为 **本地 RAM (L1) → Redis (L2) → Database (L3)**（由快到慢）：

| 层级 | 技术 | 职责 |
| :--- | :--- | :--- |
| **L1 本地** | `pkg/cache/ram`（Otter）| 进程内纳秒级极速读取，抗最高频热点流量 |
| **L2 共享** | Redis 序列化缓存 | 跨节点共享，具备 TTL 与防击穿保护 |
| **L3 权威** | 关系型数据库 (PostgreSQL / SQLite) | 唯一权威数据源 |

**纯内存模式**（`cache_memory`）只有 L1 RAM，通过 `core.EventBus` 广播 `cache:invalidate` 事件触发同进程内其他订阅者清除本地条目（仅单节点有效）。

### 标准接口契约 (`contracts.CacheService`)

定义于 `core/contracts/cache.go`：

```go
type CacheService interface {
    // Get 从缓存获取并反序列化至 target，若不存在返回 ErrCacheMiss
    Get(ctx context.Context, key string, target any) error

    // Set 存储对象至缓存并设置 TTL
    Set(ctx context.Context, key string, value any, ttl time.Duration) error

    // Delete 彻底移除缓存（清空本地 RAM、删除 Redis 并广播 Pub/Sub 通知全集群清空 RAM）
    Delete(ctx context.Context, key string) error

    // GetOrSet 优先读缓存，若未命中则执行 loader 回源加载并自动回写
    GetOrSet(ctx context.Context, key string, target any, ttl time.Duration, loader func() (any, error)) error

    // Invalidate 是 Delete 的语义别名
    Invalidate(ctx context.Context, key string) error
}
```

`ErrCacheMiss` 定义于同一文件，用 `errors.Is` 判断。

---

## 3. 业务使用标准范式

### 3.1 在插件中注入缓存服务

```go
// 在 plugin.go 的 Inject() 中声明依赖
func (p *Plugin) Inject() []reflect.Type {
    return []reflect.Type{
        reflect.TypeFor[contracts.CacheService](),
    }
}

// 在 Apply() 中通过 core.Inject 获取
func (p *Plugin) Apply(ctx *core.Context) error {
    cache, err := core.Inject[contracts.CacheService](ctx)
    if err != nil {
        return err
    }
    svc := &myService{cache: cache}
    core.Provide[contracts.MyService](ctx, svc)
    return nil
}
```

### 3.2 高性能读穿透 (`GetOrSet`)

业务 Service 推荐优先使用 `GetOrSet`，框架底层自动完成 L1/L2 穿透、回写及并发防击穿：

```go
func (s *OrderService) GetOrderWithCache(ctx context.Context, orderID string) (*Order, error) {
    var order Order
    cacheKey := "order:meta:" + orderID

    err := s.cache.GetOrSet(ctx, cacheKey, &order, 10*time.Minute, func() (any, error) {
        // Cache Miss: 执行 DB 回源查询
        var dbOrder Order
        if err := s.db.WithContext(ctx).First(&dbOrder, "id = ?", orderID).Error; err != nil {
            return nil, err
        }
        return &dbOrder, nil
    })

    if err != nil {
        return nil, err
    }
    return &order, nil
}
```

### 3.3 数据变更与失效广播 (`Invalidate` / `Delete`)

凡涉及数据创建、修改、软删除、状态变更的入口（**包含 HTTP Handler、后台 Worker 任务、定时清理任务**），必须调用缓存失效：

```go
func (s *OrderService) UpdateOrderStatus(ctx context.Context, orderID string, newStatus string) error {
    // 1. 更新数据库权威数据
    if err := s.db.WithContext(ctx).Model(&Order{}).Where("id = ?", orderID).Update("status", newStatus).Error; err != nil {
        return err
    }

    // 2. 广播失效缓存（自动清除本机 L1、删除 Redis L2，并向集群广播 Pub/Sub 消息清空其他节点 L1）
    return s.cache.Invalidate(ctx, "order:meta:"+orderID)
}
```

---

## 4. 配置说明

`cache` 插件（Redis 模式）读取 `redis.*` 配置（`plugins/infra/cache/config.go`）：

```yaml
redis:
  enabled: true
  addr: "localhost:6379"
  password: ""
  db: 0
```

`cache_memory` 插件（纯内存模式）在 `redis.enabled = false`（默认）时自动激活，无需额外配置。

两插件同时各自注册 `contracts.LimiterService`（Redis 实现 / 内存 fallback 实现）。

---

## 5. 核心规则与禁止写法 (Guardrails)

1. **严禁自研本地 map 缓存**：
   - 严禁在插件内编写 `sync.RWMutex + map[string]Xxx` 的裸内存缓存，无法感知多节点数据变更，必然引发多机脏读。
2. **写路径必须全覆盖失效**：
   - 不仅在 API 修改时失效，后台 Worker、定时任务执行数据清理或变更时，必须同步触发 `cache.Invalidate`。
3. **Key 命名空间规范**：
   - 缓存 Key 必须带插件命名空间前缀（如 `order:meta:{id}`、`auth:session:{token}`）。
4. **不可在业务高频读接口中绕过缓存直查 DB**。
5. **不可直接 import `plugins/infra/cache` 或 `plugins/infra/cache_memory`**：
   - 业务插件只面向 `contracts.CacheService` 接口，通过 `core.Inject` 获取实例。

---

## 6. 质量与测试验证

```bash
make format
make code-check
go test ./plugins/...
```
