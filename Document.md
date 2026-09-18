# MetricAgent HTTP 接口文档

---

## 第一章 HTTP 接口基础

### 1.1 基础信息

| 项目      | 说明                                                         |
| ------- | ---------------------------------------------------------- |
| 基础 URL  | `http://{host}:{port}`，默认端口 **9092**，默认绑定 **0.0.0.0:9092** |
| 鉴权方式    | 请求头 `Authentication`，值为配置文件中的 `auth.key`，大小写敏感精确匹配         |
| 鉴权白名单   | `/api/v1/exec` 跳过鉴权（自带 AES 应用层加密保护）                        |
| 请求体大小限制 | 所有接口 10MB（除 `/api/v1/upload` 另受文件大小限制）                     |
| 默认超时    | 连接读写 30s，空闲 120s；命令执行最长 600s；请求透传最长 120s                   |

**统一错误响应格式**（ErrorResponse，所有接口的失败响应均使用此结构）：

```json
{
  "code": 400,
  "message": "参数错误说明"
}
```


| 字段      | 类型     | 说明                                                  |
| ------- | ------ | --------------------------------------------------- |
| code    | int    | HTTP 状态码（400/401/403/404/405/409/413/500/502/504 等） |
| message | string | 可读的错误描述                                             |

---

### 1.2 健康检查

| 项目           | 说明                      |
| ------------ | ----------------------- |
| **Method**   | `GET`                   |
| **URL**      | `/health`               |
| **鉴权**       | ✅ 不需要（`Authentication`） |
| **Query 参数** | 无                       |
| **请求体**      | 无                       |

**成功响应**（HTTP 200，HealthResponse 裸 JSON）：

```json
{
    "status": "ok",
    "agent_id": "node-001",
    "agent_group": "k8s",
    "timestamp": 1788405726
}

```


| 字段          | 类型     | 说明                |
| ----------- | ------ | ----------------- |
| status      | string | 固定返回 `"ok"`       |
| agent_id    | string | 配置文件中的 Agent 唯一标识 |
| agent_group | string | 配置文件中的 Agent 分组   |
| timestamp   | int64  | 服务端当前 Unix 时间戳（秒） |

**cURL 示例**：

```bash
curl -H "Authentication: your-auth-key" http://127.0.0.1:9092/health
```


---

### 1.3 远程命令执行（AES 加密）

| 项目               | 说明                                     |
| ---------------- | -------------------------------------- |
| **Method**       | `POST`                                 |
| **URL**          | `/api/v1/exec`                         |
| **Content-Type** | `application/octet-stream`（AES 加密后二进制） |
| **鉴权**           | ❌ 跳过（自带 AES 应用层加密保护，请求体/响应体均需正确加密才能通过） |

本接口**统一**使用 AES-CBC + PKCS7 填充 加密传输。

#### 1.3.1 加密流程

```
请求方：
  1. 构造 ExecRequest JSON（明文结构见 1.3.2）
  2. AES-CBC + PKCS7 填充 加密 → 二进制密文
  3. POST 到 /api/v1/exec，Body 为密文，Content-Type 为 application/octet-stream

服务端：
  1. 校验 HTTP 方法必须为 POST（405 拒绝）
  2. AES 解密请求体 → 解析 ExecRequest JSON
  3. 参数校验（script 非空、timeout 合法）
  4. 执行 Shell 脚本
  5. 构造 ExecResponse JSON → AES 加密
  6. 返回密文（Content-Type: application/octet-stream）

请求方：
  1. 读取响应体
  2. AES 解密 → 解析 ExecResponse JSON
```


#### 1.3.2 加密前的请求体结构

```json
{
  "script": "echo $1",
  "args": ["hello world"],
  "timeout": 30
}
```


| 字段      | 类型       | 必填 | 说明                                                                             |
| ------- | -------- | -- | ------------------------------------------------------------------------------ |
| script  | string   | ✅  | 待执行的 Shell 脚本内容，不能为空或全空白                                                       |
| args    | []string | ❌  | 脚本参数数组，执行时以 `shell -c "script" args...` 方式传递，**保留空格语义**；脚本内用 `$@` 或 `$1 $2` 引用 |
| timeout | int      | ❌  | 执行超时（秒）；不填（nil）使用默认 60 秒；**传了但 ≤0 或 >600 直接拒绝执行**                              |

#### 1.3.3 加密前的成功响应结构

