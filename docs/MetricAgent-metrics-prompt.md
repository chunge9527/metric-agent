# Trae AI编码提示词（MetricAgent Prometheus埋点开发）
## 角色
你是资深Go后端工程师，基于现有metric-agent项目代码（config_service.go、guardian_service.go）实现Prometheus埋点；严格遵守下面《Prometheus指标埋点规范》作为**唯一真值来源**，**禁止私自新增、修改、删除任何指标、标签、枚举值**。
> 项目现状：
> 1. 项目模块：config_service.go（配置加载模块）、guardian_service.go（进程守护模块）
> 2. 技术库：`github.com/prometheus/client_golang/prometheus`
> 3. 编译注入版本变量包路径：`metric-agent/internal/version`
> 4. HTTP启动入口：`internal/bootstrap/server_init.go`
> 5. 埋点只在HTTP服务模式启用；`--exec`指令执行模式完全不初始化prometheus指标，不注册metrics接口。

## 硬性开发约束（必须逐条遵守）
> 以下约束全部源自 PRD 第六章原文，实现时必须严格遵守：
1. **唯一真值来源**：本提示词中的《指标定义清单》为埋点唯一真值来源，禁止自行新增、删减任何指标、标签、枚举值；所有埋点必须严格按照定义表实现。
2. **标签必要性判定原则**：
   - 保留：能区分独立业务分支、对故障定位和告警有明确价值的维度；
   - 移除：实现层面无区分度的冗余维度、与已有全局标签重复的维度。
3. 指标前缀固定 `metricagent_`；仅允许三种类型 Counter(只增不减) / Gauge(瞬时状态值) / Histogram(耗时分布统计)。
4. Histogram 统一 bucket：`[0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60]`，单位秒；耗时统计用 `time.Since()` 转 float64 秒传入 Observe。
5. **埋点仅在 HTTP 服务模式启用**；指令执行模式（`--exec`）完全不初始化 Prometheus 指标、不暴露 /metrics 接口。
6. metrics 接口复用项目现有暴露路径方案，默认 `/metrics`。
7. **Counter 全分支计数**：每个 Counter 必须在所有分支路径（成功、失败、跳过、超时、中断）都执行 Inc()，不能只在成功分支计数。
8. Gauge 值统一约定：`1 = 激活/正常/是，0 = 未激活/异常/否`；`metricagent_build_info` 固定值等于 1，标签携带全部构建元数据。
9. **注释强制要求**：所有埋点代码行必须增加注释，标明对应 PRD 章节与指标语义，格式：`// PRD章节：xxx，指标语义：xxx`。
10. 指标定义统一放置独立包 `metric-agent/internal/metrics/metrics.go`；`init()` 只完成指标变量实例化（NewGaugeVec/NewCounterVec/NewHistogramVec），**不执行 MustRegister**；由导出函数 `EnableMetrics()` 统一执行 MustRegister，HTTP 服务模式在 `server_init.go` 显式调用，`--exec` 指令模式不调用。这样保证指令模式不会将任何指标注册到 default registry。
11. `metricagent_build_info` 实现要点：
    - GaugeVec，init() 中完成 NewGaugeVec 实例化；`EnableMetrics()` 中与其他指标一起 MustRegister；
    - **唯一携带 agent_id / agent_group / agent_version 三个标签的指标**，其他业务指标不需要重复携带——多实例版本识别、故障溯源统一通过 build_info 完成；
    - HTTP 服务初始化阶段（server_init.go）先调 `metrics.EnableMetrics()` 再调 `metrics.BuildInfoLabelsSet()` 填充标签执行 Set(1)；
    - 进程运行期间不需要循环重复 Set，Prometheus scrape 时自动读取内存输出；
    - 指令模式完全跳过（既不 EnableMetrics 也不 BuildInfoLabelsSet）；
    - 支持 agent_id/agent_group 配置变更时调用 Reset() 清空旧标签组合，防止废弃时序残留。
12. 错误处理：Prometheus 操作不要吞 panic，使用 MustRegister；业务逻辑错误不要因为埋点而改变原有业务逻辑，埋点只做观测，不影响主流程。
13. Go 编码规范：变量命名 go 驼峰；导入包分组；不要全局魔法数字。

---

## 指标定义清单（唯一真值来源）
> 以下表格全部源自 PRD 第六章原文，禁止私自修改。

### 6.1 组件元数据埋点
| 指标名 | 类型 | 业务标签集合 | 指标语义 | 标签枚举值 | 关联PRD章节 | 标签必要性说明 |
| --- | --- | --- | --- | --- | --- | --- |
| `metricagent_build_info` | Gauge | `agent_id,agent_group,agent_version` | Agent程序构建版本信息，HTTP服务启动时设置，指标值恒为1 | agent_id: 节点ID<br>agent_group: 节点分组<br>agent_version: 业务版本号 | 3.1.2 HTTP服务模式启动 | 用于多实例版本识别、故障溯源；指标固定值1，通过标签携带全部构建元数据；仅HTTP服务模式初始化，指令模式不生成该指标 |

### 6.2 配置分发模块埋点（关联原PRD：3.2 配置加载全章节）
> 业务覆盖：3.2.1 Nacos对接、3.2.2 配置清单拉取、3.2.3 二级配置分发、3.2.4 配置清理、文件落盘备份回滚、重载脚本执行

