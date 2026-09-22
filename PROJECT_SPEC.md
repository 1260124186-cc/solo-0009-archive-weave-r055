# ArchiveWeave 项目规格

## 项目目标

ArchiveWeave 是一个单机运行的档案元数据服务。它把文字描述、来源信息、年代和主题标签整理成可版本化的档案实体，在草稿、待审核和已批准之间执行受控流转，并保留每一次变更的审计轨迹。公开检索只显示已批准档案，集合导出使用稳定排序和 SHA-256 校验和。

## 使用者与职责

- 受理人员：创建单条或批量草稿，补充标题、摘要、来源、年代和标签。
- 修订人员：在草稿阶段更新元数据，形成可追踪的新版本。
- 审核人员：对待审核档案作出批准或退回决定，退回时必须给出说明。
- 研究使用者：按公开视图、关键词、多标签组合（AND）、标签排除和年份（精确或包含两端的范围）筛选档案，并读取结构化导出。

## 核心实体

### Artifact

- `id`：不可变标识。
- `title`、`summary`、`source`：必填文字元数据。
- `year`：四位年代，范围为 1000 到当前年份。
- `tags`：最多 24 个标准化标签，每个最多 48 个字符。
- `status`：`draft`、`pending_review` 或 `approved`。
- `version`：从 1 开始，每次生命周期或元数据变更递增。
- `created_at`、`updated_at`、`submitted_at`、`decided_at`：UTC 时间。
- `reviews`：该档案的审核决定列表。

### AuditEvent

- `artifact_id`：事件所属档案。
- `action`：`create`、`update_metadata`、`submit_review`、`approve` 或 `return`。
- `from_status`、`to_status`：变更前后状态。
- `actor`、`note`：操作者和可选说明。
- `version`：对应档案版本。
- `occurred_at`：事件发生时间。

### Collection

- `schema_version`：导出格式版本。
- `generated_at`：生成时间。
- `query`：规范化后的筛选描述。
- `count`、`artifacts`：结果数量和档案列表。
- `checksum`：对不含校验和字段的 JSON 内容计算 SHA-256。

## 状态规则

- 新档案从 `draft` 开始，版本为 1。
- `draft -> pending_review` 仅在必填元数据完整时允许。
- `pending_review -> approved` 表示审核通过。
- `pending_review -> draft` 表示退回，必须包含至少两个字符的说明。
- 已提交档案不能再直接修改元数据。
- 单条导出只允许 `approved` 档案。
- 默认查询只包含 `approved` 档案；显式 `view=working` 才进入内部工作视图。

## 工作流

1. **单条受理**：`POST /artifacts` 解析并校验字段，创建草稿，原子写入档案和创建事件。
2. **元数据修订**：`PUT /artifacts/{id}/metadata` 确认状态为草稿，标准化字段，递增版本并追加事件。
3. **提交审核**：`POST /artifacts/{id}/submit` 检查完整性，执行状态迁移并记录提交时间。
4. **审核决定**：`POST /artifacts/{id}/review` 校验决定和退回说明，记录审核记录、状态和版本。
5. **筛选导出**：`GET /collections/export` 使用与 `GET /artifacts` 相同的查询解析与过滤逻辑（多标签 AND、`exclude_tag` 排除、`year` 精确年份与 `year_from`/`year_to` 范围），排序并生成带校验和的集合；筛选条件必须进入规范化的 `query` 描述。合法但无匹配返回 200 与空集合；非法组合（`year` 与范围混用、范围倒置、同一标签既要求又排除、非法年份或分页参数）返回 400。
6. **批量受理**：`POST /artifacts/batch` 先校验整批输入，再顺序持久化；失败时回滚已写入档案和事件。

## 模块与依赖方向

- `cmd` 负责组合配置、存储和服务，并提供 HTTP 与检查命令。
- `httpapi` 依赖 `catalog` 和 `domain`，只处理协议边界。
- `catalog` 依赖 `domain` 与存储接口，负责用例编排。
- `domain` 不依赖外层模块，集中维护实体与状态规则。
- `storage` 实现档案存储和审计存储，提供确定性 JSON 写入和并发保护。
- `observability` 提供结构化日志和计数，不参与业务状态。
- `workflowcheck` 只通过公开 HTTP 路由调用服务。

## 并发与资源生命周期

- 内存和 JSON 存储使用读写锁保护映射访问。
- 文件写入先落到同一目录的临时文件，完成同步后原子替换目标文件。
- 服务收到退出信号后使用配置的时限执行 HTTP 优雅停机。
- 工作流检查为每次运行创建独立临时目录，结束或失败时删除。
- 单条变更在审计追加失败时恢复旧档案；批量受理在失败时反向回滚已保存档案与事件。

## 验证计划

- `go build ./...` 验证全部包可编译。
- `go run ./cmd/archiveweave-check --workflow <name>` 逐一验证六条工作流。
- 检查内容覆盖合法输入、版本递增、状态迁移、审核限制、公开筛选、多标签 AND 组合、标签排除、年份范围、非法组合 400、空结果 200、列表与导出的筛选及 `query` 描述一致性、导出校验和、批量写入与摘要总量。
- 所有检查使用临时文件，不访问网络服务，不修改仓库内数据。

## 延后测试边界

本阶段不创建单元测试、集成测试或浏览器测试文件。后续工程任务阶段负责加入自动化测试套件和红绿验证，当前交付仅包含生产代码、文档、清单和可执行 smoke 检查。