```json
{
  "stdout": "hello world\n",
  "stderr": "",
  "exit_code": 0,
  "duration_ms": 12
}
```


| 字段          | 类型     | 说明                                   |
| ----------- | ------ | ------------------------------------ |
| stdout      | string | 脚本标准输出                               |
| stderr      | string | 脚本标准错误                               |
| exit_code   | int    | Shell 进程退出码；**< 0 表示执行器内部异常**（如启动失败） |
| duration_ms | int64  | 脚本实际执行耗时（毫秒）                         |

#### 1.3.4 错误码说明

所有错误响应（包括 4xx 和 5xx）也是**加密后二进制**（Content-Type: `application/octet-stream`），解密后为 ErrorResponse JSON。

| HTTP 状态 | 解密后 message                          | 原因                            |
| ------- | ------------------------------------ | ----------------------------- |
| 400     | 请求体解密失败                              | AES 密钥错误或密文损坏                 |
| 400     | 请求体JSON解析失败                          | 解密成功但不是合法 JSON                |
| 400     | 脚本内容不能为空                             | script 为空字符串或全空白              |
| 400     | 超时时间非法：必须大于0且不超过600秒                 | timeout 传值 ≤0 或 >600          |
| 405     | 仅支持 POST 方法                          | 使用了 GET/PUT/DELETE 等非 POST 方法 |
| 408     | （正常加密响应，exit_code 由 ExecResponse 承载） | 脚本执行超时，进程被 context 终止         |
| 500     | 命令执行失败                               | Shell 进程启动失败或运行时异常            |

> **特殊情况**：如果错误响应本身的 JSON 序列化或 AES 加密失败，会降级为**明文 JSON ErrorResponse** 返回（Content-Type: `application/json`）。

#### 1.3.5 超时响应（HTTP 408）

脚本执行超时后，服务端会 kill 整个进程组。响应的 ExecResponse 结构不变（加密后），脚本可能已被 context 终止，exit_code 可能为非零值。

#### 1.3.6 cURL 示例

```bash
# 需要先在客户端用 AES-CBC + PKCS7 加密请求体，再发送加密后的二进制数据
# 密文结构：IV(16字节随机) + 加密后正文
# 以下示例使用 Python 加密后调用，实际可根据需要替换为 Go/PowerShell 等

# 1. Python 加密请求体
python3 -c "
import json, os
from Crypto.Cipher import AES
from Crypto.Util.Padding import pad

key = b'7sK9p2R5zG8tB4vN'                    # 16 字节 AES-128 密钥
req = json.dumps({'script': 'echo hello world', 'timeout': 30}).encode()
padded = pad(req, AES.block_size)             # PKCS7 填充至 16 字节块对齐
iv = os.urandom(AES.block_size)               # 随机 16 字节 IV
cipher = AES.new(key, AES.MODE_CBC, iv)
ciphertext = cipher.encrypt(padded)
payload = iv + ciphertext
open('/tmp/exec_payload.bin', 'wb').write(payload)
"

# 2. 发送加密请求
curl -X POST -H "Content-Type: application/octet-stream" \
  --data-binary @/tmp/exec_payload.bin \
  http://127.0.0.1:9092/api/v1/exec -o /tmp/exec_resp.bin

# 3. Python 解密响应
python3 -c "
from Crypto.Cipher import AES
from Crypto.Util.Padding import unpad

key = b'7sK9p2R5zG8tB4vN'
data = open('/tmp/exec_resp.bin', 'rb').read()
iv, ciphertext = data[:16], data[16:]         # 前 16 字节为 IV
cipher = AES.new(key, AES.MODE_CBC, iv)
plaintext = unpad(cipher.decrypt(ciphertext), AES.block_size)
print(plaintext.decode())
"
```


---

### 1.4 请求透传

| 项目               | 说明                                          |
| ---------------- | ------------------------------------------- |
| **Method**       | 所有 HTTP 方法（GET/POST/PUT/DELETE/...），透传到目标服务 |
| **URL**          | `/forward` 或 `/forward/{path_suffix}`       |
| **鉴权**           | ✅ 需要（`Authentication`）                      |
| **Content-Type** | 与原始请求相同，由透传保留                               |

#### 1.4.1 Query 参数

