#!/usr/bin/env bash
# ============================================================
# MetricAgent 管理脚本
# 适用于 Linux 监控节点，支持 systemd 管理和前台调试两种模式
#
# 使用方法:
#   ./start.sh start              前台启动（调试用，日志输出到控制台）
#   ./start.sh start-daemon       后台守护启动
#   ./start.sh stop               停止服务
#   ./start.sh restart            重启服务
#   ./start.sh status             查看运行状态
#   ./start.sh install            安装为 systemd 服务
#   ./start.sh uninstall          卸载 systemd 服务
#   ./start.sh logs               查看实时日志
#   ./start.sh exec <script>      指令模式：执行Shell脚本
#   ./start.sh health             检查健康状态
# ============================================================

set -euo pipefail

# ============ 配置区（与代码保持同步） ============
APP_NAME="metric-agent"
APP_DIR="/opt/metric-agent"
BINARY="${APP_DIR}/${APP_NAME}"
CONFIG_FILE="${APP_DIR}/metricAgent.yml"
LOG_DIR="${APP_DIR}/logs"
LOG_FILE="${LOG_DIR}/metricAgent.log"
PID_FILE="${APP_DIR}/${APP_NAME}.pid"
SERVICE_NAME="${APP_NAME}"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

# HTTP 监听地址（必须与 constants.go DefaultBindAddr 一致）
BIND_ADDR="0.0.0.0:9092"
# 健康检查 URL（用 127.0.0.1 替代 0.0.0.0，后者不是有效连接目标）
HEALTH_URL="http://127.0.0.1:9092/health"

# systemd 运行用户
USER_NAME="metric-agent"

# ============ 工具函数 ============
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_dbg()   { echo -e "${CYAN}[DEBUG]${NC} $*"; }

# ============ PID 管理 ============
get_pid() {
    # 1. PID 文件优先
    if [[ -f "${PID_FILE}" ]]; then
        local pid
        pid=$(cat "${PID_FILE}" 2>/dev/null || true)
        if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
            echo "${pid}"
            return 0
        fi
        # PID 文件过期，清理
        rm -f "${PID_FILE}"
    fi
    # 2. 进程名回退查找
    local pid
    pid=$(pgrep -f "${BINARY}" 2>/dev/null | head -1 || true)
    if [[ -n "${pid}" ]]; then
        echo "${pid}"
        return 0
    fi
    echo ""
    return 1
}

is_running() {
    local pid
    pid=$(get_pid)
    [[ -n "${pid}" ]]
}

# ============ 前置检查 ============
check_prerequisites() {
    local fatal=0

    # 二进制检查
    if [[ ! -f "${BINARY}" ]]; then
        log_error "二进制文件不存在: ${BINARY}"
        log_info "请先执行 ./build.sh 编译或检查部署路径"
        fatal=1
    fi

    # 主配置检查
    if [[ ! -f "${CONFIG_FILE}" ]]; then
        log_error "主配置文件不存在: ${CONFIG_FILE}"
        log_info "请将 metricAgent.yml 放到 ${APP_DIR}/ 目录下"
        fatal=1
    fi

    # 可选配置（缺失不致命，但提示）
    [[ ! -f "${APP_DIR}/crontab.yml" ]] && \
        log_warn "守护配置不存在: ${APP_DIR}/crontab.yml (进程守护功能将不可用)"
    [[ ! -f "${APP_DIR}/scheduledConfig.yml" ]] && \
        log_warn "定时任务配置不存在: ${APP_DIR}/scheduledConfig.yml (定时任务功能将不可用)"

    # 日志目录创建
    mkdir -p "${LOG_DIR}"
    touch "${LOG_FILE}" 2>/dev/null || {
        log_error "无法写入日志文件: ${LOG_FILE}"
        log_info "请检查目录权限: ${LOG_DIR}"
        fatal=1
    }

    if [[ "${fatal}" -eq 1 ]]; then
        exit 1
    fi
}

# ============ 启动命令 ============
do_start_foreground() {
    log_info "前台启动 MetricAgent（调试模式）..."
    check_prerequisites

    cd "${APP_DIR}"
    exec "${BINARY}" \
        --bind-addr "${BIND_ADDR}"
}

