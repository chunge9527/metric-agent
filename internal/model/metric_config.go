package model

// MetricConfig 二级配置清单项，对应 Nacos 配置清单（metricFileConfig_{agent.id} 或 metricFileConfig）中的单条配置
type MetricConfig struct {
	// ConfigCode 配置项编号，必填，仅允许字母、数字、下划线、横线（PRD 3.2.2）
	// 用于 MetricConfig 原始条目层按 configCode 去重和跨清单合并优先级判定
	ConfigCode string `yaml:"configCode" json:"config_code"`
	// DataId 目标配置文件名称，也是 Nacos 拉取的 dataId，非必填；为空时按 group 查询该分组下所有配置
	DataId string `yaml:"dataId" json:"data_id"`
	// Group 配置文件分组，必填
	Group string `yaml:"group" json:"group"`
	// Suffix 文件后缀，非必填，默认 .yml（PRD 3.2.2）
	Suffix string `yaml:"suffix" json:"suffix"`
	// StorePath 本地存储目录路径，必填
	StorePath string `yaml:"storePath" json:"store_path"`
	// EnableClean 是否执行"配置对齐/清理"，非必填，默认false
	EnableClean bool `yaml:"enableClean" json:"enable_clean"`
	// ReFileName 文件重命名，未配置则不重命名
	ReFileName string `yaml:"reFileName" json:"re_file_name"`
	// FileMode 本地落地配置文件权限，八进制字符串，例："0644"、"0755"；非必填，默认 "0755"
	// 使用 string 而非 int 避免 yaml.v3 不识别 0 前缀为八进制导致解析为十进制
	FileMode string `yaml:"fileMode" json:"file_mode"`
	// ReloadScript 重载shell脚本，未配置则不执行
	ReloadScript string `yaml:"reloadScript" json:"reload_script"`

	// Source 配置来源标记（"personal" / "public"），非 YAML 字段
	// 在 LoadConfigList 的 tryParseList 之后、dedup/merge 之前设置
	Source string `yaml:"-" json:"-"`
}

// MetricConfigList 二级配置清单类型
type MetricConfigList []MetricConfig

// ConfigItem Nacos 配置项（按分组分页查询结果），用于 DataId 为空时按 group 拉取全部配置
type ConfigItem struct {
	// DataId 配置项唯一标识
	DataId string
	// Group 配置项所属分组
	Group string
	// Content 配置内容（分页查询接口可能返回，具体以SDK实现为准）
	Content string
}

// ConfigPage Nacos 配置分页查询结果
type ConfigPage struct {
	// Items 本页配置项列表
	Items []ConfigItem
	// TotalCount 配置总条数（用于分页终止判定）
	TotalCount int
}
