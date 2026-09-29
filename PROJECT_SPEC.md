# Zone Rollout Control 项目规格

## 1. 项目目标

Zone Rollout Control 是面向 DNS 运维人员的 Go HTTP 控制面。它把区域版本编排、解析节点领取租约、探针结果登记和一致性判定串成一条可追溯的上线链。服务只使用本机 JSON 快照，不连接真实 DNS 网络，也不依赖外部平台。

本仓库是健康项目初始化基线，要求可构建、可运行、无预置缺陷。测试在本阶段有意延后，由后续测试阶段补充；当前通过真实 HTTP 路由上的工作流探针进行生产路径验证。

## 2. 使用者

- 区域滚动负责人：创建滚动计划、添加区域版本、启动上线并完成计划。
- 解析节点值守人员：为目标节点领取有期限的租约，提交探针结果。
- DNS 平台集成者：通过稳定 JSON API 接入内部自动化流程。

## 3. 核心实体

### RolloutPlan

一次明确目标序列号、节点数量、成功比例和延迟上限的滚动计划。

- `id`：服务生成的稳定标识。
- `zone`：区域名称，必须是小写 DNS 名称。
- `state`：`draft`、`active` 或 `completed`。
- `minimum_success_ratio`：成功节点占比下限，范围为 0 到 1。
- `maximum_latency_ms`：允许的最大探针延迟。
- `expected_nodes`：计划预期参与的节点数，范围为 1 到 500。
- `version`：每次计划聚合变化后递增。
- `latest_revision_id`：最近一次添加的区域版本。
- `latest_assessment_id`：最近一次完成的一致性判定。
- `created_at`、`activated_at`、`completed_at`：RFC3339 时间。

### ZoneRevision

计划中不可变的区域内容版本。

- `id`、`plan_id`、`serial`、`authority`。
- `entries`：区域条目数组，每项包含 `owner`、`type` 和 `value`。
- `entry_count`：由服务计算，客户端不能覆盖。
- `created_at`。

同一计划内的序列号必须严格递增。条目类型只接受 `A`、`AAAA` 和 `CNAME`。`A` 与 `AAAA` 的值必须是 IPv4 或 IPv6 地址，`CNAME` 的值必须是规范域名。

### ResolverNode

边缘解析节点档案。

- `id`、`node_name`、`region`。
- `state`：`ready`、`leased` 或 `quarantined`。
- `version`、`created_at`、`updated_at`。

节点可以不声明任何能力；未声明能力的旧节点继续按区域规则参与分配。

### CapabilityCatalog

版本化、不可变的能力目录。每次发布得到一个严格递增的新版本，旧版本永久保留。

- `version`：从 1 开始的单调递增版本号。
- `protocols`：协议版本词汇表，规范化为小写、去重并排序。
- `capacity_tiers`：容量等级词汇表，按列表位置定义由低到高的等级顺序。
- `business_tags`：业务标签词汇表，规范化为小写、去重并排序。
- `fingerprint`：上述三类词汇的稳定指纹，用于识别重复目录。
- `request_key`：首次发布该版本的请求键。
- `created_at`。

目录只追加、不改写：发布新版本不会改变任何已有节点档案或租约。

### NodeCapabilityProfile

某个节点针对一个确切目录版本声明的能力档案。

- `node_id`、`catalog_version`。
- `protocol_versions`：该节点支持的协议版本，必须全部存在于对应目录。
- `capacity_tier`：节点容量等级，必须存在于对应目录。
- `business_tags`：节点业务标签，必须全部存在于对应目录。
- `created_at`、`updated_at`。

同一节点对同一目录版本的声明不可变：重复登记完全相同的内容返回原档案，内容不同返回 `duplicate_capability`。节点可对不同目录版本各持有一份档案；目录升级不覆盖旧档案。

### NodeLease

计划对某个解析节点的有期限占用。

- `id`、`plan_id`、`node_id`、`allocation_id`。
- `state`：`claimed`、`reported`、`released` 或 `expired`。
- `capability`：能力分配时写入的不可变能力解释（`catalog_version`、匹配时节点声明的 `protocol_versions`、`capacity_tier`、`business_tags`）。仅区域筛选的旧分配不含该字段；目录升级永不改写它。
- `claimed_at`、`expires_at`、`finished_at`。
- `probe_id`：成功登记探针后的结果标识。