do_start_daemon() {
    log_info "后台启动 MetricAgent..."
    check_prerequisites

    if is_running; then
        local pid
        pid=$(get_pid)
        log_warn "MetricAgent 已在运行中 (PID: ${pid})"
        return 0
    fi

    cd "${APP_DIR}"
    nohup "${BINARY}" \
        --bind-addr "${BIND_ADDR}" \
        >> "${LOG_DIR}/startup.log" 2>&1 &

    local new_pid=$!
    echo "${new_pid}" > "${PID_FILE}"

    # 等待启动（最多 3 秒）
    for _ in 1 2 3; do
        if kill -0 "${new_pid}" 2>/dev/null; then
            log_info "启动成功 (PID: ${new_pid})"
            log_info "健康检查: curl -s ${HEALTH_URL}"
            return 0
        fi
        sleep 1
    done

    # 启动失败
    log_error "启动失败，请查看日志: ${LOG_DIR}/startup.log"
    rm -f "${PID_FILE}"
    return 1
}

# ============ 停止命令 ============
do_stop() {
    log_info "停止 MetricAgent..."

    if ! is_running; then
        log_warn "MetricAgent 未在运行"
        rm -f "${PID_FILE}"
        return 0
    fi

    local pid
    pid=$(get_pid)
    log_info "正在终止进程 (PID: ${pid})..."

    # SIGTERM 优雅关闭，等待最多 30 秒
    kill -TERM "${pid}" 2>/dev/null || true
    local count=0
    while kill -0 "${pid}" 2>/dev/null && [[ ${count} -lt 30 ]]; do
        sleep 1
        count=$((count + 1))
    done

    # 超时强制 kill
    if kill -0 "${pid}" 2>/dev/null; then
        log_warn "进程未响应 SIGTERM，发送 SIGKILL..."
        kill -9 "${pid}" 2>/dev/null || true
        sleep 1
    fi

    rm -f "${PID_FILE}"
    log_info "已停止"
}

# ============ 重启命令 ============
do_restart() {
    log_info "重启 MetricAgent..."
    if is_running; then
        do_stop
        sleep 2
    fi
    do_start_daemon
}

# ============ 状态命令 ============
do_status() {
    if is_running; then
        local pid
        pid=$(get_pid)
        log_info "运行中 (PID: ${pid})"

        # 健康检查
        if command -v curl &>/dev/null; then
            local health
            health=$(curl -s --max-time 3 "${HEALTH_URL}" 2>/dev/null || echo "")
            if [[ -n "${health}" ]]; then
                log_dbg "健康状态: ${health}"
            else
                log_warn "健康检查失败（HTTP连接超时）"
            fi
        fi
    else
        log_error "未运行"
        return 1
    fi
}

# ============ 安装 systemd 服务 ============
do_install() {
    log_info "安装 MetricAgent 为 systemd 服务..."

    # 创建专用运行用户
    if ! id -u "${USER_NAME}" &>/dev/null; then
        log_info "创建运行用户: ${USER_NAME}"
        useradd -r -s /sbin/nologin -d "${APP_DIR}" "${USER_NAME}" 2>/dev/null || true
    fi

    # 创建部署目录
    mkdir -p "${APP_DIR}"
    mkdir -p "${LOG_DIR}"

    # 设置目录权限
    chown -R "${USER_NAME}:${USER_NAME}" "${APP_DIR}" 2>/dev/null || true

    # 优先使用 deploy/metric-agent.service（与 deploy/ 目录保持单一来源）
    local src_service=""
    # 1) 当前目录的 deploy/metric-agent.service（build.sh 打包后放在 ./deploy/ 下）
    if [[ -f "deploy/metric-agent.service" ]]; then
        src_service="deploy/metric-agent.service"
    # 2) 脚本同目录的 metric-agent.service（直接从源码 deploy/ 目录拷贝过来用）
    elif [[ -f "$(dirname "$0")/metric-agent.service" ]]; then
        src_service="$(dirname "$0")/metric-agent.service"
    fi

    if [[ -n "${src_service}" ]]; then
        cp "${src_service}" "${SERVICE_FILE}"
        log_info "已从 ${src_service} 复制服务文件"
    else
        # 兜底：找不到源文件时用最小模板生成
        log_warn "未找到 metric-agent.service 源文件，使用内置模板生成"
        cat > "${SERVICE_FILE}" << 'SERVICEEOF'
[Unit]
Description=MetricAgent - 轻量级监控节点运维代理
After=network.target
Wants=network.target

[Service]
User=metric-agent
Group=metric-agent
WorkingDirectory=/opt/metric-agent
ExecStart=/opt/metric-agent/metric-agent --bind-addr 0.0.0.0:9092
ExecStop=/bin/kill -SIGTERM $MAINPID
TimeoutStopSec=30
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
LimitNPROC=4096
Environment=GIN_MODE=release

[Install]
WantedBy=multi-user.target
SERVICEEOF
    fi

    # 重载 + 开机自启
    systemctl daemon-reload
    systemctl enable "${SERVICE_NAME}" 2>/dev/null || true

    log_info "服务已安装，请执行以下步骤完成部署："
    echo "  1. 将二进制 + 配置文件放到 ${APP_DIR}/ 下"
    echo "  2. 检查文件权限: chown -R ${USER_NAME}:${USER_NAME} ${APP_DIR}"
    echo "  3. 启动服务:    systemctl start ${SERVICE_NAME}"
    echo "  4. 查看状态:    systemctl status ${SERVICE_NAME}"
    echo "  5. 查看日志:    journalctl -u ${SERVICE_NAME} -f"
}

