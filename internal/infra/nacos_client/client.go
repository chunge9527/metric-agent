// Package nacos_client Nacos客户端实现
//
// 架构说明（PRD 3.2）：
//
//   - 配置清单（个性化/公共）走定时拉取 + 本地注册表去重
//   - 二级配置走 Nacos 监听（SDK ListenConfig），内容变更时 OnChange 回调写入本地文件
//   - 自动重连：SDK RpcClient 内置 healthCheck + reconnect；初始连接失败时 agent 自行定时重试创建 ConfigClient
//
// 运行阶段 SDK RpcClient 完全接管自动重连，本客户端不再额外维护 connected/failCount 状态。
package nacos_client

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	myconstant "metric-agent/internal/common/constant"
	myerrors "metric-agent/internal/common/errors"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/infra/iface"
	"metric-agent/internal/model"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	nacosconstant "github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// nacosExecDir 包级缓存可执行文件所在目录，init() 一次性获取
// 避免 connect() 每次调用时重复 os.Executable() 系统调用
var nacosExecDir string

func init() {
	if execPath, err := os.Executable(); err == nil {
		nacosExecDir = filepath.Dir(execPath)
	} else {
		nacosExecDir, _ = os.Getwd()
	}
}

// resolveNacosPath 路径解析：相对路径基于 nacosExecDir（可执行文件目录），绝对路径直接返回
// 与 bootstrap.resolvePath 逻辑一致，但避免 bootstrap ↔ nacos_client 循环引用
func resolveNacosPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(nacosExecDir, path)
}

// NacosClient Nacos配置中心客户端实现
//
// 字段说明：
//   - configClient: SDK 配置客户端（ConfigClient 创建成功后，SDK 内部 RpcClient 自动启动并接管重连）
//   - namespace:    Nacos namespace（AddListener/CancelListener 时 SDK 自动从 clientConfig 取，此处留作备用）
//   - mu:           保护 configClient 指针的读写（连接创建/关闭与 GetConfig/SearchConfig/AddListener 的并发访问）
//   - stopCh:       Close() 时关闭，停止后台 reconnectLoop
//   - stopOnce:     确保 Close() 幂等
type NacosClient struct {
	configClient config_client.IConfigClient
	namespace    string
	mu           sync.RWMutex
	stopCh       chan struct{}
	stopOnce     sync.Once
}

// NewNacosClient 创建Nacos客户端
// 初始化时连接失败不返回错误，后台按60s间隔重试创建 ConfigClient。
// 创建成功后，SDK RpcClient 自动启动并接管运行阶段的全部自动重连（healthCheck + reconnect）。
//
// 注意：Nacos 的 public 命名空间 ID 是空字符串 ""，不是字面量 "public"。
// 若用户误配 namespace="public"，SDK 会把 "public" 作为 tenant 传给服务端，
// 导致所有查询（GetConfig/SearchConfig）返回空。这里做一次归一化防御。
func NewNacosClient(cfg model.NacosConfig) (*NacosClient, error) {
	// 归一化 namespace：public 命名空间必须用空字符串
	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "public" || namespace == "Public" {
		namespace = ""
	}

	client := &NacosClient{
		stopCh:    make(chan struct{}),
		namespace: namespace,
	}

	// 用归一化后的 namespace 构造一份 cfg 副本传给 connect（避免修改原 cfg）
	normalizedCfg := cfg
	normalizedCfg.Namespace = namespace

	// 尝试初始创建 ConfigClient（含 TCP 连通性探测）
	if err := client.connect(normalizedCfg); err != nil {
		logger.Warn("Nacos初始连接失败，将在后台定时重试创建 ConfigClient", "error", err)
		// 初始失败时 SDK RpcClient 还没机会启动，必须 agent 自己定时尝试创建 ConfigClient
		go client.reconnectLoop(normalizedCfg)
	} else {
		logger.Info("Nacos连接成功（ConfigClient已创建，SDK RpcClient自动启动）")
		// 创建成功：SDK RpcClient.Start() 已在 clients.NewConfigClient 内部被调用
		// 后续运行阶段的自动重连完全由 SDK 接管，agent 不再额外维护
	}

	return client, nil
}

// DefaultNacosPort Nacos默认主端口
const DefaultNacosPort = 8848

