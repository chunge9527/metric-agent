// Package metrics Prometheus 指标埋点定义
// 指标前缀固定 metricagent_，仅 Counter / Gauge / Histogram 三种类型
// 所有指标在 init() 中 MustRegister，业务代码直接调用包装函数
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ============ metricagent_build_info 专属标签（PRD 6.1） ============
// 注：agent_id / agent_group / agent_version 三个标签仅在 metricagent_build_info 中携带，
// 其他指标不需要重复携带——多实例版本识别、故障溯源统一通过 build_info 完成。

// LabelAgentID 节点唯一标识
const LabelAgentID = "agent_id"

// LabelAgentGroup 节点分组
const LabelAgentGroup = "agent_group"

// LabelAgentVersion Agent 版本
const LabelAgentVersion = "agent_version"

// buildInfoLabels metricagent_build_info 指标专用标签
var buildInfoLabels = []string{LabelAgentID, LabelAgentGroup, LabelAgentVersion}

// ============ Histogram 固定 bucket（PRD 硬性约束） ============

// HistogramBuckets 统一 bucket：[0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60] 单位秒
var HistogramBuckets = []float64{0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60}

// ============ 指标变量声明 ============

// 6.1 组件元数据
var (
	// BuildInfo Agent程序构建版本信息，HTTP服务启动时Set(1)，指令模式不初始化
	BuildInfo *prometheus.GaugeVec
)

// 6.2 配置分发模块
var (
	// NacosConnectTotal Nacos连接总次数（覆盖初始连接 + 后台重连全分支）
	// 标签：result[success/fail]
	NacosConnectTotal *prometheus.CounterVec

	// ConfigListPullTotal 配置清单拉取总次数（覆盖网络拉取、YAML解析、空结果全分支）
	// 标签：config_type[personal/public], result[success/fail_pull/fail_parse/config_empty]
	ConfigListPullTotal *prometheus.CounterVec

	// ConfigItemDistributeDuration 单条二级配置完整分发流程总耗时分布
	// 标签：config_code（配置项编号，PRD 3.2.2 必填字段）, result
	// result 取值：
	//   success         —— 全链路成功（GetConfig + writeConfig + runReloadScript + AddListener）
	//   fail_pull_config —— GetConfig 网络/业务失败或返回空内容（硬失败，配置未落盘）
	//   fail_write      —— writeConfig 原子写文件失败 / 监听回调删文件失败（硬失败）
	//   fail_listen   —— AddListener 注册 Nacos 监听失败（硬失败）
	//   fail_script     —— reloadScript 执行失败（软失败：配置已落盘+监听已注册，仅脚本执行通知失败）
	// 注：skipped（配置已不在注册表）场景不埋点
	// 注：计数已由 _count 子序列替代，不再单独声明 CounterVec
	ConfigItemDistributeDuration *prometheus.HistogramVec

	// ConfigCleanTriggerTotal 配置清理触发次数
	// 标签：result[success/skipped]
	ConfigCleanTriggerTotal *prometheus.CounterVec
)

// 6.3 进程守护模块
var (
	// GuardianSelfHealDuration 组件自愈全流程总耗时分布
	// 标签：component_name, result[success/fail/interrupted]
	// 注：计数已由 _count 子序列替代，不再单独声明 CounterVec
	GuardianSelfHealDuration *prometheus.HistogramVec
)

// ============ init() 指标实例化（不注册） ============
// PRD 约束：--exec 指令模式禁止初始化 Prometheus 指标
// init() 只完成指标变量的 New*Vec 实例化，MustRegister 延迟到 EnableMetrics()
// 注：Go import 时 init() 必然执行，但未 MustRegister 就不会出现在 default registry

func init() {
	// 6.1 组件元数据（唯一携带 agent_id/agent_group/agent_version 标签）
	BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metricagent_build_info",
		Help: "Agent程序构建版本信息，HTTP服务启动时设置，指标值恒为1",
	}, buildInfoLabels)

	// 6.2 配置分发模块
	// 6.2.1 Nacos连接（仅业务标签：result）
	NacosConnectTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metricagent_nacos_connect_total",
		Help: "Nacos连接总次数，覆盖初始连接 + 后台重连全分支",
	}, []string{"result"})

	// 6.2.2 配置清单拉取（仅业务标签：config_type, result）
	ConfigListPullTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metricagent_config_list_pull_total",
		Help: "配置清单拉取总次数，覆盖网络拉取、YAML解析、空结果全分支",
	}, []string{"config_type", "result"})

	// 6.2.2 二级配置分发与监听（仅业务标签：result）
	// 注：ConfigItemDistributeTotal 已移除，计数由 Histogram 的 _count 子序列替代
	ConfigItemDistributeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metricagent_config_item_distribute_duration_seconds",
		Help:    "单条二级配置完整分发流程总耗时（包含拉取配置、写文件、重载脚本、注册监听）分布；result 标签取值：success 全链路成功；fail_pull_config 拉取配置失败；fail_write 写文件失败；fail_listen 注册监听失败；fail_script 重载脚本软失败（配置已落盘+监听已注册，仅脚本执行通知失败）",
		Buckets: HistogramBuckets,
	}, []string{"config_code", "result"})

	// 6.2.3 配置清理（仅业务标签：result）
	ConfigCleanTriggerTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metricagent_config_clean_trigger_total",
		Help: "配置清理触发次数",
	}, []string{"result"})

	// 6.3.1 健康检查与自愈（仅业务标签）
	// 注：GuardianSelfHealTotal 已移除，计数由 Histogram 的 _count 子序列替代
	GuardianSelfHealDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metricagent_guardian_self_heal_duration_seconds",
		Help:    "组件自愈全流程（启动脚本执行+拉起后健康检查）总耗时分布",
		Buckets: HistogramBuckets,
	}, []string{"component_name", "result"})
}

// ============ HTTP 模式显式启用 ============

// metricsEnabled 防重复注册标记（HTTP 模式只调用一次）
var metricsEnabled bool

// EnableMetrics 将全部指标 MustRegister 到 default registry
// 仅 HTTP 服务模式调用；--exec 指令模式不调用，registry 为空
// 内部使用 sync.Once 等价的布尔标记保证幂等
func EnableMetrics() {
	if metricsEnabled {
		return
	}
	metricsEnabled = true
	// PRD 约束：统一 MustRegister（panic on duplicate registration）
	prometheus.MustRegister(
		BuildInfo,
		NacosConnectTotal,
		ConfigListPullTotal,
		ConfigItemDistributeDuration,
		ConfigCleanTriggerTotal,
		GuardianSelfHealDuration,
	)
}

// ============ metricagent_build_info 标签设置 ============

// BuildInfoLabelsSet 设置 metricagent_build_info 指标值为1
// HTTP服务启动时调用；指令模式不初始化
// agent_id/agent_group 变更时调用 Reset() 清空旧标签组合
func BuildInfoLabelsSet(agentID, agentGroup, agentVersion string) {
	labels := prometheus.Labels{
		LabelAgentID:      agentID,
		LabelAgentGroup:   agentGroup,
		LabelAgentVersion: agentVersion,
	}
	BuildInfo.With(labels).Set(1)
}
