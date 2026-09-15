package service

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/filekit"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/iface"
	"metric-agent/internal/model"

	"gopkg.in/yaml.v3"
)

// configCodePattern configCode 正则：仅允许字母、数字、下划线、横线（PRD 3.2.2 L163）
var configCodePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ConfigServiceParams 配置管理服务构造参数
type ConfigServiceParams struct {
	// AgentID 节点唯一标识
	AgentID string
	// AgentGroup 节点分组（用于拼配置清单 dataId）
	AgentGroup string
	// Namespace Nacos 命名空间
	Namespace string
	// NacosGroup Nacos 默认分组（用于拉取配置清单）
	NacosGroup string
	// AgentExecDir Agent 可执行文件所在目录（reloadScript 工作目录基准）
	AgentExecDir string
	// ReloadScriptTimeout 重载脚本超时（秒）
	ReloadScriptTimeout int
	// CleanOrphanFile 孤儿配置文件清理配置（PRD 3.2.4）
	CleanOrphanFile model.CleanOrphanFileConfig
}

// ConfigService 配置管理服务（PRD 3.2）
// 架构：配置清单（个性化/公共）走定时拉取；二级配置走 Nacos 监听
type ConfigService struct {
	cc     iface.ConfigCenter
	se     iface.ShellExecutor
	params ConfigServiceParams

	registry *ListenRegistry

	// cleanFixHours 归一化后的定点清理小时（去重、仅 0~23）
	cleanFixHours []int
	// cleanSuffixes 归一化后的清理后缀（去空、去重、小写；空则默认为 .yml、.yaml）
	cleanSuffixes []string

	// 任务锁（PRD 3.2.2：互斥锁 + 10分钟超时自动解锁）
	taskMu        sync.Mutex
	taskLocked    bool
	taskToken     uint64
	taskLockTimer *time.Timer

	// 清理控制（PRD 3.2.4）：由 DistributeAllConfigs 直接触发，不独立起协程
	cleanMu      sync.Mutex           // 仅保护 cleanLastRun map
	cleanLastRun map[string]time.Time // key: storePath，保证每小时每目录只清理一次

	// 定时拉取循环控制
	pullStopOnce sync.Once
	pullStop     chan struct{}
	pullWg       sync.WaitGroup
	pullMu       sync.Mutex
	pullRunning  bool
	pullInterval time.Duration // 实际生效的拉取间隔（StartPullLoop 赋值，用于日志/测试）

	// 监听回调串行化：按 itemKey 粒度加锁，防止 Nacos 连续推送同一 dataId 变更时
	// 多个 on-change goroutine 并发操作同一个 finalPath（writeConfig 备份/重命名竞争）
	listenerMuMu sync.Mutex             // 保护 listenerMu map 自身的并发访问
	listenerMu   map[string]*sync.Mutex // key: itemKey，value: 该配置项的串行化锁
}

// configListResult 配置清单拉取+去重+合并+展开的完整结果
// PRD 3.2.2：个性化配置和公共配置都拉取，各自先按 configCode 去重（先配置先生效），
// 再合并（同 configCode 个性化优先覆盖公共），展开后按 itemKey 去重
type configListResult struct {
	// 个性化配置拉取状态
	personalDataID string
	personalOK     bool // 个性化是否成功拉取并解析为非空数组
	// 公共配置拉取状态
	publicDataID string
	publicOK     bool // 公共是否成功拉取并解析为非空数组

	// 合并后的 MetricConfigList（已完成 configCode 层去重 + 跨清单合并，个性化优先）
	mergedConfigs model.MetricConfigList
	// 展开后的最终结果（已 itemKey 去重）
	mergedTargets map[string]*targetConfig
	// enableClean 触发的清理目录列表（来自个性化+公共，需去重）
	cleanStorePaths []string
}

// targetConfig 展开后的具体二级配置（itemKey 去重与分发的最小单元）
type targetConfig struct {
	namespace    string
	group        string
	dataId       string
	suffix       string // 文件后缀，直接取自 MetricConfig.Suffix（不做归一化）
	storePath    string // 已补全路径分隔符
	reFileName   string
	reloadScript string
	finalName    string
	fileMode     os.FileMode // 最终文件权限，八进制
	fileModeStr  string      // fileMode 原始字符串（参与 itemKey）
	enableClean  bool        // 是否执行配置对齐（不参与 itemKey，仅用于配置清理）
	configCode   string      // PRD 3.2.2 新增，用于跨清单 configCode 去重
	source       string      // "personal" 或 "public"，标识配置来源（PRD 3.2.2）

	// itemKey_ 缓存字段：buildTarget 时一次性算出，后续 itemKey() 直接返回
	// 覆盖所有影响行为的属性，任何变更都会生成新 itemKey
	itemKey_ string
}

// itemKey PRD 3.2.3：md5(namespace + '#' + group + '#' + dataId + '#' + suffix + '#' + storePath + '#' + fileModeStr + '#' + reloadScript + '#' + reFileName)
// 注意分隔符是单 #，字段顺序不可调换；configCode、enableClean 不参与 itemKey 生成
func itemKeyOf(namespace, group, dataId, suffix, storePath, fileModeStr, reloadScript, reFileName string) string {
	h := md5.New()
	h.Write([]byte(namespace + "#" + group + "#" + dataId + "#" + suffix + "#" + storePath + "#" + fileModeStr + "#" + reloadScript + "#" + reFileName))
	return hex.EncodeToString(h.Sum(nil))
}

func (t *targetConfig) itemKey() string {
	return t.itemKey_
}

// NewConfigService 创建配置管理服务
func NewConfigService(cc iface.ConfigCenter, se iface.ShellExecutor, p ConfigServiceParams) *ConfigService {
	if p.ReloadScriptTimeout <= 0 {
		p.ReloadScriptTimeout = myconstant.DefaultReloadScriptTimeout
	}
	cs := &ConfigService{
		cc:            cc,
		se:            se,
		params:        p,
		registry:      NewListenRegistry(),
		cleanFixHours: normalizeCleanFixHours(p.CleanOrphanFile.CleanFixHour),
		cleanSuffixes: normalizeCleanSuffixes(p.CleanOrphanFile.CleanSuffix),
		cleanLastRun:  make(map[string]time.Time),
		listenerMu:    make(map[string]*sync.Mutex),
	}
	return cs
}