| 参数     | 类型     | 必填 | 说明                                                                  |
| ------ | ------ | -- | ------------------------------------------------------------------- |
| target | string | ✅  | 目标基础 URL，必须以 `http://` 或 `https://` 开头（如 `http://backend:9090/api`） |

#### 1.4.2 路径规则

```
原始请求路径: /forward/api/v1/query
Query: target=http://backend:9090/base?x=1

构造后: http://backend:9090/base/api/v1/query?x=1&[其他原始query参数]
```


* target 自带路径优先，无路径时使用 `/forward` 之后的后缀部分

* 查询参数合并：原始请求参数（排除 target）+ target 参数，**target 同名参数优先**

#### 1.4.3 成功响应

**直接透传目标服务的原始响应**（状态码、响应头、响应体），不套任何 JSON 壳。

#### 1.4.4 失败响应（JSON ErrorResponse）

| HTTP 状态 | 原因                                             |
| ------- | ---------------------------------------------- |
| 400     | 缺少 target 参数、target 协议非法（非 http/https）、host 为空 |
| 502     | 目标服务不可达、连接失败                                   |
| 504     | 转发请求超时（默认 30s，最大 120s）                         |

> **内网部署说明**：ForwardService 不做 SSRF 防护，允许代理访问任意 http/https 服务（含回环、私有网段）。请求体无大小限制（依赖上游网关防护）。

#### 1.4.5 cURL 示例

```bash
# 查询 VictoriaMetrics
curl -H "Authentication: your-auth-key" \
  'http://127.0.0.1:9092/forward/api/v1/query?target=http://10.0.0.5:8428&query=up'

# POST 请求透传
curl -X POST -H "Authentication: your-auth-key" \
  'http://127.0.0.1:9092/forward?target=http://backend:9090/submit' \
  -d '{"key": "value"}'
```


---

### 1.5 文件上传

| 项目               | 说明                                         |
| ---------------- | ------------------------------------------ |
| **Method**       | `POST`                                     |
| **URL**          | `/api/v1/upload`                           |
| **鉴权**           | ✅ 需要（`Authentication`）                     |
| **Content-Type** | `multipart/form-data`                      |
| **单文件大小上限**      | 默认 **100MB**，可通过配置 `upload.maxFileSize` 修改 |

#### 1.5.1 multipart 字段

| 字段        | 类型     | 必填 | 说明                                                   |
| --------- | ------ | -- | ---------------------------------------------------- |
| storePath | text   | ✅  | 存储目录（绝对路径，或相对于可执行文件目录的相对路径），会被 `filepath.Clean` 清理   |
| fileName  | text   | ❌  | 目标文件名；不填时取上传文件的原始 base 名；禁止 `/`、`\`、`..`、空、NUL 字符    |
| overwrite | text   | ❌  | 是否覆盖已存在文件；默认 `"true"`；接受 `true/false`、`1/0`、`yes/no` |
| file      | binary | ✅  | 上传的文件内容（multipart file part）                         |

#### 1.5.2 成功响应（HTTP 200）

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


| data 字段    | 类型     | 说明                                            |
| ---------- | ------ | --------------------------------------------- |
| fileName   | string | 最终落盘的目标文件名                                    |
| size       | int64  | 上传文件实际大小（字节）                                  |
| targetPath | string | 文件完整落盘绝对路径                                    |
| backup     | string | 原文件备份路径；仅当 `overwrite=true` 且存在原文件时返回，否则为空字符串 |

#### 1.5.3 覆盖逻辑

当 `overwrite=true` 且目标文件已存在时：

1. 将原文件重命名为 `{name}_agent_bak`（单版本备份，多次覆盖直接替换）
2. 写入新文件到目标路径
3. 如果写入失败，自动将备份文件重命名回原路径（回滚）

#### 1.5.4 失败响应（HTTP ErrorResponse）

| HTTP 状态 | 原因                                                                          |
| ------- | --------------------------------------------------------------------------- |
| 400     | 缺少 `storePath` 或 `file` 字段、请求体不是 multipart、multipart 解析失败、`overwrite` 参数值非法 |
| 403     | 存储路径命中敏感目录黑名单、`fileName` 包含非法字符、`storePath` 无法解析                            |
| 409     | 目标文件已存在且 `overwrite=false`                                                  |
| 413     | 文件大小超过限制                                                                    |
| 500     | 创建目录失败、磁盘空间不足、写入临时文件失败、rename 失败、回滚失败                                       |

#### 1.5.5 敏感目录黑名单（命中即拒绝上传）

| Linux                  | Windows                  |
| ---------------------- | ------------------------ |
| `/etc`                 | `C:\windows`             |
| `/root`                | `C:\program files`       |
| `/proc`                | `C:\program files (x86)` |
| `/sys`                 | `C:\windows\system32`    |
| `/dev`                 | <br />                   |
| `/var/run`、`/var/lib`  | <br />                   |
| `/boot`、`/sbin`、`/bin` | <br />                   |
| `/usr/bin`、`/usr/sbin` | <br />                   |

> Windows 下会先用 `GetLongPathNameW` 展开 8.3 短路径（如 `C:\PROGRA~1` → `C:\Program Files`），再做黑名单匹配。  
> 可通过配置 `upload.sensitivePaths` 覆盖默认列表。

#### 1.5.6 cURL 示例

```bash
# 覆盖上传
curl -X POST -H "Authentication: your-auth-key" \
  -F "storePath=/opt/metric-agent/conf" \
  -F "fileName=my-config.yaml" \
  -F "overwrite=true" \
  -F "file=@./local-config.yaml" \
  http://127.0.0.1:9092/api/v1/upload

