### 需求1. 【项目框架初始化】提示词

```
基于 Go 1.25.0 初始化 MetricAgent 项目框架，完成以下操作：
1. 在当前目录生成 go.mod 文件，模块名：metric-agent
2. 严格锁定以下第三方依赖版本，禁止引入任何额外依赖：
   - github.com/nacos-group/nacos-sdk-go/v2 v2.3.5
   - gopkg.in/yaml.v3 v3.0.1
   - github.com/robfig/cron/v3 v3.0.1
   - gopkg.in/natefinch/lumberjack.v2 v2.0.0
3. 创建完整的项目目录结构，路径如下：
   cmd/metric-agent/
   internal/bootstrap/
   internal/service/
   internal/infra/iface/
   internal/infra/nacos_client/
   internal/infra/shell_exec/
   internal/infra/crypto/
   internal/model/
   internal/common/logger/
   internal/common/filekit/
   internal/common/constant/
   internal/common/errors/
4. 每个目录下生成占位的 .go 文件，包名与目录名一致，确保项目可正常执行 go mod tidy
5. 生成完成后自行执行 go mod tidy 校验依赖，修复所有编译错误
6. 如有不明确的地方请先提问再执行

```


***

### 需求2. 【公共层 - 常量与错误定义】提示词

```
实现 internal/common/constant/constants.go 和 internal/common/errors/errors.go
技术栈：Go 1.25.0，无第三方依赖

功能要求：
1. constants.go：定义全局常量与默认值
   - 默认HTTP监听地址、默认配置文件路径、默认日志路径
   - 各类超时默认值：远程命令60s、配置重载60s、健康检查60s、启动脚本120s、透传30s、定时任务30s
   - Nacos重连间隔：启动阶段60s、运行阶段30s
   - 守护巡检周期1分钟、熔断失败阈值3次、熔断周期5分钟、健康检查重试间隔5s
   - 日志默认单文件100MB、保留5个历史文件
   - 配置备份文件后缀、临时文件前缀
   所有常量命名规范，导出常量必须有文档注释

2. errors.go：定义统一错误类型
   - 通用错误分类：参数错误、配置错误、网络错误、执行错误
   - 错误码与错误信息一一对应
   - 支持错误包装，实现 Is、Unwrap 方法

代码规范：
- 包名分别为 constant、errors
- 遵循 Go 官方代码规范
- 禁止 panic，所有错误通过返回值传递

生成要求：
- 代码可直接编译通过
- 自行检查语法错误并修复
- 如有不明确的逻辑请先提问

```


***

### 需求3. 【公共层 - 文件工具集】提示词

```
实现 internal/common/filekit/atomic_file.go
技术栈：Go 1.25.0，仅使用标准库

功能要求：
实现文件操作工具集，包含以下导出函数：
1. AtomicWrite(filePath string, data []byte, perm os.FileMode) error
   - 先写入同目录下 .tmp_ 前缀的临时文件
   - 写入完成后通过 os.Rename 原子替换目标文件
   - 写入失败自动清理临时文件

2. BackupFile(filePath string) error
   - 将目标文件重命名为 {原文件名}_agent_bak
   - 备份文件与原文件同目录，已存在备份则直接覆盖
   - 原文件不存在则返回 nil 不报错

3. RestoreBackup(filePath string) error
   - 将备份文件恢复为原文件名
   - 备份不存在则返回明确错误
   - 恢复失败不破坏原备份文件

4. PathExists(path string) bool：判断文件/目录是否存在
5. EnsureDir(dirPath string, perm os.FileMode) error：确保目录存在，不存在则创建

代码规范：
- 包名 filekit
- 所有错误都明确返回，禁止 panic
- 导出函数必须有文档注释
- 处理所有边界情况与错误场景

生成要求：
- 代码可直接编译通过
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求4. 【公共层 - 结构化日志】提示词

```
实现 internal/common/logger/logger.go
技术栈：Go 1.25.0
依赖：标准库 log/slog、gopkg.in/natefinch/lumberjack.v2

功能要求：
1. 封装全局结构化 JSON 日志，支持 INFO、WARN、ERROR 三个级别
2. 提供两种初始化函数：
   - InitConsoleLogger()：仅输出到控制台，用于指令执行模式
   - InitFileLogger(logPath string)：同时输出到控制台和日志文件，用于HTTP服务模式