// LoadConfigList 加载配置清单（定时拉取模式下每次全量刷新都调用）
// PRD 3.2.2 语义（configCode + itemKey 改造后）：
//
//	阶段1：独立拉取个性化和公共配置清单，各自处理失败（两者都拉取失败才报错）
//	阶段2：各自 MetricConfig 层按 configCode 先配置先生效去重
//	阶段3：跨清单合并——同 configCode 个性化覆盖公共
//	阶段4：展开为 targetConfig + itemKey 去重
//
// 配置清单本身不注册监听，采用定时拉取（PRD L157）
func (s *ConfigService) LoadConfigList() (*configListResult, error) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, s.params.AgentGroup, s.params.AgentID)
	publicDataID := fmt.Sprintf(myconstant.ConfigListPublicDataIDFormat, s.params.AgentGroup)

	// 阶段1：独立拉取两份，各自处理失败
	personalList, personalOK := s.tryParseList(personalDataID, "personal")
	publicList, publicOK := s.tryParseList(publicDataID, "public")

	// 给每条 MetricConfig 设置 Source 标记（dedup/merge 后 buildTarget 时需要保留来源）
	for i := range personalList {
		personalList[i].Source = "personal"
	}
	for i := range publicList {
		publicList[i].Source = "public"
	}

	// 两者都不可用才报错
	if !personalOK && !publicOK {
		return nil, fmt.Errorf("个性化配置及公共配置均拉取失败 %s / %s", personalDataID, publicDataID)
	}

	result := &configListResult{
		personalDataID: personalDataID,
		publicDataID:   publicDataID,
		personalOK:     personalOK,
		publicOK:       publicOK,
	}

	// 阶段2+3：各自 MetricConfig 层 configCode 去重 + 跨清单合并
	// 注意：configCode 去重发生在 MetricConfig 原始条目层，所有条目都参与（不区分 DataId）
	var dedupPersonal, dedupPublic model.MetricConfigList
	if personalOK {
		dedupPersonal = dedupMetricConfigList(personalList, "personal")
	}
	if publicOK {
		dedupPublic = dedupMetricConfigList(publicList, "public")
	}
	result.mergedConfigs = mergeMetricConfigLists(dedupPersonal, dedupPublic)

	logger.Info("原始配置清单去重、合并、继承处理完成",
		"personal_dataId", personalDataID, "personal_ok", personalOK,
		"public_dataId", publicDataID, "public_ok", publicOK,
		"merged_configs_count", len(result.mergedConfigs),
	)

	// 阶段4：展开为 targetConfig + itemKey 去重（自然满足注册表去重）
	result.mergedTargets, result.cleanStorePaths = s.expandAndDedup(result.mergedConfigs)

	logger.Info("配置清单列表组装完成",
		"merged_targets_count", len(result.mergedTargets),
		"clean_store_paths", len(result.cleanStorePaths),
	)

	return result, nil
}

// dedupMetricConfigList MetricConfig 原始条目层按 configCode 先配置先生效去重
// 所有条目都参与，不区分 DataId；configCode 为空或含非法字符的条目跳过（已在 validateMetricConfigEntry 校验）
func dedupMetricConfigList(configs model.MetricConfigList, source string) model.MetricConfigList {
	seen := make(map[string]bool, len(configs))
	out := make(model.MetricConfigList, 0, len(configs))
	for i := range configs {
		cfg := &configs[i]
		// configCode 为空/非法已在 validateMetricConfigEntry 拦截，但 dedup 单独防御一次
		if strings.TrimSpace(cfg.ConfigCode) == "" || !configCodePattern.MatchString(cfg.ConfigCode) {
			logger.Warn("配置项 configCode 为空或含非法字符，跳过 configCode 去重",
				"source", source, "index", i, "configCode", cfg.ConfigCode)
			continue
		}
		if seen[cfg.ConfigCode] {
			logger.Warn("配置项 configCode 重复，先配置先生效，跳过",
				"source", source, "index", i, "configCode", cfg.ConfigCode)
			continue
		}
		seen[cfg.ConfigCode] = true
		out = append(out, *cfg)
	}
	return out
}

// mergeMetricConfigLists 跨清单合并：同 configCode 个性化覆盖公共
// personal 和 public 都必须是已各自 configCode 去重后的结果
func mergeMetricConfigLists(personal, public model.MetricConfigList) model.MetricConfigList {
	// 先放 personal（个性化，优先级高）
	out := make(model.MetricConfigList, 0, len(personal)+len(public))
	seen := make(map[string]bool, len(personal))
	for i := range personal {
		cfg := &personal[i]
		seen[cfg.ConfigCode] = true
		out = append(out, *cfg)
	}
	// 再补 public 中没被 personal 覆盖的
	for i := range public {
		cfg := &public[i]
		if seen[cfg.ConfigCode] {
			logger.Warn("配置项 configCode 重复，个性化配置先生效，跳过",
				"configCode", cfg.ConfigCode)
			continue // 被个性化覆盖，跳过
		}
		out = append(out, *cfg)
	}
	return out
}

// tryParseList 拉取并解析单个配置清单；成功返回 MetricConfigList + true，失败返回 nil,false 并打印对应日志
func (s *ConfigService) tryParseList(dataID, source string) (model.MetricConfigList, bool) {
	content, err := s.cc.GetConfig(dataID, s.params.NacosGroup)
	if err != nil {
		logger.Warn("配置清单拉取失败", "source", source, "dataId", dataID, "error", err)
		return nil, false
	}
	if strings.TrimSpace(content) == "" {
		logger.Warn("配置清单内容为空，视为失败", "source", source, "dataId", dataID)
		return nil, false
	}

	var configs model.MetricConfigList
	if err := yaml.Unmarshal([]byte(content), &configs); err != nil {
		logger.Error("配置清单解析失败", "source", source, "dataId", dataID, "error", err)
		return nil, false
	}
	// 解析结果必须是非 nil 数组，否则视为失败（PRD 3.2.2）
	if len(configs) == 0 {
		logger.Warn("配置清单解析结果为空数组，视为失败", "source", source, "dataId", dataID)
		return nil, false
	}

	logger.Info("配置清单加载成功", "source", source, "dataId", dataID, "count", len(configs))
	return configs, true
}

