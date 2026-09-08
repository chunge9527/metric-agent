package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/common/filekit"
	"metric-agent/internal/common/logger"
	"metric-agent/internal/model"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// ScheduleService 定时任务服务
type ScheduleService struct {
	vmURL      string
	vmHeaders  []model.HeaderKV
	configPath string
	cron       *cron.Cron
	httpClient *http.Client

	mu    sync.RWMutex
	tasks model.ScheduleConfigList
	once  sync.Once

	// taskMu 保证多个任务按配置顺序串行执行（PRD 3.6）
	taskMu sync.Mutex

	// fileMu 保护输出文件的写入和滚动操作
	// executeTask→writeTaskOutput 和 dailyRotateLoop→rotateAllOutputFiles
	// 都调 checkAndRotate，两者不在 taskMu 保护下，需要独立互斥锁
	fileMu sync.Mutex

	// dailyStopCh 每日0点滚动定时器停止信号
	dailyStopCh chan struct{}
	// dailyWg 等待 dailyRotateLoop 协程退出；Stop 时 close channel 后需 Wait 确保真正收敛
	dailyWg sync.WaitGroup
}

// NewScheduleService 创建定时任务服务
// vmHeaders 为VictoriaMetrics请求头列表，value支持环境变量引用（如 ${VM_AUTH_HEADER}）
// 兼容旧authHeader字段：若headers为空且authHeader非空，自动转为单条headers
func NewScheduleService(vmURL string, vmHeaders []model.HeaderKV, configPath string) *ScheduleService {
	// 解析每个header value中的环境变量引用
	resolved := make([]model.HeaderKV, 0, len(vmHeaders))
	for _, h := range vmHeaders {
		resolved = append(resolved, model.HeaderKV{
			Key:   h.Key,
			Value: resolveEnvVars(h.Value),
		})
	}

	return &ScheduleService{
		vmURL:      vmURL,
		vmHeaders:  resolved,
		configPath: configPath,
		// http.Client 不设置全局 Timeout，超时由每个任务的 context deadline 控制
		// （executeQuery 中 http.NewRequestWithContext 会传入任务级 timeout）
		httpClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     60 * time.Second,
			},
		},
		dailyStopCh: make(chan struct{}),
	}
}

// envVarRegex 匹配 ${VAR_NAME} 格式的环境变量引用
var envVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

// resolveEnvVars 解析字符串中的 ${VAR_NAME} 环境变量引用
func resolveEnvVars(s string) string {
	return envVarRegex.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1] // strip ${ and }
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		logger.Warn("环境变量未设置", "varName", varName, "match", match)
		return match // 保留原字符串
	})
}

// LoadTasks 加载定时任务配置
func (s *ScheduleService) LoadTasks() error {
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		return fmt.Errorf("读取定时任务配置失败: %w", err)
	}

	var tasks model.ScheduleConfigList
	if err := yaml.Unmarshal(data, &tasks); err != nil {
		return fmt.Errorf("解析定时任务配置失败: %w", err)
	}

	s.mu.Lock()
	s.tasks = tasks
	s.mu.Unlock()

	logger.Info("定时任务配置加载成功", "count", len(tasks))
	return nil
}

// Start 启动定时任务调度器
func (s *ScheduleService) Start() error {
	s.cron = cron.New(
		cron.WithParser(cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow)),
		cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		),
	)

	s.mu.RLock()
	tasks := s.tasks
	s.mu.RUnlock()

	for _, task := range tasks {
		task := task
		_, err := s.cron.AddFunc(task.Cron, func() {
			s.executeTask(task)
		})
		if err != nil {
			logger.Error("注册定时任务失败", "name", task.Name, "cron", task.Cron, "error", err)
			continue
		}
		logger.Info("定时任务已注册", "name", task.Name, "cron", task.Cron)
	}

	s.cron.Start()

	// 启动每日0点文件滚动定时器
	s.dailyWg.Add(1)
	go s.dailyRotateLoop()

	logger.Info("定时任务调度服务已启动，执行时间间隔1分钟，共注册任务数", "count", len(tasks))
	return nil
}

// Stop 停止调度器
func (s *ScheduleService) Stop() {
	s.once.Do(func() {
		// 停止每日0点滚动定时器
		close(s.dailyStopCh)
		s.dailyWg.Wait() // 等待 dailyRotateLoop 真正退出

		// 关闭 httpClient 的空闲连接，避免 TIME_WAIT 残留
		if s.httpClient != nil {
			s.httpClient.CloseIdleConnections()
		}

		if s.cron != nil {
			ctx := s.cron.Stop()
			<-ctx.Done()
		}
	})
	logger.Info("定时任务调度器已停止")
}

// dailyRotateLoop 每日0点触发所有输出文件滚动
// P2-2 修复：safeRotate 包装 defer recover，一次 panic 不杀死 goroutine
func (s *ScheduleService) dailyRotateLoop() {
	defer s.dailyWg.Done() // 确保 Stop 时 Wait 能等到协程退出

	for {
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		duration := time.Until(nextMidnight)

		timer := time.NewTimer(duration)
		select {
		case <-s.dailyStopCh:
			timer.Stop()
			return
		case <-timer.C:
			s.safeRotate()
		}
	}
}

