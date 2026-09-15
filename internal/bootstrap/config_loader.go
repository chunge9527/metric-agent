// Package bootstrap 启动引导层
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/infra/nacos_client"
	"metric-agent/internal/model"

	"gopkg.in/yaml.v3"
)

// execDir 包级缓存可执行文件所在目录，init() 一次性获取
// 避免 resolvePath() 每次调用时重复 os.Executable() 系统调用
var execDir string

func init() {
	if execPath, err := os.Executable(); err == nil {
		execDir = filepath.Dir(execPath)
	} else {
		execDir, _ = os.Getwd()
	}
}

// agentIDRegex agent_id校验正则：仅允许字母、数字、下划线、横线
var agentIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// LoadBaseConfig 加载基础配置
// configPath为配置文件路径，相对路径基于可执行文件所在目录
func LoadBaseConfig(configPath string) (*model.AgentConfig, error) {
	// 路径处理
	absPath := resolvePath(configPath)

	// 读取文件
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("配置文件不存在: %s", absPath)
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// YAML解析
	var cfg model.AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("配置文件格式错误: %w", err)
	}

	// 填充默认值（nacos.group 默认值在这里填上）
	fillDefaults(&cfg)

	// 回填：agent.group 统一使用 nacos.group（yaml 中不再需要单独配置 agent.group）
	// fillDefaults 已确保 nacos.group 不为空
	cfg.Agent.Group = cfg.Nacos.Group

	// 必填校验
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// resolvePath 路径解析：相对路径基于可执行文件所在目录（init() 中一次性缓存，零运行期开销）
func resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(execDir, path)
}

// BootstrapPath 对外暴露的路径解析函数
// 相对路径基于可执行文件所在目录，绝对路径直接返回
func BootstrapPath(path string) string {
	return resolvePath(path)
}

// GetExecDir 返回可执行文件所在目录（init() 一次性缓存，零开销）
// 供其他包需要相对路径解析基准时使用，避免重复 os.Executable 系统调用
func GetExecDir() string {
	return execDir
}

// validateConfig 配置校验
func validateConfig(cfg *model.AgentConfig) error {
	// agent_id必填校验
	if strings.TrimSpace(cfg.Agent.ID) == "" {
		return fmt.Errorf("agent.id为必填字段，不能为空")
	}

	// agent.id格式校验
	if !agentIDRegex.MatchString(cfg.Agent.ID) {
		return fmt.Errorf("agent.id格式错误: %s，仅允许字母、数字、下划线、横线", cfg.Agent.ID)
	}

	// agent_groupgroup必填校验
	if strings.TrimSpace(cfg.Agent.Group) == "" {
		return fmt.Errorf("agent.group为必填字段，不能为空")
	}

	// Nacos配置校验（仅当 feature.enableNacos 未关闭时执行）
	// 用户显式禁用 Nacos 时（enableNacos=false），不应强制校验地址格式
	if cfg.Feature.IsNacosEnabled() {
		if cfg.Nacos.Address == "" {
			return fmt.Errorf("nacos.address为必填字段（feature.enableNacos启用时）")
		}
		// Nacos地址格式校验（格式非法重连永远无法成功，启动时即报错）
		if _, _, err := nacos_client.ParseNacosAddress(cfg.Nacos.Address); err != nil {
			return err
		}
	}

	// VictoriaMetrics配置校验（仅当 feature.enableSchedule 未关闭时执行）
	// VM URL为空时定时任务的查询请求全部失败，只能靠翻错误日志发现问题，启动时即报错
	if cfg.Feature.IsScheduleEnabled() {
		if strings.TrimSpace(cfg.VictoriaMetrics.URL) == "" {
			return fmt.Errorf("victoriametrics.url为必填字段（feature.enableSchedule启用时）")
		}
	}

	// 鉴权密钥必填校验（防止auth.key为空时空字符串匹配导致鉴权绕过）
	if strings.TrimSpace(cfg.Auth.Key) == "" {
		return fmt.Errorf("auth.key为必填字段，不能为空")
	}

	return nil
}

