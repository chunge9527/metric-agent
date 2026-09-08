// Package iface 基础设施抽象接口层
package iface

import "metric-agent/internal/model"

// noopConfigCenter 空实现的ConfigCenter（Nacos禁用时使用）
// 所有方法返回空/成功，避免typed-nil接口调用panic
type noopConfigCenter struct{}

// GetConfig 返回空字符串（配置中心未启用）
func (n *noopConfigCenter) GetConfig(dataId, group string) (string, error) {
	return "", nil
}

// SearchConfig 返回空结果（配置中心未启用）
func (n *noopConfigCenter) SearchConfig(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
	return &model.ConfigPage{}, nil
}

// AddListener 空实现（配置中心未启用）
func (n *noopConfigCenter) AddListener(dataId, group string, onChange func(namespace, group, dataId, data string)) error {
	return nil
}

// CancelListener 空实现（配置中心未启用）
func (n *noopConfigCenter) CancelListener(dataId, group string) error {
	return nil
}

// Close 空关闭（配置中心未启用）
func (n *noopConfigCenter) Close() error {
	return nil
}

// NoopConfigCenter 返回一个空实现的ConfigCenter
// 用于Nacos禁用场景，保证ConfigService构造时接口不为nil（避免typed-nil陷阱）
func NoopConfigCenter() ConfigCenter {
	return &noopConfigCenter{}
}