### LeaseGroup

一次租约分配请求的幂等记录。

- `id`、`plan_id`、`request_key`、`request_fingerprint`、`region`、`lease_ids`。
- `requirement`：本次匹配使用的显式能力条件（含 `catalog_version`、`protocols`、`min_capacity_tier`、`business_tags`）；缺省表示仅按区域的旧分配。
- `selections`：每个租约选择了哪个节点及其能力解释，用于说明“为什么是这个节点、按哪个能力版本”。
- 相同请求键和相同内容重放时返回原分配组，内容不一致时返回冲突。

### Probe

解析节点对指定租约提交的一次上线观察。

- `id`、`lease_id`、`plan_id`、`node_id`。
- `request_key`、`success`、`observed_serial`、`latency_ms`。
- `observed_at`、`stored_at`、`payload_fingerprint`。

### RolloutAssessment

一次计划范围内的确定性一致性判定。

- `id`、`plan_id`、`revision_id`、`request_key`。
- `verdict`：`consistent` 或 `review_required`。
- `reasons`：稳定原因代码列表。
- `probe_count`、`success_count`、`success_ratio`、`maximum_latency_ms`。
- `created_at`。

## 4. 状态和规则

### 滚动计划状态

```text
draft --activate--> active --complete--> completed
```

- 只有 `draft` 可以激活。
- 只有 `active` 可以完成。
- 激活后不能继续添加区域版本。
- 激活和完成需要匹配当前版本。

### 节点与租约生命周期

```text
ready --lease--> leased --report--> ready
                   |
                   +--release/expire--> ready
```

- 隔离节点不能被分配。
- 一个节点同一时刻最多有一个 `claimed` 租约。
- 租约到期后不能提交探针。
- 首次有效探针完成后，租约转为 `reported`，节点恢复 `ready`。

### 能力目录与能力匹配

- 能力目录按版本追加发布，版本号单调递增；词汇项规范化为小写，协议与标签去重排序，容量等级保持给定顺序作为等级排序。
- 目录内容完全相同（忽略请求键）的重复发布返回 `duplicate_catalog`；相同请求键相同内容重放返回原版本；相同请求键不同内容返回 `idempotency_conflict`。
- 节点按确切目录版本登记能力，声明值必须全部出现在该版本词汇表内。
- 同一节点对同一目录版本的相同声明可安全重放；不同声明返回 `duplicate_capability`，不覆盖旧声明。
- 分配请求带 `capability` 时，先按区域筛出 `ready` 节点，再要求节点持有该目录版本的档案，并满足全部显式条件：
  - `protocols`：节点必须支持列出的全部协议版本。
  - `min_capacity_tier`：节点容量等级在目录顺序中不低于要求等级。
  - `business_tags`：节点必须包含列出的全部业务标签。
- 区域内没有任何 `ready` 节点时返回 `node_unavailable`；有 `ready` 节点但无一满足能力条件时返回 `capability_insufficient`，两者可区分。
- 能力条件中的版本与词汇必须存在，引用不存在的目录版本返回 `not_found`，引用目录中不存在的词汇返回 `validation_failed`。
- 分配确定后，把匹配所用的目录版本和节点声明快照写入每条租约和租约组 `selections`；之后发布新目录版本或为节点追加新档案都不追溯改写已有租约。
- 分配请求不带 `capability` 时维持原区域规则：未声明任何能力的旧节点照常参与，租约不写能力解释。
- 目录发布、档案登记和租约分配都在同一互斥锁事务内串行化，因此并发重复登记、目录升级与并发分配都有确定结果：相同登记收敛为同一份档案，并发分配对同一节点至多成功一次，其余按当时状态得到确定性的 `capability_insufficient`。

### 区域版本规则

- 计划必须已有一个版本才能激活。
- 同一计划中的序列号必须严格递增。
- 序列号使用无符号 32 位整数。
- 条目数量范围为 1 到 500。
- 区域名称、所有者名称和规范名称必须规范化为小写并移除末尾句点。
- 重复加入完全相同的版本内容返回 `duplicate_revision`。

### 探针幂等

