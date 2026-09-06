---
name: "file-upload"
description: "Wavelet 项目专用：当业务需要上传文件、读取已上传文件、在 Worker/任务中程序化摄取字节流、选择存储引擎能力、或排查 w_uploads / 文件统计异常时必须使用。本技能指导 storage 与 upload 分层、upload.Ingest 策略选型、前后端接入与禁止旁路写表。"
---

# 存储引擎与文件上传开发规范

本技能是 Wavelet **文件上传与对象存储**的唯一开发指导。开始开发前先阅读仓库根目录 [AGENTS.md](../../../AGENTS.md)，遵守项目级核心规则。

---

## 架构分层（必须理解）

Wavelet 将「对象存储」与「上传业务」分为两层，**禁止混用职责**：

| 层级 | 包路径 | 职责 | 业务是否直接调用 |
| :--- | :--- | :--- | :--- |
| **对象存储引擎** | `plugins/infra/storage/` | `StorageService` 接口：`Put` / `Get` / `Delete`；按配置切换 Local / S3 / R2 / OSS / WebDAV | **禁止**（仅 upload 域内部使用） |
| **上传域服务** | `plugins/domain/upload` | `w_uploads` 记录、权限、秒传、统计、文件服务、`upload.Ingest` | **upload 插件内部**直接调用 |
| **跨插件接口** | `core/contracts.UploadService` | 跨插件调用上传域能力的统一合约 | **其他插件**必须使用此接口 |
| **上传 HTTP 入口** | `plugins/domain/upload/handler` | `POST /api/v1/upload` 等 multipart 接口 | 前端 / 用户侧上传 |
| **文件访问** | `plugins/domain/upload/filesrv` | `GET /f/:id` 流式响应、访问控制、图片 WebP 压缩 | 展示 / 下载 |

```text
其他插件 ──► core.Inject[contracts.UploadService](ctx).Ingest / GetByID / OpenStoredUpload
upload 域内 ──► upload.Ingest / upload.Remove（唯一写入门禁）
                  ├── storage.Backend.Put/Get/Delete
                  ├── repository.CreateUpload（仅 upload 内部）
                  └── uploadstats.ApplyUploadStatsDeltaTx（ingest 内置，禁止业务直调）
```

---

## 核心防线（Guardrails）

以下写法**一律禁止**：

```go
// ❌ 业务包直接写 blob（StorageService 仅 upload 域使用）
storageSvc.Put(ctx, key, reader, size, mime)

// ❌ 旁路写 w_uploads
db.DB(ctx).Create(&model.Upload{})
repository.CreateUpload(ctx, upload)   // 仅 plugins/domain/upload 内部允许

// ❌ 手动维护统计
uploadstats.ApplyUploadStatsAdd(ctx, upload)  // 已 Deprecated

// ❌ 业务表存物理路径
invoice.FilePath = "uploads/2026/01/02/123.pdf"

// ❌ 跨插件直接 import upload 实现包
import "Wavelet/plugins/domain/upload"  // 其他插件禁止
```

**正确做法**：
- 业务表只存 `upload_id`（`uint64` / JSON string），通过 `/f/{id}` 或 `contracts.UploadService.OpenStoredUpload` 访问
- 其他插件通过 `core.Inject[contracts.UploadService](ctx)` 获取服务
- upload 域内部直接调用 `upload.Ingest` / `upload.Remove`

---

## 跨插件：使用 contracts.UploadService

当 **其他插件**（非 upload 插件本身）需要上传或读取文件时，必须通过合约接口：

```go
import (
    "Wavelet/core"
    "Wavelet/core/contracts"
)

func (p *Plugin) Apply(ctx *core.Context) error {
    ctx.Using(func(uploadSvc contracts.UploadService) {
        // 使用 uploadSvc.Ingest / GetByID / OpenStoredUpload 等
    })
    return nil
}

// 在 logics 中注入后调用
func doSomething(ctx context.Context, uploadSvc contracts.UploadService, data []byte) error {
    dto, err := uploadSvc.Ingest(ctx, contracts.UploadIngestRequest{
        UserID:    userID,
        Type:      "invoice",
        Reader:    bytes.NewReader(data),
        Size:      int64(len(data)),
        FileName:  "report.pdf",
        MimeType:  "application/pdf",
        Extension: "pdf",
    })
    // contracts.UploadService.Ingest 内部自动计算 SHA-256 并使用 PolicyDedupNewRecord
    _ = dto.ID // uint64 上传 ID
    return err
}
```

> `contracts.UploadIngestRequest` 不需要手动传 `Hash`，服务内部自动计算。

---

