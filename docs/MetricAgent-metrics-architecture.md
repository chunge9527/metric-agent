# MetricAgent Prometheus 指标注册与暴露完整分析

> 文档版本：2026-09-24
> 覆盖范围：指标定义、注册时序、暴露链路、第三方库指标、运行时防御机制、生命周期

---

## 一、整体架构：两段式注册

MetricAgent 采用 **"init() 实例化 + EnableMetrics() 显式注册"** 的两段式架构，目的是让 `--exec` 指令执行模式可以完全跳过 Prometheus 初始化。

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Go import 链                                   │
│                                                                             │
│  main ──▶ bootstrap ──▶ metrics ──────────────────────────────────┐        │
│         │                                                          │        │
│         └──▶ nacos_client ──▶ nacos-sdk-go/common/monitor ──┐     │        │
│                                                             │     │        │
│                                                             ▼     ▼        │
│                                           ┌──────────────────────────┐     │
│                                           │  prometheus client_golang │     │
│                                           │  DefaultRegisterer        │     │
│                                           │  DefaultGatherer          │     │
│                                           └──────────────────────────┘     │
└─────────────────────────────────────────────────────────────────────────────┘
         ▲                              ▲                              ▲
         │                              │                              │
    nacos-sdk-go                    metrics.go                   promhttp.Handler()
    monitor.init()                  init()                        /metrics 暴露
    (自动注册 SDK                   (仅实例化，
     2 个指标)                      不注册)


                    init() 阶段（程序启动时）
                    ┌────────────────────────────────┐
                    │ nacos-sdk-go/monitor           │
                    │   init() → MustRegister(2 个)  │
                    │                                │
                    │ metrics                        │
                    │   init() → New*Vec(8 个)       │
                    │   （不注册，不 panic）           │
                    └────────────────────────────────┘

                    HTTP 启动阶段（InitServer）
                    ┌────────────────────────────────┐
                    │ metrics.EnableMetrics()        │
                    │   → MustRegister(8 个)         │
                    │                                │
                    │ metrics.BuildInfoLabelsSet()    │
                    │   → Set(1)                     │
                    │                                │
                    │ mux.Handle("/metrics",          │
                    │     promhttp.Handler())        │
                    └────────────────────────────────┘
```

### 架构设计决策

| 决策点 | 选择 | 原因 |
|---|---|---|
| init() 里实例化还是注册 | **只实例化，不注册** | `--exec` 模式下 metrics 包也会被 import（main → bootstrap → metrics），init() 必然执行。如果 init() 里 MustRegister，exec 模式也会把 8 个指标塞进 default registry，违反 PRD 约束 5 |
| EnableMetrics 幂等性 | `metricsEnabled` 布尔标记 | HTTP 模式只调用一次；多次调用 MustRegister 会 panic |
| CounterVec 未注册就 Inc() 会 panic 吗？ | **不会** | `prometheus.CounterVec.Inc()` 内部是 `atomic.AddFloat64`，不检查 registry 注册状态；注册后 scrape 会读到累计值 |
| 什么时候必须注册？ | **在 scrape 之前** | `/metrics` 路由暴露时，registry 里有什么就 scrape 什么 |

---

## 二、注册时序分析

### 2.1 Go init() 链执行顺序

Go 的 init() 按包依赖拓扑序执行，无环时按 import 出现顺序：

| 顺序 | 包 | init() 行为 | 注册到 default registry？ |
|---|---|---|---|
| 1 | `prometheus/client_golang/prometheus` | New 默认 registry + MustRegister go_* / process_* runtime | ✅ 是（Go runtime 指标） |
| 2 | `nacos-sdk-go/v2/common/monitor` | MustRegister `nacos_monitor` + `nacos_client_request` | ✅ 是（SDK 自带指标） |
| 3 | `metric-agent/internal/metrics` | `New*Vec()` 实例化 8 个变量 | ❌ **否**（故意延迟） |

### 2.2 HTTP 模式启动时序（InitServer）

```
main.runHTTPMode()
  ├── bootstrap.LoadBaseConfig()          ← 无 metrics 调用
  ├── logger.InitFileLogger()             ← 无 metrics 调用
  ├── nacos_client.NewNacosClient()
  │     └── connect()                     ← ⚠️ Inc() 在 EnableMetrics 之前！
  │         defer NacosConnectTotal.Inc()    CounterVec.Inc() = atomic.AddFloat64，不检查 registry
  │                                          合法，但此时 registry 里还没这个指标
  │
  │    （如果初始 connect 失败，后台协程 reconnectLoop 延迟 60s+ 才触发）
  │
  ├── configSvc.StartPullLoop()
  │     └── pullLoop 首次 tick = pullInterval（默认 60s）← 远晚于 EnableMetrics
  │
  ├── guardianSvc.Start()
  │     └── safeInspect() 立即执行一次 ← ⚠️ Counter.Inc() 也在 EnableMetrics 之前！
  │
  └── ★ metrics.EnableMetrics()           ← 终于注册 8 个指标
      └── metrics.BuildInfoLabelsSet()
          └── mux.Handle("/metrics", ...) ← 路由生效