- `observed_at` 不能晚于服务时间五分钟以上。
- `latency_ms` 范围为 0 到 600000。
- 同租约同请求键同内容重放返回原探针，不重复计数。
- 同请求键不同载荷返回 `idempotency_conflict`。
- 探针标识符由服务生成，客户端不能覆盖。

### 一致性判定

- 至少需要一个探针。
- 成功比例按成功探针数除以全部探针数计算，结果保留 6 位小数。
- 每个参与节点都必须提交至少一个探针。
- 所有成功探针的 `observed_serial` 必须等于计划最新版本的序列号。
- 成功比例必须不低于 `minimum_success_ratio`。
- 最大延迟必须不超过 `maximum_latency_ms`。

不满足条件时结论为 `review_required`，并返回以下稳定原因代码：

- `no_probes`
- `node_not_observed`
- `serial_mismatch`
- `success_ratio_below_minimum`
- `latency_limit_exceeded`

## 5. 工作流

### W1 计划生命周期和区域版本

入口：

- `PUT /v1/plans`
- `PATCH /v1/plans/{id}/revisions`
- `PATCH /v1/plans/{id}/activate`
- `PATCH /v1/plans/{id}/complete`

步骤：

1. HTTP 层严格解码 JSON。
2. 计划服务校验资金、区域名称、版本内容和状态迁移。
3. 存储层在锁内创建计划或版本，并原子替换状态快照。
4. 返回带版本的新聚合。

成功行为：计划按 `draft -> active -> completed` 迁移，区域版本在激活前可追加。

失败行为：重复序列号、非法名称、缺少版本、版本冲突或非法迁移不会修改状态。

### W2 节点领取租约

入口：`PATCH /v1/plans/{id}/leases`。

步骤：

1. 入口校验计划、区域、请求键、租约秒数和数量；携带能力条件时校验条件形状。
2. 分配服务清理已到期租约。
3. 按区域筛选 `ready` 节点；存在能力条件时，载入所引用的确切目录版本并校验条件词汇，再筛掉未声明该版本档案或不满足协议、容量等级、业务标签条件的节点。
4. 在一个事务中把节点改为 `leased`，写入租约组和租约，并把所用能力版本与节点能力快照写入租约解释。

成功行为：每个节点获得一个有期限的 `claimed` 租约；能力分配的租约和租约组 `selections` 都携带目录版本和节点声明快照。

失败行为：计划非活动、无可用节点（`node_unavailable`）、有节点但能力不足（`capability_insufficient`）、请求键同键异载荷或保存失败时不会产生部分租约。

### W2b 能力目录与节点能力登记

入口：

- `PUT /v1/capability-catalogs`
- `GET /v1/capability-catalogs/latest`
- `GET /v1/capability-catalogs/{version}`
- `PUT /v1/nodes/{id}/capabilities`
- `GET /v1/nodes/{id}/capabilities`

步骤：

1. HTTP 层严格解码并规范化小写词汇、去重排序与容量等级顺序。
2. 能力服务在锁内校验请求键幂等、目录内容重复和版本号递增。
3. 节点档案在锁内校验节点存在、目录版本存在和词汇归属，再按“节点 + 目录版本”幂等写入。

成功行为：目录按不可变新版本追加，节点按版本持有不可变能力档案。

失败行为：重复目录、重复登记冲突、同键异载荷、未知节点、未知目录版本或越界词汇都不会修改状态。

### W3 探针登记

入口：`PATCH /v1/leases/{id}/probes`。

步骤：

1. 入口校验租约、请求键、序列号、延迟和时间。
2. 探针服务计算载荷指纹。
3. 存储层写入探针、完成租约并释放节点。
4. 返回探针及关联租约状态。

成功行为：首条探针落库；同键同内容安全重放。

失败行为：租约过期、计划未激活、同键异载荷或持久化失败时保持原状态。

### W4 一致性判定

入口：

- `PATCH /v1/plans/{id}/assessments`
- `GET /v1/plans/{id}/assessments/latest`

步骤：

1. 入口校验计划、请求键和最新区域版本。
2. 判定服务收集计划探针并按节点分组。
3. 纯领域计算器生成比例、原因和最终结论。
4. 存储层写入不可变判定并更新计划摘要。

