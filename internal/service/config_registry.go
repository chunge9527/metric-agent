package service

import (
	"os"
	"sync"
)

// ListenRegistryItem 本地监听注册表单条记录（PRD 3.2.3）
// ItemKey = md5(namespace + '#' + group + '#' + dataId + '#' + StorePath + '#' + ReFileName)
// ConfigCode 用于 MetricConfig 层 configCode 去重和跨清单优先级判定，不参与 ItemKey 生成
type ListenRegistryItem struct {
	// ItemKey 唯一主键
	ItemKey string
	// Namespace 命名空间
	Namespace string
	// Group 配置分组
	Group string
	// DataId 配置项标识（fileName）
	DataId string
	// ConfigCode 配置项编号（PRD 3.2.2 新增，用于 configCode 排重和跨清单优先级）
	ConfigCode string
	// StorePath 原始storePath（已自动补全路径分隔符）
	StorePath string
	// FinalName 计算后的最终文件名
	FinalName string
	// ReFileName 文件重命名配置
	ReFileName string
	// FileMode 最终文件权限（八进制）
	FileMode os.FileMode
	// ReloadScript 重载脚本
	ReloadScript string
}

// ListenRegistry 监听注册表（全局并发安全，读写加锁）
type ListenRegistry struct {
	mu    sync.RWMutex
	items map[string]*ListenRegistryItem // key: ItemKey
}

// NewListenRegistry 创建监听注册表
func NewListenRegistry() *ListenRegistry {
	return &ListenRegistry{items: make(map[string]*ListenRegistryItem)}
}

// Add 注册一条记录；ItemKey 已存在时返回 false（去重）
func (r *ListenRegistry) Add(item *ListenRegistryItem) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[item.ItemKey]; ok {
		return false
	}
	r.items[item.ItemKey] = item
	return true
}

// Get 按 ItemKey 查询记录
func (r *ListenRegistry) Get(itemKey string) (*ListenRegistryItem, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	it, ok := r.items[itemKey]
	return it, ok
}

// Exists 判断 ItemKey 是否存在
func (r *ListenRegistry) Exists(itemKey string) bool {
	_, ok := r.Get(itemKey)
	return ok
}

// Update 按 ItemKey 更新注册表条目；不存在时返回 false
func (r *ListenRegistry) Update(item *ListenRegistryItem) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[item.ItemKey]; !ok {
		return false
	}
	r.items[item.ItemKey] = item
	return true
}

// Remove 移除记录；不存在返回 false
func (r *ListenRegistry) Remove(itemKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[itemKey]; !ok {
		return false
	}
	delete(r.items, itemKey)
	return true
}

// Len 返回注册表条目数
func (r *ListenRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Items 返回注册表条目快照
func (r *ListenRegistry) Items() []*ListenRegistryItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ListenRegistryItem, 0, len(r.items))
	for _, it := range r.items {
		out = append(out, it)
	}
	return out
}

// Reset 清空注册表（Agent 退出前取消所有 Nacos 监听后调用）
func (r *ListenRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = make(map[string]*ListenRegistryItem)
}
