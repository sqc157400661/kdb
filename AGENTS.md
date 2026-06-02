# Project Overview

## DeployArch 统一拓扑方案（2026-05）

本仓库在 `deployArch` 上采用“**一次拓扑决策，步骤统一消费**”的工程策略，核心规则如下：

- 支持架构：`Master-Slave` / `Master-Replica` / `MGR`
- 强约束：
  - `Master-Slave`：`replicas == 2`
  - `Master-Replica`：`replicas >= 2`
  - `MGR`：`replicas >= 3`
- 约束在两层生效：
  - API 声明层（CRD 标注：Enum + XValidation）
  - Reconcile 入口 fail-fast 校验（controller 中调用 topology 校验）

### 设计落点

- 统一拓扑模块：`internal/topology/topology.go`
  - `ValidateInstanceSpec`
  - `ValidateClusterSpec`
  - `ResolveInstancePlan`
  - `LeaderForInstance`
  - `ResolveClusterPlan`
  - `ClusterLeaders`
- 控制器入口校验：
  - `pkg/controller/instance_controller.go`
  - `pkg/controller/cluster_controller.go`
- 步骤层消费拓扑：
  - `pkg/reconcile/steps/instance_step.go`（`EnsureLeader` 仅 standalone 兜底）
  - `internal/generate/instance_statefulset.go`（`instanceSetRole`）
  - `pkg/reconcile/steps/cluster_step.go`（`ScaleUp` 直接调用 `ResolveClusterPlan` 校验）
  - `internal/generate/instance.go`（`InitKDBInstance` 直接消费 `ClusterPlan`，移除 leader 二次选择）

### 配置渲染约定（当前实现）

> 当前迁移决策：复制配置仅使用 `replications[]`，不再维护单 `replication` 兼容路径。

- sidecar 配置模板包含：
  - `replications[]`
  - `deploy_arch`
  - `mgr.enabled`
  - `mgr.seeds`
- `Master-Slave` 下推荐下发所有 pod 候选到 `replications[]`，由 sidecar 本地跳过 self 选上游
- 相关文件：
  - `internal/config/tmpl/instance.tmpl`
  - `pkg/reconcile/steps/mysql/instance.go`（注入 `Replications` / `DeployArch` / `MGRSeeds`）
  - `kdb-sidecar/pkg/mysql/config/config.go`（`MySQLConfig.Replications`、`MySQLConfig.MGR`）

### Before / After（DeployArch 方案收敛）

| 维度 | Before | After |
|---|---|---|
| 约束位置 | 步骤逻辑隐式约束 | API（Enum + XValidation）+ Reconcile fail-fast |
| Leader 决策 | 多处决策，存在重复 | topology 集中决策，步骤统一消费 |
| Cluster ScaleUp | `picMasterInstances` 中间层 | 直接 `ResolveClusterPlan` |
| InitKDBInstance | `masters` 二次选择 | `ClusterPlan + self` 直接推导 |
| Master-Replica | 语义曾不稳定 | 明确单主多从 |
| MGR | 以透传为主 | 模板/配置字段已打通，运行时编排持续演进 |

### 语义目标

- `Master-Slave`：固定双节点，双向复制（operator 下发全量 `replications[]`，sidecar 跳过当前 Pod 后选择对端）
- `Master-Replica`：单主多从，单向复制（`replications[]` 仅包含 primary）
- `MGR`：组复制模式（`replications[]` 可表达有主/无主，运行时编排持续演进）


`kdb-sidecar` 是面向 **Kubernetes 场景的数据库实例运行时管控组件**。

- 以 **Sidecar** 方式与 MySQL 主容器同 Pod 部署，拥有同等权限，用于完成实例运行时的初始化、健康巡检、拓扑维护（如主从复制）、备份上传与运行时 API 暴露。
- 提供 **CLI（mysqlctl）**，用于在 MySQL 主容器内快速、安全地进行人工运维（通常通过调用 Sidecar API 完成，便于审计与权限控制）。

> 重要前提：集群侧存在独立的 **K8s Operator（CRD Controller）** 负责实例生命周期（创建/扩缩容/删除 Pod/PVC/Service 等）与配置下发。本仓库只关注 **实例运行时管理**。

# Responsibilities（职责边界）

