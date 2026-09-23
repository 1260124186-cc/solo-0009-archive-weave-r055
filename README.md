# ArchiveWeave

ArchiveWeave 是 `solo-0009-archive-weave` 的 Go 后端基线。它面向地方档案馆、整理团队和研究使用者，提供档案元数据受理、修订、提交审核、审核决定、公开筛选和结构化导出能力。服务只使用本机磁盘数据，不依赖数据库或外部接口。

## 构建与运行

要求 Go 1.26 或更高版本。

```bash
go build ./...
go run ./cmd/archiveweave --addr 127.0.0.1:8080 --data ./var/artifacts.json --audit ./var/history.json
```

服务启动后可访问 `GET /healthz`。写入接口默认开启，可通过 `--read-only` 或 `ARCHIVE_WEAVE_READ_ONLY=true` 只提供读取能力。

## 目录结构

- `cmd/archiveweave`：HTTP 服务入口。
- `cmd/archiveweave-check`：本地工作流检查入口。
- `internal/domain`：档案实体、生命周期、审计事件和校验规则。
- `internal/catalog`：受理、修订、审核、检索、导出、摘要和组合编排。
- `internal/storage`：内存存储、JSON 持久化、原子文件替换和审计保存。
- `internal/httpapi`：HTTP 路由、请求解析、错误映射和只读边界。
- `internal/observability`：结构化日志和运行计数。
- `internal/workflowcheck`：通过真实 HTTP 路径执行验收场景。

## HTTP 接口

- `GET /healthz`：服务状态。
- `GET /metrics`：进程内请求、写入和失败计数。
- `GET /catalog/summary`：档案总量、状态分布、年份范围和常用标签。
- `POST /artifacts`：受理单条草稿档案。
- `POST /artifacts/batch`：批量受理草稿档案。
- `GET /artifacts`：检索档案，支持关键词、多标签组合、标签排除、年份范围、状态、视图、排序和分页。
- `GET /artifacts/{id}`：读取单条档案。
- `PUT /artifacts/{id}/metadata`：修订草稿元数据。
- `POST /artifacts/{id}/submit`：提交审核。
- `POST /artifacts/{id}/review`：批准或退回。
- `GET /artifacts/{id}/history`：读取按版本排序的审计轨迹。
- `GET /artifacts/{id}/export`：导出单条已批准档案。
- `GET /collections/export`：导出带校验和的公开集合。

写接口可以读取 `X-Archive-Actor` 请求头记录操作者。档案创建、修订、提交、批准和退回都会生成不可覆盖的审计事件。

## 筛选参数

`GET /artifacts` 与 `GET /collections/export` 共享同一套筛选参数，列表命中的集合与导出的集合始终一致，规范化后的筛选描述会写入响应的 `query` 字段：

- `q`：关键词，匹配标题、摘要、来源和标签。
- `tag`：包含标签，支持逗号分隔（`tag=mining,map`）或重复参数（`tag=mining&tag=map`），最多 24 个。
- `tag_mode`：多标签组合关系，`all`（默认，须全部命中）或 `any`（命中任一即可）。
- `exclude_tag`：排除标签，格式同 `tag`，命中任一排除标签的档案被剔除。
- `year`：精确年份；`year_from` / `year_to`：闭区间年份范围。`year` 不能与 `year_from`、`year_to` 同时使用。
- `view`：`public`（默认，只含已批准档案）或 `working`（内部工作视图，含草稿与待审核）。
- `status`、`sort`、`offset`、`limit`：状态过滤、排序与分页；公开视图只允许 `status=approved`。

没有命中的请求返回 `200`，`count` 为 0 且 `artifacts` 为空数组，导出同样生成带有效校验和的空集合。非法组合（如 `year` 与年份范围混用、`year_from` 晚于 `year_to`、同一标签既包含又排除、`tag_mode` 缺少 `tag`）返回 `400` 与 `invalid_input` 错误，并指出冲突字段。

## 工作流检查

每条生产工作流都有独立的本机检查命令：

```bash
go run ./cmd/archiveweave-check --workflow intake-artifact
go run ./cmd/archiveweave-check --workflow update-metadata
go run ./cmd/archiveweave-check --workflow submit-review
go run ./cmd/archiveweave-check --workflow decide-review
go run ./cmd/archiveweave-check --workflow search-export
go run ./cmd/archiveweave-check --workflow import-batch
```

也可以执行 `go run ./cmd/archiveweave-check --workflow all` 顺序检查全部场景。检查工具会启动真实 HTTP handler，创建临时数据文件并在结束时清理。

## 配置

| 环境变量 | 作用 | 默认值 |
| --- | --- | --- |
| `ARCHIVE_WEAVE_ADDR` | HTTP 监听地址 | `:8080` |
| `ARCHIVE_WEAVE_DATA` | 档案 JSON 路径 | `./archive-weave-data.json` |
| `ARCHIVE_WEAVE_AUDIT` | 审计 JSON 路径 | `./archive-weave-history.json` |
| `ARCHIVE_WEAVE_READ_ONLY` | 是否禁用写接口 | `false` |
| `ARCHIVE_WEAVE_SHUTDOWN_TIMEOUT` | 优雅停机时限 | `5s` |

## 测试边界

本阶段按 healthy baseline 规则刻意不生成单元测试、测试夹具或浏览器测试，也不提供 `test_command`。后续工程任务阶段负责加入红绿验证测试。当前 `workflow_checks` 是生产级的本机 smoke 路径，用于验证构建、状态迁移、错误传播、持久化和导出行为。