3. 文件日志支持轮转：单文件默认100MB，保留5个历史文件
4. 提供包级导出方法：Info(msg string, args ...any)、Warn(msg string, args ...any)、Error(msg string, args ...any)
5. 日志固定包含时间、级别、消息字段，结构化输出

代码规范：
- 包名 logger
- 初始化失败返回 error，禁止 panic
- 遵循 slog 标准使用规范

生成要求：
- 代码可直接编译
- 自行检查依赖引用与语法错误并修复
- 如有不明确的地方请先提问

```


***

### 需求5. 【数据模型层】提示词

```
实现 internal/model 下全部数据模型文件：
agent_config.go、metric_config.go、guardian_config.go、schedule_config.go、api_model.go
技术栈：Go 1.25.0
依赖：gopkg.in/yaml.v3

功能要求：
1. agent_config.go：基础配置结构体，对应 metricAgent.yml
   - 字段：AgentID、Nacos配置（地址、命名空间、分组）、Shell加密配置、配置重载超时、Crontab配置、透传超时、VictoriaMetrics配置（地址、鉴权头）、鉴权密钥
   - yaml 标签与 PRD 配置字段完全对应，支持嵌套结构

2. metric_config.go：二级配置清单结构体
   - 数组结构，每个元素包含 fileName、storePath、reFileName、reloadScript
   - 对应 metricFileConfig.yml 结构

3. guardian_config.go：守护配置结构体
   - 数组结构，每个元素包含 componentName、ips数组、healthCheckScript、startScript
   - 对应 crontab.yml 结构

4. schedule_config.go：定时任务配置结构体
   - 数组结构，每个任务包含 name、cron、queryConfigs（queries数组，每个含queryKey、promql）、output（path、maxFile）
   - 对应 scheduledConfig.yml 结构

5. api_model.go：HTTP接口数据模型
   - ExecRequest：Script、Timeout 字段
   - ExecResponse：Stdout、Stderr、ExitCode、Duration 字段
   - HealthResponse：Status、AgentID、Timestamp 字段

代码规范：
- 所有结构体字段首字母大写，yaml 标签准确
- 包名 model
- 导出类型必须有文档注释

生成要求：
- 所有结构体严格对齐 PRD 配置字段
- 代码可直接编译
- 自行检查语法错误并修复
- 如有字段不明确请先提问

```


***

### 需求6. 【基础设施接口层】提示词

```
实现 internal/infra/iface 下所有抽象接口：
config_center.go、shell_executor.go、crypto.go
技术栈：Go 1.25.0，仅使用标准库

功能要求：
1. config_center.go：定义配置中心接口 ConfigCenter
   - GetConfig(dataId string) (string, error)：拉取指定 dataId 的配置内容
   - ListenConfig(dataId string, onChange func(content string)) error：监听配置变更，变更时触发回调
   - RemoveListen(dataId string) error：移除配置监听
   - Close() error：关闭连接释放资源
   - 所有方法都有清晰的文档注释

2. shell_executor.go：定义Shell执行器接口 ShellExecutor
   - Exec(ctx context.Context, script string) (stdout string, stderr string, exitCode int, err error)
   - 支持通过 context 控制执行超时

3. crypto.go：定义加解密接口 Crypto
   - Encrypt(plaintext []byte) ([]byte, error)：加密
   - Decrypt(ciphertext []byte) ([]byte, error)：解密

代码规范：
- 包名 iface
- 接口定义遵循依赖倒置原则，只定义行为不涉及实现细节

生成要求：
- 代码可直接编译
- 自行检查语法错误
- 如有不明确的地方请先提问

```


***

### 需求7. 【基础设施层 - Nacos 客户端实现】提示词

```
实现 internal/infra/nacos_client/client.go，完整实现 infra/iface.ConfigCenter 接口
技术栈：Go 1.25.0
依赖：github.com/nacos-group/nacos-sdk-go/v2、internal/model、internal/common/logger、internal/common/constant