# 不覆盖（已存在时返回 409）
curl -X POST -H "Authentication: your-auth-key" \
  -F "storePath=/opt/metric-agent/conf" \
  -F "fileName=my-config.yaml" \
  -F "overwrite=false" \
  -F "file=@./local-config.yaml" \
  http://127.0.0.1:9092/api/v1/upload

# 上传脚本（不指定 fileName 时使用文件原始名称）
curl -X POST -H "Authentication: your-auth-key" \
  -F "storePath=/opt/metric-agent/bin" \
  -F "file=@./deploy-script.sh" \
  http://127.0.0.1:9092/api/v1/upload
```


---

### 1.6 进程守护控制

| 项目           | 说明                                        |
| ------------ | ----------------------------------------- |
| **Method**   | `GET`                                     |
| **URL**      | `/api/v1/guardian`                        |
| **鉴权**       | ❌ 跳过（与 `/health`、`/api/v1/exec` 同为免鉴权白名单） |
| **Query 参数** | `action`：`pause` / `resume` / `status`    |

> 本接口仅在 `feature.enableGuardian=true`（默认 true）时可用；若守护服务未启用，所有 action 请求均返回 **HTTP 503**。

#### 1.6.1 action=status（查询守护状态）

**请求**：

```
GET /api/v1/guardian?action=status
```


**成功响应**（HTTP 200）：

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


| status 字段        | 类型     | 说明                                                      |
| ---------------- | ------ | ------------------------------------------------------- |
| running          | bool   | 守护服务是否已启动（Start 后为 true，Stop 后为 false）                  |
| paused           | bool   | 是否暂停中（Pause 后为 true；暂停时协程仍存活但跳过巡检执行）                    |
| interval_minutes | int    | 巡检周期（分钟）                                                |
| last_action      | string | 最近一次控制操作：`start` / `pause` / `resume` / `stop`（空字符串时省略） |
| last_action_at   | int64  | 最近一次操作的 Unix 时间戳（秒）（0 时省略）                              |

#### 1.6.2 action=pause（暂停巡检）

**请求**：

```
GET /api/v1/guardian?action=pause
```


**成功响应**（HTTP 200）：

```json
{
  "success": true,
  "status": {
    "running": true,
    "paused": true,
    "interval_minutes": 1,
    "last_action": "pause",
    "last_action_at": 1725897600
  }
}
```


> 暂停后守护服务不再执行健康检查和拉起逻辑，直到收到 `resume` 指令。`paused=true` 表示协程仍存活但跳过巡检执行。

#### 1.6.3 action=resume（恢复巡检）

**请求**：

```
GET /api/v1/guardian?action=resume
```


**成功响应**（HTTP 200）：

```json
{
  "success": true,
  "status": {
    "running": true,
    "paused": false,
    "interval_minutes": 1,
    "last_action": "resume",
    "last_action_at": 1725897600
  }
}
```


#### 1.6.4 错误响应

| HTTP 状态 | 原因                                                             |
| ------- | -------------------------------------------------------------- |
| 400     | `action` 值非法、守护服务未启动时执行 `pause/resume`（返回 `"守护服务未启动，无法暂停/恢复"`） |
| 405     | 使用了非 GET 方法                                                    |
| 503     | 守护服务未启用（`feature.enableGuardian=false`）                        |

#### 1.6.5 cURL 示例

```bash
# 查询状态
curl http://127.0.0.1:9092/api/v1/guardian?action=status

