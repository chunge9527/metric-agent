### 一、问题清单

#### 🔴 安全漏洞与逻辑缺陷

##### 3. Hop-by-hop 头字段名拼写错误，清理失效

- **位置**：`forward_service.go` → `hopByHopHeaders` 列表
- **问题描述**：
列表中写入的是 `Trailer`（单数），但标准 HTTP 头字段名为 `Trailers`（复数），导致该头不会被移除，违反 HTTP 代理规范，可能引发异常行为。

##### 4. 进程守护排除回环地址，导致本地组件无法被守护

- **位置**：`guardian_service.go` → `getLocalIPs()`
- **问题描述**：
采集本机 IP 时直接跳过所有回环地址（`127.0.0.0/8`、`::1`）。如果组件仅监听 `127.0.0.1`，即使配置文件中写入该 IP，也无法匹配命中，守护逻辑完全不生效。
- **影响**：仅监听回环地址的组件（如本地数据库、管理接口）异常时无法被自愈。

#### 🟠 功能 Bug

##### 8. 定时任务 HTTP 客户端超时与任务超时脱节

- **位置**：`schedule_service.go` → `NewScheduleService()`
- **问题描述**：
`http.Client.Timeout` 硬编码为 30 秒，而每个任务可配置独立的 `timeout`。如果任务配置超时大于 30 秒，会被 HTTP 客户端提前中断，配置不生效。

---


#### ⚪ 死代码与冗余

##### 12. 空包声明文件（无实际逻辑）

以下文件仅包含包声明，无任何有效代码：

- `bootstrap.go`
- `iface.go`
- `model.go`
- `filekit.go`
- `nacos_client.go`
- `service.go`
- `shell_exec.go`
- **说明**：同包下已有其他功能文件，这些空文件不属于严格错误，但属于冗余文件，建议清理或补充包级文档。

---

#### 🟢 其他细节问题

##### 14. 磁盘空间检查回退路径不严谨

- **位置**：`upload_service.go` → `UploadHandler()`
- **问题描述**：
当存储路径和父目录都不存在时，直接回退到检查当前工作目录（`.`），可能导致检查的磁盘与实际写入磁盘不一致，空间检查失效。


##### 16. 错误包装与断言一致性风险

- **位置**：`forward_service.go` → `ssrfSafeDialContext()`、`classifyForwardError()`
- **问题描述**：
通过 `fmt.Errorf("%w: ...", errForbiddenAddress)` 包装错误，依赖 `errors.Is` 识别。如果中间经过额外的错误包装（如 SDK 内部包装），`errors.Is` 可能匹配失败，导致错误分类错误。

---