功能要求：
1. 结构体 NacosClient，包含 nacos 客户端实例、命名空间、分组、日志实例等字段
2. 提供 NewNacosClient(cfg model.NacosConfig) (*NacosClient, error) 构造函数
3. 完整实现 ConfigCenter 接口的四个方法：
   - GetConfig：从 Nacos 拉取指定 dataId 的配置内容
   - ListenConfig：注册配置变更监听，配置变更时调用回调函数
   - RemoveListen：移除指定 dataId 的监听
   - Close：关闭客户端连接
4. 重连策略：
   - 初始化时连接失败，记录错误日志，不返回错误，后台按 60s 间隔重试
   - 运行中断连自动按 30s 间隔重试
5. 适配 nacos-server-2.5.3，使用 gRPC 通信
6. 所有异常都记录详细错误日志，不 panic

代码规范：
- 包名 nacos_client
- 错误处理完善，所有方法返回明确错误
- 遵循 Nacos SDK 官方使用规范

生成要求：
- 代码可直接编译通过
- 自行检查依赖引用、语法错误并修复
- 如有 API 不明确的地方请先提问

```


***

### 需求8. 【基础设施层 - Shell 执行器实现】提示词

```
实现 internal/infra/shell_exec/executor.go，完整实现 infra/iface.ShellExecutor 接口
技术栈：Go 1.25.0
依赖：标准库 os/exec、context、internal/common/logger

功能要求：
1. 结构体 ShellExecutor，实现 ShellExecutor 接口
2. 提供 NewShellExecutor() *ShellExecutor 构造函数
3. Exec 方法核心逻辑：
   - 使用 bash -c 执行传入的脚本字符串
   - 基于 exec.CommandContext 实现，通过 context 控制超时
   - 捕获标准输出、标准错误，获取进程退出码
   - 超时自动终止子进程，避免僵尸进程
   - 执行权限与工作目录和当前进程一致
4. 异常场景处理：脚本执行失败、超时、权限不足等，都返回明确错误
5. 执行异常记录错误日志

代码规范：
- 包名 shell_exec
- 禁止 panic，所有错误通过返回值返回
- 正确区分执行错误与脚本非0退出码

生成要求：
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求9. 【基础设施层 - AES 加密实现】提示词

```
实现 internal/infra/crypto/aes.go，完整实现 infra/iface.Crypto 接口
技术栈：Go 1.25.0
依赖：标准库 crypto/aes、crypto/cipher、bytes

功能要求：
1. 结构体 AESCrypto，包含加密密钥
2. 提供 NewAESCrypto(key string) (*AESCrypto, error) 构造函数，校验密钥长度（支持16/24/32字节）
3. 实现 Crypto 接口的两个方法：
   - Encrypt：采用 AES-CBC 模式，PKCS7 填充，加密结果前附加 IV
   - Decrypt：从密文前提取 IV，解密并去除填充
4. 密钥非法、加解密失败都返回明确错误

代码规范：
- 包名 crypto
- 禁止 panic，错误明确返回
- 遵循密码学最佳实践

生成要求：
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求10. 【核心服务层 - 鉴权中间件】提示词

```
实现 internal/service/auth_middleware.go
技术栈：Go 1.25.0
依赖：标准库 net/http、encoding/json、internal/common/logger

功能要求：
1. 实现 HTTP 鉴权中间件函数：
   func AuthMiddleware(authKey string, next http.Handler) http.Handler
2. 鉴权规则：
   - 从请求头读取 Authentication 和 CIB-AUTHORIZATION 两个字段
   - 与配置的 authKey 进行大小写敏感的精确匹配
   - 任意一个请求头匹配成功即鉴权通过，放行请求
   - 都不匹配则返回 401 Unauthorized，返回 JSON 格式错误信息
3. 所有 HTTP 接口强制鉴权，无豁免
4. 鉴权失败记录 WARN 日志

代码规范：
- 包名 service
- 遵循标准 net/http 中间件写法
- 错误响应格式统一

生成要求：
- 代码可直接编译
- 自行检查语法错误并修复
- 如有不明确的地方请先提问

```


***

### 需求11. 【核心服务层 - 配置管理服务】提示词

```
实现 internal/service/config_service.go
技术栈：Go 1.25.0
依赖：internal/infra/iface、internal/model、internal/common/filekit、internal/common/logger、internal/common/constant