// expandAndDedup 展开 MetricConfigList 为 targetConfig map 并按 itemKey 去重
// 此时传入的 configs 已经完成 configCode 层去重 + 跨清单合并（LoadConfigList 阶段），
// 本函数只负责展开为 targetConfig + itemKey 去重（注册表自然满足 itemKey 唯一性）
// 返回：targets（展开+itemKey 去重后的二级配置）、cleanStorePaths（enableClean=true 的 storePath 列表）
// 优化：相同 group 的多个 DataId=="" 条目会缓存 searchGroupDataIds 结果，避免重复分页查询
func (s *ConfigService) expandAndDedup(configs model.MetricConfigList) (map[string]*targetConfig, []string) {
	targets := make(map[string]*targetConfig)
	var cleanStorePaths []string
	// cleanStorePaths 去重 set（#1：不同 MetricConfig 可能指向同 storePath + enableClean=true）
	cleanSeen := make(map[string]bool)

	// 缓存：group → dataIds 映射，避免相同 group 重复 searchGroupDataIds
	groupDataIdsCache := make(map[string][]string)

	for i := range configs {
		cfg := &configs[i]
		if !s.validateMetricConfigEntry(cfg, i) {
			continue
		}

		storePath := normalizeStorePath(cfg.StorePath)

		switch {
		case cfg.DataId != "" && cfg.Group != "":
			t := s.buildTarget(storePath, cfg, cfg.DataId)
			key := t.itemKey()
			if _, exists := targets[key]; !exists {
				targets[key] = t
			} else {
				logger.Warn("展开后 itemKey 重复，先配置先生效，跳过",
					"index", i, "itemKey", key, "configCode", cfg.ConfigCode)
			}

		case cfg.DataId == "" && cfg.Group != "":
			// 缓存命中检查
			dataIds, cached := groupDataIdsCache[cfg.Group]
			if !cached {
				var err error
				dataIds, err = s.searchGroupDataIds(cfg.Group)
				if err != nil {
					logger.Error("查询分组配置失败，跳过该条",
						"namespace", s.params.Namespace, "group", cfg.Group,
						"storePath", storePath, "error", err)
					continue
				}
				groupDataIdsCache[cfg.Group] = dataIds
			}
			// 需求：只有按分组拉取配置，才有必要清理storePath下多余的配置文件
			if cfg.EnableClean && !cleanSeen[storePath] {
				cleanStorePaths = append(cleanStorePaths, storePath)
				cleanSeen[storePath] = true
			}
			// DataId 为空时 reFileName 没有意义，用局部 copy 避免修改原 config
			cfgCopy := *cfg
			cfgCopy.ReFileName = ""
			for _, dataId := range dataIds {
				t := s.buildTarget(storePath, &cfgCopy, dataId)
				key := t.itemKey()
				if _, exists := targets[key]; !exists {
					targets[key] = t
				} else {
					logger.Warn("展开后 itemKey 重复，先配置先生效，跳过",
						"index", i, "itemKey", key, "configCode", cfg.ConfigCode)
				}
			}

		default:
			// group 为空已被 validateMetricConfigEntry 拦截，理论不可达
			logger.Warn("配置项 group 为空，跳过", "index", i)
		}
	}

	return targets, cleanStorePaths
}

// validateMetricConfigEntry 校验 MetricConfig 必填字段
// PRD 3.2.2：configCode 必填、正则 ^[a-zA-Z0-9_-]+$；group 必填；storePath 必填
func (s *ConfigService) validateMetricConfigEntry(cfg *model.MetricConfig, idx int) bool {
	if strings.TrimSpace(cfg.ConfigCode) == "" {
		logger.Warn("配置项缺少必填字段 configCode，跳过", "index", idx)
		return false
	}
	if !configCodePattern.MatchString(cfg.ConfigCode) {
		logger.Warn("配置项 configCode 含非法字符，仅允许字母、数字、下划线、横线",
			"index", idx, "configCode", cfg.ConfigCode)
		return false
	}
	if strings.TrimSpace(cfg.Group) == "" {
		logger.Warn("配置项缺少必填字段 group，跳过", "index", idx, "configCode", cfg.ConfigCode)
		return false
	}
	if strings.TrimSpace(cfg.StorePath) == "" {
		logger.Warn("配置项缺少必填字段 storePath，跳过", "index", idx, "configCode", cfg.ConfigCode)
		return false
	}
	return true
}

// buildTarget 构建具体目标配置（itemKey 也一并算好缓存）
func (s *ConfigService) buildTarget(storePath string, cfg *model.MetricConfig, dataId string) *targetConfig {
	suffix := normalizeSuffix(cfg.Suffix)
	t := &targetConfig{
		namespace:    s.params.Namespace,
		group:        cfg.Group,
		dataId:       dataId,
		suffix:       suffix,
		storePath:    storePath,
		reFileName:   cfg.ReFileName,
		reloadScript: cfg.ReloadScript,
		finalName:    finalNameOf(dataId, cfg.ReFileName, suffix),
		fileMode:     parseFileMode(cfg.FileMode, myconstant.DefaultConfigFilePerm),
		fileModeStr:  cfg.FileMode,
		enableClean:  cfg.EnableClean,
		configCode:   cfg.ConfigCode,
		source:       cfg.Source,
	}
	t.itemKey_ = itemKeyOf(t.namespace, t.group, t.dataId, t.suffix, t.storePath, t.fileModeStr, t.reloadScript, t.reFileName)
	return t
}

