package model

// ScheduleConfig 定时任务配置项，对应 scheduledConfig.yml 中的单条任务
type ScheduleConfig struct {
	// Name 任务名称，必填，唯一
	Name string `yaml:"name" json:"name"`
	// Cron 标准cron表达式，必填
	Cron string `yaml:"cron" json:"cron"`
	// QueryConfigs 查询配置
	QueryConfigs QueryConfigsConfig `yaml:"queryConfigs" json:"query_configs"`
	// Output 输出配置
	Output OutputConfig `yaml:"output" json:"output"`
	// Timeout 任务执行超时（秒），默认30秒
	Timeout int `yaml:"timeout" json:"timeout"`
}

// QueryConfigsConfig 查询配置
type QueryConfigsConfig struct {
	// Queries PromQL查询列表
	Queries []QueryConfig `yaml:"queries" json:"queries"`
}

// QueryConfig 单个PromQL查询配置
type QueryConfig struct {
	// QueryKey 查询键名，用于结果标识
	QueryKey string `yaml:"queryKey" json:"query_key"`
	// PromQL PromQL查询语句
	PromQL string `yaml:"promql" json:"promql"`
}

// OutputConfig 输出配置
type OutputConfig struct {
	// Path 输出文件路径，必填
	Path string `yaml:"path" json:"path"`
	// MaxFile 单文件最大上限（MB），非必填
	MaxFile int `yaml:"maxFile" json:"max_file"`
	// WriteMode 写入模式：append（默认追加）或 overwrite（覆盖）
	WriteMode string `yaml:"writeMode" json:"write_mode"`
}

// ScheduleConfigList 定时任务配置列表类型
type ScheduleConfigList []ScheduleConfig
