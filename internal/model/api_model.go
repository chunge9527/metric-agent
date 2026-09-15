package model

// ExecRequest 远程命令执行请求（加密前）
type ExecRequest struct {
	// Script 待执行的Shell脚本内容
	Script string `json:"script"`
	// Args 脚本参数数组，保留原始 shell 语义（含空格参数不丢失）
	// 执行时通过 shell -c "script" args... 方式传递，脚本内可用 $@ 或 $1 $2 引用
	Args []string `json:"args,omitempty"`
	// Timeout 自定义超时时间（秒）；指针类型用于区分"未传"（nil，用默认值）与"传0"（非法值，拒绝执行）
	Timeout *int `json:"timeout"`
}

// ExecResponse 远程命令执行响应（加密前）
type ExecResponse struct {
	// Stdout 标准输出
	Stdout string `json:"stdout"`
	// Stderr 标准错误
	Stderr string `json:"stderr"`
	// ExitCode 进程退出码
	ExitCode int `json:"exit_code"`
	// Duration 执行耗时（毫秒）
	Duration int64 `json:"duration_ms"`
}

// HealthResponse 健康检查响应
type HealthResponse struct {
	// Status 服务状态
	Status string `json:"status"`
	// AgentID Agent唯一标识
	AgentID string `json:"agent_id"`
	// AgentGroup Agent分组
	AgentGroup string `json:"agent_group"`
	// Version 应用版本号（ldflags 注入，构建时指定）
	Version string `json:"version"`
	// Timestamp 时间戳
	Timestamp int64 `json:"timestamp"`
}

// ErrorResponse 统一错误响应
type ErrorResponse struct {
	// Code 错误码
	Code int `json:"code"`
	// Message 错误信息
	Message string `json:"message"`
}

// MetricQueryResult 单条PromQL查询结果（executeQuery内部使用）
type MetricQueryResult struct {
	// QueryKey 查询键名
	QueryKey string `json:"queryKey"`
	// Values 提取后的样本值数组（float64）
	Values []float64 `json:"values"`
}

// TaskQueryOutput 单个定时任务执行后的输出结构
// 每次任务执行写一条JSON（任务级汇总，所有查询共用同一个queryTime）
type TaskQueryOutput struct {
	// QueryTime 任务执行发起时间（所有PromQL查询共用），格式：YYYYMMDDHHmmss
	QueryTime string `json:"queryTime"`
	// Result 查询结果集合：queryKey → 样本值数组
	Result map[string][]float64 `json:"result"`
}

// UploadResult 文件上传成功响应（PRD 3.8）
type UploadResult struct {
	// FileName 最终落盘的目标文件名（重命名后）
	FileName string `json:"fileName"`
	// Size 上传文件大小（字节）
	Size int64 `json:"size"`
	// TargetPath 文件完整落盘路径
	TargetPath string `json:"targetPath"`
	// Backup 原文件备份路径（如有覆盖且存在原文件），无备份时为空字符串
	Backup string `json:"backup,omitempty"`
}

// GuardianStatus GuardianService 状态快照
// 用于 /api/v1/guardian?action=status 返回当前巡检运行状态
type GuardianStatus struct {
	// Running 服务是否已启动（Start 后为 true，Stop 后为 false）
	Running bool `json:"running"`
	// Paused 是否暂停中（Pause 后为 true，Resume 后为 false）
	// 注意：Paused=true 不代表协程已退出，仅代表跳过巡检执行
	Paused bool `json:"paused"`
	// Interval 巡检周期（分钟）
	Interval int `json:"interval_minutes"`
	// LastAction 最近一次操作：start / pause / resume / stop
	LastAction string `json:"last_action,omitempty"`
	// LastActionAt 最近一次操作的 Unix 时间戳（秒）
	LastActionAt int64 `json:"last_action_at,omitempty"`
}