// DistributeAllConfigs 串行分发所有二级配置（PRD 3.2.3）
// 架构：配置清单走定时拉取（LoadConfigList 完成拉取+展开+去重+合并）；二级配置走 Nacos 监听（AddListener）
//   - 新配置 / 属性变更配置 / AddListener 曾失败未入注册表：itemKey 查不到 → 完整流程 processTarget
//   - 已存在且属性完全一致：跳过（Nacos 监听已持续推送变更）
//   - 已删除配置：removeStale 先移除
//
// 注：itemKey 覆盖影响 Nacos 监听标识 + 最终落盘路径的属性
// （ns/group/dataId/suffix/storePath/fileMode/reloadScript/reFileName），
// enableClean 不参与 itemKey，仅控制是否执行配置清理
func (s *ConfigService) DistributeAllConfigs(result *configListResult) {
	targets := result.mergedTargets
	total := len(targets)
	success := 0
	failed := 0

	// 先移除失效项，避免移除监听导致非group\dataid变更时，需要监听的配置没有监听
	// 例如配置项group/dataid相同，storePath不同，需要监听的配置没有监听，因为被移除了，先执行移除监听即可避免。
	removed := s.removeStale(targets, result)

	// 处理配置：itemKey 查不到（全新 / 属性变更 / AddListener 曾失败）→ processTarget；已存在 → 跳过
	for _, t := range targets {
		if _, exists := s.registry.Get(t.itemKey()); exists {
			// 已存在且 itemKey 完全一致 = 所有属性一致，跳过
			continue
		}
		if s.processTarget(t) {
			success++
		} else {
			failed++
		}
	}

	// enableClean 触发的配置清理（失败不影响后续，PRD 3.2.3 / 3.2.4）
	s.distributeClean(result, result.cleanStorePaths, targets)

	// 清理 cleanLastRun 中已不在本轮 cleanStorePaths 的条目
	// 防止配置清单变更后 storePath 被删除但 cleanLastRun 条目永久残留（内存泄漏）
	s.pruneCleanLastRun(result.cleanStorePaths)

	logger.Info("二级配置分发、配置清理完成",
		"personal_dataId", result.personalDataID, "personal_ok", result.personalOK,
		"public_dataId", result.publicDataID, "public_ok", result.publicOK,
		"total", total,
		"success", success,
		"failed", failed,
		"current_listeners", s.registry.Len(),
		"removed", removed,
	)
}

// parseFileMode 解析配置项的 fileMode 字符串为 os.FileMode
// 空串或非法值 fallback 到 defaultVal（通常是 DefaultConfigFilePerm 即 0755）
// 使用 strconv.ParseInt(s, 8, 64) 显式按八进制解析，避免 yaml.v3 不识别 0 前缀为八进制
func parseFileMode(raw string, defaultVal os.FileMode) os.FileMode {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultVal
	}
	// 去掉可能的 0o 前缀（YAML 1.2 写法）
	raw = strings.TrimPrefix(raw, "0o")
	mode, err := strconv.ParseInt(raw, 8, 64)
	if err != nil {
		logger.Warn("fileMode 解析失败，使用默认值", "raw", raw, "error", err, "default", defaultVal)
		return defaultVal
	}
	if mode <= 0 || mode > 07777 {
		logger.Warn("fileMode 超出有效范围，使用默认值", "raw", raw, "parsed", mode, "default", defaultVal)
		return defaultVal
	}
	return os.FileMode(mode)
}

// searchGroupDataIds 分页查询指定分组下所有配置的 dataId
func (s *ConfigService) searchGroupDataIds(group string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)

	pageNo := 1
	for {
		page, err := s.cc.SearchConfig(group, pageNo, myconstant.NacosSearchPageSize)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			dataId := strings.TrimSpace(it.DataId)
			if dataId == "" || seen[dataId] {
				continue
			}
			seen[dataId] = true
			out = append(out, dataId)
		}
		// 终止条件：本页为空，或已取满总条数（防止依赖不可靠的 TotalCount 时死循环）
		if len(page.Items) == 0 || pageNo*myconstant.NacosSearchPageSize >= page.TotalCount {
			break
		}
		pageNo++
	}
	return out, nil
}

// processTarget 拉取、写入、注册监听（PRD 3.2.3）
// 流程：GetConfig → writeConfig → runReloadScript → AddListener → AddRegistry
// 注册监听时通过闭包捕获 t 的上下文，变更回调异步触发 writeConfig + runReloadScript
// 使用 perConfigLock 与 handleListenerCallback 互斥，防止极端场景下（SDK AddListener 后
// 立即触发首次 OnChange）processTarget 的 writeConfig 与回调 writeConfig 竞争同一 finalPath
//
// 返回值语义：true 表示 GetConfig + writeConfig + runReloadScript + AddListener 全部成功，
// 配置项已加入 registry 由 Nacos 监听持续推送变更；
// false 表示任一步骤失败，配置项不加入 registry（per listenerMu 会同步清理），
// 下一轮定时拉取自然走 !exists 分支重新执行 processTarget（完整重试）。
//
// #2 防止 listenerMu 内存泄漏 + 防止 manual Unlock 的 panic 风险：
//
//	defer mu.Unlock() 保证任何 panic 路径都能解锁；
//	用 needCleanupListenerMu 标志在 registry 未 Add 时（含 writeConfig 失败 + AddListener 失败）清理 listenerMu 条目。
func (s *ConfigService) processTarget(t *targetConfig) bool {
	content, err := s.cc.GetConfig(t.dataId, t.group)
	if err != nil {
		logger.Error("首次拉取二级配置失败",
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath, "error", err)
		return false
	}
	if content == "" {
		logger.Warn("拉取到的配置内容为空，跳过写入",
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath)
		return false
	}

	// 与 handleListenerCallback 共享 per-itemKey 锁
	mu := s.perConfigLock(t.itemKey())
	mu.Lock()
	defer mu.Unlock()

	// #2 listenerMu 清理标志：仅在 writeConfig 失败（registry 未 Add）时需要清理
	// registry 已 Add 的场景：removeStale 会遍历 registry 正确清理 listenerMu
	needCleanupListenerMu := false
	defer func() {
		if needCleanupListenerMu {
			s.listenerMuMu.Lock()
			delete(s.listenerMu, t.itemKey())
			s.listenerMuMu.Unlock()
		}
	}()

	finalPath := t.storePath + t.finalName
	if err := s.writeConfig(t, content, finalPath); err != nil {
		needCleanupListenerMu = true
		logger.Error("首次写入二级配置失败",
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath, "error", err)
		return false
	}

	// 首次执行 reloadScript
	if t.reloadScript != "" {
		s.runReloadScript(t)
	}

	// 注册 Nacos 监听：配置变更时 OnChange 回调触发
	onChange := func(namespace, group, dataId, data string) {
		// 必须异步！不能在 SDK 内部监听协程中做 I/O + reload，会阻塞其他配置变更检测
		go s.handleListenerCallback(t, data)
	}
	if err := s.cc.AddListener(t.dataId, t.group, onChange); err != nil {
		// 监听注册失败 → 不加入 registry（否则后续轮次 DistributeAllConfigs 会误以为已注册）
		// 下一轮定时拉取该条目自然不在 registry 中，会走 !exists 分支重新完整重试 processTarget
		needCleanupListenerMu = true
		logger.Error("注册 Nacos 监听失败，等待下一轮定时拉取重新注册",
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId,
			"storePath", t.storePath, "error", err)
		return false
	}

	// AddListener 成功 → 才加入 registry，由 Nacos 监听持续推送后续变更
	item := &ListenRegistryItem{
		ItemKey:      t.itemKey(),
		Namespace:    t.namespace,
		Group:        t.group,
		DataId:       t.dataId,
		Suffix:       t.suffix,
		ConfigCode:   t.configCode,
		StorePath:    t.storePath,
		FinalName:    t.finalName,
		ReFileName:   t.reFileName,
		FileMode:     t.fileMode,
		FileModeStr:  t.fileModeStr,
		ReloadScript: t.reloadScript,
		EnableClean:  t.enableClean,
	}
	if s.registry.Add(item) {
		logger.Info("二级配置分发成功",
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath)
	}
	// registry.Add 返回 false 是死代码：
	// perConfigLock(t.itemKey()) 已串行化了整个 writeConfig→Add 流程，
	// 同一个 itemKey 不可能被两个 goroutine 同时走到这里。
	// 即使极端情况发生，副作用（writeConfig + reloadScript）已落盘，也无需回滚。

	return true
}