# 暂停巡检
curl http://127.0.0.1:9092/api/v1/guardian?action=pause

# 恢复巡检
curl http://127.0.0.1:9092/api/v1/guardian?action=resume
```


---

## 第二章 指令执行模式（CLI）

### 2.1 概述

指令执行模式是 MetricAgent 的**一次性执行模式**：

* **不加载任何配置文件**、**不初始化日志**、**不启动 HTTP 服务**

* 组装 HTTP POST 请求，发往**本机已运行的 MetricAgent HTTP 服务**（默认 `127.0.0.1:9092`）

* **请求体和响应体均使用 AES-CBC + PKCS7 加密传输**（与 Remote 模式加密协议一致）

* 使用硬编码密钥常量 `DefaultExecModeAESKey`（不加载配置文件）

* 脚本执行完成后进程直接退出，退出码等于脚本的 Shell 退出码

**典型流程**：

```
Step 1: 先启动 MetricAgent HTTP 服务（后台常驻）
        metric-agent --config ./metricAgent.yml

Step 2: 指令模式执行脚本（向已运行的服务发 AES 加密请求）
        metric-agent --exec 'hostname && uptime'
        ↑ 进程打印 stdout/stderr 后退出
```


### 2.2 命令行参数

| 参数            | 类型     | 默认值            | 说明                                                  |
| ------------- | ------ | -------------- | --------------------------------------------------- |
| `--exec`      | string | 空              | 脚本内容（与 `--exec-file` 互斥）                            |
| `--exec-file` | string | 空              | 脚本文件路径（相对路径基于可执行文件目录，与 `--exec` 互斥）                 |
| `--port`      | int    | `9092`         | 目标 HTTP 服务端口                                        |
| `--timeout`   | int    | `0`（服务端默认 60s） | 执行超时秒数；**客户端上限 1800s**，服务端最终拦截 600s；≤0 或 >1800 直接报错 |
| `--`          | 分隔符    | —              | 之后所有内容作为脚本参数传递，**保留空格语义**                           |
| `--help`      | flag   | `false`        | 显示帮助信息（最高优先级）                                       |

### 2.3 互斥规则

| 规则                          | 错误示例                                                    | 结果                            |
| --------------------------- | ------------------------------------------------------- | ----------------------------- |
| `--exec` 与 `--exec-file` 互斥 | `metric-agent --exec 'echo' --exec-file test.sh`        | 报错退出                          |
| 指令模式参数与 HTTP 模式参数互斥         | `metric-agent --exec 'echo' --config ./metricAgent.yml` | 报错退出                          |
| HTTP 模式参数与指令模式参数互斥          | `metric-agent --config ./metricAgent.yml --exec 'echo'` | 报错退出（提示需先传入 `--exec` 才进入指令模式） |
| `--help` 与其他参数同时出现          | `metric-agent --help --exec 'echo'`                     | 仅输出帮助并退出                      |

### 2.4 退出码语义

| 退出码范围     | 含义                                          |
| --------- | ------------------------------------------- |
| `0`       | 脚本执行成功（`exit_code = 0`）                     |
| `1 ~ 255` | Shell 脚本非零退出码（原样透传 `ExecResponse.ExitCode`） |
| `< 0`     | Shell 执行器内部异常（启动失败、超时等），统一返回 `1`            |
| `-1`      | 客户端连接失败、参数校验失败、响应解析失败（由 `runExecMode` 返回）   |

### 2.5 示例

```bash
# 1. 先启动 MetricAgent HTTP 服务
metric-agent --config ./metricAgent.yml

# 2. 指令模式 - 脚本字符串（向 127.0.0.1:9092 发 AES 加密请求）
metric-agent --exec 'echo "hello world"'

# 3. 指令模式 - 脚本文件（相对路径基于可执行文件目录）
metric-agent --exec-file ./scripts/health-check.sh

# 4. 指令模式 - 指定端口、超时
metric-agent --exec 'curl -s http://localhost:8428/api/v1/query?query=up' \
  --port 9090 \
  --timeout 120