## upload 域内：Ingest 策略选型（Policy Decision）

**仅 `plugins/domain/upload` 内部**使用 `upload.Ingest`，根据场景选择 `Policy`：

| 场景 | Policy | 哈希命中时 | 未命中时 | 典型调用方 |
| :--- | :--- | :--- | :--- | :--- |
| 用户 HTTP 上传（含秒传） | `PolicyDedupNewRecord` | 复用 path，**新建记录 + 统计** | 写 blob + 新建记录 + 统计 | `handler.UploadFile`（已内置） |
| Worker 生成全新文件 | `PolicyCreate` | 不查重，始终写 blob + 记录 | 同左 | 报表导出、定时生成 |
| 镜像 / 去重摄取（Pixez） | `PolicyResolveExisting` | **直接返回已有记录**，不建新记录、不加统计 | 写 blob + 新建记录 + 统计 | 异步镜像任务 |
| 业务只需引用已有文件 | 不调 Ingest | — | — | 业务 API 校验 `upload_id` 即可 |

### Result 字段含义

| 字段 | 含义 |
| :--- | :--- |
| `Created` | 是否新建了 `w_uploads` 记录 |
| `Stored` | 是否写入了新 blob |
| `Resolved` | 是否通过哈希解析到已有记录（仅 `PolicyResolveExisting`） |

---

## 后端：upload 域内程序化上传（Worker / 业务逻辑）

在 `logics.go`（接受 `context.Context`，不依赖 `*gin.Context`）中调用：

```go
import (
    "bytes"

    "Wavelet/plugins/domain/upload"
    "Wavelet/plugins/domain/upload/models"
)

func ingestMirrorFile(ctx context.Context, userID uint64, data []byte, hash, filename, mime, ext string) (models.Upload, error) {
    accessMode := 1
    result, err := upload.Ingest(ctx, upload.IngestRequest{
        UserID:     userID,
        Reader:     bytes.NewReader(data),
        Size:       int64(len(data)),
        FileName:   filename,
        MimeType:   mime,
        Extension:  ext,
        Hash:       hash, // 必填：SHA-256 hex
        Type:       "your_biz_type",
        AccessMode: &accessMode,
        Metadata: models.UploadMetadata{
            Extra: map[string]any{"source": "worker"},
        },
        Policy: upload.PolicyResolveExisting,
    })
    if err != nil {
        return models.Upload{}, err
    }
    return result.Upload, nil
}
```

### Request 关键字段

| 字段 | 说明 |
| :--- | :--- |
| `Hash` | **必填**，推荐 SHA-256 hex；用于秒传 / 镜像去重 |
| `Type` | 业务分类（如 `avatar`、`invoice`、`pixez_mirror`），用于筛选与统计 |
| `AccessMode` | `nil` 时按 type 默认：`avatar`（`shared.DefaultPublicUploadType`）→ 公开(1)，其余 → 私有(0) |
| `SkipExtensionCheck` | Worker 场景若已自行校验扩展名，可设为 `true` |
| `ObjectKeyFn` | 可选自定义存储路径；默认 `uploads/YYYY/MM/DD/{id}.{ext}` |
| `Status` | 默认 `UploadStatusUsed`；可设为 `UploadStatusPending`（未确认） |

### 错误处理

| 错误变量 | 含义 | Handler 映射建议 |
| :--- | :--- | :--- |
| `upload.ErrIngestStorageReadOnly` | 存储迁移维护中 | `response.AbortConflict` |
| `upload.ErrIngestForbidden` | 无权删除他人文件 | HTTP 403 |
| `shared.ErrUnsupportedFormat`（字符串常量） | 扩展名不在白名单 | `response.AbortBadRequest` |

### 删除

```go
// 管理员 / 系统删除
_, err := upload.Remove(ctx, uploadID)

// 用户删除自己的文件
_, err := upload.RemoveOwned(ctx, userID, uploadID)
```

### 查找与读取

```go
// 按 ID 读取（upload 域内部）
uploadRec, err := upload.GetActiveUploadByID(ctx, uploadID)

// 按哈希查找可复用记录（upload 域内部）
existing, err := upload.FindByHash(ctx, hash, size)

// 批量按 ID 读取 active 记录（upload 域内部）
uploads, err := upload.ListUploadsByIDs(ctx, ids)

// 跨插件：读取并打开流（其他插件用 contracts.UploadService）
opened, err := uploadSvc.OpenStoredUpload(ctx, uploadID)
defer opened.Body.Close()
```

---

## 后端：业务 API 引用已上传文件

推荐 **两步流程**（先上传、后提交业务）：