// handleListenerCallback Nacos 配置变更回调处理器
// 由 SDK 内部监听协程触发 → 已在 processTarget 里 go 出本方法
// 使用 per-itemKey 锁串行化同一配置项的写操作（防止连续推送并发冲突）
//
// 加 panic recovery：防止 writeConfig/runReloadScript 内部 panic 后
// 该 itemKey 的监听回调 goroutine 永久死亡，导致这个配置项不再自动更新
func (s *ConfigService) handleListenerCallback(t *targetConfig, newContent string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("监听回调 panic 已捕获，本配置项后续变更可能不再自动同步",
				"itemKey", t.itemKey(),
				"namespace", t.namespace, "group", t.group, "dataId", t.dataId,
				"storePath", t.storePath, "panic", fmt.Sprint(r))
		}
	}()

	// 配置已被 removeStale 从 registry 移除（itemKey 已不存在），可能原因：
	// 1) 定时拉取检测到配置清单变更，old itemKey 被取消监听；
	// 2) Nacos SDK CancelListener 之后仍有延迟到达的 onChange 回调。
	// 此时放弃执行，防止旧配置闭包覆盖新文件或触发错误的 reloadScript。
	if _, ok := s.registry.Get(t.itemKey()); !ok {
		logger.Info("监听回调：配置已不在注册表，跳过执行（可能已被移除或属性变更）",
			"itemKey", t.itemKey(),
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId,
			"storePath", t.storePath)
		return
	}

	mu := s.perConfigLock(t.itemKey())
	mu.Lock()
	defer mu.Unlock()

	finalPath := t.storePath + t.finalName
	if newContent == "" {
		if filekit.PathExists(finalPath) {
			if err := os.Remove(finalPath); err != nil {
				logger.Error("监听回调：配置内容为空，删除本地文件失败",
					"namespace", t.namespace, "group", t.group, "dataId", t.dataId,
					"storePath", t.storePath, "path", finalPath, "error", err)
			} else {
				logger.Info("监听回调：配置内容为空，已删除本地文件",
					"namespace", t.namespace, "group", t.group, "dataId", t.dataId,
					"storePath", t.storePath, "path", finalPath)
			}
		} else {
			logger.Info("监听回调：配置内容为空，本地文件不存在，无需删除",
				"namespace", t.namespace, "group", t.group, "dataId", t.dataId,
				"storePath", t.storePath, "path", finalPath)
		}
		return
	}
	if err := s.writeConfig(t, newContent, finalPath); err != nil {
		logger.Error("监听回调：配置处理失败",
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath, "error", err)
		return
	}
	if t.reloadScript != "" {
		s.runReloadScript(t)
	}
	logger.Info("监听回调：配置更新成功",
		"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath)
}

// perConfigLock 获取指定 itemKey 的串行化锁（不存在则懒创建）
func (s *ConfigService) perConfigLock(itemKey string) *sync.Mutex {
	s.listenerMuMu.Lock()
	defer s.listenerMuMu.Unlock()
	m, ok := s.listenerMu[itemKey]
	if !ok {
		m = &sync.Mutex{}
		s.listenerMu[itemKey] = m
	}
	return m
}

