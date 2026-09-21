// Package model 数据模型层
package model

// AgentConfig 基础配置结构体，对应 metricAgent.yml
type AgentConfig struct {
	// Agent 节点基础信息（嵌套结构）
	Agent AgentInfo `yaml:"agent" json:"agent"`

	// Feature 服务特性开关（默认全部true，保持向后兼容）
	Feature FeatureConfig `yaml:"feature" json:"feature"`

	// Nacos Nacos配置
	Nacos NacosConfig `yaml:"nacos" json:"nacos"`

	// Shell Shell相关配置（嵌套结构）
	Shell ShellConfig `yaml:"shell" json:"shell"`

	// Config 配置管理相关（嵌套结构）
	Config ConfigConfig `yaml:"config" json:"config"`

	// Crontab 进程守护配置
	Crontab CrontabConfig `yaml:"crontab" json:"crontab"`

	// Forward 请求透传配置
	Forward ForwardConfig `yaml:"forward" json:"forward"`

	// VictoriaMetrics VictoriaMetrics配置
	VictoriaMetrics VictoriaMetricsConfig `yaml:"victoriaMetrics" json:"victoria_metrics"`

	// Auth 鉴权配置
	Auth AuthConfig `yaml:"auth" json:"auth"`

	// Upload 文件上传配置（PRD 3.8）
	Upload UploadConfig `yaml:"upload" json:"upload"`

	// Log 日志配置（PRD 5.1~5.7）
	Log LogConfig `yaml:"log" json:"log"`
}

// AgentInfo 节点基础信息
type AgentInfo struct {
	// ID 节点唯一标识，必填，仅允许字母、数字、下划线、横线
	ID string `yaml:"id" json:"id"`
	// Group 节点分组（可选，用于多 Agent 分组管理）
	Group string `yaml:"group" json:"group"`
}

// FeatureConfig 服务特性开关
// 使用 bool 类型：Go 零值 false 即"未配置时默认关闭"，需显式 true 才开启
type FeatureConfig struct {
	// EnableNacos Nacos配置中心和配置变更监听开关（默认 false，显式 true 开启）
	EnableNacos bool `yaml:"enableNacos" json:"enable_nacos"`
	// EnableGuardian 进程守护服务开关（默认 false，显式 true 开启）
	EnableGuardian bool `yaml:"enableGuardian" json:"enable_guardian"`
	// EnableSchedule 定时任务调度服务开关（默认 false，显式 true 开启）
	EnableSchedule bool `yaml:"enableSchedule" json:"enable_schedule"`
}

// IsNacosEnabled Nacos是否启用
func (f FeatureConfig) IsNacosEnabled() bool {
	return f.EnableNacos
}

// IsGuardianEnabled 进程守护是否启用
func (f FeatureConfig) IsGuardianEnabled() bool {
	return f.EnableGuardian
}

// IsScheduleEnabled 定时任务是否启用
func (f FeatureConfig) IsScheduleEnabled() bool {
	return f.EnableSchedule
}

// NacosConfig Nacos配置
type NacosConfig struct {
	// Address Nacos服务地址（支持 http://host:port / host:port / host:8848）
	Address string `yaml:"address" json:"address"`
	// Namespace 命名空间（public 填空字符串）
	Namespace string `yaml:"namespace" json:"namespace"`
	// Group 分组
	Group string `yaml:"group" json:"group"`
	// Username Nacos鉴权用户名（空字符串表示不启用鉴权）
	Username string `yaml:"username" json:"username"`
	// Password Nacos鉴权密码（空字符串表示不启用鉴权）
	Password string `yaml:"password" json:"password"`
	// Timeout Nacos单次HTTP/gRPC请求超时（毫秒）；<=0 时使用默认 5000ms
	Timeout int `yaml:"timeout" json:"timeout"`
	// LogDir nacos sdk日志输出目录（相对路径基于可执行文件目录）
	// 未配置时默认 ./logs/nacos
	LogDir string `yaml:"logDir" json:"log_dir"`
	// LogLevel nacos sdk日志级别：debug/info/warn/error；空或非法默认 warn
	LogLevel string `yaml:"logLevel" json:"log_level"`
	// CacheDir nacos sdk本地缓存目录（相对路径基于可执行文件目录）
	// 未配置时使用 nacos sdk 默认值 ./cache
	CacheDir string `yaml:"cacheDir" json:"cache_dir"`
	// LogRollingConfig nacos sdk日志滚动配置
	// 未配置时使用 nacos sdk 内置默认值（MaxSize=100MB, MaxAge=30天, MaxBackups=5）
	LogRollingConfig *NacosLogRollingConfig `yaml:"logRollingConfig" json:"log_rolling_config"`
}

// NacosLogRollingConfig Nacos SDK日志滚动配置
// 字段对齐 nacos-sdk-go ClientLogRollingConfig（Lumberjack 风格）
type NacosLogRollingConfig struct {
	// MaxSize 单个日志文件最大（MB），超过切割；<=0 时 SDK 默认 100
	MaxSize int `yaml:"maxSize" json:"max_size"`
	// MaxAge 归档日志保留天数；<=0 时 SDK 默认 30
	MaxAge int `yaml:"maxAge" json:"max_age"`
	// MaxBackups 最多保留归档日志文件数；<=0 时 SDK 默认 5
	MaxBackups int `yaml:"maxBackups" json:"max_backups"`
	// LocalTime 备份文件名使用本地时区时间，默认 true
	LocalTime bool `yaml:"localTime" json:"local_time"`
	// Compress 是否 gzip 压缩旧日志，默认 false
	Compress bool `yaml:"compress" json:"compress"`
}