// fillDefaults 填充默认值
// Feature开关默认值由IsXxxEnabled方法统一处理（nil→true，保持向后兼容）
func fillDefaults(cfg *model.AgentConfig) {
	// nacos.group 默认值（Nacos SDK 内部默认就是 DEFAULT_GROUP）
	if cfg.Nacos.Group == "" {
		cfg.Nacos.Group = myconstant.DefaultNacosGroup
	}

	// nacos.LogLevel 默认值：空或非法都兜底为 info
	if !isValidLogLevel(cfg.Nacos.LogLevel) {
		cfg.Nacos.LogLevel = "info"
	} else {
		cfg.Nacos.LogLevel = strings.ToLower(strings.TrimSpace(cfg.Nacos.LogLevel))
	}

	// nacos.LogRollingConfig 默认值：用户显式配置后，各 int 字段 <=0 回填 SDK 默认值
	if cfg.Nacos.LogRollingConfig != nil {
		if cfg.Nacos.LogRollingConfig.MaxSize <= 0 {
			cfg.Nacos.LogRollingConfig.MaxSize = 100
		}
		if cfg.Nacos.LogRollingConfig.MaxAge <= 0 {
			cfg.Nacos.LogRollingConfig.MaxAge = 30
		}
		if cfg.Nacos.LogRollingConfig.MaxBackups <= 0 {
			cfg.Nacos.LogRollingConfig.MaxBackups = 5
		}
	}

	if cfg.Config.PullInterval < 0 {
		cfg.Config.PullInterval = myconstant.DefaultPullIntervalMinutes
	}

	if cfg.Config.ReloadScript.Timeout <= 0 {
		cfg.Config.ReloadScript.Timeout = myconstant.DefaultReloadScriptTimeout
	}

	if cfg.Crontab.HealthCheck.Timeout <= 0 {
		cfg.Crontab.HealthCheck.Timeout = myconstant.DefaultHealthCheckTimeout
	}

	if cfg.Crontab.StartScript.Timeout <= 0 {
		cfg.Crontab.StartScript.Timeout = myconstant.DefaultStartScriptTimeout
	}

	if cfg.Forward.Timeout <= 0 {
		cfg.Forward.Timeout = myconstant.DefaultForwardTimeout
	}

	// Upload 默认值（PRD 3.8）
	if cfg.Upload.MaxFileSize <= 0 {
		cfg.Upload.MaxFileSize = myconstant.DefaultMaxUploadSize
	}
	// 敏感目录黑名单：未配置时使用内置默认列表
	if len(cfg.Upload.SensitivePaths) == 0 {
		cfg.Upload.SensitivePaths = append([]string{}, myconstant.DefaultSensitivePaths...)
	}

	// Log 默认值（PRD 5.1~5.7）
	// 日志级别：空字符串或非法值 → 默认 info
	if !isValidLogLevel(cfg.Log.Level) {
		cfg.Log.Level = myconstant.DefaultLogLevel
	}
	// 单文件最大尺寸：<=0 → 默认 100MB
	if cfg.Log.MaxFileSize <= 0 {
		cfg.Log.MaxFileSize = myconstant.LogMaxSizeMB
	}
	// 保留天数：<=0 → 默认 30 天
	if cfg.Log.MaxRetainDays <= 0 {
		cfg.Log.MaxRetainDays = myconstant.LogMaxRetainDays
	}
}

// isValidLogLevel 校验日志级别是否合法
// 仅接受 debug / info / warn / error（大小写不敏感，内部统一转小写）
func isValidLogLevel(level string) bool {
	l := strings.ToLower(strings.TrimSpace(level))
	for _, valid := range myconstant.LogValidLevels {
		if l == valid {
			return true
		}
	}
	return false
}