// ParseNacosAddress 解析Nacos服务地址
// 支持格式：http://host:port、https://host:port、host:port、host（默认端口8848）
// 返回解析后的host和port，格式非法时返回错误
func ParseNacosAddress(address string) (string, uint64, error) {
	addr := strings.TrimSpace(address)
	// 剥离scheme
	if strings.HasPrefix(strings.ToLower(addr), "http://") {
		addr = addr[len("http://"):]
	} else if strings.HasPrefix(strings.ToLower(addr), "https://") {
		addr = addr[len("https://"):]
	}
	// 剥离尾部路径分隔符
	addr = strings.TrimSuffix(addr, "/")

	if addr == "" {
		return "", 0, fmt.Errorf("Nacos地址为空")
	}

	// 解析host:port
	if strings.Contains(addr, ":") {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return "", 0, fmt.Errorf("Nacos地址格式非法 %q: %w", address, err)
		}
		if host == "" {
			return "", 0, fmt.Errorf("Nacos地址缺少主机 %q", address)
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port == 0 {
			return "", 0, fmt.Errorf("Nacos端口非法 %q: 端口须为1-65535", address)
		}
		return host, port, nil
	}

	// 未指定端口，使用默认端口
	return addr, DefaultNacosPort, nil
}

// probeNacosServer 探测Nacos服务连通性（TCP拨号主端口）
// SDK的gRPC连接为懒连接，NewConfigClient成功不代表服务可达，需主动探测
func probeNacosServer(host string, port uint64) error {
	target := net.JoinHostPort(host, strconv.FormatUint(port, 10))
	conn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		return fmt.Errorf("Nacos服务不可达 %s: %w", target, err)
	}
	conn.Close()
	return nil
}

// connect 创建 ConfigClient（定时拉取模式）
// 调用前需确保服务可达（通过 probeNacosServer），创建成功后 SDK RpcClient 自动启动
func (n *NacosClient) connect(cfg model.NacosConfig) error {
	// 解析并校验地址格式（支持带scheme的URL写法）
	host, port, err := ParseNacosAddress(cfg.Address)
	if err != nil {
		return err
	}

	// 真实连通性探测（SDK懒连接，需主动确认服务可达，避免假"连接成功"）
	if err := probeNacosServer(host, port); err != nil {
		return err
	}

	// 配置超时：<=0 时用默认值（默认 5000ms）
	var timeoutMs uint64
	if cfg.Timeout <= 0 {
		timeoutMs = myconstant.DefaultNacosTimeoutMs
	} else {
		timeoutMs = uint64(cfg.Timeout)
	}

	// 配置启动时是否不加载缓存：nil→默认true；显式值→使用
	notLoadCache := true
	if cfg.NotLoadCacheAtStart != nil {
		notLoadCache = *cfg.NotLoadCacheAtStart
	}

	// nacos sdk 日志输出目录（sdk 内部固定文件名 nacos-sdk.log）
	// 用户配置了 LogDir → 用它（相对路径基于可执行文件目录）；没配 → 默认 ./logs/nacos
	logDir := filepath.Join(nacosExecDir, "logs", "nacos")
	if strings.TrimSpace(cfg.LogDir) != "" {
		logDir = resolveNacosPath(cfg.LogDir)
	}

	// nacos sdk 日志级别；fillDefaults 已兜底并归一化，直接信任
	logLevel := cfg.LogLevel

	// 构造 ClientConfig
	clientConfig := &nacosconstant.ClientConfig{
		NamespaceId:         cfg.Namespace,
		TimeoutMs:           timeoutMs,
		NotLoadCacheAtStart: notLoadCache,
		Username:            cfg.Username,
		Password:            cfg.Password,
		LogDir:              logDir,
		LogLevel:            logLevel,
	}

	// nacos sdk 本地缓存目录；用户配了 CacheDir 才设置，没配就走 SDK 默认值 ./cache
	if strings.TrimSpace(cfg.CacheDir) != "" {
		clientConfig.CacheDir = resolveNacosPath(cfg.CacheDir)
	}

	// nacos sdk 日志滚动配置；用户配了 LogRollingConfig 才传，没配走 SDK 原生默认值
	if cfg.LogRollingConfig != nil {
		clientConfig.LogRollingConfig = &nacosconstant.ClientLogRollingConfig{
			MaxSize:    cfg.LogRollingConfig.MaxSize,
			MaxAge:     cfg.LogRollingConfig.MaxAge,
			MaxBackups: cfg.LogRollingConfig.MaxBackups,
			LocalTime:  cfg.LogRollingConfig.LocalTime,
			Compress:   cfg.LogRollingConfig.Compress,
		}
	}

	param := vo.NacosClientParam{
		ClientConfig: clientConfig,
		ServerConfigs: []nacosconstant.ServerConfig{
			{
				IpAddr: host,
				Port:   port,
			},
		},
	}

	client, err := clients.NewConfigClient(param)
	if err != nil {
		return fmt.Errorf("创建Nacos配置服务失败: %w", err)
	}

	n.mu.Lock()
	oldClient := n.configClient
	n.configClient = client
	n.mu.Unlock()

	// 关闭旧连接（定时拉取模式，无需显式 CancelListenConfig，直接 CloseClient 即可）
	if oldClient != nil {
		oldClient.CloseClient()
	}

	return nil
}

