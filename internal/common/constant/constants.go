// Package constant 全局常量与默认值定义
package constant

// ============ 路径与地址配置 ============

// DefaultBindAddr 默认HTTP监听地址
const DefaultBindAddr = "0.0.0.0:9092"

// DefaultConfigPath 默认配置文件路径（相对二进制目录）
const DefaultConfigPath = "./metricAgent.yml"

// DefaultLogPath 默认日志文件路径（相对二进制目录）
const DefaultLogPath = "./logs/metricAgent.log"

// ============ 超时配置（秒） ============

// DefaultExecTimeout 远程命令执行默认超时（秒）
const DefaultExecTimeout = 60

// MaxExecTimeout 远程命令执行服务端最大超时（秒）
const MaxExecTimeout = 600

// ClientMaxExecTimeout 客户端 --timeout 参数允许的最大值（秒）
// 客户端提前做边界校验，超出直接报错不发请求；
// 服务端 MaxExecTimeout=600 做最终拦截，两层保护
const ClientMaxExecTimeout = 1800

// DefaultPullIntervalMinutes 配置定时拉取默认间隔（分钟）
// PullInterval 小于 0 时使用此值
const DefaultPullIntervalMinutes = 1

// DefaultReloadScriptTimeout 配置重载脚本默认超时（秒）
const DefaultReloadScriptTimeout = 60

// DefaultHealthCheckTimeout 健康检查默认超时（秒）
const DefaultHealthCheckTimeout = 60

// DefaultStartScriptTimeout 启动脚本默认超时（秒）
const DefaultStartScriptTimeout = 120

// DefaultForwardTimeout 请求透传默认超时（秒）
const DefaultForwardTimeout = 30

// MaxForwardTimeout 请求透传最大超时（秒）
const MaxForwardTimeout = 120

// DefaultScheduleTimeout 定时任务执行默认超时（秒）
const DefaultScheduleTimeout = 30

// ============ Shell 执行相关 ============

// MaxShellOutputBytes Shell 命令 stdout/stderr 单流最大捕获字节数（1MB）
// 超出部分丢弃并在日志中标记，防止脚本输出大量数据导致 OOM
const MaxShellOutputBytes = 1 * 1024 * 1024

// ShellLogPreviewBytes Shell 命令日志中 stdout/stderr 预览最大字节数（5KB）
// 超过截断，防止日志爆炸
const ShellLogPreviewBytes = 5 * 1024

// ============ HTTP 请求体限制 ============

// MaxRequestBodyBytes HTTP请求体最大字节数（10MB）
// ExecHandler 的远程命令执行请求体需要 AES 加密后的 JSON，正常请求不会超过这个量
const MaxRequestBodyBytes = 10 * 1024 * 1024

// ============ Nacos 重连间隔 ============

// NacosStartupRetryInterval Nacos启动阶段重连间隔（秒）
const NacosStartupRetryInterval = 60

// NacosRuntimeRetryInterval Nacos运行阶段重连间隔（秒）
const NacosRuntimeRetryInterval = 30

// DefaultNacosTimeoutMs Nacos客户端默认单次HTTP/gRPC请求超时（毫秒）
// 对应 nacos-sdk-go ClientConfig.TimeoutMs 默认值
const DefaultNacosTimeoutMs = 5000

// DefaultNacosGroup Nacos 默认分组（Nacos SDK 内部 constant.DEFAULT_GROUP = "DEFAULT_GROUP"）
// 当 yaml 中 nacos.group 未配置或为空时，回填此默认值
const DefaultNacosGroup = "DEFAULT_GROUP"

// NacosSearchPageSize Nacos配置分页查询默认每页条数
const NacosSearchPageSize = 100

// ============ 日志配置（PRD 5.1~5.7） ============

// DefaultLogLevel 默认日志级别
const DefaultLogLevel = "info"

// LogMaxSizeMB 日志单文件最大尺寸（MB），默认值；<=0 时 fillDefaults 使用此值
const LogMaxSizeMB = 100

// LogMaxRetainDays 日志默认保留天数，默认 30
const LogMaxRetainDays = 30

// LogRotateFilePattern 轮转文件命名模板
// PRD 5.5：{前缀}-yyyy-MM-dd-{序号}.log
// 示例：metricAgent-2026-09-07-0.log
// 参数顺序：前缀, 日期(yyyy-MM-dd), 序号
const LogRotateFilePattern = "%s-%s-%d.log"

// LogRotateDateLayout 轮转文件名中的日期格式
const LogRotateDateLayout = "2006-01-02"

// LogValidLevels 合法日志级别列表（小写）
var LogValidLevels = []string{"debug", "info", "warn", "error"}

// ============ 文件操作 ============