// ShellConfig Shell相关配置（嵌套容器）
type ShellConfig struct {
	// Encrypt Shell命令加密配置
	Encrypt ShellEncryptConfig `yaml:"encrypt" json:"encrypt"`
}

// ShellEncryptConfig Shell加密配置
type ShellEncryptConfig struct {
	// Key AES加密密钥
	Key string `yaml:"key" json:"key"`
}

// ConfigConfig 配置管理相关（嵌套容器）
type ConfigConfig struct {
	// PullInterval 配置主动拉取间隔（分钟）；小于 0 时使用默认 1 分钟（PRD 3.2）
	PullInterval int `yaml:"pullInterval" json:"pullInterval"`
	// ReloadScript 配置重载脚本超时
	ReloadScript ConfigReloadScriptConfig `yaml:"reloadScript" json:"reload_script"`
	// CleanOrphanFile 孤儿配置文件清理（PRD 3.2.4）
	CleanOrphanFile CleanOrphanFileConfig `yaml:"cleanOrphanFile" json:"clean_orphan_file"`
}

// ConfigReloadScriptConfig 配置重载脚本超时
type ConfigReloadScriptConfig struct {
	// Timeout 超时时间（秒）
	Timeout int `yaml:"timeout" json:"timeout"`
}

// CleanOrphanFileConfig 孤儿配置文件清理配置（PRD 3.2.4）
type CleanOrphanFileConfig struct {
	// Enable 是否开启配置对齐（默认false关闭，防止误删）
	Enable bool `yaml:"enable" json:"enable"`
	// CleanFixHour 定点执行清理时间（小时），支持多个时间点，仅 1~23 有效，需排重
	CleanFixHour []int `yaml:"cleanFixHour" json:"clean_fix_hour"`
	// CleanSuffix 需要清理的文件后缀，排除空字符串
	CleanSuffix []string `yaml:"cleanSuffix" json:"clean_suffix"`
}

// CrontabConfig 进程守护配置
type CrontabConfig struct {
	// Interval 守护巡检周期（分钟）；未配置或 <=0 时默认 1 分钟（PRD 3.4.1）
	Interval int `yaml:"interval" json:"interval"`
	// HealthCheck 健康检查配置
	HealthCheck HealthCheckConfig `yaml:"healthCheck" json:"health_check"`
	// StartScript 启动脚本配置
	StartScript StartScriptConfig `yaml:"startScript" json:"start_script"`
}

// HealthCheckConfig 健康检查配置
type HealthCheckConfig struct {
	// Timeout 超时时间（秒）
	Timeout int `yaml:"timeout" json:"timeout"`
}

// StartScriptConfig 启动脚本配置
type StartScriptConfig struct {
	// Timeout 超时时间（秒）
	Timeout int `yaml:"timeout" json:"timeout"`
}

// ForwardConfig 请求透传配置
type ForwardConfig struct {
	// Timeout 默认超时时间（秒）
	Timeout int `yaml:"timeout" json:"timeout"`
}

// HeaderKV HTTP请求头键值对
type HeaderKV struct {
	// Key 请求头名称
	Key string `yaml:"key" json:"key"`
	// Value 请求头值，支持环境变量引用（如 ${VAR_NAME}）
	Value string `yaml:"value" json:"value"`
}

// VictoriaMetricsConfig VictoriaMetrics配置
type VictoriaMetricsConfig struct {
	// URL VictoriaMetrics请求地址
	URL string `yaml:"url" json:"url"`
	// Headers 请求头列表，支持多个自定义头（如Authorization），value支持环境变量引用
	Headers []HeaderKV `yaml:"headers" json:"headers"`
	// AuthHeader 鉴权请求头（已废弃，请使用headers字段），保留兼容旧配置
	AuthHeader string `yaml:"authHeader" json:"auth_header"`
}

// AuthConfig 鉴权配置
type AuthConfig struct {
	// Key 鉴权密钥
	Key string `yaml:"key" json:"key"`
}

// UploadConfig 文件上传配置（PRD 3.8）
type UploadConfig struct {
	// MaxFileSize 单文件最大上传大小（字节）；<=0 时使用默认 100MB
	MaxFileSize int64 `yaml:"maxFileSize" json:"max_file_size"`
	// SensitivePaths 敏感目录黑名单（绝对路径，大小写不敏感匹配）；
	// 命中黑名单即拒绝上传，防止覆盖系统关键文件；
	// 未配置时使用 constants.DefaultSensitivePaths 默认列表
	SensitivePaths []string `yaml:"sensitivePaths" json:"sensitive_paths"`
}

// LogConfig 日志配置（PRD 5.1~5.7）
type LogConfig struct {
	// LogFile 日志文件路径（相对路径基于可执行文件目录）
	// 优先级：--logs 命令行参数 > 此处配置 > 默认值 ./logs/metricAgent.log
	LogFile string `yaml:"logFile" json:"log_file"`
	// Level 日志级别：debug / info / warn / error，默认 info
	Level string `yaml:"level" json:"level"`
	// MaxFileSize 单个日志文件最大大小（MB），默认 100；<=0 时使用默认值
	MaxFileSize int `yaml:"maxFileSize" json:"max_file_size"`
	// MaxRetainDays 日志最大保留天数，默认 30；<=0 时使用默认值
	MaxRetainDays int `yaml:"maxRetainDays" json:"max_retain_days"`
}
