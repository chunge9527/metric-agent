# MetricAgent

轻量级监控节点运维代理 — 为 VictoriaMetrics / Promxy / Grafana 等监控组件提供配置分发、生命周期管理、命令执行、请求转发、文件上传和定时任务能力。

---

## 目录

* [架构概览](#架构概览)

* [功能特性](#功能特性)

* [技术栈](#技术栈)

* [环境要求](#环境要求)

* [快速开始](#快速开始)

* [编译构建](#编译构建)

* [部署安装](#部署安装)

* [配置说明](#配置说明)

* [运行模式](#运行模式)

* [API 接口](#api-接口)

* [管理脚本](#管理脚本)

* [常见问题](#常见问题)

* [目录结构](#目录结构)

---

## 架构概览

```
                    ┌─────────────┐
                    │   Nacos     │
                    │  配置中心   │
                    └──────┬──────┘
                           │ 定时拉取 (PullInterval 分钟)
                    ┌──────▼──────┐
                    │ MetricAgent  │  默认 0.0.0.0:9092
                    └──────┬──────┘
              ┌────────────┼────────────┐──────────────┐
              │            │            │              │
       ┌──────▼──────┐ ┌──▼───┐ ┌──────▼──────┐ ┌─────▼─────┐
       │ 配置分发服务 │ │ 指令 │ │ 进程守护服务 │ │ 定时任务   │
       │ ConfigSvc   │ │ 执行 │ │ GuardianSvc │ │ ScheduleSvc│
       └──────┬──────┘ └──┬───┘ └──────┬──────┘ └─────┬─────┘
              │            │            │              │
              ▼            ▼            ▼              ▼
         原子写入         Shell       健康巡检       PromQL 查询
         _agent_bak       远程执行    熔断保护       JSON 输出
```


## 功能特性

| # | 功能        | 说明                                                                                              |
| - | --------- | ----------------------------------------------------------------------------------------------- |
| 1 | **配置分发**  | 从 Nacos 配置中心定时拉取 YAML 配置，原子性写入本地（`.tmp_` 前缀临时文件 + `os.Rename`），变更后自动执行重载脚本；支持备份回滚（`_agent_bak`） |
| 2 | **进程守护**  | 定时巡检监控组件健康状态（每分钟），异常时自动拉起，含熔断保护防止雪崩                                                             |
| 3 | **命令执行**  | 通过 HTTP API 远程执行 Shell 脚本，请求/响应均 AES-CBC + PKCS7 加密；Shell 输出单流上限 1MB 防 OOM |
| 4 | **请求透传**  | 内网代理转发（无 SSRF 防护，允许访问内网任意 http/https 服务，符合内网运维场景预期） |
| 5 | **文件上传**  | 通过 `/api/v1/upload` 上传文件到指定目录，支持覆盖备份、敏感路径校验、磁盘空间检查                                              |
| 6 | **定时任务**  | Cron 调度 PromQL 查询 VictoriaMetrics，结果写入 JSON 文件，支持文件自动滚动                                         |
| 7 | **优雅关闭**  | 捕获 SIGTERM/SIGINT，HTTP Shutdown 10s 超时自动 SetKeepAlivesEnabled(false) 阻止新连接；依次停止 Nacos、守护巡检、定时任务、日志        |
| 8 | **双模式运行** | 指令模式（CLI）和 HTTP 服务模式自动识别，无需特殊参数                                                                 |
| 9 | **特性开关**  | `feature.enableNacos / enableGuardian / enableSchedule` 独立控制各服务启停                               |

## 技术栈

* **语言**: Go 1.25.0

* **配置中心**: nacos-sdk-go/v2 v2.3.5

* **调度**: robfig/cron/v3 v3.0.1

* **日志**: log/slog + 自研 DailyRotator（本地时区午夜自动轮转 + cleaner 按 RetentionDays 清理历史文件）

* **加密**: AES-CBC + PKCS7（`crypto/aes` + `crypto/cipher`）

* **序列化**: gopkg.in/yaml.v3 v3.0.1

* **构建**: 纯静态编译 (CGO_ENABLED=0)

## 环境要求

| 项目    | 要求                                            |
| ----- | --------------------------------------------- |
| 操作系统  | Linux (glibc 2.17+) / Windows 10+ / macOS 11+ |
| Go 版本 | 1.25+（仅编译时需要）                                |
| Nacos | 2.x（配置中心服务端）                                  |
| 网络    | Agent 需能访问 Nacos 服务端（默认 8848）                 |
| 磁盘    | 日志目录 >= 100MB，配置存储目录 >= 50MB                  |
| 内存    | >= 64MB                                       |

## 快速开始

### 1. 克隆项目

```bash
git clone <repo-url>/metric-agent.git
cd metric-agent
```


### 2. 编译

```bash
# 编译 Linux amd64 版本
./deploy/build.sh linux

# 或直接用 Go 编译
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o metric-agent ./cmd/metric-agent/
```


### 3. 部署

```bash
# 将 dist/metric-agent-linux-amd64/ 上传到目标节点
scp dist/metric-agent-linux-amd64.tar.gz user@node:/tmp/

# 在目标节点上
cd /opt && tar xzf /tmp/metric-agent-linux-amd64.tar.gz
cd metric-agent
```


### 4. 主配置

编辑 `metricAgent.yml`（修改 agent.id、nacos.address、nacos.group、auth.key 等）：

```yaml
# MetricAgent 基础配置文件
# 对应 model.AgentConfig 结构体
# 部署时请根据实际环境修改以下配置

agent:
  # 节点唯一标识，必填，仅允许字母、数字、下划线、横线
  # agent.group 会自动从 nacos.group 回填，无需单独配置
  id: node-001
  
# 服务特性总开关，true启用，false禁用；未配置时默认全部true
feature:
  # Nacos配置中心和配置变更监听开关，关闭后不初始化nacos客户端、不监听配置变更
  enableNacos: true
  # 进程守护服务开关，关闭不启动进程守护、不加载crontab.yml
  enableGuardian: false
  # 定时任务调度服务开关，关闭不加载scheduledConfig.yml、不运行定时任务
  enableSchedule: false
  
# Nacos 配置中心连接信息
nacos:
  # Nacos 服务地址
  address: http://192.168.50.177:8848
  # 命名空间（public 填空字符串或 "public"）
  namespace: public
  # 分组（默认 DEFAULT_GROUP）；同时回填为 agent.group
  group: DEFAULT_GROUP
  # 鉴权账号密码（Nacos 控制台创建的账号，只读权限最佳）
  username: your_user
  password: pwd

  # 可选参数
  # 单次 Nacos http/gRPC 请求超时时间，单位毫秒（默认 5000ms）
  timeout: 5000
  # 是否在启动时不加载配置缓存，默认 true
  notLoadCacheAtStart: true

# 配置清单拉取、二级配置分发、配置清理
config:
  # 主动拉取间隔（分钟）；<0 时默认 1 分钟
  pullInterval: 1
  # 孤儿配置文件清理（PRD 3.2.4）
  cleanOrphanFile:
    # 是否开启配置清理，默认 false
    # 仅对按 group 批量拉取的配置生效
    enable: false
    # 定点执行清理时间（小时），支持多个时间点，1~23 有效
    cleanFixHour: [0, 2]
    # 需要清理的文件后缀（必填），默认 [".yml", ".yaml"]
    cleanSuffix: [".yml", ".yaml"]
  # 配置重载脚本超时（秒）
  reloadScript:
    timeout: 60

# Shell 远程命令加密配置
shell:
  encrypt:
    # AES-128加密密钥（16字节），用于远程命令请求/响应加密
    # 不建议修改默认值，会导致命令行模式无法正常工作
    # 不要修改长度，否则 JAVA 侧加密会比较麻烦
    key: "7sK9p2R5zG8tB4vN"

# 进程守护超时配置
crontab:
  # 守护巡检时间间隔（分钟）
  interval: 1
  healthCheck:
    # 健康检查脚本超时（秒）
    timeout: 60
  startScript:
    # 启动脚本超时（秒）
    timeout: 120

# 请求透传默认超时（秒）
forward:
  timeout: 30

# VictoriaMetrics 监控组件配置
victoriaMetrics:
  # VictoriaMetrics 查询地址
  url: http://192.168.50.177:9090
  # 鉴权请求头（如需要），支持环境变量引用
  headers:
    - key: Authorization
      value: value

# 文件上传配置
upload:
  # 单文件最大上传大小（字节），默认 100MB
  maxFileSize: 104857600
  # 敏感目录黑名单（可选），命中即拒绝上传；未配置时使用内置默认列表
  # sensitivePaths:
  #   - /etc
  #   - /root
  #   - C:\windows

# HTTP 接口鉴权配置
auth:
  # 鉴权密钥，客户端需在请求头携带 Authentication 或 CIB-AUTHORIZATION
  # 生产禁止硬编码，优先环境变量注入
  key: ""

# 日志配置
log:
  # 日志级别：debug / info / warn / error，默认 info
  level: info
  # 单个日志文件最大大小，单位 MB，默认 100
  maxFileSize: 100
  # 日志最大保留天数，默认 30（DailyRotator 按天轮转）
  maxRetainDays: 30
```


### 5. 运行

```bash
# 前台运行（调试用）
./metric-agent --config ./metricAgent.yml --bind-addr 0.0.0.0:9092

# 检查健康状态（需要带鉴权头）
curl http://127.0.0.1:9092/health
```


## 编译构建

### 本地编译

```bash
# 编译当前平台
go build -mod=mod -o metric-agent ./cmd/metric-agent/

# 或使用脚本
./deploy/build.sh local
```


### 交叉编译

```bash
# Linux amd64（默认，最常用）
./deploy/build.sh linux

# Linux arm64
./deploy/build.sh arm64

# Windows amd64
./deploy/build.sh windows

# macOS amd64
./deploy/build.sh darwin

# 所有平台
./deploy/build.sh all
```


输出产物位于 `dist/` 目录：

```
dist/
├── metric-agent-linux-amd64          # 可执行文件
├── metric-agent-linux-amd64.tar.gz   # 部署包（含配置和脚本）
├── metric-agent-windows-amd64.exe
├── metric-agent-windows-amd64.zip
└── ...
```


### 编译参数说明

| 参数                 | 说明                           |
| ------------------ | ---------------------------- |
| `CGO_ENABLED=0`    | 纯静态编译，无外部依赖，可在任意 Linux 发行版运行 |
| `-ldflags="-s -w"` | 去除调试信息，减小体积约 40%             |
| `-mod=mod`         | 使用 Go modules 模式             |

## 部署安装

### 目录布局

部署后的目录结构如下：

```
/opt/metric-agent/
├── metric-agent                  # 可执行文件
├── metricAgent.yml              # 主配置文件（必改）
├── crontab.yml                  # 进程守护配置（enableGuardian=true 时生效）
├── scheduledConfig.yml          # 定时任务配置（enableSchedule=true 时生效）
├── logs/
│   └── metricAgent.log           # 运行日志
├── deploy/
│   ├── metric-agent.service      # systemd 服务文件
│   ├── start.sh                  # 管理脚本
│   └── build.sh                  # 编译脚本
└── VERSION                       # 版本信息
```


### 方式一：systemd 服务（推荐）

```bash
# 1. 安装为 systemd 服务
cd /opt/metric-agent
sudo ./deploy/start.sh install

# 2. 启动
sudo systemctl start metric-agent

# 3. 设置开机自启
sudo systemctl enable metric-agent

# 4. 查看状态
sudo systemctl status metric-agent

# 5. 查看日志
journalctl -u metric-agent -f
```


### 方式二：管理脚本

```bash
cd /opt/metric-agent

# 后台启动
./deploy/start.sh start-daemon

# 停止
./deploy/start.sh stop

# 重启
./deploy/start.sh restart

# 查看状态
./deploy/start.sh status

# 实时日志
./deploy/start.sh logs

# 健康检查
./deploy/start.sh health
```


### 方式三：直接运行

```bash
cd /opt/metric-agent

# 前台运行（调试模式）
./metric-agent --config ./metricAgent.yml --bind-addr 0.0.0.0:9092

# 后台运行
nohup ./metric-agent --config ./metricAgent.yml --bind-addr 0.0.0.0:9092 \
    --logs ./logs/metricAgent.log > /dev/null 2>&1 &
```


## 配置说明

### crontab.yml（进程守护配置）

定义需要守护的监控组件列表，Agent 每分钟巡检一次（按组件独立计数，互不影响）：

```yaml
- componentName: vmagent          # 组件名
  ips:                            # 宿主机 IP 匹配（任意 IP 命中即执行守护）
    - "127.0.0.1"
  healthCheckScript: "pgrep -f vmagent > /dev/null && exit 0 || exit 1"
  startScript: "systemctl start vmagent"
```


**守护逻辑**：

1. 每分钟执行 `healthCheckScript`，退出码 0 = 存活，非0执行startScript

### scheduledConfig.yml（定时任务配置）

定义 PromQL 定时查询任务，由 robfig/cron/v3 调度：

```yaml
- name: cpu_usage                 # 任务名（唯一）
  cron: "* * * * *"              # Cron 表达式（分/时/日/月/周）
  timeout: 30                     # 单任务超时（秒）
  queryConfigs:
    queries:
      - queryKey: cpu_usage_percent
        promql: '100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[1m])) * 100)'
      - queryKey: memory_usage
        promql: '100 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes * 100)'
  output:
    path: /var/log/metric-agent/data/cpu_usage.json   # 结果输出文件路径
    maxFile: 100                  # MB，超出自动滚动
    writeMode: append             # 写入模式：append（默认追加）或 overwrite（覆盖）
```
**说明**：

1. 暂不支持 scheduledConfig.yml 动态更新，需要重启 agent

### Nacos 配置清单

Agent 从 Nacos 拉取配置清单 dataId：

* **个性化配置**（优先级高）：`metricFileConfig_{agent.group}_{agent.id}`

* **公共配置**（回退）：`metricFileConfig_{agent.group}`

在 Nacos 上创建 dataId，YAML 格式示例：

```yaml
# 按 fileName 指定拉取单个配置
- configCode: vmagent_config                  # 配置项编号（可选），用于去重和优先级判定
  fileName: vmagent.yaml                     # Nacos 上的 dataId；为空时按 group 拉取全部配置
  group: VICTORIAMETRICS                    # Nacos 配置分组
  storePath: /etc/victoriametrics            # 本地存储目录（必填）
  reFileName: vmagent-production.yaml        # 重命名（可选）
  fileMode: "0755"                           # 落地文件权限（八进制字符串，默认 "0755"）
  reloadScript: "systemctl reload vmagent"   # 变更后执行的重载脚本（可选）
  enableClean: false                         # 是否参与配置清理对齐（可选）

# 按 group 批量拉取（fileName 为空时生效）
- configCode: batch_config
  group: VICTORIAMETRICS
  storePath: /opt/vmagent/etc
```


**配置分发流程**：

1. Agent 启动时立即拉取一次（不等 PullLoop）
2. 之后按 `config.pullInterval` 分钟定时拉取
3. 分发时：`BackupFile`（重命名为 `_agent_bak`）→ `AtomicWrite`（写入 `.tmp_` + Rename）→ `reloadScript`
4. 写入失败或 reloadScript 失败自动回滚（恢复备份）

## 运行模式

### HTTP 服务模式（默认）

Agent 作为 HTTP 服务运行，提供 API 接口：

```bash
# 使用默认配置
./metric-agent

# 自定义参数
./metric-agent \
    --config /path/to/metricAgent.yml \
    --bind-addr 0.0.0.0:9092 \
    --logs /path/to/logs/agent.log
```


**参数说明**：

| 参数            | 默认值                      | 说明                    |
| ------------- | ------------------------ | --------------------- |
| `--config`    | `./metricAgent.yml`     | 配置文件路径（相对路径基于可执行文件目录） |
| `--bind-addr` | `0.0.0.0:9092`           | HTTP 监听地址             |
| `--logs`      | `./logs/metricAgent.log` | 日志文件路径（相对路径基于可执行文件目录） |
| `--help`      | -                        | 显示帮助信息                |

### 指令执行模式

不启动 HTTP 服务，向已运行的本地 MetricAgent 服务发 AES 加密请求，执行完成后直接退出：

```bash
# 直接传入脚本内容（向 127.0.0.1:9092 发加密请求）
./metric-agent --exec 'echo "hello world"'

# 执行脚本文件（相对路径基于可执行文件目录）
./metric-agent --exec-file ./script.sh

# 传递参数（含空格参数正确传递）
./metric-agent --exec 'echo $@' -- "hello world" arg2

# 指定端口和超时
./metric-agent --exec 'hostname' --port 9090 --timeout 120
```


**参数说明**：

| 参数            | 默认值            | 说明                              |
| ------------- | -------------- | ------------------------------- |
| `--exec`      | -              | Shell 脚本字符串（与 `--exec-file` 互斥） |
| `--exec-file` | -              | Shell 脚本文件路径（相对路径基于可执行文件目录）     |
| `--port`      | `9092`         | 目标 HTTP 服务端口                    |
| `--timeout`   | `0`（服务端默认 60s） | 执行超时秒数；客户端上限 1800s，服务端最终拦截 600s |
| `--`          | 分隔符            | 之后所有内容作为脚本参数传递，**保留空格语义**       |
| `--help`      | -              | 显示帮助信息                          |

**退出码语义**：

| 退出码范围     | 含义                                        |
| --------- | ----------------------------------------- |
| `0`       | 脚本执行成功（exit_code = 0）                     |
| `1 ~ 255` | Shell 脚本非零退出码（原样透传 ExecResponse.ExitCode） |
| `< 0`     | Shell 执行器内部异常（启动失败等），统一返回 `1`             |
| `-1`      | 客户端连接失败、参数校验失败、响应解析失败                     |

**互斥规则**：

| 规则                            | 结果       |
| ----------------------------- | -------- |
| `--exec` 与 `--exec-file` 同时传入 | 报错退出     |
| 指令模式参数 + HTTP 模式参数 混合传入       | 报错退出     |
| `--help` 与其他参数同时出现            | 仅输出帮助并退出 |

## API 接口

所有接口（除 `/api/v1/exec`）需在请求头携带鉴权密钥：

* `Authentication: <auth_key>`

* 或 `CIB-AUTHORIZATION: <auth_key>`

鉴权密钥在 `metricAgent.yml` 的 `auth.key` 中配置。

### 1. 健康检查

```
GET /health
```


响应：

```json
{
    "status": "ok",
    "agent_id": "node-001",
    "agent_group": "DEFAULT_GROUP",
    "timestamp": 1700000000
}
```


### 2. 远程命令执行（AES 加密）

```
POST /api/v1/exec
Content-Type: application/octet-stream
```


| 项目 | 说明                   |
| -- | -------------------- |
| 鉴权 | ❌ 跳过（自带 AES 应用层加密保护） |

请求体：ExecRequest JSON 的 AES-CBC + PKCS7 加密后二进制。

加密前的 JSON 结构：

```json
{
    "script": "echo hello && df -h",
    "args": ["arg1", "arg2"],
    "timeout": 60
}
```


| 字段      | 类型       | 必填 | 说明                                     |
| ------- | -------- | -- | -------------------------------------- |
| script  | string   | ✅  | Shell 脚本内容，不能为空                        |
| args    | []string | ❌  | 脚本参数数组（shell -c "script" args... 方式传递） |
| timeout | int      | ❌  | 超时秒数；传了但 ≤0 或 >600 直接拒绝；不传使用默认 60s     |

响应（加密前）：

```json
{
    "stdout": "hello\n",
    "stderr": "",
    "exit_code": 0,
    "duration_ms": 15
}
```


### 3. 请求透传

```
GET  /forward?target=http://victoriametrics:8428/api/v1/query?query=up
POST /forward?target=http://promxy:9092/api/v1/query
Authentication: <auth_key>
```


| 项目 | 说明   |
| -- | ---- |
| 鉴权 | ✅ 需要 |

将请求透传到目标 URL，响应原样返回（状态码、响应头、响应体）。

**路径规则**：

```
原始请求: /forward/api/v1/query?target=http://backend:9090/base?x=1
构造后:   http://backend:9090/base/api/v1/query?x=1&[其他原始query参数]
```


**安全特性**：

**内网部署说明**：ForwardService 不做 SSRF 防护，允许代理访问任意 http/https 服务（含回环、私有网段）。请求体无大小限制（依赖上游网关防护）。

### 4. 文件上传

```
POST /api/v1/upload
Content-Type: multipart/form-data
Authentication: <auth_key>
```


| 项目    | 说明                                    |
| ----- | ------------------------------------- |
| 鉴权    | ✅ 需要                                  |
| 单文件上限 | 默认 100MB（可通过 `upload.maxFileSize` 修改） |

multipart 字段：

| 字段        | 类型     | 必填 | 说明                                      |
| --------- | ------ | -- | --------------------------------------- |
| storePath | text   | ✅  | 存储目录（绝对路径或相对路径）                         |
| fileName  | text   | ❌  | 目标文件名（禁止 `/`、`\`、`..`）；不填取上传文件原始 base 名 |
| overwrite | text   | ❌  | 是否覆盖已存在文件；默认 `"true"`                   |
| file      | binary | ✅  | 上传的文件内容                                 |

成功响应：

```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "fileName": "my-script.sh",
        "size": 2048,
        "targetPath": "/opt/metric-agent/bin/my-script.sh",
        "backup": "/opt/metric-agent/bin/my-script.sh_agent_bak"
    }
}
```


**覆盖逻辑**：存在原文件时先重命名为 `_agent_bak`（单版本备份），再写入新文件。

### 5. 进程守护控制

```
GET /api/v1/guardian?action=status|pause|resume
```

| 项目 | 说明                                 |
| -- | ---------------------------------- |
| 鉴权 | ❌ 跳过（与 /health、/api/v1/exec 同为免鉴权白名单） |
| 方法 | 仅 GET |

| action  | 说明                                     |
| ------- | -------------------------------------- |
| status  | 查询守护服务当前状态（running/paused/last_action） |
| pause   | 暂停巡检（守护服务停止健康检查和拉起逻辑，协程仍存活）       |
| resume  | 恢复巡检                                   |

成功响应（action=status）：

```json
{
  "status": {
    "running": true,
    "paused": false,
    "interval_minutes": 1,
    "last_action": "start",
    "last_action_at": 1725897600
  }
}
```

成功响应（action=pause/resume）：

```json
{
  "success": true,
  "status": { /* GuardianStatus 完整对象 */ }
}
```

> 若 `feature.enableGuardian=false`，所有 action 返回 HTTP 503。

## 管理脚本

`deploy/start.sh` 提供一站式管理能力：

| 命令                           | 说明                           |
| ---------------------------- | ---------------------------- |
| `./start.sh start`           | 前台启动（调试）                     |
| `./start.sh start-daemon`    | 后台守护启动                       |
| `./start.sh stop`            | 优雅停止（SIGTERM → 超时 → SIGKILL） |
| `./start.sh restart`         | 重启                           |
| `./start.sh status`          | 查看运行状态 + 健康检查                |
| `./start.sh install`         | 安装为 systemd 服务               |
| `./start.sh uninstall`       | 卸载 systemd 服务                |
| `./start.sh logs`            | 实时查看日志                       |
| `./start.sh exec '<script>'` | 指令模式执行脚本                     |
| `./start.sh health`          | 健康检查                         |

## 常见问题

### Q1: Nacos 连接失败怎么办？

1. 确认 Nacos 服务正常运行：`curl http://<nacos>:8848/nacos`
2. 检查 `metricAgent.yml` 中 `nacos.address` 配置
3. 确认 `feature.enableNacos: true`（默认）
4. Agent 启动后会按 `config.pullInterval` 分钟定期重试，日志中可见重连状态

### Q2: 配置分发失败怎么办？

1. 确认 Nacos 上存在 `metricFileConfig_{group}_{id}` 或 `metricFileConfig_{group}` dataId
2. 确认 `storePath` 目录有写入权限
3. 确认 `reloadScript` 命令存在且可执行
4. 查看日志中的详细错误信息

### Q3: 进程守护不触发？

1. 确认 `feature.enableGuardian: true`
2. 确认 `crontab.yml` 中 `ips` 字段包含当前节点的 IP
3. 确认 `healthCheckScript` 正确（退出码 0 = 存活）

### Q4: 定时任务不执行？

1. 确认 `feature.enableSchedule: true`
2. 确认 `scheduledConfig.yml` 中 `cron` 表达式正确
3. 确认 VictoriaMetrics URL 可访问
4. 确认 `output.path` 目录有写入权限

### Q5: 指令执行模式请求解密失败？

1. 确认 `metricAgent.yml` 中 `shell.encrypt.key` 与客户端默认密钥 `7sK9p2R5zG8tB4vN`（16字节）一致
2. 服务端启动日志会提示密钥一致性状态
3. 如果修改了 YAML 中的 key，需同步修改客户端常量 `DefaultExecModeAESKey`

### Q6: HTTP 端口被占用？

修改启动参数：

```bash
./metric-agent --config ./metricAgent.yml --bind-addr 0.0.0.0:9093
```


### Q7: 如何升级版本？

```bash
# 1. 停止服务
sudo systemctl stop metric-agent

# 2. 备份配置
cp -r /opt/metric-agent /opt/metric-agent.bak

# 3. 替换二进制 + 配置文件
cp metric-agent /opt/metric-agent/
chmod +x /opt/metric-agent/metric-agent

# 4. 重启服务
sudo systemctl start metric-agent

# 5. 验证
curl -H "Authentication: <auth-key>" http://127.0.0.1:9092/health
```


### Q8: 日志轮转策略？

* **轮转方式**：自研 DailyRotator，按本地时区午夜自动轮转
* **轮转文件命名**：`metricAgent-{yyyy-MM-dd}-{序号}.log`（如 `metricAgent-2026-09-10-0.log`）
* **单文件最大**：100MB（可通过 `log.maxFileSize` 配置）
* **保留天数**：30 天（可通过 `log.maxRetainDays` 配置），由后台清理协程每日扫描删除过期文件
* **日志级别**：支持 debug / info / warn / error（通过 `log.level` 配置）
* **日志路径**：默认 `./logs/metricAgent.log`（相对于二进制目录），可通过 `--logs` 参数指定

### Q9: 备份机制说明？

配置分发和文件上传都使用单版本备份：

* 备份文件后缀：`{原文件名}_agent_bak`

* 配置分发：写入新文件前将原文件重命名为 `_agent_bak`，写入/重载失败时自动回滚

* 文件上传：覆盖上传时将原文件重命名为 `_agent_bak`，上传失败时回滚

* 多次备份直接覆盖旧备份（单版本）

## 目录结构

```
metric-agent/
├── cmd/metric-agent/           # 入口包
│   ├── main.go                  # CLI 入口 + HTTP 服务器
│   ├── metricAgent.yml         # 主配置文件（部署时修改）
│   ├── crontab.yml             # 进程守护配置
│   ├── scheduledConfig.yml     # 定时任务配置
│   └── metricFileConfig_group.yml  # Nacos 公共配置清单示例
├── internal/
│   ├── bootstrap/               # 启动引导
│   │   ├── bootstrap.go         # BootstrapPath 路径解析
│   │   ├── config_loader.go     # YAML 配置加载
│   │   ├── server_init.go       # 依赖注入 + 服务初始化 + 路由注册
│   │   └── shutdown.go          # 优雅关闭
│   ├── common/
│   │   ├── constant/constants.go # 全局常量与默认值
│   │   ├── errors/              # 错误定义
│   │   ├── filekit/             # 原子文件操作（AtomicWrite / BackupFile / RestoreBackup）
│   │   └── logger/              # 日志封装
│   ├── model/                   # 数据模型
│   │   ├── agent_config.go      # 主配置结构体
│   │   ├── api_model.go         # API 请求/响应模型
│   │   ├── guardian_config.go   # 进程守护配置
│   │   ├── metric_config.go     # Nacos 配置清单项
│   │   └── schedule_config.go   # 定时任务配置
│   ├── infra/
│   │   ├── crypto/aes.go        # AES-CBC + PKCS7 加解密（前 16 字节为随机 IV）
│   │   ├── iface/               # 接口定义（ConfigCenter / Crypto / ShellExecutor）
│   │   ├── nacos_client/        # Nacos 客户端封装
│   │   └── shell_exec/          # Shell 执行器（支持 Linux / Windows）
│   └── service/
│       ├── auth_middleware.go   # HTTP 鉴权中间件（/api/v1/exec 跳过）
│       ├── command_service.go   # 远程命令执行（AES 加解密 + POST 校验）
│       ├── config_service.go    # 配置分发（PullLoop + 原子写入 + 备份回滚）
│       ├── forward_service.go   # 请求透传（内网代理，无 SSRF 防护）
│       ├── guardian_service.go  # 进程守护（熔断保护）
│       ├── schedule_service.go  # 定时任务（Cron 调度 + VictoriaMetrics 查询）
│       └── upload_service.go    # 文件上传（敏感路径校验 + 磁盘检查 + 备份回滚）
├── deploy/
│   ├── metric-agent.service     # systemd 服务文件
│   ├── start.sh                 # 管理脚本
│   └── build.sh                 # 编译脚本
├── go.mod
├── go.sum
└── README.md
```


## 核心调用路径
```
main()  
→ runHTTPMode()  
→ logger.InitFileLogger()       # 初始化文件日志  
→ bootstrap.LoadBaseConfig()    # 加载并校验 metricAgent.yml 配置  
→ bootstrap.InitServer()        # 核心服务初始化  
├─ nacos_client.NewNacosClient()  # Nacos 配置中心客户端（可选）  
├─ shell_exec.NewShellExecutor()  # Shell 脚本执行器  
├─ crypto.NewAESCrypto()          # AES 加解密器  
├─ service.NewConfigService()     # 配置分发服务（定时拉取 Nacos）  
├─ service.NewCommandService()    # 远程命令执行服务  
├─ service.NewGuardianService()   # 进程守护服务（可选）  
├─ service.NewScheduleService()   # PromQL 定时任务服务（可选）  
├─ service.NewForwardService()    # HTTP 请求透传服务  
└─ service.NewUploadService()     # 文件上传服务  
→ 注册路由 + AuthMiddleware 鉴权中间件  
→ srv.ListenAndServe()         # 启动 HTTP 服务  
→ bootstrap.WaitForShutdown()  # 监听信号，优雅关闭
```
------------------------------------------

**MetricAgent** — 为监控而生的轻量级运维代理 🚀
