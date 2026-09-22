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
- `GET /artifacts`：检索档案，支持关键词、标签、年份、状态、视图、排序和分页。
- `GET /artifacts/{id}`：读取单条档案。
- `PUT /artifacts/{id}/metadata`：修订草稿元数据。
- `POST /artifacts/{id}/submit`：提交审核。
- `POST /artifacts/{id}/review`：批准或退回。
- `GET /artifacts/{id}/history`：读取按版本排序的审计轨迹。
- `GET /artifacts/{id}/export`：导出单条已批准档案。
- `GET /collections/export`：导出带校验和的集合，筛选规则与 `GET /artifacts` 完全一致。

### 筛选参数

`GET /artifacts` 与 `GET /collections/export` 使用同一套筛选参数和同一个规范化解析器，返回体（或集合文件）中的 `query` 字段会完整描述实际生效的筛选条件，因此列表和导出不可能出现一边筛选、一边未筛选的差异：

| 参数 | 说明 |
| --- | --- |
| `q` | 关键词，在标题、摘要、来源和标签文本中做不区分大小写的包含匹配。 |
| `tag` | 必需标签，可重复给出（如 `tag=map&tag=mining`），也支持逗号分隔；多个标签为 **AND** 关系，档案必须同时拥有全部标签。标签匹配包含别名。 |
| `exclude_tag` | 排除标签，语法同 `tag`；命中其中任意一个标签的档案都会被排除。 |
| `year` | 精确年份（1000–2100 的四位数字）。 |
| `year_from` / `year_to` | 包含两端的年份范围，可单独或组合给出。 |
| `status` | 状态过滤；默认视图下只允许 `approved`。 |
| `view` | `public`（默认，仅已批准）或 `working`（草稿、待审核和已批准均可见）。 |
| `sort` | `recent`（默认）、`oldest` 或 `title`。 |
| `offset` / `limit` | 分页，`limit` 范围 1–200。 |

非法组合返回 `400` 与 `invalid_input` 错误体（含字段名），包括：年份不是四位数字；`year` 与 `year_from`/`year_to` 同时出现；`year_from` 晚于 `year_to`；同一标签同时出现在 `tag` 和 `exclude_tag`；以及原有的非法 `view`、`sort`、`status`、分页参数。筛选本身合法但没有匹配项时返回 `200`，`count` 为 `0`、`artifacts` 为空数组；集合导出同样返回 `count: 0` 和对空集合计算的有效校验和。

写接口可以读取 `X-Archive-Actor` 请求头记录操作者。档案创建、修订、提交、批准和退回都会生成不可覆盖的审计事件。

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