// BackupFileSuffix 配置备份文件后缀
const BackupFileSuffix = "_agent_bak"

// DefaultFilePerm 默认文件权限
const DefaultFilePerm = 0644

// DefaultDirPerm 默认目录权限
const DefaultDirPerm = 0755

// ============ 配置加载（PRD 3.2） ============

// DefaultConfigFilePerm 配置分发写入的文件权限（PRD 3.2.3：755）
const DefaultConfigFilePerm = 0755

// ConfigBackupDirName 配置分发备份目录名（PRD 3.2.3：storePath/bak）
const ConfigBackupDirName = "bak"

// ConfigTaskLockTimeoutSeconds 配置定时任务锁超时时间（秒，PRD 3.2.2：10分钟）
const ConfigTaskLockTimeoutSeconds = 10 * 60

// ConfigKnownSuffixes 最终文件名判定用已知后缀（PRD 3.2.3，忽略大小写）
// dataId 命中任一后缀时直接作为最终文件名，否则补 .yml
var ConfigKnownSuffixes = []string{".yaml", ".yml", ".properties", ".json", ".xml", ".html", ".htm", ".txt"}

// DefaultCleanSuffixes cleanSuffix 未配置时的默认值（PRD 3.2.4：默认为 .yml、.yaml）
var DefaultCleanSuffixes = []string{".yml", ".yaml"}

// DefaultConfigCleanBlacklist 配置清理高危目录黑名单（PRD 3.2.4）
// storePath 命中即跳过，不做任何删除动作
var DefaultConfigCleanBlacklist = []string{"/etc", "/bin", "/sbin", "/usr/bin"}

// ============ 版本信息 ============

// AppVersion 应用版本号，通过 -ldflags 注入构建时版本
// 构建命令示例：
//
//	go build -ldflags="-X metric-agent/internal/common/constant.AppVersion=20260903-gitabc123"
//
// 未注入时使用默认值 "dev"
var AppVersion = "0.0.1"

// ============ 模式标识 ============

// ============ HTTP 路由 ============

// RouteHealth 健康检查接口路径
const RouteHealth = "/health"

// RouteExec 远程命令执行接口路径
const RouteExec = "/api/v1/exec"

// RouteForward 请求透传接口路径
const RouteForward = "/forward"

// RouteUpload 文件上传接口路径（PRD 3.8）
const RouteUpload = "/api/v1/upload"

// RouteGuardianControl 进程守护控制接口路径
// 支持 GET ?action=pause / resume / status 三种操作
const RouteGuardianControl = "/api/v1/guardian"

// ============ 文件上传配置（PRD 3.8） ============

// DefaultMaxUploadSize 默认单文件最大上传大小（字节），100MB
const DefaultMaxUploadSize = 100 * 1024 * 1024

// DefaultUploadFilePerm 上传文件默认权限（PRD 3.8：0755）
// 与 DefaultFilePerm(0644) 区分：上传的文件可能是脚本/二进制需要可执行权限
const DefaultUploadFilePerm = 0755

// DefaultSensitivePaths 默认敏感目录黑名单（PRD 3.8 路径安全校验）
// 命中即拒绝上传，防止覆盖系统关键文件
var DefaultSensitivePaths = []string{
	// Linux
	"/etc",
	"/root",
	"/proc",
	"/sys",
	"/dev",
	"/var/run",
	"/var/lib",
	"/boot",
	"/sbin",
	"/bin",
	"/usr/bin",
	"/usr/sbin",
	// Windows（大小写不敏感，PathToLower 后比较）
	"c:\\windows",
	"c:\\program files",
	"c:\\program files (x86)",
	"c:\\windows\\system32",
}

// ============ 指令执行模式（runExecMode） ============

// DefaultExecModeAESKey 指令执行模式客户端硬编码 AES 密钥
// PRD 2.2：runExecMode 不加载配置文件，密钥由此常量提供；
// 与 metricAgent.yml shell.encrypt.key 默认值保持一致
const DefaultExecModeAESKey = "7sK9p2R5zG8tB4vN"

// ============ 请求头 ============

// HeaderAuth Authentication请求头
const HeaderAuth = "Authentication"

// ============ 配置标识 ============

// ConfigListPersonalDataIDFormat 个性化配置清单dataId格式（PRD 3.2.1）
// 格式：metricFileConfig_{agent.group}_{agent.id}，参数顺序：group, id
const ConfigListPersonalDataIDFormat = "metricFileConfig_%s_%s"

// ConfigListPublicDataIDFormat 公共配置清单dataId格式（PRD 3.2.1）
// 格式：metricFileConfig_{agent.group}，参数顺序：group
const ConfigListPublicDataIDFormat = "metricFileConfig_%s"

// ============ 时间常量 ============