```

**关键洞察**：`EnableMetrics()` 在 L201，晚于 nacos_client.NewNacosClient()（L107）和 guardianSvc.Start() 首跑（L179）。但因为 CounterVec.Inc() 是纯 atomic 操作不依赖 registry，**时序合法**——注册后 scrape 能读到累计值。

### 2.3 --exec 指令模式时序

```
main.runExecMode()
  ├── crypto.NewAESCrypto()               ← 无 metrics 调用
  ├── http.NewRequestWithContext()        ← 无 metrics 调用
  └── http.DefaultClient.Do()             ← 无 metrics 调用
      （metrics 包 init() 已在程序启动时执行，但只做了 New*Vec，没有 MustRegister）
```

**exec 模式下 default registry 的内容**：
- ✅ go_*、process_*（prometheus client_golang 默认）
- ✅ nacos_monitor、nacos_client_request（nacos-sdk-go monitor init()）
- ❌ **没有** metricagent_* 8 个指标（EnableMetrics() 未被调用）
- ❌ **没有** /metrics 路由暴露（mux.Handle 只在 InitServer 里注册，exec 模式不走 InitServer）

---

## 三、暴露链路分析

### 3.1 /metrics 路由注册

```go
// server_init.go L201-L205
metrics.EnableMetrics()                              // 步骤 1: 注册 8 个指标
metrics.BuildInfoLabelsSet(cfg.Agent.ID, ...)        // 步骤 2: 初始化 build_info
mux.Handle(myconstant.RouteMetrics, promhttp.Handler())  // 步骤 3: 暴露 /metrics
```

`promhttp.Handler()` 内部等价于：

```go
promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
```

即每次 scrape 时动态扫描 `prometheus.DefaultGatherer`（它聚合了 `DefaultRegisterer` 里所有已注册的指标）。

### 3.2 当前 default registry 完整指标清单

| 来源 | 指标名 | 类型 | 注册时机 |
|---|---|---|---|
| client_golang 默认 | `go_*`（约 20+ 个） | Gauge/Counter/Histogram | prometheus 包 init() |
| client_golang 默认 | `process_*`（约 15+ 个） | Gauge/Counter | prometheus 包 init() |
| nacos-sdk-go | `nacos_monitor` | GaugeVec | sdk monitor init() |
| nacos-sdk-go | `nacos_client_request` | HistogramVec | sdk monitor init() |
| **MetricAgent 自定义** | `metricagent_build_info` | GaugeVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_nacos_connect_total` | CounterVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_config_list_pull_total` | CounterVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_config_item_distribute_total` | CounterVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_config_item_distribute_duration_seconds` | HistogramVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_config_clean_trigger_total` | CounterVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_guardian_self_heal_total` | CounterVec | EnableMetrics() |
| **MetricAgent 自定义** | `metricagent_guardian_self_heal_duration_seconds` | HistogramVec | EnableMetrics() |

### 3.3 /metrics 鉴权状态

`/metrics` 已加入 AuthMiddleware 白名单（与 `/health`、`/api/v1/exec`、`/api/v1/guardian` 并列），Prometheus scrape 无需携带 `Authentication` 请求头。

---

## 四、指标分类汇总（8 个自定义）

### 4.1 组件元数据（1 个）

| 指标名 | 类型 | 标签 | 说明 |
|---|---|---|---|
| `metricagent_build_info` | GaugeVec | `agent_id, agent_group, agent_version` | 唯一携带 agent 三元组标签；值恒为 1；HTTP 模式启动时 Set(1) |

### 4.2 配置分发模块（5 个）

| 指标名 | 类型 | 标签 | 触发位置 |
|---|---|---|---|
| `metricagent_nacos_connect_total` | CounterVec | `result` | nacos_client.connect() defer |
| `metricagent_config_list_pull_total` | CounterVec | `config_type, result` | config_service.tryParseList() |
| `metricagent_config_item_distribute_total` | CounterVec | `result` | DistributeAllConfigs + handleListenerCallback |
| `metricagent_config_item_distribute_duration_seconds` | HistogramVec | `result` | 同上，Observe(0) 或 time.Since() |
| `metricagent_config_clean_trigger_total` | CounterVec | `result` | config_service.cleanStorePath() |

### 4.3 进程守护模块（2 个）

| 指标名 | 类型 | 标签 | 触发位置 |
|---|---|---|---|
| `metricagent_guardian_self_heal_total` | CounterVec | `component_name, heal_result, health_result` | performSelfHealing() defer |
| `metricagent_guardian_self_heal_duration_seconds` | HistogramVec | `component_name, result` | performSelfHealing() defer |

### 4.4 Histogram 统一 bucket

```go
[]float64{0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60}  // 单位：秒
```

PRD 硬性约束，所有 Histogram 指标共享。

---

## 五、第三方库指标分析

### 5.1 nacos-sdk-go 内置指标

[vendor/nacos-sdk-go/v2/common/monitor/monitor.go](file:///e:/monitor_project/metric-agent/vendor/github.com/nacos-group/nacos-sdk-go/v2/common/monitor/monitor.go) 在 init() 里自动注册：

| 指标名 | 类型 | 标签 | 用途 |
|---|---|---|---|
| `nacos_monitor` | GaugeVec | `module, name` | SDK 内部状态（serviceInfoMapSize、listenConfigCount 等） |
| `nacos_client_request` | HistogramVec | `module, method, url, code` | SDK Nacos HTTP 调用耗时（Config/Fetch/List 等） |

### 5.2 为什么会出现在 /metrics

```
nacos-sdk-go/common/monitor  init()
  └── prometheus.MustRegister(gaugeMonitorVec, histogramMonitorVec)
      └── prometheus.DefaultRegisterer.MustRegister(...)
          └── 全局 default registry 持有这两个 collector
              └── promhttp.Handler() → DefaultGatherer.Gather()
                  └── /metrics 输出包含 SDK 指标
