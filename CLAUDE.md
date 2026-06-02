# CLAUDE.md

This file provides guidance to Claude Code when working in the `kdb/` repository.

## DeployArch 统一拓扑方案（2026-05）

在 `kdb-operator` 内，`deployArch` 采用“**一次拓扑决策，步骤统一消费**”策略，避免在多个步骤散落判断。

### 支持架构

- `Master-Slave`
- `Master-Replica`
- `MGR`

### 强约束（必须遵守）

- `Master-Slave`: `replicas == 2`
- `Master-Replica`: `replicas >= 2`
- `MGR`: `replicas >= 3`

### 约束生效位置

1. API 声明层（CRD 注释）
   - `apis/kdb.com/v1/kdbinstance_types.go`
   - `apis/kdb.com/v1/kdbcluster_types.go`
2. Reconcile 入口 fail-fast
   - `pkg/controller/instance_controller.go`
   - `pkg/controller/cluster_controller.go`

## Topology 单一真相（Single Source of Truth）

统一拓扑模块：`internal/topology/topology.go`

- `ValidateInstanceSpec`
- `ValidateClusterSpec`
- `ResolveInstancePlan`
- `LeaderForInstance`
- `ResolveClusterPlan`
- `ClusterLeaders`

除 topology 模块外，其他步骤不应再引入新的 deployArch 分叉逻辑。

## 关键消费点

- `pkg/reconcile/steps/instance_step.go`
  - `EnsureLeader` 通过 topology 决策
- `internal/generate/instance_statefulset.go`
  - `instanceSetRole` 通过 topology role 映射
- `pkg/reconcile/steps/cluster_step.go`
  - `ScaleUp` 直接调用 `ResolveClusterPlan` 做拓扑校验（已移除 `picMasterInstances` 中间层）
- `internal/generate/instance.go`
  - `InitKDBInstance` 直接按 `ClusterPlan + self` 推导 leader（移除 `masters` 二次选择）

## 配置渲染约定

- 模板：`internal/config/tmpl/instance.tmpl`
  - `replications[]`
  - `deploy_arch`
  - `mgr.enabled`
  - `mgr.seeds`
- 渲染注入：`pkg/reconcile/steps/mysql/instance.go`
  - 注入 `Replications`、`DeployArch`、`MGRSeeds`
- Sidecar 配置结构：`../kdb-sidecar/pkg/mysql/config/config.go`
  - `MySQLConfig.Replications`、`MySQLConfig.MGR` 字段

## 行为目标

- `Master-Slave`：双节点、双向复制（operator 下发全量 `replications[]`，sidecar 跳过当前 Pod 选择对端）
- `Master-Replica`：单主多从、单向复制（`replications[]` 中仅 primary 一条）
- `MGR`：组复制（配置字段已打通；`replications[]` 可表达有主/无主模式，运行时编排继续演进）

## Before / After（DeployArch 方案收敛）

| 维度 | Before | After |
|---|---|---|
| 约束位置 | 主要依赖步骤内隐式逻辑 | API 声明层（Enum + XValidation）+ Reconcile fail-fast |
| Leader 决策 | cluster 与 instance 路径分散决策 | topology 统一决策（Instance/Cluster 各自集中） |
| Cluster ScaleUp | 通过 `picMasterInstances` 间接校验 | 直接调用 `ResolveClusterPlan` |
| InitKDBInstance | 依赖 `masters` 并二次选择 leader | 直接基于 `ClusterPlan + self` 推导 leader |
| Master-Replica 语义 | 历史上存在“双主候选”路径 | 统一为单主多从（replica 指向统一 primary） |
| MGR | 常量/透传为主 | 模板与配置结构已具备 `mgr.*` 字段，运行时编排待完善 |

## Working notes

- 修改 deployArch 相关逻辑时，必须同时检查：
  - API 约束是否一致
  - topology 输出是否一致
  - 模板渲染字段是否一致
  - sidecar 配置结构是否匹配
- 优先做“集中化修正”，不要在单个 step 做临时分支补丁。