# 5. 指令模式 - 带参数（含空格参数不丢失语义）
#    -- 分隔符之后的内容原样传给脚本，shell 内通过 $@ 或 $1 $2 引用
metric-agent --exec-file ./deploy.sh -- \
  "production env" \
  "/data/backup with spaces" \
  arg3

# 6. help
metric-agent --help
```


### 2.6 常见错误

| 错误信息                                      | 原因                          | 解决方法                                                 |
| ----------------------------------------- | --------------------------- | ---------------------------------------------------- |
| `无法连接到本地 MetricAgent 服务 (127.0.0.1:9092)` | HTTP 服务未启动或端口不对             | 先启动 HTTP 服务，或用 `--port` 指定正确端口                       |
| `--exec 和 --exec-file 不能同时使用`             | 互斥参数同时传入                    | 二选一                                                  |
| `HTTP 400` + `脚本内容不能为空`                   | `--exec` 传入了空字符串或全空白        | 检查脚本内容                                               |
| `HTTP 400` + `超时时间非法`                     | `--timeout` 值 ≤0 或 >600     | 改为 1~1800 之间的整数                                      |
| `--timeout 参数值 X 超出客户端上限 1800`            | 客户端提前拦截了超限请求                | 改为 ≤1800 的值                                          |
| `请求体AES加密失败` / `响应体AES解密失败`               | AES 密钥不匹配                   | 确认 YAML 中 `shell.encrypt.key` 与客户端默认密钥一致（服务端启动日志会提示） |
| `读取脚本文件失败`                                | `--exec-file` 指定的路径不存在或无法读取 | 检查文件路径（相对路径基于可执行文件目录）                                |

---

## 附录：默认常量速查

| 常量                         | 值                        | 说明                             |
| -------------------------- | ------------------------ | ------------------------------ |
| DefaultBindAddr            | `0.0.0.0:9092`           | HTTP 监听地址                      |
| DefaultConfigPath          | `./metricAgent.yml`      | 默认配置文件路径                       |
| DefaultLogPath             | `./logs/metricAgent.log` | 默认日志路径                         |
| DefaultExecTimeout         | `60`                     | 命令执行默认超时（秒）                    |
| MaxExecTimeout             | `600`                    | 命令执行服务端最大超时（秒）                 |
| ClientMaxExecTimeout       | `1800`                   | 指令模式客户端超时上限（秒）                 |
| DefaultReloadScriptTimeout | `60`                     | 配置重载脚本默认超时（秒）                  |
| DefaultHealthCheckTimeout  | `60`                     | 守护健康检查默认超时（秒）                  |
| DefaultStartScriptTimeout  | `120`                    | 守护启动脚本默认超时（秒）                  |
| DefaultScheduleTimeout     | `30`                     | 定时任务执行默认超时（秒）                  |
| DefaultForwardTimeout      | `30`                     | 请求透传默认超时（秒）                    |
| MaxForwardTimeout          | `120`                    | 请求透传最大超时（秒）                    |
| DefaultPullIntervalMinutes | `1`                      | Nacos 配置定时拉取默认间隔（分钟）           |
| DefaultMaxUploadSize       | `104857600`              | 单文件最大上传 100MB                  |
| MaxRequestBodyBytes        | `10485760`               | HTTP 请求体最大 10MB                |
| MaxShellOutputBytes        | `1048576`                | Shell 单流输出上限 1MB               |
| ShellLogPreviewBytes       | `5120`                   | Shell 日志预览截断 5KB               |
| BackupFileSuffix           | `_agent_bak`             | 备份文件后缀                         |
| DefaultExecModeAESKey      | `7sK9p2R5zG8tB4vN`       | 指令模式客户端硬编码 AES 密钥（16字节）        |
| DefaultNacosGroup          | `DEFAULT_GROUP`          | Nacos 默认分组（yaml 未配置时回填）        |
| DefaultNacosTimeoutMs      | `5000`                   | Nacos 单次 HTTP/gRPC 请求超时（毫秒）    |
| LogMaxSizeMB               | `100`                    | 单日志文件最大 100MB                  |
| LogMaxRetainDays           | `30`                     | 日志默认保留 30 天（DailyRotator 清理协程） |
| LogRotateFilePattern       | `%s-%s-%d.log`           | 轮转文件命名模板（前缀-yyyy-MM-dd-序号.log） |
| AppVersion                 | `"0.0.1"`                | 应用版本号（构建时可通过 -ldflags 注入）      |