## Operator（控制面）
- 负责创建/扩缩容/删除 Pod、PVC、Service/Endpoint 等资源
- 负责决定并下发架构/角色/策略（例如 `DEPLOY_ARCH`、`ROLE`、复制与备份配置）
- 负责流量路由与单活判定（主备切换编排、Lease 等）

## Sidecar
- 读取 Operator 下发的 env/配置文件并执行 **本机动作**（如只读切换、配置复制、执行备份上传）
- 暴露 HTTP API/健康探针/元数据与诊断信息
- 不默认写 K8s 资源（需要写权限时必须显式开关并在设计中说明）

# Tech Stack

- 语言：Go `1.22`（见 `go.mod` 的 `go 1.22` 与 `toolchain go1.22.5`）
- Web 框架：Gin（HTTP API）
- CLI：Cobra（`mysqlctl` / `sidecar` 命令）
- ORM/DB：xorm + SQLite（本地元数据：实例列表、备份计划/历史）
- MySQL 交互：`github.com/sqc157400661/helper/mysql.Executor`（SQL 执行与复制动作）
- K8s：client-go（主要用于只读查询/兜底发现）
- 日志：klog
- 配置：YAML（`gopkg.in/yaml.v3`）+ INI（`gopkg.in/ini.v1`，用于 `my.cnf`）
- 定时：`robfig/cron/v3`（备份定时）
- 对象存储：
  - OSS：`aliyun-oss-go-sdk`
  - S3 兼容：`minio-go`（兼容 AWS S3 / MinIO / TOS 等）

# Project Structure


# Configuration

## 关键环境变量（Operator 下发）

### MYSQL_SERVER_ID（当前约定）

用于 Sidecar 在运行时确定实例最终 `server_id`。

- 注入位置：`internal/generate/env.go`
- 计算方式：`serverID := naming.InstanceStsNum(instance, statefulSetName)`
  - `statefulSetName` 由 `internal/generate/pod.go` 传入（`sts.Name`）
  - `InstanceStsNum` 定义在 `internal/naming/instance.go`，按 `statefulSetName = instanceName + 编号` 解析编号
- 当前意图：一个 StatefulSet 管理一个 Pod，因此以 StatefulSet 编号 +1 作为 `MYSQL_SERVER_ID` 基值

### 最终生效的 MySQL server_id（与共享模板关系）

`my.cnf` 模板在 ConfigMap 中是共享内容（`server-id` 默认值可相同），**不在 Operator 侧按实例渲染**。

最终 `server_id` 由 Sidecar 在 Pod 内根据环境变量执行写入与运行时设置：

- Sidecar读取：`kdb-sidecar/pkg/mysql/config/env.go`（`MYSQL_SERVER_ID`、`KDB_HOSTNAME`）
- 生效位置：`kdb-sidecar/cmd/sidecar/mysql/mysql.go`
  - 写回 `my.cnf`：`server-id` / `server_id`
  - 运行时执行：`SET GLOBAL server_id = ...`

> 说明：当前实现沿用“环境变量注入 + Sidecar本地生效”链路，避免因共享 ConfigMap 导致每实例 `server_id` 无法区分。

## Sidecar 配置文件（YAML）


# Build & Test


# Code Style

- 格式化：统一 `gofmt`（必要时配合 `goimports`）
- 错误处理：
  - 边界处（API/Service 层）返回可诊断的错误信息
  - 需要保留上下文时使用 `fmt.Errorf("...: %w", err)` 或 `pkg/errors` 包装（保持风格一致）
- 日志：使用 `klog`，并在关键动作（切换/备份/复制修复）输出结构化字段（clusterID/podName 等）
- 并发与生命周期：
  - goroutine/ticker 必须有 stop 路径
  - 对外动作优先支持 `context.Context`（超时/取消）
- 注释：对外接口与关键流程使用 **中英文注释**（便于跨团队协作）

# Naming Conventions

- Go 文件/包名：小写，下划线仅在必要时使用（遵循 Go 社区惯例）
- 导出类型/函数：驼峰命名（`BackupRecord`, `NewBackupService`）
- 常量：驼峰或全大写均可，但同包内保持一致（优先 Go 惯例，如 `DefaultDaysForOSSStorage`）