// writeConfig 写入配置：临时文件 → 备份 → 原子重命名 → 权限 → 重载（重载失败不回滚）
func (s *ConfigService) writeConfig(t *targetConfig, content, finalPath string) error {
	// 1. 确保目录存在（755）
	if err := filekit.EnsureDir(t.storePath, myconstant.DefaultDirPerm); err != nil {
		return fmt.Errorf("创建存储目录失败: %w", err)
	}

	// 2. 写临时文件 {最终文件名}.{uuid}.tmp
	tmpPath := t.storePath + t.finalName + "." + newUUID() + ".tmp"
	if err := writeFileAll(tmpPath, content, t.fileMode); err != nil {
		return fmt.Errorf("写临时文件失败: %w", err)
	}

	// 3. 目标已存在则备份到 storePath/bak/{最终文件名}
	backupPath := ""
	if filekit.PathExists(finalPath) {
		bakDir := t.storePath + myconstant.ConfigBackupDirName
		if err := filekit.EnsureDir(bakDir, myconstant.DefaultDirPerm); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("创建备份目录失败: %w", err)
		}
		backupPath = bakDir + string(os.PathSeparator) + t.finalName
		if err := renameOverwrite(finalPath, backupPath); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("备份原文件失败: %w", err)
		}
	}

	// 4. 临时文件原子重命名为最终文件
	if err := os.Rename(tmpPath, finalPath); err != nil {
		// 回滚：删除临时文件，恢复备份
		_ = os.Remove(tmpPath)
		if backupPath != "" {
			_ = renameOverwrite(backupPath, finalPath)
		}
		return fmt.Errorf("重命名临时文件失败: %w", err)
	}

	// 5. 设置最终文件权限（per-config fileMode，默认 0755）
	if err := os.Chmod(finalPath, t.fileMode); err != nil {
		logger.Warn("设置文件权限失败", "path", finalPath, "error", err)
	}

	return nil
}

// runReloadScript 执行重载脚本（失败不回滚，仅捕获 stdout/stderr 写日志）
// 工作目录为 Agent 二进制目录（PRD 3.2.3）
// 使用独立 context（独立 timeout），不依赖 pullStopCtx（避免并发数据竞争）
func (s *ConfigService) runReloadScript(t *targetConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.params.ReloadScriptTimeout)*time.Second)
	defer cancel()

	stdout, stderr, exitCode, err := s.se.ExecWithDir(ctx, s.params.AgentExecDir, t.reloadScript)
	if err != nil {
		logger.Error("重载脚本执行失败（不回滚）",
			"script", truncateForLog(t.reloadScript),
			"stdout_preview", truncateForLog(stdout), "stderr_preview", truncateForLog(stderr),
			"error", err,
			"workDir", s.params.AgentExecDir,
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath)
		return
	}
	if exitCode != 0 {
		logger.Warn("重载脚本非零退出码（不回滚）",
			"script", truncateForLog(t.reloadScript),
			"exitCode", exitCode,
			"stdout_preview", truncateForLog(stdout), "stderr_preview", truncateForLog(stderr),
			"workDir", s.params.AgentExecDir,
			"namespace", t.namespace, "group", t.group, "dataId", t.dataId, "storePath", t.storePath)
		return
	}
	logger.Info("重载脚本执行成功",
		"script", truncateForLog(t.reloadScript),
		"workDir", s.params.AgentExecDir,
		"namespace", t.namespace, "group", t.group, "dataId", t.dataId)
}

// removeStale 取消 Nacos 监听并移除注册表中不在本次目标列表内的失效项
func (s *ConfigService) removeStale(targets map[string]*targetConfig, result *configListResult) int {
	removed := 0
	for _, item := range s.registry.Items() {
		if _, ok := targets[item.ItemKey]; ok {
			continue
		}
		// 先取消 Nacos 监听，再从本地注册表移除
		if err := s.cc.CancelListener(item.DataId, item.Group); err != nil {
			logger.Error("取消失效配置的 Nacos 监听失败",
				"error", err, "itemKey", item.ItemKey, "namespace", item.Namespace,
				"group", item.Group, "dataId", item.DataId)
		} else {
			logger.Info("移除失效配置（Nacos监听已取消）",
				"itemKey", item.ItemKey, "namespace", item.Namespace,
				"group", item.Group, "dataId", item.DataId)
		}
		s.registry.Remove(item.ItemKey)
		// 同步清理 per-itemKey mutex，避免长期运行时 map 无限增长
		s.listenerMuMu.Lock()
		delete(s.listenerMu, item.ItemKey)
		s.listenerMuMu.Unlock()
		removed++
	}
	logger.Info("移除监听完成",
		"current_listeners", s.registry.Len(), "removed", removed,
		"personal_dataId", result.personalDataID, "public_dataId", result.publicDataID)
	return removed
}

// ============ 配置清理（PRD 3.2.4） ============

// pruneCleanLastRun 清理 cleanLastRun 中已不在本轮 cleanStorePaths 的条目
// 防止配置清单变更后 storePath 被删除但 cleanLastRun 条目永久残留（内存泄漏）
func (s *ConfigService) pruneCleanLastRun(cleanStorePaths []string) {
	// 构建 set 加速 O(1) 查找
	keep := make(map[string]bool, len(cleanStorePaths))
	for _, sp := range cleanStorePaths {
		keep[sp] = true
	}

	s.cleanMu.Lock()
	defer s.cleanMu.Unlock()

	for key := range s.cleanLastRun {
		if !keep[key] {
			delete(s.cleanLastRun, key)
		}
	}
}

// distributeClean 由 DistributeAllConfigs 触发配置清理
// PRD 3.2.4 约束：清理由二级分发功能触发，不单独使用协程实现；
// 修复：只要当前小时命中 cleanFixHours 且本小时尚未清理过，立即执行（移除 0~10 分钟窗口限制）
func (s *ConfigService) distributeClean(result *configListResult, cleanStorePaths []string, targets map[string]*targetConfig) {
	personalDataID := result.personalDataID
	publicDataID := result.publicDataID

	if !s.params.CleanOrphanFile.Enable {
		logger.Warn("配置清理功能已禁用（cleanOrphanFile.enable=false），跳过本次清理",
			"personal_dataId", personalDataID, "public_dataId", publicDataID)
		return
	}
	if len(s.cleanFixHours) == 0 {
		logger.Warn("cleanFixHour 无有效配置，不执行配置清理",
			"personal_dataId", personalDataID, "public_dataId", publicDataID)
		return
	}
	// cleanStorePaths为空，表明没有需要清理配置的路径
	if len(cleanStorePaths) == 0 {
		return
	}

	now := time.Now()
	if !containsInt(s.cleanFixHours, now.Hour()) {
		//只在匹配时打印
		// logger.Info("当前时刻不在配置清理定点小时内，跳过本次清理",
		// 	"current_hour", now.Hour(), "cleanFixHour", s.cleanFixHours,
		// 	"personal_dataId", personalDataID, "public_dataId", publicDataID)
		return
	}

	// finalNameSets 从 mergedTargets 构建即可
	// 因为 cleanStorePaths 也来自 mergedConfigs 展开阶段，storePath 覆盖自然一致
	finalNameSets := buildFinalNameSets(targets)

	for _, sp := range cleanStorePaths {
		if names, ok := finalNameSets[sp]; ok {
			s.cleanStorePathWithDedup(sp, names, now, personalDataID, publicDataID)
		} else {
			logger.Warn("enableClean 的 storePath 在 finalNameSets 中找不到对应条目，跳过清理",
				"storePath", sp)
		}
	}
}