#### 6.2.1 配置清单拉取指标
| 指标名 | 类型 | 业务标签集合 | 指标语义 | 标签枚举值 | 关联PRD章节 | 标签必要性说明 |
| --- | --- | --- | --- | --- | --- | --- |
| `metricagent_config_list_pull_total` | Counter | `config_type,result` | 配置清单拉取总次数，覆盖网络拉取、YAML解析、空结果全分支 | config_type: [personal, public]<br>result: [success, fail_pull, fail_parse, result_empty] | 3.2.2 配置清单拉取 | 保留`config_type`：个性化/公共为两次独立拉取动作，可分别统计成功率；<br>保留`result`：区分网络异常、解析异常、空结果三类失败场景，便于故障定位 |

#### 6.2.2 二级配置分发与监听指标
| 指标名 | 类型 | 业务标签集合 | 指标语义 | 标签枚举值 | 关联PRD章节 | 标签必要性说明 |
| --- | --- | --- | --- | --- | --- | --- |
| `metricagent_config_item_distribute_total` | Counter | `result` | 二级配置分发处理总次数 | result: [success, fail, skipped] | 3.2.3 二级配置分发 | 保留`result`：区分成功、失败、已存在跳过三类分支，符合全分支计数要求 |
| `metricagent_config_item_distribute_duration_seconds` | Histogram | `result` | 单条二级配置完整分发流程总耗时（包含拉取配置、写文件、重载脚本、注册监听）分布 | result: [success, fail, skipped] | 3.2.3 二级配置分发 | 保留`result`：区分不同处理结果的耗时；统一使用全局标准bucket，用于监控分发链路性能、定位慢配置问题 |

#### 6.2.3 配置清理指标
| 指标名 | 类型 | 业务标签集合 | 指标语义 | 标签枚举值 | 关联PRD章节 | 标签必要性说明 |
| --- | --- | --- | --- | --- | --- | --- |
| `metricagent_config_clean_trigger_total` | Counter | `result` | 配置清理触发次数 | result: [success, skipped_high_risk] | 3.2.4 配置清理执行约束 | 保留`result`：覆盖正常执行、命中高危目录两类分支 |

### 6.3 进程守护模块埋点（关联原PRD：3.5 进程守护全章节）
> 业务覆盖：3.5.1 守护配置读取、3.5.2 健康检查与自愈、3.5.3 守护巡检暂停与恢复

#### 6.3.1 健康检查与自愈指标
| 指标名 | 类型 | 业务标签集合 | 指标语义 | 标签枚举值 | 关联PRD章节 | 标签必要性说明 |
| --- | --- | --- | --- | --- | --- | --- |
| `metricagent_guardian_self_heal_total` | Counter | `component_name,heal_result,health_result` | 组件自愈执行次数 | component_name: [组件名]<br>heal_result: [success, fail, interrupted]<br>health_result: [success, fail, interrupted] | 3.5.2 自愈逻辑 | 保留`component_name`：按组件维度统计；<br>保留`heal_result`：启动脚本执行结果；<br>保留`health_result`：拉起后健康检查结果 |
| `metricagent_guardian_self_heal_duration_seconds` | Histogram | `component_name,result` | 组件自愈全流程（启动脚本执行+拉起后健康检查）总耗时分布 | component_name: [组件名]<br>result: [success, fail, interrupted] | 3.5.2 自愈逻辑 | 保留`component_name`：按组件维度统计耗时，便于定位性能瓶颈；<br>保留`result`：区分不同执行结果下的耗时分布，辅助故障与性能联合排查；统一使用全局标准bucket |

---

## 指标汇总（共7个）
| # | 指标名 | 类型 | 标签数 | 归属模块 |
| --- | --- | --- | --- | --- |
| 1 | `metricagent_build_info` | Gauge | 3 | 组件元数据 |
| 2 | `metricagent_config_list_pull_total` | Counter | 2 | 配置分发 |
| 3 | `metricagent_config_item_distribute_total` | Counter | 1 | 配置分发 |
| 4 | `metricagent_config_item_distribute_duration_seconds` | Histogram | 1 | 配置分发 |
| 5 | `metricagent_config_clean_trigger_total` | Counter | 1 | 配置分发 |
| 6 | `metricagent_guardian_self_heal_total` | Counter | 3 | 进程守护 |
| 7 | `metricagent_guardian_self_heal_duration_seconds` | Histogram | 2 | 进程守护 |

---

## 输出交付物要求
1. 生成完整 `internal/metrics/metrics.go`：全部7个指标定义，init()注册；`metricagent_build_info` 携带 agent_id/agent_group/agent_version 三个标签，其他指标只携带各自定义表中的业务标签；仅对 build_info 提供 `BuildInfoLabelsSet()` 设置函数。
2. 对 `config_service.go`、`guardian_service.go` 做 diff 式修改，在对应业务分支插入埋点调用；每一行埋点代码注释写明 `// PRD章节：xxx，指标语义：xxx`。
3. 修改 `internal/bootstrap/server_init.go`：HTTP 服务启动时初始化 build_info 指标；指令模式跳过 metrics 初始化。
4. 给出关键调用示例，说明埋点触发位置；**不要改动原有业务逻辑**，埋点只做观测。
5. 不要写单元测试，只实现业务埋点代码。
6. 输出先给出任务拆解清单，再输出各个文件完整代码 diff 片段。
