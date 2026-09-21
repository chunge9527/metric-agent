# MetricAgent 产品需求文档（PRD）

## 一、产品概述

### 1.1、产品定位

MetricAgent 是部署于监控节点的轻量级运维代理产品，面向 VictoriaMetrics、Promxy 等监控组件，提供统一的配置分发、监控组件生命周期管理、定时任务、进程自愈、命令执行与请求透传能力，实现监控集群节点的标准化远程运维。

### 1.2、目标用户

* 监控运维工程师

### 1.3、核心价值

* **统一纳管：** 标准化节点侧监控组件的运维操作，降低零散脚本维护成本。

* **配置自动化：** 支持远程配置下发与自动重载，提升配置变更效率。

* **自愈能力：** 进程守护机制保障监控组件高可用。

* **安全可控：** 全链路鉴权与加密传输，保障运维操作安全。

* **定时任务：** 根据配置定时执行任务。

## 二、总体运行模式

MetricAgent 以单可执行文件形式部署，支持两种 **互斥** 的运行模式，不可同时启用；未指定指令模式参数时，默认启动 HTTP 服务模式。

每个节点只启动一个MetricAgent HTTP服务，启动多个会产生不可预知问题；

### 2.1、HTTP 服务模式

**需求描述：** 常驻后台运行，对外提供 HTTP 接口，接收外部 HTTP 请求并执行对应功能；启动时加载本地基础配置，全量启用配置加载、任务执行、进程守护、请求透传、定时任务、鉴权等核心能力。

**HTTP模式参数如下：**

* --config：指定启动基础配置文件，例 --config=./metricAgent.yaml

* --bind-addr：指定网络监听配置，不传默认为0.0.0.0:9092，例 --bind-addr=0.0.0.0:9092

* --logs：指定日志文件路径，未设置，默认为./logs/metricAgent.log(agent当前目录)

**业务规则：**

指令执行模式仅支持 --exec 参数，不支持 --config、--bind-addr 等 HTTP 模式参数；同时传入两类参数时启动直接报错。

<br />

### 2.2、指令执行模式

**需求描述：** 一次性执行脚本，执行完成后进程直接退出。该模式不加载任何配置文件，不启动 HTTP 服务，也不加密，所有执行脚本参数均通过 命令行传入，参数 --exec，返回指令标准输出，指令执行失败返回错误信息。

**业务规则：**

* 指令执行模式仅支持 --exec、--target参数，不支持 --config、--bind-addr 等 HTTP 模式参数；同时传入两类参数时启动直接报错。

* --exec 参数值作为完整 shell 脚本字符串传入。若脚本包含空格、引号等特殊字符，推荐使用 --exec-file 参数指定脚本文件路径（该文件内容作为脚本执行）。

* --port 参数值指定端口，不传使用默认值9092。

* --timeout 参数值指定超时时间，不传默认60秒，单位秒。