// cleanStorePathWithDedup 保证每小时每个 storePath 只执行一次清理
func (s *ConfigService) cleanStorePathWithDedup(storePath string, finalNames map[string]bool, now time.Time, personalDataID, publicDataID string) {
	hourKey := now.Format("20060102-15")
	s.cleanMu.Lock()
	if last, ok := s.cleanLastRun[storePath]; ok && last.Format("20060102-15") == hourKey {
		s.cleanMu.Unlock()
		//别打印日志了
		//logger.Info("本小时已清理过该目录，跳过", "storePath", storePath, "hour", hourKey, "source", source, "dataId", dataID)
		return
	}
	s.cleanLastRun[storePath] = now
	s.cleanMu.Unlock()

	s.cleanStorePath(storePath, finalNames)
}

// cleanStorePath 清理单个 storePath 下的孤儿文件（PRD 3.2.4 核心逻辑）
func (s *ConfigService) cleanStorePath(storePath string, finalNames map[string]bool) {
	if isHighRiskDir(storePath) {
		logger.Warn("storePath 命中高危目录黑名单，跳过清理", "storePath", storePath)
		return
	}
	if !filekit.PathExists(storePath) {
		logger.Warn("storePath 目录不存在，跳过清理", "storePath", storePath)
		return
	}

	entries, err := os.ReadDir(storePath)
	if err != nil {
		logger.Error("读取 storePath 目录失败", "storePath", storePath, "error", err)
		return
	}

	scanned := 0
	var orphans []string
	for _, e := range entries {
		if e.IsDir() {
			continue // 只扫描一级目录文件，禁止递归
		}
		name := e.Name()
		if !s.hasCleanSuffix(name) {
			continue
		}
		scanned++
		if finalNames[name] {
			continue
		}
		orphans = append(orphans, name)
	}

	deleted := 0
	failedDel := 0
	for _, name := range orphans {
		if err := os.Remove(storePath + name); err != nil {
			failedDel++
			logger.Warn("删除孤儿文件失败", "storePath", storePath, "file", name, "error", err)
		} else {
			deleted++
		}
	}

	logger.Info("配置清理完成",
		"storePath", storePath,
		"scanned", scanned,
		// 孤儿数量
		"orphans", len(orphans),
		"deleted", deleted,
		"delete_failed", failedDel,
	)
}

func (s *ConfigService) hasCleanSuffix(name string) bool {
	lower := strings.ToLower(name)
	for _, suf := range s.cleanSuffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

// ============ 任务锁（PRD 3.2.2） ============

// acquireTaskLock 尝试获取任务锁，返回非零 token 表示成功
func (s *ConfigService) acquireTaskLock() uint64 {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.taskLocked {
		return 0
	}
	s.taskLocked = true
	s.taskToken++
	token := s.taskToken
	s.taskLockTimer = time.AfterFunc(time.Duration(myconstant.ConfigTaskLockTimeoutSeconds)*time.Second, func() {
		s.taskMu.Lock()
		if s.taskLocked && s.taskToken == token {
			s.taskLocked = false
			s.taskLockTimer = nil
			logger.Warn("配置定时任务锁超时，自动解锁",
				"timeout", (time.Duration(myconstant.ConfigTaskLockTimeoutSeconds) * time.Second).String())
		}
		s.taskMu.Unlock()
	})
	return token
}

// releaseTaskLock 释放任务锁（仅当 token 匹配当前锁时生效）
func (s *ConfigService) releaseTaskLock(token uint64) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if !s.taskLocked || s.taskToken != token {
		return
	}
	if s.taskLockTimer != nil {
		s.taskLockTimer.Stop()
		s.taskLockTimer = nil
	}
	s.taskLocked = false
}

// ============ 定时拉取循环 ============

// StartPullLoop 启动定时拉取循环（PRD 3.2）
// 每隔 pullInterval 分钟，重新执行 LoadConfigList → DistributeAllConfigs 全量流程
// 仅配置清单（一级）走定时拉取；二级配置走 Nacos 监听（已存在不重复处理）
// pullInterval 小于等于 0 时使用默认 1 分钟（time.NewTicker(0) 会 panic）
func (s *ConfigService) StartPullLoop(pullInterval int) {
	if pullInterval <= 0 {
		pullInterval = myconstant.DefaultPullIntervalMinutes
	}

	s.pullMu.Lock()
	if s.pullRunning {
		s.pullMu.Unlock()
		logger.Info("配置定时拉取循环已在运行，忽略重复启动请求")
		return
	}
	s.pullStopOnce = sync.Once{} // 重置，允许下一次 StopPullLoop 再次 close
	s.pullStop = make(chan struct{})
	s.pullRunning = true
	s.pullInterval = time.Duration(pullInterval) * time.Minute
	s.pullWg.Add(1)
	s.pullMu.Unlock()

	go s.pullLoop(time.Duration(pullInterval) * time.Minute)
	logger.Info("配置定时拉取服务已启动", "pullInterval_minutes", pullInterval)
}

