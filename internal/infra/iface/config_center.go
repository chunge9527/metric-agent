package iface

import "metric-agent/internal/model"

// ConfigCenter 配置中心接口
// PRD 3.2：配置清单（个性化/公共）走定时拉取；二级配置走 Nacos 监听（ListenConfig）
type ConfigCenter interface {
	// GetConfig 拉取指定 dataId + group 的配置内容
	// dataId: 配置项的唯一标识
	// group: 配置项所属分组
	// 返回: 配置内容字符串，或错误
	GetConfig(dataId, group string) (string, error)

	// SearchConfig 按分组分页查询配置列表（DataId 为空时用于拉取该分组下全部配置）
	// group: 目标分组
	// pageNo/pageSize: 分页参数（从1开始）
	// 返回: 本页结果（含总条数），或错误
	SearchConfig(group string, pageNo, pageSize int) (*model.ConfigPage, error)

	// AddListener 注册 Nacos 配置变更监听（PRD 3.2.3 二级配置监听）
	// dataId + group + namespace 唯一标识一个监听目标
	// onChange: 配置变更回调，由 SDK 内部监听协程触发（注意不要在回调中做长时间阻塞操作）
	AddListener(dataId, group string, onChange func(namespace, group, dataId, data string)) error

	// CancelListener 取消指定配置项的 Nacos 监听
	// dataId + group + namespace 需与 AddListener 时一致
	CancelListener(dataId, group string) error

	// Close 关闭连接释放资源
	// 返回: 关闭错误
	Close() error
}