1. 前端 `POST /api/v1/upload` → 获得 `upload.id`
2. 业务 API 接收 `upload_id`，用 `upload.GetActiveUploadByID` 校验存在且 `status` 不为 deleted
3. （可选）校验 `upload.Type` 是否为预期业务类型
4. 将 `upload_id` 写入业务表字段（如 `cover_file_id`）

**禁止**在业务 Handler 中重复实现 multipart 解析，除非有极强的特殊协议需求。

---

## 前端：用户侧上传

使用 `frontend/lib/services/upload/`：

```typescript
import { services } from '@/lib/services'
import { getFileUrl } from '@/lib/services/upload'

// 上传文件
const upload = await services.upload.uploadFile(file, 'invoice', { orderId: '123' })

// 展示（支持 low / medium / high / origin 质量）
const url = getFileUrl(upload.id)           // → /f/{id}（origin）
const thumb = getFileUrl(upload.id, 'low')  // → /f/{id}?quality=low

// Base64 图片（头像等）
const res = await services.upload.uploadBase64Image(croppedBase64, 'avatar', 'avatar.png')

// 我的文件列表
const list = await services.upload.listMyUploads(1, 20, 'keyword', 'invoice', 'pdf')

// 更新文件信息（文件名、访问权限）
await services.upload.updateMyFile(id, '新文件名.pdf', 0)

// 批量下载（ZIP）
const blob = await services.upload.batchDownloadMyFiles(['id1', 'id2'])

// 删除我的文件
await services.upload.deleteMyFile(id)

// 获取下载直链
const downloadUrl = services.upload.getDownloadUrl(id)
```

### 前端规范

- 新增上传相关 API 时，扩展 `UploadService` / `AdminUploadService`，在 `frontend/lib/services/index.ts` 注册
- 图片预览使用 `getFileUrl(id, quality?)` 或 `FileImagePreview` 组件
- 业务表单项只提交 `upload_id`（字符串形式），不要提交 blob URL 或 `file_path`
- `Upload.id` 在前端为 `string`（后端 uint64 通过 `json:",string"` 传输）

---

## 统计与排查

`w_upload_stats` 由 `upload.Ingest` / `upload.Remove` **自动维护**，业务不得手动增量。

若发现 trend / total 与 `w_uploads` 不一致（常见于历史旁路写表）：

```go
upload.RebuildUploadStats(ctx) // 从 w_uploads 全量重建统计
```

排查清单：

1. 业务是否绕过 `upload.Ingest` 直接 `db.Create(&model.Upload{})`？
2. 是否手动调用已 Deprecated 的 `ApplyUploadStatsAdd` / `ApplyUploadStatsRemove`？
3. 删除是否走 `upload.Remove`（须在软删**前**扣减统计）？

---

## 测试要求

### 后端 ingest 测试

- 使用 `shared.SetupTestEnvironment(t)` 初始化 DB
- 存储 mock：`storage.MockStorage(...)` + `storage.IsEnabledFunc = func() bool { return true }`
- **禁止**在源码目录硬编码 `uploads/test` 路径；本地文件测试用 `t.TempDir()` 或 mock backend
- 覆盖：三种 Policy、Remove 后统计归零、ReadOnly 拒绝写入

参考：`plugins/domain/upload/ingest/ingest_test.go`

### Handler 回归

修改 upload handler 后运行：

```bash
go test ./plugins/domain/upload/...
make code-check
```

若变更 HTTP 接口，运行 `make swagger`。

---

## 存量代码迁移（旁路写表 → Ingest）

将以下模式：

```go
storageSvc.Put(ctx, key, reader, size, mime)
db.DB(ctx).Create(&upload)
```

替换为（upload 域内）：

```go
upload.Ingest(ctx, upload.IngestRequest{ Policy: upload.PolicyResolveExisting, ... })
```

或（其他插件跨域调用）：

```go
uploadSvc.Ingest(ctx, contracts.UploadIngestRequest{ ... })
```

迁移完成后执行一次 `upload.RebuildUploadStats(ctx)` 修复历史统计偏差。

---

## 质量门禁 Checklist

完成文件上传相关开发后，确认：

- [ ] 其他插件通过 `contracts.UploadService` 接口访问上传域，不直接 import `plugins/domain/upload`
- [ ] upload 域内业务无 `repository.CreateUpload` / `SoftDeleteUpload` 旁路调用
- [ ] 业务表存 `upload_id`，不存 `file_path`
- [ ] Worker 摄取使用正确的 `Policy`
- [ ] 新增测试覆盖 ingest 路径（`t.TempDir()` 代替硬编码路径）
- [ ] `make code-check` 通过
- [ ] HTTP 变更已 `make swagger`