* 接收到--exec和--port，组装HTTP POST请求，向[http\://127.0.0.1:port/api/v1/exec，](http://127.0.0.1:port/api/v1/exec，)消息体：  
  {  
  "script": --exec 参数值,  
  "timeout": --timeout参数值  
  }，消息体使用aes加密模块加密，接口返回值使用aes模块解密，密钥使用7sK9p2R5zG8tB4vN1qX6dF3hJ7cM0aS2

* 指令执行模式使用当前系统用户权限运行，工作目录为当前目录，执行完成后进程退出，退出码 0 表示成功，非 0 表示失败。

* --help 参数优先级最高，与其他参数同时出现时仅输出帮助并退出。

## 三、详细功能需求

### 3.1、部署与启动

#### 3.1.1、单文件交付

**需求描述：**  产品整体打包为单个可执行文件，无需额外依赖安装，拷贝即可部署。

**业务规则：**

* 通过启动参数区分运行模式，未传入指令模式专属参数`--exec`时，默认启动 HTTP 服务模式。

* 两种运行模式参数集相互独立、互不兼容，同时传入两类参数时启动直接报错。

* 支持 `--help` 参数输出完整参数说明与用法示例。

* `--config`相对路径以**二进制可执行文件所在目录**为基准，而非用户执行命令的工作目录；支持绝对路径。

**异常处理：**

* 启动参数缺失、格式错误或不合法时，进程直接退出并输出明确的错误提示。

* 端口占用、二进制缺少执行权限、配置文件无读取权限，进程退出并输出精准错误提示。

#### **3.1.2、HTTP 服务模式启动**

**需求描述：**  以 HTTP 服务模式启动时，支持通过参数指定基础配置文件路径。

**业务规则：**

* 默认基础配置文件 ./metricAgent.yaml，支持通过启动参数 --config 自定义配置文件路径，支持绝对路径与相对路径，相对路径指 MetricAgent 所在目录。

* 基础配置文件 metricAgent.yaml 必须包含 agent_id 字段，作为当前节点的唯一标识；未配置该字段时启动直接报错，agent_id 只允许字母、数字、下划线和横线。

* 启动时自动加载并解析配置文件，完成 Nacos 连接、鉴权密钥初始化等操作。

* 若启动时Nacos 连接失败，打印错误日志，并每隔1分钟尝试重连，不影响继续启动，依然提供配置分发、命令执行、透传、定时任务、进程守护等能力。

**异常处理：**

* 配置文件不存在时，启动失败并提示文件路径错误

* 配置文件格式校验不通过、必填字段缺失时，启动失败并提示具体错误项

### 3.2、配置加载

#### 3.2.1、Nacos 配置中心对接

**需求描述：** 支持对接 Nacos 配置中心，从Nacos拉取配置。

**业务规则：**

* Nacos 服务地址、默认namespace、默认group、username、password、timeout、notLoadCacheAtStart在本地基础配置文件 metricAgent.yaml 中配置；

#### 3.2.2、配置清单拉取

**需求描述：** 从nacos拉取个性化配置，个性化配置拉取失败、配置内容为空或空数组时，降级拉取公共配置

**业务规则：**

* Agent 初始化时，开启定时任务，由定时任务从nacos优先拉取个性化配置清单，如果个性化配置清单拉取失败、配置内容为空或空数组时，则降级拉取公共配置清单，如果公共配置拉取失败、配置内容为空或空数组时，打印告警日志，本次任务执行结束；Agent 初始化不执行拉取配置清单功能；

* 定时任务执行时间间隔配置在metricAgent.yaml中，见属性config.pullInterval，单位分钟，定时任务增加**任务锁（互斥锁）**，任务执行期间拒绝下一轮调度并打印警告日志，任务执行完成打印耗时、监听配置个数、移除监听个数等日志。

* 定时任务锁增加超时时间10分钟，超时自动解锁；

  ```
  个性化配置清单dataId拼接规则：metricFileConfig_{agent.group}_{agent.id}
  公共配置清单dataId拼接规则：metricFileConfig_{agent.group}
  ```


* **配置优先级与回退策略**：

  * 个性化配置存在且内容非空时，使用个性化配置；

  * 若个性化配置拉取失败（网络错误、配置不存在、内容为空或空数组等），则拉取公共配置作为兜底；

  * 公共配置也拉取失败（网络错误、配置不存在、内容为空等），打印警告日志，维持当前监听列表不变，不改动本地文件、不修改监听注册表，本次任务执行结束。

* 个性化配置清单和公共配置清单文件都不注册监听，采用定时任务主动拉取，清单中配置的二级配置才注册监听；

* 个性化配置清单和公共配置清单都是yaml格式文件，解析内容按照yaml格式解析，解析结果必须是非 nil 数组，否则视为本次配置清单拉取失败，打印告警日志，打印配置dataid，不改动本地监听注册表，本次任务执行结束，解析成功打印当前配置加载dataid；

* 个性化配置清单和公共配置清单内容都是数组结构，支持同时管理多份监控组件配置文件，每条配置项包含5个核心字段；

  * fileName：目标配置文件名称，直接作为 Nacos 拉取的 dataId，非必填

  * group：配置文件分组，必填

  * storePath：本地存储目录路径，必填

  * enableClean：true，是否执行“配置对齐”功能，非必填，默认false

  * reFileName：文件重命名，未配置则不重命名，非必填

  * reloadScript：重载 shell 脚本，未配置则不执行，非必填，超时时间默认 60 秒，可在 metricAgent.yaml 中通过 config.reloadScript.timeout 配置

* 打印日志需要澄清当前加载的配置是个性化配置，还是公共配置；

#### 3.2.3、二级配置分发

**需求描述：** 根据配置清单中配置的多条数据，继续拉取二级配置，写入本地目录并执行重载脚本，所有二级配置都需要依赖监听完成后续配置更新。

**数据结构：**

// 本地监听注册表单条记录结构（全局并发安全，读写加锁）  
type ListenRegistryItem struct {  
GroupKey string // groupKey = namespace + '##' + group + '##' + dataId，注册表唯一主键  
Namespace string  
Group string  
DataId string  
StorePath string // 原始storePath（已自动补全路径分隔符）  
FinalName string // 计算后的最终文件名  
ReFileName string  
}  
// 监听注册表  
type ListenRegistry struct {  
sync.RWMutex  
Items map[string]*ListenRegistryItem // key: groupKey  
}

<br />

**业务规则：**

* 循环遍历配置清单读取的数据列表，执行以下处理逻辑：

  * 必填项校验不通过，跳过；

  * storePath最后一个字符不为当前操作系统文件路径分隔符，自动补全；

  * 如果fileName和group都非空且不存在于本地监听注册表，执行配置拉取、配置处理逻辑、设置监听，监听注册成功存入本地监听注册表，监听失败打印警告日志，继续处理下一条数据。

  * 如果fileName为空但group不为空，执行以下逻辑

    * 根据group查询所有配置，使用http分页接口<span style="color:rgb(31, 31, 31)">/nacos/v1/cs/configs?group={nacos.group}&appName=config.appName&pageNo=1&pageSize=100&types=yaml,json&search=blur&username=nacos.username</span>，需要循环查询出该group下所有配置列表。

    * <span style="color:rgb(31, 31, 31)">types使用cleanOrphanFile.cleanSuffix进行转换</span>

      * cleanSuffix未配置，默认为.yml和.yaml

      * nacos配置类型与cleanSuffix后缀名进行映射关系

        * .json 对应 type=json

        * .yaml 对应 type=yaml

        * .yml 对应 type=yaml

        * .xml 对应 type=xml

        * .html 对应 type=html

        * .htm 对应 type=html

        * .properties 对应 type=properties

        * .txt 对应 type=text

    * 如果配置项enableClean=true，执行 配置清理 逻辑，配置清理逻辑执行失败不影响后续逻辑。

    * 遍历查询到的配置列表，如果当前配置不存在于本地监听注册表则拉取配置、执行配置处理逻辑、注册监听，监听注册成功存入本地监听注册表，监听失败打印警告日志，继续处理下一条数据。

  * 配置监听注册注意去重，根据`groupKey`去重，使用本地监听注册表去重。

  * 任意一条数据处理逻辑异常，不影响其他数据继续处理。

* 移除监听：

  * 如果本地监听注册表有不存在于本次拉取的二级配置列表中（已通过配置必填项校验），移除nacos 监听，从本地注册表移除；

  * 执行完打印日志，当前监听个数，移除监听个数，本次配置拉取是个性化配置还是公共配置，dataid

* 监听回调函数/配置处理逻辑 执行规则如下：

  * 最终文件名拼接规则：

    * 如果reFileName不为空，拼接规则为：reFileName

    * 如果reFileName为空且dataid包含后缀.yaml、.yml、.properties、.json、.xml、.html、.htm、.txt（忽略大小写），拼接规则为：dataid，否则拼接规则为：dataid+".yml"

  * **写入机制**：

    * 如果storePath中某一级文件夹不存在，需要支持自动创建文件夹，并设置权限为755。

    * 写入采用原子写：先写入临时文件，路径： `storePath+{最终文件名}.{uuid}.tmp`。

    * 写入临时文件成功后，如果`storePath+{最终文件名}`已存在，则先备份；备份文件夹  `storePath+"bak"` ，备份文件名：`{最终文件名}`，多次更新直接覆盖旧备份；如果bak文件夹不存在，先创建文件夹并设置755权限。

    * 备份成功后，临时文件原子重命名为`{最终文件名}`，设置文件权限为755。

    * 如果reloadScript不为空，使用agent进程用户执行脚本，需设置执行超时时间，配置见metricAgent.yaml中config.reloadScript.timeout，单位秒，不考虑脚本是否执行安全，reloadScript脚本执行失败不回滚，捕获脚本 stdout/stderr，写入日志。

    * 脚本执行超时，kill 整个进程组，防止僵尸进程，脚本执行工作目录为 Agent 工作目录。

  * **失败处理与回滚**：

    * 监听回调函数执行异常，打印具体的异常日志，包括文件夹创建异常，本地写入权限不足，重命名失败等。

    * 如果执行异常，需要进行回滚，回滚逻辑如下：

      * 删除写入的tmp文件或者最终文件

      * 如果有备份文件，恢复备份文件

* 日志规范：

  * 各个失败场景日志需要带上 namespace、group、dataId、storePath

  * 本轮任务结束输出汇总日志：二级配置总条目、校验通过条目、处理成功条目、处理失败条目

**异常处理：**

* Nacos 连接失败、配置拉取失败时，记录错误日志，不终止 Agent 主进程

#### 3.2.4、配置清理

**需求描述：**

* 检查本地storePath目录下的配置文件是否与需要从nacos拉取的配置文件一致，本地多余的配置文件删除。

* 配置清理执行流程由二级配置分发功能触发，不单独使用协程实现。

**执行约束：**

* 扫描范围：

  * 主配置目录：storePath一级目录，**禁止递归遍历子目录**

  * 复用全局storePath高危目录黑名单：如果storePath命中高危黑名单（/etc、/bin、/sbin、/usr/bin），直接跳过该目录，不做任何删除动作。

**本地多余文件定义：**

* storePath一级目录下，文件后缀在cleanSuffix内，文件名不存在于当前二级配置列表的 【最终文件名】，判定为多余文件。

**业务规则：**

* 执行配置清理的配置见基础配置文件metricAgent.yaml

  ```
  # 配置清单拉取、二级配置分发、配置清理配置
  config:
    pullInterval: 1  # 小于0默认为1，单位分钟
    cleanOrphanFile:
      enable: false # 是否开启配置对齐，默认false关闭，防止误删  
      cleanFixHour: [2,4,8]   # 定点执行清理时间配置，2代表2点执行，可配置多个时间点，需排重配置重复的时间点，只支持大于0小于23的正整数，不符合要求的直接过滤，必填，若无有效配置不执行配置清理逻辑   
      cleanSuffix: [".yml",".yaml"]      # 需要清理的文件后缀，排除空字符串，必填，不配置默认为.yml、.yaml
  ```


* 如果config.cleanOrphanFile.enable=false，打印日志，执行结束。

* 如果cleanOrphanFile.cleanFixHour无有效配置或者cleanOrphanFile.cleanSuffix无有效配置，打印日志，执行结束。

* 如果storePath命中高危目录黑名单，打印日志，执行结束。

* 如果storePath目录不存在，打印日志，执行结束。

* 如果当前小时命中 cleanFixHours 且本小时尚未清理过，立即执行一次，否则不执行。

**完整执行流程：**

* 计算配置列表所有数据项最终文件名；

* 读取storePath目录下，文件后缀在cleanSuffix内的文件列表；

* 计算本地目录多余文件列表，如果不为空，逐个删除本地多余文件，删除失败打印告警日志，继续处理下一个多余文件；

* 本轮扫描结束，输出汇总日志：扫描文件数量、发现多余文件总数、成功删除数量、删除失败数量，storePath。

**边界场景处理：**

1. 文件被外部进程占用：删除失败，打印告警，等待下一轮扫描重试。

**非功能约束：**

1. 目录扫描IO不要阻塞Agent主线程，在后台goroutine执行。
2. 所有文件操作异常捕获，不panic，不终止Agent主进程。
3. Windows/Linux跨平台兼容路径分隔符处理。
4. agent退出，需要清理所有锁和定时任务。

<br />

### **3.4、指令下发与执行**

#### **3.4.1、远程 Shell 命令执行**

**需求描述**：接收外部 HTTP 请求传递的 Shell 脚本，在本地执行并返回执行结果，全程加密传输，执行用户为 Agent 运行用户。

**业务规则：**

* 加密规则：采用 AES 对称加密算法，加密密钥在 `metricAgent.yaml` 中通过 `shell.encrypt.key` 字段独立配置；**请求体内脚本内容、响应体内执行结果（标准输出、标准错误、退出码、执行耗时）加密；鉴权请求头不加密**。拒绝空脚本、纯空白字符脚本。

* 超时规则：脚本执行默认超时时间 60 秒；支持请求参数自定义超时，最大不超过 10 分钟（600s）。

* 超时参数校验：传入 ≤0 或 >600s，拒绝执行，返回参数错误。

* 超时处置：达到超时时间后，强制终止当前 Shell 进程，回收子进程资源，返回超时错误标识与已产生的输出内容。

* 返回完整结果：标准输出、标准错误、退出码、执行耗时。

**异常处理**

* 脚本解密失败、内容格式非法时，拒绝执行并返回错误。

* 脚本执行权限不足、进程异常终止时，返回对应错误原因与退出码。

#### **3.4.2、指令模式执行**

**需求描述**：Agent 支持以指令模式单次运行，执行预定义功能后直接退出，脚本执行用户为 Agent 运行用户

**业务规则**

* 通过启动参数 --exec 或 --exec-file 指定待执行的 shell 脚本，不读取任何配置文件

* 执行完成后进程自动退出，通过退出码标识执行结果（0 为成功，非 0 为失败），输出 shell 脚本执行标准输出

* 支持 -- 分隔符用于明确脚本参数边界，-- 后的所有内容作为脚本参数原样传递

* 指令模式不启用日志文件写入，仅向标准输出/标准错误输出信息

**异常处理**：指令不支持、参数缺失或错误时，输出用法说明并以错误码退出

### **3.5、进程守护**

#### **3.5.1、守护配置读取**

**需求描述**：定时读取本地守护配置文件，按配置对监控组件进行健康检查与自愈

**业务规则：**

* 守护配置文件固定路径：./crontab.yaml（默认为 Agent 工作目录）

* 读取周期为每 1 分钟（周期由metricAgent.yaml中配置决定，见crontab.interval，未配置，默认为1分钟）执行一次；新配置在下一轮巡检周期生效，**已经正在运行的健康检查 / 拉起任务继续执行完毕，不被中断**。

* 配置文件为数组结构，每个组件包含字段：

  * `componentName`：组件类型，比如 vmselect，必填

  * `ips`：组件部署的 ip，数组，必填；仅做精准 IP 匹配，不支持通配符；宿主机任意 IP 命中数组任意一项即执行守护逻辑。

  * `healthCheckScript`‑ 健康检查脚本，必填

  * `startScript`：启动脚本，非必填

* 调度防重叠：若上一轮守护检查尚未完成，新一轮调度不启动，直接跳过，并记录 WARN 日志。

**异常处理**：配置文件读取失败、格式错误时，记录日志，沿用上次有效配置，不中断守护流程

#### **3.5.2、健康检查与自愈**

**需求描述**：按配置执行健康检查，组件异常时自动执行拉起操作，具备降级与熔断能力

**业务规则：**

* 组件检查采用串行执行：按配置数组顺序依次检查每个组件。

* 先获取当前宿主机 IP，只要宿主机 IP 存在于 ips 数组的任一 IP 内，则执行该组件的 健康检查脚本healthCheckScript。

* 存活判定标准：健康检查脚本健康检查脚本退出码为 0 表示组件存活，非 0 表示组件不存活。

* 自愈逻辑：判定组件不存活时，立即执行对应 startScript 拉起组件，如果未配置`startScript则不执行，仅打印warn日志`。

* **启动脚本执行**：

  * startScript 超时时间默认 120 秒，可在基础配置 crontab.startScript.timeout 配置。

  * startScript 执行成功（脚本退出码为 0），立即再次执行健康检查，如果检查不通过，打印错误日志。

* **超时约束**：单组件健康检查脚本超时时间为 60 秒，超时直接判定为检查失败（仅记录日志），超时时间可在 metricAgent.yaml 中 crontab.healthCheck.timeout 字段独立配置。超时后强制 kill 子进程，避免僵尸进程。

* 守护逻辑后台异步运行，不阻塞 Agent 其他功能。

**异常处理**：健康检查脚本执行异常、启动脚本执行失败，均记录详细错误日志。

#### **3.5.3、守护巡检暂停与恢复**

**需求描述**：运维可通过 HTTP 接口对进程守护巡检进行临时暂停与恢复操作。暂停期间，守护巡检协程保持存活但跳过 inspect 执行，已注册的巡检配置与协程状态全部保留；恢复后立即按巡检周期恢复正常执行。典型场景：组件人工维护期间临时冻结巡检，避免健康检查误报或干扰人工操作。

**业务规则**：

* **控制接口**：通过 `GET /api/v1/guardian?action=<操作>` 调用，支持三类操作：

  | action 值 | 说明                            |
  | -------- | ----------------------------- |
  | `status` | 查询当前守护巡检状态（默认值，未传 action 时等价） |
  | `pause`  | 暂停巡检执行                        |
  | `resume` | 恢复巡检执行                        |

* **轻量级暂停**：`pause` 仅在服务内部设置暂停标记（`atomic.Bool`），**不销毁** `guardianLoop` 协程，ticker 照常触发但跳过 inspect 分支。恢复时 `resume` 置回标记，下一次 tick 触发即正常执行，无重启开销。

* **与 Start/Stop 的关系**：暂停/恢复是运行期控制，不影响完整生命周期管理。`Pause()` / `Resume()` 内部先检查 `running` 状态：服务未启动时调用直接返回 warn 日志，不写入暂停标记。`Stop()` 执行时自动重置暂停标记为 false，协程正常退出；后续 `Start()` 时也会重置暂停状态为 false。

* **幂等性**：重复 `pause` 已暂停的服务，或重复 `resume` 运行中的服务，均返回成功响应，仅日志提示"已处于 XX 状态，跳过"，不报错。

* **状态查询**：`status` 返回完整快照，包含：

  ```json
  {
    "status": {
      "running": true,
      "paused": false,
      "interval_minutes": 1,
      "last_action": "resume",
      "last_action_at": 1757251200
    }
  }
  ```


  * `running`：守护服务是否已启动（Start 后 true，Stop 后 false）

  * `paused`：当前是否处于暂停中（Pause 后 true，Resume 或 Start/Stop 后 false）

  * `interval_minutes`：巡检周期（分钟）

  * `last_action`：最近一次控制操作类型：`start` / `pause` / `resume` / `stop`

  * `last_action_at`：最近一次控制操作的 Unix 时间戳（秒）

* **鉴权**：守护控制接口纳入统一鉴权体系，需在请求头传入 `Authentication` 或 `CIB-AUTHORIZATION`，与 `metricAgent.yaml` 中 `auth.key` 精确匹配。未鉴权返回 401。

* **feature 开关兼容**：当 `feature.enableGuardian=false`（进程守护未启用）时，HTTP Handler 返回 503 Service Unavailable，提示"进程守护服务未启用"。

**线程安全设计**：

* `paused` 使用 `sync/atomic.Bool`，多线程（HTTP Handler ↔ guardianLoop）读写无锁冲突。

* `Pause()` / `Resume()` 在同一把 `mu` 锁内完成 `running` 状态检查与 `paused` CAS 操作，消除 TOCTOU 竞态（避免 Stop() 在检查与赋值之间介入导致对已停止服务写入 misleading 状态）。

* `Status()` 分别用 `mu`（读 running）和 `statusMu`（读 lastAction/lastActionAt）取快照快照值，两者解耦避免锁嵌套。

**边界场景处理**：

| 场景                    | 处理                                                                                      |
| --------------------- | --------------------------------------------------------------------------------------- |
| 服务未启动时调用 pause/resume | 返回 HTTP 400 + 错误消息"守护服务未启动，无法暂停/恢复"                                                     |
| 已暂停时重复 pause          | 幂等返回（HTTP 200），日志提示"已处于暂停状态，跳过"                                                         |
| 运行中时重复 resume         | 幂等返回（HTTP 200），日志提示"已处于运行状态，跳过"                                                         |
| Stop() + Pause() 并发   | `mu` 锁串行化，Stop 先置 running=false 后 Pause 检查拒绝，Pause 先设置后 Stop 重置 paused=false，两种路径状态最终一致 |
| 暂停期间的 ticker          | ticker 照常触发但被跳过，不累积；Resume 后下一次 tick 立即生效                                               |
| 暂停期间 Stop             | Stop() 不受 paused 影响，正常终止协程并重置 paused=false                                              |
| feature 开关未启用         | Handler 返回 HTTP 503                                                                     |

**异常处理**：

* 未知 `action` 参数值：返回 HTTP 400，提示"未知 action: xxx，可选值: pause, resume, status"。

* 使用非 GET 方法调用：返回 HTTP 405 Method Not Allowed。

* 鉴权失败：返回 HTTP 401 Unauthorized。

**实现锚点**：

| 文件                                      | 改动                                                                                                                                                                              |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/common/constant/constants.go` | 新增路由常量 `RouteGuardianControl = "/api/v1/guardian"`                                                                                                                              |
| `internal/model/api_model.go`           | 新增 `GuardianStatus` 状态快照结构体                                                                                                                                                     |
| `internal/service/guardian_service.go`  | 新增 `paused atomic.Bool` 字段、`Pause()` / `Resume()` / `IsPaused()` / `IsRunning()` / `Status()` / `setLastAction()` 方法；`Start()` / `Stop()` 增加暂停状态重置与操作日志；`guardianLoop()` 增加暂停检查 |
| `internal/bootstrap/server_init.go`     | 注册 `/api/v1/guardian` HTTP Handler，内部处理 nil 安全（guardianSvc == nil 返回 503）、method 校验、action 分发；新增 `writeJSON` 通用 JSON 响应辅助函数                                                     |

**HTTP 接口汇总**：

| 接口                               | 方法  | 鉴权       | 返回                                            |
| -------------------------------- | --- | -------- | --------------------------------------------- |
| `/api/v1/guardian?action=status` | GET | auth.key | `{"status": GuardianStatus}`                  |
| `/api/v1/guardian?action=pause`  | GET | auth.key | `{"success": true, "status": GuardianStatus}` |
| `/api/v1/guardian?action=resume` | GET | auth.key | `{"success": true, "status": GuardianStatus}` |
| `/api/v1/guardian`               | GET | auth.key | 默认等同于 `action=status`                         |

### **3.6、请求透传**

**需求描述**：提供 HTTP 请求透传能力，将请求转发至指定目标服务并返回响应，支持全类型 HTTP 请求。

**业务规则：**

* **接口格式**：[http\://Agent地址/forward?target=](http://Agent地址/forward?target=)<目标服务基础URL>，target 参数需经 URL 编码，Agent 侧自动解码。

  * target 为**基础 URL**（如 [http\://backend:9090](http://backend:9090)），实际转发目标为 undefined<原始请求路径>?<原始查询参数>。

  * 例如：Agent 收到 /forward?target=[http\://backend:9090/api/v1/query?x=1](http://backend:9090/api/v1/query?x=1)，实际转发到 [http\://backend:9090/api/v1/query?x=1](http://backend:9090/api/v1/query?x=1)。

  * 若 target 已包含查询参数，则与原始请求的查询参数合并（目标参数优先）。

* **请求保留**：透传时完整保留原请求的请求方法、请求头、请求体、cookie（除规则明确剔除的头）。

* **响应返回**：原样返回目标服务的响应状态、响应头、响应体。

* **超时**：透传默认超时时间 30 秒，可在基础配置 forward.timeout 中调整，最大 120 秒。

* **地址合法性校验规则**：

  * 仅允许 http/https 协议。

  * 禁止自动跟随重定向；若目标返回 3xx 状态，Agent 直接返回该响应，不自动跳转。

**异常处理**

* 目标地址不合法、拒绝透传并返回 403 错误

* 目标服务不可达、请求超时时，返回对应错误信息（状态码 502 或 504）

* 目标地址解析失败或包含多个 IP时，返回 403

### **3.7、定时任务**

**需求描述**：根据配置文件 ./scheduledConfig.yaml 定时查询 VictoriaMetrics 指标，并按规则写入指定文件

**业务规则：**

* VictoriaMetrics 请求地址配置在基础配置文件 metricAgent.yaml 中，字段为 victoriametrics.url。

* VictoriaMetrics 请求鉴权配置：在基础配置中通过 victoriametrics.authHeader 字段设置请求头 Authorization 的值（例如 Basic dXNlc192Tp7U01TNH02b1RkV1glc1U5dysrTXg5WjZuV3dwSU16Y1cr043a2pHTjJna2d6ZGpzPQ==）。该字段支持从环境变量引用（如 \${VM_AUTH_HEADER}），避免明文写入配置文件。

* scheduledConfig.yaml 为数组结构，支持配置多个任务，每个任务支持配置多个promql，包含以下字段：

  ```
  - name: 任务名称                      # 必填，唯一
    cron: */5 * * * *                 # 必填，标准 cron 表达式
    queryConfigs: 
      queries:   # 改成复数，数组格式
        - queryKey: cpu
          promql: sum(rate(node_cpu_seconds_total[5m]))
        - queryKey: mem
          promql: sum(node_memory_MemTotal_bytes - node_memory_MemFree_bytes)
        - queryKey: disk
          promql: sum(node_filesystem_size_bytes{fstype!~"tmpfs|overlay"})
    
    output:
      path: /cib/ctmp/services/vm_cluster/metricdata.txt   # 必填，输出文件路径
      maxFile: 100   # 非必填，单位MB


  ```


- 多个任务按配置顺序串行执行，单个任务失败不影响其他任务。

- 文件写入：支持配置本地输出路径，默认追加写入模式，支持配置覆盖模式；配置单文件最大上限，防止磁盘占满。

- 任务执行超时时间默认 30 秒，可在任务配置中通过 timeout 字段覆盖。

- 查询结果写入文件数据结构：{"queryTime":"20260828091210","queryKey":"cpu","data":[{"key1":"value1"},{"key2":"value2"}]}

- 文件滚动：写入文件大小超过maxFile（如果配置，单位MB）或者每日00:00:00点，重命名当前文件为metricdata.txt_yyyyMMdd

**异常处理**

* 单个查询语句处理失败（如 PromQL 非法、VM 请求超时、文件写入失败）不影响其他语句查询和写入文件

* 配置文件格式错误时，记录错误日志，不影响下次执行。

### 3.8、鉴权机制

**需求描述**：所有 HTTP 接口均需鉴权，保障访问安全

**业务规则：**

* 鉴权凭证通过请求头传递，支持两个字段：Authentication、CIB-AUTHORIZATION

* 凭证为字符串格式，与 metricAgent.yaml 中配置的密钥进行精确、大小写敏感匹配；两个请求头任意一个匹配通过，即鉴权成功

* 所有 HTTP 接口（含透传接口）均强制开启鉴权，无豁免接口

### 3.9、 文件上传功能

#### 需求描述：

提供 HTTP 方式的文件上传能力，支持运维管理端向节点侧远程分发文件（如配置片段、运维脚本、工具二进制等），可通过请求参数指定文件存储路径与目标文件名，作为 Nacos 配置分发机制的补充，满足临时、小批量文件的线下发需求。文件上传全链路纳入统一鉴权体系，保障操作安全可控。

#### 业务规则

1. **鉴权规则**

   * 文件上传接口强制启用鉴权，完全复用 3.7 节鉴权机制，通过`Authentication`或`CIB-AUTHORIZATION`请求头校验，无豁免接口。

   * 鉴权逻辑与其他 HTTP 接口完全一致，不单独设置鉴权规则。
2. **请求参数规则**

   * `storePath`（必填）：文件在节点本地的存储目录路径，支持绝对路径与相对路径；相对路径统一以 Agent 二进制可执行文件所在目录为基准，与配置文件路径规则保持一致。

   * `fileName`（非必填）：上传后重命名的目标文件名；未配置时，默认使用上传文件的原始文件名。

   * `overwrite`（非必填）：是否覆盖已存在的同名文件，默认值为`true`；设为`false`时，目标路径已存在同名文件则直接拒绝，不执行覆盖。
3. **文件写入规则**

   * 原子写入机制：先写入目标目录下`.tmp_`前缀的临时文件，写入完成后通过系统原子重命名替换为最终文件名，避免写入过程中程序异常导致文件损坏，与二级配置分发的写入规则一致。

   * 备份机制：目标路径已存在同名文件且允许覆盖时，先将原文件备份为`{原文件名}_agent_bak`，与原文件同目录；多次上传直接覆盖旧备份，仅保留最近 1 个备份版本。

   * 目录自动创建：`storePath`指定的目录不存在时，自动递归创建目录，目录权限默认 0755。

   * 权限控制：文件写入权限与 Agent 运行用户权限一致，不主动提权；文件权限默认 0755。
4. **大小与资源约束**

   * 默认单文件最大上传大小为 100MB，可在基础配置`metricAgent.yaml`中通过`upload.maxFileSize`字段自定义调整。

   * 超过大小限制的请求直接拒绝，不写入磁盘，避免占满节点磁盘。
5. **路径安全校验**

   * 禁止路径穿越：校验`storePath`与最终文件全路径，禁止包含`../`相对路径穿越，禁止指向系统敏感目录（如`/etc`、`/root`等，可在基础配置中配置黑名单）。

   * 路径非法时直接拒绝请求，不执行写入操作。

#### 异常处理

* 鉴权失败：返回 401 Unauthorized，提示鉴权凭证无效。

* 参数缺失：`storePath`必填参数为空时，返回 400 Bad Request，明确提示缺失参数。

* 文件大小超限：返回 413 Payload Too Large，提示单文件最大限制。

* 路径非法 / 目录穿越：返回 403 Forbidden，提示存储路径非法。

* 无写入权限：目标目录无写入权限时，返回 500 Internal Server Error，提示权限不足。

* 磁盘空间不足：写入前检测剩余空间，不足时返回 500，提示磁盘空间不足。

* 文件已存在且禁止覆盖：返回 409 Conflict，提示目标文件已存在。

## **四、非功能需求**

### **4.1、性能要求**

* 加密与解密过程低耗时，不对命令执行效率产生明显影响

* 常规 HTTP 接口（状态查询、鉴权校验等）响应耗时低于 100ms

* 进程守护调度时间误差不超过 5 秒

* 配置同步（从 Nacos 拉取到写入本地）单条平均耗时不超过 2 秒

### **4.2、可靠性要求**

* 单个功能模块异常不得导致 Agent 主进程崩溃

* 配置加载、健康检查失败均有日志记录与容错机制

* 支持异常场景下的配置回退能力，保留最近 1 个备份版本

* Nacos 断连自动重连，降级模式保证基础可用

### **4.3、兼容性要求**

* 支持主流 Linux 操作系统部署运行（CentOS 7+、Ubuntu 18.04+、Debian 10+），支持arm和amd架构

* 单可执行文件无外部依赖，可直接拷贝部署

***

## **五、日志与可观测性**

### 1. 日志格式规范

* 所有日志统一输出 **JSON 结构化日志**，禁止普通文本日志

* JSON 字段至少包含：`timestamp`(ISO 时间)、`level`(日志等级小写 debug/info/warn/error)、`msg`、`error`(异常时填充错误信息，无错误则省略)，可按需扩展业务字段

* 覆盖全场景日志：**程序启动、配置加载、任务执行、健康检查、异常捕获、退出**等 Agent 运行全流程，每个阶段输出对应结构化 JSON 日志

### 2. 日志级别与优先级定义

日志级别优先级从低到高：`debug < info < warn < error`

* 配置 `level = debug`：输出 debug、info、warn、error 全部等级日志

* 配置 `level = info`：输出 info、warn、error，丢弃 debug

* 配置 `level = warn`：输出 warn、error，丢弃 debug、info

* 配置 `level = error`：仅输出 error，丢弃 debug、info、warn

* 日志级别读取自 metricAgent.yaml `log.level`，默认值 `info`

* 该级别全局生效，stdout 与文件输出共用同一日志级别，低于该级别的日志直接丢弃不输出

### 3. 输出目标：双输出

日志**同时输出到 stdout 标准输出 + 本地日志文件**，两个输出通道并行生效，不可互斥

### 4. 日志文件路径、前缀规则

* 默认日志路径：`./logs/metricAgent.log`

* 命令行参数 `--logs`：优先级最高；示例 `--logs=/home/cib/agent/logs/metricAgent.log`

* `--logs` 参数作用：提取文件名前缀，用于轮转文件命名；不传`--logs`使用默认前缀 metricAgent

* 配置优先级：命令行`--logs` > yaml 配置 > 代码内置默认值

* 自动创建日志目录：日志所在目录不存在时，Agent 启动自动创建目录，不能抛出致命启动异常

### 5. 日志轮转策略（双触发：文件大小达到阈值 OR 每日 0 点，任一条件满足即触发轮转）

* 触发条件（满足任意一条即执行日志切割轮转）

  1. 当前日志文件大小达到 `maxFileSize`，单位 MB，默认 100MB，取自 metricAgent.yaml `log.maxFileSize`
  2. 到达每日 0 点，自动触发一次轮转（跨天切割）

* 轮转文件命名模板：`{前缀}-yyyy-MM-dd-{文件序号}.log`

* 示例文件名：`metricAgent-2026-09-07-0.log`

* 序号规则：同一天内多次轮转，序号从 0 开始递增：0,1,2...；跨天后序号重置为 0

> 举例：同一天文件写满 2 次 → metricAgent-2026-09-07-0.log、metricAgent-2026-09-07-1.log；次日 0 点轮转后新建 metricAgent-2026-09-08-0.log

### 6. 日志保留策略（新增）

* 日志文件最多保留 **30 天**，Agent 启动时 + 每次日志轮转完成后执行清理逻辑

* 清理规则：扫描日志目录下所有符合命名格式的历史日志文件，判断文件内日期，早于当前时间 30 天的日志文件直接删除

* 只清理本 Agent 产生的、匹配前缀命名规则的日志文件，不要删除目录内其他无关文件

### 7. 基础配置文件 metricAgent.yaml

```
log:
  # 日志级别：debug / info / warn / error，默认 info
  level: info
  # 单个日志文件最大大小，单位 MB，默认 100
  maxFileSize: 100
  # 日志最大保留天数，默认30天
  maxRetainDays: 30

```


### 8. 代码要求

1. Go 语言实现，适配 metricAgent 项目架构
2. 实现配置加载逻辑：yaml 配置解析 + 命令行参数解析 + 默认值合并
3. 日志封装为独立 logger 包，对外提供统一日志方法 Debug/Info/Warn/Error，自动输出 JSON 结构
4. 代码增加详细注释，附带关键调用示例
5. 增加基础单元测试：级别过滤、轮转文件命名、目录自动创建、双输出、跨天轮转、过期日志清理验证