功能要求：
1. 结构体 ConfigService，依赖 ConfigCenter 接口、ShellExecutor 接口
2. 提供 NewConfigService(cc iface.ConfigCenter, se iface.ShellExecutor, agentID string) *ConfigService 构造函数
3. 核心方法：
   a. LoadConfigList() ([]model.MetricConfig, error)
      - 优先拉取 metricFileConfig-{agent_id}.yml 个性化配置
      - 个性化配置不存在/拉取失败则拉取公共配置 metricFileConfig.yml 兜底
   
   b. DistributeAllConfigs(configs []model.MetricConfig)
      - 串行依次处理所有配置项
      - 单条配置执行流程：拉取配置 -> 写入临时文件 -> 备份原文件 -> 原子重命名 -> 执行重载脚本
      - 单步骤失败立即重试1次
      - 重试仍失败：记录错误日志 -> 恢复备份 -> 跳过当前项，继续执行下一条
   
   c. StartConfigListen() error
      - 监听当前生效的配置清单
      - 配置变更时自动重新拉取清单并全量分发
      - 动态更新二级配置项的监听列表

4. 原子写、备份、回滚逻辑复用 filekit 工具
5. 所有异常都记录详细错误日志，携带配置项信息
6. 单条配置失败不影响其他配置，不中断主进程

代码规范：
- 包名 service
- 禁止 panic，错误处理完善
- 串行执行，流程清晰

生成要求：
- 严格对齐 PRD 配置分发逻辑
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求12. 【核心服务层 - 指令执行服务】提示词

```
实现 internal/service/command_service.go
技术栈：Go 1.25.0
依赖：internal/infra/iface、internal/model、internal/common/logger、internal/common/constant、标准库 net/http

功能要求：
1. 结构体 CommandService，依赖 ShellExecutor 接口、Crypto 接口
2. 提供 NewCommandService(se iface.ShellExecutor, crypto iface.Crypto) *CommandService 构造函数
3. HTTP 远程命令执行方法：
   func (s *CommandService) ExecHandler(w http.ResponseWriter, r *http.Request)
   - 读取请求体，AES 解密得到 ExecRequest
   - 参数校验：拒绝空脚本、纯空白脚本；超时默认60s，最大600s，非法返回400
   - 调用 ShellExecutor 执行脚本，统计执行耗时
   - 封装 ExecResponse，AES 加密后返回
   - 超时返回 408 错误

4. 本地指令执行方法：
   func (s *CommandService) RunLocalExec(script string) int
   - 同步执行脚本
   - 标准输出打印到 stdout，错误打印到 stderr
   - 返回脚本退出码

5. 所有异常记录错误日志

代码规范：
- 包名 service
- HTTP 处理遵循标准库规范
- 错误处理完善

生成要求：
- 严格对齐 PRD 两种执行模式逻辑
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求13. 【核心服务层 - 进程守护服务】提示词

```
实现 internal/service/guardian_service.go
技术栈：Go 1.25.0
依赖：internal/infra/iface、internal/model、internal/common/logger、internal/common/constant、标准库 net、gopkg.in/yaml.v3

功能要求：
1. 结构体 GuardianService，依赖 ShellExecutor 接口
2. 提供 NewGuardianService(se iface.ShellExecutor, configPath string) *GuardianService 构造函数
3. 核心方法：
   a. Start()：启动守护巡检协程，每分钟执行一次
   b. Stop()：停止守护巡检
4. 核心逻辑：
   - 调度防重叠：上一轮未完成则跳过本轮，记录 WARN 日志
   - 配置加载：每次巡检读取 crontab.yml，解析失败沿用上次有效配置
   - IP匹配：获取本机所有网卡IP，与组件 ips 数组精确匹配，命中则执行守护逻辑
   - 健康检查：执行 healthCheckScript，退出码0判定存活；默认超时60s，超时判定失败
   - 自愈逻辑：组件异常执行 startScript，默认超时120s；拉起后间隔5s重试健康检查（默认1次）
   - 熔断机制：连续3次拉起失败进入5分钟熔断期；熔断期仅做检查不拉起；熔断结束自动重置计数
   - 每个组件独立计数，互不影响