成功行为：返回确定性的 `consistent` 或 `review_required`。

失败行为：缺少区域版本、没有探针、请求键冲突或保存失败不会更新计划摘要。

## 6. 模块边界

| 模块 | 路径 | 责任 |
| --- | --- | --- |
| entry | `cmd/zrc` | 进程启动、参数解析、信号关闭 |
| httpapi | `internal/httpapi` | 路由、严格解码、中间件和错误映射 |
| plan | `internal/plan` | 计划生命周期和区域版本规则 |
| fleet | `internal/fleet` | 解析节点档案、唯一名称和分页查询 |
| capability | `internal/capability` | 版本化能力目录发布和节点能力档案登记 |
| allocation | `internal/allocation` | 区域与能力两级筛选、幂等分配组、租约生命周期和能力解释 |
| probe | `internal/probe` | 探针校验、幂等写入和租约完成 |
| assessment | `internal/assessment` | 探针聚合和一致性判定 |
| domain | `internal/domain` | 聚合、值校验、状态迁移和纯计算 |
| store | `internal/store` | 锁内副本事务和原子 JSON 持久化 |
| platform | `internal/platform` | 时钟、标识、游标和文件锁 |
| runtime-probe | `internal/workflowprobe`, `cmd/workflowprobe` | 真实 HTTP 路径验证 |

依赖方向：入口和 HTTP 层依赖应用服务；应用服务依赖领域规则和存储；领域包不依赖 HTTP、文件系统或网络。

## 7. 对外接口

成功响应：

```json
{"data": {}, "meta": {"request_id": "..."}}
```

失败响应：

```json
{"error": {"code": "validation_failed", "message": "human readable message", "details": {}}}
```

主要路由：

- `GET /healthz`
- `PUT /v1/nodes`
- `GET /v1/nodes`
- `PUT /v1/nodes/{id}/capabilities`
- `GET /v1/nodes/{id}/capabilities`
- `PUT /v1/capability-catalogs`
- `GET /v1/capability-catalogs/latest`
- `GET /v1/capability-catalogs/{version}`
- `PUT /v1/plans`
- `GET /v1/plans`
- `GET /v1/plans/{id}`
- `PATCH /v1/plans/{id}/revisions`
- `PATCH /v1/plans/{id}/activate`
- `PATCH /v1/plans/{id}/complete`
- `PATCH /v1/plans/{id}/leases`
- `PATCH /v1/leases/{id}/release`
- `PATCH /v1/leases/{id}/probes`
- `PATCH /v1/plans/{id}/assessments`
- `GET /v1/plans/{id}/assessments/latest`

## 8. 工程机制

- 状态迁移：计划、节点和租约使用显式迁移表。
- 乐观并发：计划版本不匹配时拒绝变更。
- 幂等写入：租约分配和探针登记都保存请求键和载荷指纹。
- 资源生命周期：租约带截止时间，支持释放、完成和过期清理。
- 原子持久化：事务先复制状态，成功后再通过临时文件替换快照。
- 错误传播：领域错误稳定映射到 HTTP 状态和错误代码。
- 边界校验：DNS 名称、IP、序列号、延迟、比例、分页和能力均显式验证。
- 并发入口：HTTP 处理器可并发运行，状态修改通过互斥锁串行化。

这些机制为后续工程任务提供了多个独立行为面，但本阶段不创建缺陷、不写测试，也不生成缺陷票据。

## 9. 验证计划

`.project-runtime.json` 的 `testing` 固定为 `deferred`，不生成单元测试、夹具或浏览器测试。交付前使用 `cmd/workflowprobe` 启动真实本机服务并在随机端口完成：

1. 计划创建、版本追加、激活、完成和非法重复迁移。
2. 节点分配、租约唯一性、幂等重放和无可用节点冲突。
3. 能力目录发布与重放、节点能力重复登记、区域后能力匹配、能力不足与无节点区分、目录升级不追溯、旧节点区域分配和并发分配确定性。
4. 探针首写、同键重放、同键异载荷冲突和释放冲突。
5. 一致性通过、审核触发、最新结果查询和幂等重放。

每条探针使用独立临时目录，不访问外部网络，结束后关闭服务。