```

这是 Prometheus Go client 的通用做法——第三方库往 default registry 注册指标。Go runtime 的 `go_*` / `process_*` 也是同理。

### 5.3 三种处理方案对比

| 方案 | 操作 | 优点 | 缺点 | 决策 |
|---|---|---|---|---|
| **A. 保留** | 不处理 | ✅ 零成本；✅ 可观测 SDK 内部请求耗时 | ❌ /metrics 多了非前缀指标 | ✅ **采纳** |
| B. Unregister | EnableMetrics 后 `DefaultRegisterer.Unregister(sdkVec)` | ✅ /metrics 干净 | ❌ SDK GetHistogram() 后续调用可能 panic | — |
| C. 自定义 Registry | 8 个指标注册到独立 NewRegistry()，/metrics 用 HandlerFor(独立 registry) | ✅ 完全隔离 | ❌ 丢了 go_* runtime 指标；❌ 改动大 | — |

---

## 六、运行时防御机制

### 6.1 防御链全景

| 调用入口 | metrics defer 内部防御 | 外层 recover | 覆盖 panic 场景 |
|---|---|---|---|
| `nacos_client.connect()` | ✅ defer 内 `recover()` | ✅ safeDoReconnect 外层 recover | ParseNacosAddress、probeNacosServer、SDK 创建 |
| `config_service.tryParseList()` | 直接 Inc()，无 defer | ✅ safeDoPullOnce 外层 recover | YAML 解析、Nacos SDK GetConfig |
| `config_service.DistributeAllConfigs()` | 直接 Inc()/Observe() | ✅ safeDoPullOnce 外层 recover | writeConfig、runReloadScript |
| `config_service.handleListenerCallback()` | 直接 Inc()/Observe() | ✅ 内层 recover defer（先注册先执行） | 监听回调闭包 |
| `config_service.cleanStorePath()` | 直接 Inc() | ✅ safeDoPullOnce 外层 recover | 文件删除、目录遍历 |
| `guardian.performSelfHealing()` | ✅ defer 内 `recover()` | ✅ safeInspect 外层 recover | Shell Exec、context 超时 |

### 6.2 关键防御代码模式

#### Nacos connect() — 命名返回值 + recover defer

```go
func (n *NacosClient) connect(cfg model.NacosConfig) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("connect panic: %v", r)  // panic 时手动赋值
        }
        result := "success"
        if err != nil {
            result = "fail"  // error / panic 都走 fail
        }
        metrics.NacosConnectTotal.WithLabelValues(result).Inc()
    }()
    // ... 正常逻辑，err 被显式赋值
}
```

#### Guardian performSelfHealing() — 零值默认 + recover defer

```go
var healResult, healthResult, overallResult = "fail", "fail", "fail"  // 零值默认
defer func() {
    if r := recover(); r != nil {
        healResult, healthResult, overallResult = "fail", "fail", "fail"
    }
    // ... metrics 上报
}()
```

#### handleListenerCallback — 双 defer LIFO 顺序

```go
var listenerResult string
defer func() {
    // 先注册，后执行（LIFO）
    metrics.ConfigItemDistributeTotal.With(...).Inc()
}()
defer func() {
    // 后注册，先执行 —— recover 先把 listenerResult 设为 "fail"
    if r := recover(); r != nil {
        listenerResult = "fail"
    }
}()
```

### 6.3 Prometheus 客户端安全边界

| 操作 | 是否 panic | 触发条件 |
|---|---|---|
| `prometheus.NewCounterVec(...)` | ❌ 否 | 纯实例化 |
| `prometheus.MustRegister(c)` | ✅ 是 | 重复注册相同名称的 collector |
| `CounterVec.With(Labels{"k":"v"}).Inc()` | ❌ 否 | 纯 atomic.AddFloat64 |
| `CounterVec.With(Labels{})` | ✅ 是（runtime panic） | 标签数量不匹配定义 |
| `CounterVec.WithLabelValues(...)` | ✅ 是（runtime panic） | 值数量不匹配定义 |
| `GaugeVec.With(Labels{}).Set(1)` | ❌ 否 | 纯 atomic.StoreFloat64 |

**运行时标签匹配风险**：所有 With() 调用如果标签数量不匹配，会在 scrape 或 Inc() 调用时 panic。当前实现中所有标签数量与 metrics.go 定义一致。

---

## 七、生命周期总结

### 7.1 HTTP 服务模式（完整链路）

```
程序启动
  ├── nacos-sdk-go monitor.init()   → 注册 2 个指标到 default registry
  ├── metrics.init()                → 实例化 8 个指标变量（New*Vec）
  │
  ├── InitServer()
  │     ├── NewNacosClient → connect() → NacosConnectTotal.Inc()  ← 在注册前，但合法
  │     ├── guardianSvc.Start() → safeInspect() → Total.Inc()      ← 在注册前，但合法
  │     ├── metrics.EnableMetrics() → MustRegister(8 个)
  │     ├── metrics.BuildInfoLabelsSet() → Set(1)
  │     └── mux.Handle("/metrics", promhttp.Handler())
  │
  ├── ← Prometheus scrape 开始读到 8 个自定义指标 + SDK 指标 + go_* 指标
  │
  └── 进程退出