5. 采用状态机模式管理熔断状态，逻辑清晰
6. 所有异常记录错误日志，携带组件名
7. 后台异步运行，不阻塞主进程

代码规范：
- 包名 service
- 禁止 panic，错误处理完善
- 状态流转逻辑清晰

生成要求：
- 严格对齐 PRD 守护与熔断规则
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求14. 【核心服务层 - 请求透传服务】提示词

```
实现 internal/service/forward_service.go
技术栈：Go 1.25.0
依赖：标准库 net/http、net/url、io、internal/common/logger、internal/common/constant

功能要求：
1. 结构体 ForwardService，可配置默认超时
2. 提供 NewForwardService(timeout time.Duration) *ForwardService 构造函数
3. HTTP 透传处理方法：
   func (s *ForwardService) ForwardHandler(w http.ResponseWriter, r *http.Request)
   - 从查询参数获取 target，URL 解码
   - 地址合法性校验：仅允许 http/https 协议；禁止解析出多个IP；不合法返回403
   - 构建转发请求：完整保留原始请求方法、请求头、Cookie、请求体；移除 hop-by-hop 头
   - 转发地址规则：target基础URL + 原始请求路径 + 查询参数；target自带参数优先级更高
   - 禁止自动跟随重定向，目标返回3xx直接透传给客户端
   - 超时控制：默认30s，最大120s
   - 原样返回目标服务的状态码、响应头、响应体

4. 异常处理：
   - 目标不可达返回 502
   - 请求超时返回 504
   - 地址非法返回 403

代码规范：
- 包名 service
- 遵循标准 HTTP 代理实现规范
- 错误处理完善

生成要求：
- 严格对齐 PRD 透传规则
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求15. 【核心服务层 - 定时任务服务】提示词

```
实现 internal/service/schedule_service.go
技术栈：Go 1.25.0
依赖：github.com/robfig/cron/v3、internal/model、internal/common/logger、internal/common/filekit、internal/common/constant、标准库 net/http、encoding/json

功能要求：
1. 结构体 ScheduleService，包含 cron 调度器、VM 配置、任务列表
2. 提供 NewScheduleService(vmURL string, vmAuthHeader string, configPath string) *ScheduleService 构造函数
3. 核心方法：
   a. LoadTasks() error：加载 scheduledConfig.yml 配置，解析任务列表
   b. Start() error：启动调度器，注册所有任务，开启 panic 恢复
   c. Stop()：停止调度器，等待运行中任务完成
4. 任务执行逻辑：
   - 按配置串行执行多个 PromQL 查询
   - 携带配置的 Authorization 请求头访问 VictoriaMetrics
   - 格式化结果为指定 JSON 结构：{"queryTime":"YYYYMMDDHHmmss","queryKey":"xxx","data":[...]}
   - 追加写入目标文件
5. 文件滚动机制：
   - 超过 maxFile 大小或每日 00:00 触发滚动
   - 重命名当前文件为 metricdata.txt_yyyyMMdd，新建文件写入

6. 单个查询失败、文件写入失败记录错误日志，不影响其他查询与任务
7. 任务执行超时默认 30s

代码规范：
- 包名 service
- 禁止 panic，错误处理完善
- Cron 使用标准表达式

生成要求：
- 严格对齐 PRD 定时任务与文件滚动规则
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求16. 【启动引导层 - 配置加载器】提示词

```
实现 internal/bootstrap/config_loader.go
技术栈：Go 1.25.0
依赖：gopkg.in/yaml.v3、internal/model、internal/common/constant、标准库 os、path/filepath、regexp

功能要求：
1. 函数：LoadBaseConfig(configPath string) (*model.AgentConfig, error)
2. 路径处理：
   - 相对路径以二进制可执行文件所在目录为基准
   - 支持绝对路径
3. 读取配置文件，YAML 解析为 AgentConfig 结构体
4. 配置校验：
   - agent_id 为必填字段，仅允许字母、数字、下划线、横线，正则校验
   - 必填字段缺失返回明确错误信息
5. 默认值填充：补全所有超时、端口等配置的默认值
6. 异常处理：
   - 文件不存在返回路径错误
   - 格式错误返回具体错误项

代码规范：
- 包名 bootstrap
- 错误信息清晰明确，便于定位问题
- 禁止 panic

生成要求：
- 严格对齐 PRD 配置校验规则
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求17. 【启动引导层 - 服务初始化器】提示词

```
实现 internal/bootstrap/server_init.go
技术栈：Go 1.25.0
依赖：internal/model、internal/service、internal/infra/iface、internal/infra/nacos_client、internal/infra/shell_exec、internal/infra/crypto、internal/common/logger、标准库 net/http