// StopPullLoop 停止定时拉取循环，等待 goroutine 退出
// 注意：此方法不取消已注册的 Nacos 监听；Nacos 监听随 SDK CloseClient 自然失效
// ConfigService 的 Close/Cleanup 由上层（bootstrap.shutdown）统一负责
func (s *ConfigService) StopPullLoop() {
	s.pullMu.Lock()
	if !s.pullRunning {
		s.pullMu.Unlock()
		return
	}
	s.pullRunning = false
	s.pullStopOnce.Do(func() {
		close(s.pullStop)
	})
	s.pullMu.Unlock()

	// 等待 pullLoop goroutine 退出，但设置超时保护：
	// pullLoop 可能正在执行 safeDoPullOnce（含 Nacos GetConfig 请求），
	// 若 Nacos 不可达，单次请求最长 5s。这里设 2s 超时，
	// 超时后 goroutine 随进程退出自然终止。
	done := make(chan struct{})
	go func() {
		s.pullWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("配置定时拉取循环已停止")
	case <-time.After(2 * time.Second):
		logger.Warn("配置定时拉取循环停止超时，跳过等待")
	}
}

func (s *ConfigService) pullLoop(interval time.Duration) {
	defer s.pullWg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.pullStop:
			return
		case <-ticker.C:
			s.safeDoPullOnce()
		}
	}
}

func (s *ConfigService) safeDoPullOnce() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("定时拉取执行 panic 已捕获，本轮跳过，下次继续", "panic", fmt.Sprint(r))
		}
	}()
	s.doPullOnce()
}

func (s *ConfigService) doPullOnce() {
	token := s.acquireTaskLock()
	if token == 0 {
		logger.Warn("上一轮配置定时任务仍在执行，拒绝本轮调度")
		return
	}
	defer s.releaseTaskLock(token)

	start := time.Now()
	logger.Info("定时拉取：开始全量刷新配置")

	result, err := s.LoadConfigList()
	if err != nil {
		logger.Warn("定时拉取配置失败，跳过本轮", "error", err)
		return
	}

	s.DistributeAllConfigs(result)

	logger.Info("定时拉取：全量刷新完成",
		"personal_dataId", result.personalDataID, "personal_ok", result.personalOK,
		"public_dataId", result.publicDataID, "public_ok", result.publicOK,
		"merged_count", len(result.mergedTargets),
		"listeners_now", s.registry.Len(),
		"duration_ms", time.Since(start).Milliseconds())
}

// ============ 工具函数 ============

// normalizeStorePath 补全 storePath 末尾路径分隔符（跨平台安全）
// 不做 filepath.Join 避免相对路径前缀 ./ 被剥掉；仅尾部清理多余分隔符后补一个
func normalizeStorePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	sep := string(os.PathSeparator)
	// 同时清理尾部的 os.PathSeparator 和正斜杠（跨平台兼容）
	trimmed := strings.TrimRight(p, sep+"/")
	return trimmed + sep
}

// finalNameOf 计算最终文件名（PRD 3.2.3）
// reFileName 非空优先；否则 dataId + suffix（suffix 已通过 normalizeSuffix 归一化）
func finalNameOf(dataId, reFileName, suffix string) string {
	if reFileName != "" {
		return reFileName
	}
	return dataId + suffix
}

// normalizeSuffix 归一化文件后缀（PRD 3.2.2）
// TrimSpace 去空白；空值返回 ""（不补默认值）；无前导 "." 自动补齐
func normalizeSuffix(suffix string) string {
	s := strings.TrimSpace(suffix)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, ".") {
		s = "." + s
	}
	return s
}

// buildFinalNameSets 按 storePath 汇总最终文件名集合
func buildFinalNameSets(targets map[string]*targetConfig) map[string]map[string]bool {
	m := make(map[string]map[string]bool)
	for _, t := range targets {
		if m[t.storePath] == nil {
			m[t.storePath] = make(map[string]bool)
		}
		m[t.storePath][t.finalName] = true
	}
	return m
}

// normalizeCleanFixHours 归一化定点清理小时（去重、仅 0~23 有效，PRD 3.2.4）
// 直接构造 out 再 sort，避免原地 sort 调用方传入的原始 slice（副作用）
func normalizeCleanFixHours(raw []int) []int {
	seen := make(map[int]bool)
	out := make([]int, 0, len(raw))
	for _, h := range raw {
		if h < 0 || h > 23 {
			continue
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Ints(out)
	return out
}

// normalizeCleanSuffixes 归一化清理后缀（去空、去重、小写）
// 归一化后为空时填充默认值 [".yml", ".yaml"]（PRD 3.2.4：cleanSuffix 未配置默认为 .yml、.yaml）
func normalizeCleanSuffixes(raw []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, suf := range raw {
		suf = strings.ToLower(strings.TrimSpace(suf))
		if suf == "" {
			continue
		}
		if !strings.HasPrefix(suf, ".") {
			suf = "." + suf
		}
		if seen[suf] {
			continue
		}
		seen[suf] = true
		out = append(out, suf)
	}
	// PRD 3.2.4：未配置 cleanSuffix 时默认为 .yml、.yaml
	if len(out) == 0 {
		out = append(out, myconstant.DefaultCleanSuffixes...)
	}
	return out
}

// dedupStrings 字符串切片去重，保持首次出现的顺序
func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// truncateForLog 截断字符串用于日志输出
// 超过 ShellLogPreviewBytes 时截断并追加 "...(truncated)" 标记
func truncateForLog(s string) string {
	if len(s) <= myconstant.ShellLogPreviewBytes {
		return s
	}
	return s[:myconstant.ShellLogPreviewBytes] + "...(truncated)"
}

// isHighRiskDir 判断 storePath 是否命中高危目录黑名单（PRD 3.2.4）
func isHighRiskDir(storePath string) bool {
	cleaned := strings.ToLower(filepath.Clean(storePath))
	for _, dir := range myconstant.DefaultConfigCleanBlacklist {
		d := strings.ToLower(filepath.Clean(dir))
		if cleaned == d || strings.HasPrefix(cleaned, d+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// writeFileAll 全量写入文件（截断写 + 落盘 + 关闭），失败自动清理
func writeFileAll(path, content string, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// renameOverwrite 跨平台覆盖式重命名（目标已存在则先删除）
func renameOverwrite(src, dst string) error {
	if filekit.PathExists(dst) {
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	return os.Rename(src, dst)
}

// newUUID 生成随机 UUID 字符串（用于临时文件名）
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return hex.EncodeToString(b)
}