// reconnectLoop 后台重试创建 ConfigClient
//
// 仅在 configClient == nil 时运行：
//   - 初始连接失败场景：NewNacosClient 时 TCP 探测失败 / SDK 创建失败
//   - Close() 后不应再调，但 Close 时会 close stopCh 让循环退出
//
// 创建成功后循环自动退出，SDK RpcClient 接管运行阶段全部自动重连（healthCheck + reconnect）。
// 运行阶段即使 SDK RpcClient 内部连接断开，它会自己重连，agent 不需要再介入。
func (n *NacosClient) reconnectLoop(cfg model.NacosConfig) {
	interval := time.Duration(myconstant.NacosStartupRetryInterval) * time.Second
	retryCount := 0

	// 立即执行首次尝试，避免静默等待 60s 才看到第一条日志
	if n.safeDoReconnect(cfg, retryCount, interval) {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-n.stopCh:
			logger.Info("Nacos后台重连循环已停止")
			return
		case <-ticker.C:
			// 创建成功则退出循环，SDK RpcClient 接管后续运行阶段
			n.mu.RLock()
			hasClient := n.configClient != nil
			n.mu.RUnlock()
			if hasClient {
				logger.Info("Nacos ConfigClient 已就绪，后台重连循环退出（SDK RpcClient 接管）")
				return
			}

			retryCount++
			if n.safeDoReconnect(cfg, retryCount, interval) {
				return
			}
		}
	}
}

// safeDoReconnect 带 panic recover 的 doReconnect 包装
// 一次 panic 不杀死循环，下次继续重试
func (n *NacosClient) safeDoReconnect(cfg model.NacosConfig, retryCount int, interval time.Duration) (success bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Nacos重连 panic 已捕获，本轮跳过，下次继续", "panic", fmt.Sprint(r))
			success = false
		}
	}()
	return n.doReconnect(cfg, retryCount, interval)
}

// doReconnect 执行单次 ConfigClient 创建尝试
// 返回是否创建成功
func (n *NacosClient) doReconnect(cfg model.NacosConfig, retryCount int, interval time.Duration) bool {
	logger.Info("尝试创建 Nacos ConfigClient",
		"retry", retryCount,
		"interval", interval.String(),
	)
	if err := n.connect(cfg); err != nil {
		logger.Warn("Nacos ConfigClient 创建失败",
			"retry", retryCount,
			"error", err,
			"next_retry_in", interval.String(),
		)
		return false
	}
	logger.Info("Nacos ConfigClient 创建成功，SDK RpcClient 已接管", "retry", retryCount)
	return true
}

// GetConfig 拉取指定 dataId + group 的配置内容
// 直接透传 SDK 调用；连接状态由 SDK 内部 RpcClient 自动管理（重试 + healthCheck + reconnect）
// configClient == nil 时返回错误（说明 ConfigClient 尚未创建成功）
func (n *NacosClient) GetConfig(dataId, group string) (string, error) {
	n.mu.RLock()
	client := n.configClient
	n.mu.RUnlock()

	if client == nil {
		return "", myerrors.ErrNacosPull(dataId, fmt.Errorf("Nacos ConfigClient 尚未创建"))
	}

	content, err := client.GetConfig(vo.ConfigParam{
		DataId: dataId,
		Group:  group,
	})
	if err != nil {
		logger.Warn("Nacos GetConfig 请求失败（SDK RpcClient 正在自动重连）",
			"error", err, "dataId", dataId, "group", group)
		return "", myerrors.ErrNacosPull(dataId, err)
	}
	return content, nil
}