功能要求：
1. 函数：InitServer(cfg *model.AgentConfig, bindAddr string) (*http.Server, func(), error)
2. 依赖注入与初始化顺序：
   a. 初始化基础设施层：Nacos 客户端、Shell 执行器、AES 加密工具
   b. 初始化核心服务层：配置服务、指令服务、守护服务、透传服务、定时任务服务
   c. 拉取配置清单并执行全量分发
   d. 启动配置变更监听
   e. 启动进程守护协程
   f. 启动定时任务调度器
3. 注册 HTTP 路由：
   - GET /health：健康检查接口
   - POST /api/v1/exec：远程命令执行接口
   - 所有方法 /forward：请求透传接口
   - 所有路由都经过鉴权中间件
4. 创建并返回 HTTP Server 实例，以及资源清理函数

代码规范：
- 包名 bootstrap
- 初始化顺序清晰，依赖注入明确
- 错误处理完善

生成要求：
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求18. 【启动引导层 - 优雅关闭】提示词

```
实现 internal/bootstrap/shutdown.go
技术栈：Go 1.25.0
依赖：标准库 os、os/signal、syscall、context、time、internal/common/logger

功能要求：
1. 函数：WaitForShutdown(httpSrv *http.Server, stopFuncs ...func())
2. 监听 SIGINT、SIGTERM 系统信号
3. 收到信号后按顺序执行关闭：
   a. 关闭 HTTP 服务，设置 10s 超时，拒绝新请求，等待处理中请求完成
   b. 依次执行传入的 stop 函数（停止定时任务、关闭 Nacos 连接等）
   c. 记录关闭日志，正常退出
4. 设置 10 秒强制退出超时，超时未完成则强制退出
5. 正常关闭退出码 0，异常退出非 0

代码规范：
- 包名 bootstrap
- 关闭顺序合理，避免资源泄漏
- 禁止 panic

生成要求：
- 代码可直接编译
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问

```


***

### 需求19. 【主入口模块】提示词

```
实现 cmd/metric-agent/main.go
技术栈：Go 1.25.0
依赖：internal/bootstrap、internal/common/logger、internal/service、internal/infra/shell_exec、internal/common/constant、标准库 flag、os、io/ioutil

功能要求：
1. 命令行参数解析：
   - --help：最高优先级，输出完整用法说明后以 0 码退出
   - --exec：指令模式，脚本字符串
   - --exec-file：指令模式，脚本文件路径
   - --config：HTTP模式，配置文件路径，默认 ./metricAgent.yml
   - --bind-addr：HTTP模式，监听地址，默认 0.0.0.0:8082
   - --logs：HTTP模式，日志路径，默认 ./logs/metricAgent.log
   - 支持 -- 分隔符，其后所有内容原样作为脚本参数

2. 模式互斥校验：
   - 同时传入指令模式参数与 HTTP 模式参数，输出错误并以非 0 码退出
   - 存在 --exec 或 --exec-file 则为指令执行模式，否则默认 HTTP 服务模式

3. 指令执行模式流程：
   - 初始化控制台日志
   - 读取脚本内容（--exec 直接读取，--exec-file 读取文件）
   - 拼接 -- 分隔符后的参数
   - 调用 ShellExecutor 执行
   - 输出结果到控制台
   - 以脚本退出码退出

4. HTTP 服务模式流程：
   - 加载基础配置
   - 初始化文件日志
   - 初始化 HTTP 服务
   - 启动服务监听
   - 注册优雅关闭监听
   - 等待退出

5. 参数错误输出清晰的用法说明

代码规范：
- 包名 main
- 逻辑清晰，流程明确
- 错误处理完善

生成要求：
- 严格对齐 PRD 运行模式与参数规则
- 代码可直接编译运行
- 自行检查语法与逻辑错误并修复
- 如有不明确的地方请先提问
```