// safeRotate 带 panic recover 的 rotateAllOutputFiles 包装（P2-2 修复）
func (s *ScheduleService) safeRotate() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("每日文件滚动 panic 已捕获，本轮跳过，下次继续", "panic", fmt.Sprint(r))
		}
	}()
	s.rotateAllOutputFiles()
}

// rotateAllOutputFiles 滚动所有定时任务的输出文件
func (s *ScheduleService) rotateAllOutputFiles() {
	// fileMu 保护滚动循环，与 writeTaskOutput 的 fileMu 互斥
	// 每日0点触发，阻塞等待即可（一天一次，可接受）
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	s.mu.RLock()
	tasks := s.tasks
	s.mu.RUnlock()

	for _, task := range tasks {
		if task.Output.Path != "" {
			s.checkAndRotate(task.Output.Path, task.Output.MaxFile)
		}
	}
	logger.Info("每日0点文件滚动完成")
}

// executeTask 执行单个定时任务
// 通过taskMu互斥锁保证多个任务串行执行（PRD 3.6：按配置顺序串行执行）
// 采用TryLock跳过策略：若上一轮任务未完成则跳过本轮，避免goroutine堆积
// 每次任务执行生成统一queryTime，所有PromQL查询共用，执行完统一写一条JSON
func (s *ScheduleService) executeTask(task model.ScheduleConfig) {
	// TryLock语义：上一轮未完成则跳过本轮，避免goroutine堆积
	if !s.taskMu.TryLock() {
		logger.Warn("上一轮任务未完成，跳过本轮", "name", task.Name, "cron", task.Cron)
		return
	}
	defer s.taskMu.Unlock()

	startTime := time.Now()
	// 任务级统一queryTime：所有PromQL查询共用这个时间戳
	queryTime := startTime.Format("20060102150405")
	unixTime := startTime.Unix()

	timeout := task.Timeout
	if timeout <= 0 {
		timeout = myconstant.DefaultScheduleTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// 任务级汇总结构：queryKey → 样本值数组
	taskOutput := model.TaskQueryOutput{
		QueryTime: queryTime,
		Result:    make(map[string][]float64),
	}

	failedCount := 0
	for _, query := range task.QueryConfigs.Queries {
		result, err := s.executeQuery(ctx, query, unixTime)
		if err != nil {
			failedCount++
			logger.Error("定时任务查询执行失败",
				"queryTime", queryTime,
				"name", task.Name,
				"queryKey", query.QueryKey,
				"promql", query.PromQL,
				"error", err,
			)
			continue
		}
		logger.Info("定时任务查询执行成功",
			"queryTime", queryTime,
			"name", task.Name,
			"queryKey", query.QueryKey,
			"promql", query.PromQL,
			"valueCount", len(result.Values),
		)
		// 汇总到任务输出
		taskOutput.Result[result.QueryKey] = result.Values
	}

	// 统一写一次（任务级汇总JSON）
	if len(taskOutput.Result) > 0 {
		if err := s.writeTaskOutput(task.Output.Path, task.Output.MaxFile, task.Output.WriteMode, taskOutput); err != nil {
			logger.Error("写入任务结果文件失败",
				"queryTime", queryTime,
				"name", task.Name,
				"path", task.Output.Path,
				"error", err,
			)
		}
	}

	// 任务级执行结果日志
	logFields := []interface{}{
		"queryTime", queryTime,
		"name", task.Name,
		"total", len(task.QueryConfigs.Queries),
		"success", len(taskOutput.Result),
		"failed", failedCount,
		"cost", time.Since(startTime).String(),
	}
	if failedCount > 0 {
		logger.Error("定时任务执行失败（存在失败查询）", logFields...)
	} else {
		logger.Info("定时任务执行成功", logFields...)
	}
}

// executeQuery 执行单个PromQL查询
// unixTime 为任务级统一的Unix时间戳，作为 /api/v1/query 的 time 参数
// 返回提取后的样本值数组（float64），而非完整 Prometheus 响应
func (s *ScheduleService) executeQuery(ctx context.Context, query model.QueryConfig, unixTime int64) (model.MetricQueryResult, error) {
	result := model.MetricQueryResult{
		QueryKey: query.QueryKey,
		Values:   []float64{},
	}

	reqURL := fmt.Sprintf("%s/api/v1/query?query=%s&time=%d", s.vmURL, url.QueryEscape(query.PromQL), unixTime)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return result, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置所有配置的请求头（支持多个自定义头，如Authorization）
	for _, h := range s.vmHeaders {
		if h.Key != "" && h.Value != "" {
			req.Header.Set(h.Key, h.Value)
		}
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("请求VictoriaMetrics失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("VictoriaMetrics返回非200状态: %d", resp.StatusCode)
	}

	// 解析 Prometheus/VictoriaMetrics /api/v1/query 响应
	// result 每项格式: {"metric": {...}, "value": [timestamp, "样本值"]}
	var vmResp struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				// 匹配 "value": [1756913400, "0.523"]
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&vmResp); err != nil {
		return result, fmt.Errorf("解析VictoriaMetrics响应失败: %w", err)
	}

	// 提取每个 result 的 value[1]（样本值字符串 → float64）
	for _, item := range vmResp.Data.Result {
		if len(item.Value) < 2 {
			continue
		}
		// value[1] 是样本值（string 类型，如 "0.523"、"1"）
		if strVal, ok := item.Value[1].(string); ok {
			if f, err := strconv.ParseFloat(strVal, 64); err == nil {
				result.Values = append(result.Values, f)
			}
		}
	}

	return result, nil
}

// writeTaskOutput 写入任务汇总结果到文件
// 每次任务执行写一条JSON（JSON Lines格式，末尾换行），超过maxFileMB触发滚动
func (s *ScheduleService) writeTaskOutput(outputPath string, maxFileMB int, writeMode string, output model.TaskQueryOutput) error {
	// fileMu 保护整个 write+checkAndRotate 序列，防止与 dailyRotateLoop 并发导致竞态
	s.fileMu.Lock()
	defer s.fileMu.Unlock()

	dir := filepath.Dir(outputPath)
	if err := filekit.EnsureDir(dir, myconstant.DefaultDirPerm); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	data, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("序列化结果失败: %w", err)
	}

	// 每条任务执行结果一行（JSON Lines格式），末尾追加换行符
	data = append(data, '\n')

	// 根据写模式决定open flag
	var flag int
	if writeMode == "overwrite" {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else {
		// 默认追加模式
		flag = os.O_APPEND | os.O_CREATE | os.O_WRONLY
	}

	f, err := os.OpenFile(outputPath, flag, myconstant.DefaultFilePerm)
	if err != nil {
		return fmt.Errorf("打开输出文件失败: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("写入输出文件失败: %w", err)
	}

	// 写入后检查文件大小，超过maxFileMB触发滚动
	if maxFileMB > 0 {
		s.checkAndRotate(outputPath, maxFileMB)
	}

	return nil
}

// MaxRotateIndex 滚动文件最大序号
// 限制序号扫描范围（PathExists 次数上限），同时限制磁盘滚动文件数量
const MaxRotateIndex = 99

// checkAndRotate 检查并执行文件滚动
// 超过maxFileMB时，将当前文件重命名为 {原文件名}_{NN}.json（从01开始递增，已存在则02、03...）
// 然后创建新的空文件继续写入
func (s *ScheduleService) checkAndRotate(filePath string, maxFileMB int) {
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}

	maxSize := int64(maxFileMB) * 1024 * 1024
	if info.Size() < maxSize {
		return
	}

	// 计算目标序号文件名：metricas.json → metricas_01.json
	dir := filepath.Dir(filePath)
	ext := filepath.Ext(filePath)                            // .json
	base := strings.TrimSuffix(filepath.Base(filePath), ext) // metricas

	// 从01开始递增找第一个不存在的序号
	// 序号语义：_01 最旧，序号越大越新（新滚动文件取当前最大序号+1）
	idx := 1
	found := false
	for idx <= MaxRotateIndex {
		// metricas_01.json 格式：2位补零
		rotatedPath := filepath.Join(dir, fmt.Sprintf("%s_%02d%s", base, idx, ext))
		if !filekit.PathExists(rotatedPath) {
			found = true
			break
		}
		idx++
	}

	if !found {
		// 达最大序号：删除最旧文件 _01，_02.._99 依次前移一位，当前文件滚动到 _99
		// 注意：_01 才是最旧（分配时从空位的最大序号+1取号），不能删 _99
		oldestPath := filepath.Join(dir, fmt.Sprintf("%s_%02d%s", base, 1, ext))
		if err := os.Remove(oldestPath); err != nil {
			logger.Error("删除最旧滚动文件失败", "path", oldestPath, "error", err)
			return
		}
		for i := 2; i <= MaxRotateIndex; i++ {
			srcPath := filepath.Join(dir, fmt.Sprintf("%s_%02d%s", base, i, ext))
			dstPath := filepath.Join(dir, fmt.Sprintf("%s_%02d%s", base, i-1, ext))
			if err := os.Rename(srcPath, dstPath); err != nil {
				// 前移失败时中止：避免序号空洞导致后续覆盖错乱
				logger.Error("滚动文件前移失败", "from", srcPath, "to", dstPath, "error", err)
				return
			}
		}
		idx = MaxRotateIndex
	}

	// 找到空位或腾出最旧文件后执行滚动
	rotatedPath := filepath.Join(dir, fmt.Sprintf("%s_%02d%s", base, idx, ext))
	logger.Info("文件滚动", "source", filePath, "target", rotatedPath, "sizeMB", info.Size()/1024/1024)
	if err := os.Rename(filePath, rotatedPath); err != nil {
		logger.Error("文件滚动失败", "error", err)
		return
	}
	// 创建新的空文件
	f, err := os.Create(filePath)
	if err != nil {
		logger.Error("创建新文件失败", "error", err)
		return
	}
	f.Close()
}