// SearchConfig 按分组分页查询配置列表
// 直透传 SDK 调用；连接状态由 SDK 内部 RpcClient 自动管理
//
// 注意：显式传 DataId="*" 而非留空。Nacos v2 API `/v1/cs/configs` 在部分版本中
// 对 dataId=""（空字符串）处理不规范，可能返回空结果；Nacos 控制台使用 v3 API
// 则能正确返回。blur 模式下 "*" 是标准通配符，表示匹配所有 dataId，兼容性最好。
func (n *NacosClient) SearchConfig(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
	n.mu.RLock()
	client := n.configClient
	n.mu.RUnlock()

	if client == nil {
		return nil, myerrors.ErrNacosPull(fmt.Sprintf("group=%s", group), fmt.Errorf("Nacos ConfigClient 尚未创建"))
	}

	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = myconstant.NacosSearchPageSize
	}

	page, err := client.SearchConfig(vo.SearchConfigParam{
		Search:   "blur",
		DataId:   "*",
		Group:    group,
		PageNo:   pageNo,
		PageSize: pageSize,
	})
	if err != nil {
		logger.Warn("Nacos SearchConfig 请求失败（SDK RpcClient 正在自动重连）",
			"error", err, "group", group, "namespace", n.namespace)
		return nil, myerrors.ErrNacosPull(fmt.Sprintf("group=%s", group), err)
	}

	logger.Info("Nacos SearchConfig 返回",
		"namespace", n.namespace, "group", group,
		"pageNo", pageNo, "pageSize", pageSize,
		"totalCount", page.TotalCount, "items", len(page.PageItems))

	items := make([]model.ConfigItem, 0, len(page.PageItems))
	for _, it := range page.PageItems {
		items = append(items, model.ConfigItem{
			DataId:  it.DataId,
			Group:   it.Group,
			Content: it.Content,
		})
	}
	return &model.ConfigPage{Items: items, TotalCount: page.TotalCount}, nil
}

// AddListener 注册 Nacos 配置变更监听（PRD 3.2.3 二级配置监听）
// SDK 内部自动从 clientConfig.NamespaceId 取 namespace，无需显式传 tenant
// onChange 回调由 SDK 内部监听协程触发，不要在回调中做长时间阻塞操作（会延迟其他配置变更检测）
func (n *NacosClient) AddListener(dataId, group string, onChange func(namespace, group, dataId, data string)) error {
	n.mu.RLock()
	client := n.configClient
	n.mu.RUnlock()

	if client == nil {
		return myerrors.ErrNacosPull(dataId, fmt.Errorf("Nacos ConfigClient 尚未创建，无法注册监听"))
	}

	if err := client.ListenConfig(vo.ConfigParam{
		DataId:   dataId,
		Group:    group,
		OnChange: onChange,
	}); err != nil {
		logger.Warn("Nacos AddListener 注册监听失败",
			"error", err, "dataId", dataId, "group", group)
		return myerrors.ErrNacosPull(dataId, fmt.Errorf("注册 Nacos 监听失败: %w", err))
	}
	return nil
}

// CancelListener 取消指定配置项的 Nacos 监听（PRD 3.2.3）
func (n *NacosClient) CancelListener(dataId, group string) error {
	n.mu.RLock()
	client := n.configClient
	n.mu.RUnlock()

	if client == nil {
		logger.Warn("Nacos ConfigClient 尚未创建，无法取消监听", "dataId", dataId, "group", group)
		return nil // ConfigClient 都没了，监听自然不存在，视为成功
	}

	if err := client.CancelListenConfig(vo.ConfigParam{
		DataId: dataId,
		Group:  group,
	}); err != nil {
		logger.Warn("Nacos CancelListener 取消监听失败",
			"error", err, "dataId", dataId, "group", group)
		return myerrors.ErrNacosPull(dataId, fmt.Errorf("取消 Nacos 监听失败: %w", err))
	}
	return nil
}

// Close 关闭 Nacos 连接，释放 SDK ConfigClient 资源
// 幂等：多次调用只执行一次
func (n *NacosClient) Close() error {
	n.stopOnce.Do(func() {
		close(n.stopCh) // 通知后台 reconnectLoop 退出

		n.mu.Lock()
		client := n.configClient
		n.configClient = nil
		n.mu.Unlock()

		if client != nil {
			client.CloseClient()
		}
	})

	// Nacos SDK 的 CloseClient 不返回 error，这里返回 nil
	return nil
}

// 确保实现 ConfigCenter 接口
var _ iface.ConfigCenter = (*NacosClient)(nil)