```

### 7.2 --exec 指令执行模式（完全跳过）

```
程序启动
  ├── nacos-sdk-go monitor.init()   → 注册 2 个指标到 default registry
  ├── metrics.init()                → 实例化 8 个指标变量（New*Vec，但不注册）
  │
  ├── runExecMode()                 → 纯 HTTP 客户端逻辑，不调用 metrics 包任何函数
  │
  └── 进程退出
  （default registry 里只有 SDK 指标 + go_* 指标；没有 metricagent_*；没有 /metrics 路由）
```

---

## 八、已知设计决策与 Trade-off

| # | 决策 | 选择 | 理由 | 潜在影响 |
|---|---|---|---|---|
| 1 | init() 注册 vs EnableMetrics() 注册 | **EnableMetrics()** | exec 模式跳过指标初始化 | MustRegister 与 Inc() 时序差需保证 Inc() 合法（CounterVec.Inc() 不检查 registry） |
| 2 | /metrics 鉴权 | **加入白名单** | Prometheus scrape 通常不携带业务认证头 | 如果 YAML auth.key 为空，白名单路径等价于完全开放 |
| 3 | SDK 指标处理 | **保留** | 可观测 SDK 内部请求，零成本 | /metrics 输出包含非 metricagent_ 前缀指标 |
| 4 | Guardian 零值防御 | **显式赋 "fail"** + recover defer | 防御性编程，panic 时标签值合法而非空字符串 | 代码多了 1 行初始化 |
| 5 | connect() 恢复策略 | **defer 内 recover 转 error** | Go defer 在 panic 时依然执行，recover 转 error 后 defer 内的 err!=nil 判断自然走 fail 分支 | 新增 fmt.Errorf 包装 |
| 6 | Histogram skipped 分支 | **Observe(0)** | 让 skipped 分支有采样数据，与 Counter 对齐 | skipped 分支 Histogram 值恒为 0，bucket 计数集中在 bucket=0 |
