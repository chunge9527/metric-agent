package model

// GuardianConfig 守护配置项，对应 crontab.yml 中的单条配置（PRD 3.4.1）
type GuardianConfig struct {
	// ComponentName 组件类型，如 vmselect，必填
	ComponentName string `yaml:"componentName" json:"component_name"`
	// IPs 组件部署的IP数组，必填；宿主机任意IP命中数组任意一项即执行守护逻辑
	IPs []string `yaml:"ips" json:"ips"`
	// HealthCheckScript 健康检查脚本，必填；退出码 0 判定存活
	HealthCheckScript string `yaml:"healthCheckScript" json:"health_check_script"`
	// StartScript 启动脚本，非必填；组件异常时执行拉起
	StartScript string `yaml:"startScript" json:"start_script"`
}

// GuardianConfigList 守护配置列表类型
type GuardianConfigList []GuardianConfig