# ============ 卸载 systemd 服务 ============
do_uninstall() {
    log_info "卸载 MetricAgent systemd 服务..."
    systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
    systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
    rm -f "${SERVICE_FILE}"
    systemctl daemon-reload
    log_info "服务已卸载（部署目录 ${APP_DIR}/ 保留，请手动清理）"
}

# ============ 查看日志 ============
do_logs() {
    if [[ -f "${LOG_FILE}" ]]; then
        tail -f "${LOG_FILE}"
    else
        log_error "日志文件不存在: ${LOG_FILE}"
    fi
}

# ============ 指令模式 ============
do_exec() {
    local script="$1"
    local script_args="${2:-}"

    log_info "指令模式执行: ${script}"
    cd "${APP_DIR}"

    # 默认端口 9092（与 DefaultBindAddr 一致）
    local exec_port=9092

    if [[ -n "${script_args}" ]]; then
        exec "${BINARY}" --exec "${script}" --port "${exec_port}" -- "${script_args}"
    else
        exec "${BINARY}" --exec "${script}" --port "${exec_port}"
    fi
}

# ============ 健康检查 ============
do_health() {
    if command -v curl &>/dev/null; then
        local resp
        resp=$(curl -s --max-time 5 "${HEALTH_URL}" 2>/dev/null) || {
            log_error "健康检查失败：无法连接到 ${HEALTH_URL}"
            return 1
        }
        echo "${resp}"
        echo ""
    else
        log_error "curl 命令不存在，请安装 curl"
        return 1
    fi
}

# ============ 帮助信息 ============
print_help() {
    cat << 'HELPEOF'
MetricAgent 管理脚本
====================

用法:
  ./start.sh start              前台启动（调试用）
  ./start.sh start-daemon       后台守护启动
  ./start.sh stop               停止服务
  ./start.sh restart            重启服务
  ./start.sh status             查看运行状态 + 健康检查
  ./start.sh install            安装为 systemd 服务
  ./start.sh uninstall          卸载 systemd 服务
  ./start.sh logs               查看实时日志
  ./start.sh exec <script>      指令模式执行 Shell 脚本（通过 HTTP 调用本地 agent）
  ./start.sh health             调用 /health 接口检查健康状态
  ./start.sh help               显示此帮助

配置说明（与代码保持同步）:
  部署目录:   /opt/metric-agent/
  二进制:     metric-agent
  主配置:     metricAgent.yml
  守护配置:   crontab.yml
  定时配置:   scheduledConfig.yml
  日志目录:   logs/
  日志文件:   logs/metricAgent.log
  HTTP监听:   0.0.0.0:9092
  健康检查:   http://127.0.0.1:9092/health
  PID文件:    metric-agent.pid

环境要求:
  - Linux (glibc 2.17+ 或 musl)
  - 已编译的静态二进制（CGO_ENABLED=0）
  - bash / sh
  - systemd（install/uninstall 子命令）
HELPEOF
}

# ============ 主入口 ============
case "${1:-}" in
    start)
        do_start_foreground
        ;;
    start-daemon|daemon)
        do_start_daemon
        ;;
    stop)
        do_stop
        ;;
    restart)
        do_restart
        ;;
    status)
        do_status
        ;;
    install)
        do_install
        ;;
    uninstall)
        do_uninstall
        ;;
    logs)
        do_logs
        ;;
    exec)
        if [[ -z "${2:-}" ]]; then
            log_error "请提供要执行的脚本内容"
            echo "用法: ./start.sh exec 'echo hello'"
            exit 1
        fi
        do_exec "$2" "${3:-}"
        ;;
    health)
        do_health
        ;;
    help|-h|--help)
        print_help
        ;;
    *)
        print_help
        exit 1
        ;;
esac
