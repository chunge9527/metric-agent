#!/usr/bin/env bash
# ============================================================
# MetricAgent 编译打包脚本
# 支持跨平台编译、配置文件同步、一键打包
#
# 用法:
#   ./build.sh                    # 编译 Linux amd64 (默认)
#   ./build.sh linux              # 编译 Linux amd64
#   ./build.sh windows            # 编译 Windows amd64
#   ./build.sh all                # 编译并打包所有支持平台
#   ./build.sh arm64              # 编译 Linux arm64
#   ./build.sh darwin             # 编译 macOS amd64
#   ./build.sh local              # 编译当前平台（含调试信息）
#   ./build.sh check-env          # 环境检查（不编译）
#
# 输出: dist/ 目录下
# ============================================================

set -euo pipefail

# ============ 配置区（与代码保持同步） ============
APP_NAME="metric-agent"
MAIN_PKG="./cmd/metric-agent"
# Go module name（go.mod 第一行 module 后面的值）
MODULE_NAME="metric-agent"
# ldflags 版本注入目标（必须与 constants.go 中 AppVersion 变量路径一致）
VERSION_LDFLAGS="-X ${MODULE_NAME}/internal/common/constant.AppVersion=$(date +%Y%m%d)-$(git rev-parse --short HEAD 2>/dev/null || echo local)"
OUTPUT_DIR="dist"
BUILD_FLAGS="-mod=mod"

# 主配置文件目录（yaml 文件与 main.go 同目录）
CONFIG_SRC_DIR="cmd/metric-agent"
# 需要复制到 dist/ 的配置文件清单（仅列实际存在的文件）
CONFIG_FILES=(
    "metricAgent.yml"
    "crontab.yml"
    "scheduledConfig.yml"
    "metricFileConfig.yml"
)

# ============ 工具函数 ============
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[BUILD]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_dbg()   { echo -e "${CYAN}  $*${NC}"; }

# ============ 环境检查 ============
check_environment() {
    local fatal=0

    # Go 版本检查（用 sed 替代 grep -oP，兼容 BSD grep / macOS）
    if ! command -v go &>/dev/null; then
        log_error "Go 未安装或不在 PATH 中"
        fatal=1
    else
        local go_version go_output
        go_output=$(go version)
        go_version=$(echo "${go_output}" | sed -n 's/.*\(go[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' || echo "unknown")
        log_info "Go版本: ${go_output}"
        # go.mod 要求 go 1.25+，低于此版本警告但不致命
        local major minor
        major=$(echo "${go_version#go}" | cut -d. -f1)
        minor=$(echo "${go_version#go}" | cut -d. -f2)
        if [[ "${major}" -lt 1 ]] || { [[ "${major}" -eq 1 ]] && [[ "${minor}" -lt 25 ]]; }; then
            log_warn "go.mod 要求 go 1.25+，当前 ${go_version} 可能不兼容"
        fi
    fi

    # go.mod 存在检查
    if [[ ! -f "go.mod" ]]; then
        log_error "go.mod 不存在，请在项目根目录执行此脚本"
        fatal=1
    fi

    # 主入口存在检查
    if [[ ! -d "${MAIN_PKG}" ]]; then
        log_error "主入口目录不存在: ${MAIN_PKG}"
        fatal=1
    fi

    # 配置源目录检查
    if [[ ! -d "${CONFIG_SRC_DIR}" ]]; then
        log_error "配置源目录不存在: ${CONFIG_SRC_DIR}"
        fatal=1
    fi

    # Git 可用性（版本注入用）
    if ! command -v git &>/dev/null; then
        log_warn "git 未安装，版本号将使用 'local' 后缀"
    fi

    if [[ "${fatal}" -eq 1 ]]; then
        exit 1
    fi
}

# ============ 编译单个平台 ============
build_platform() {
    local goos="$1"
    local goarch="$2"
    local suffix=""

    if [[ "${goos}" == "windows" ]]; then
        suffix=".exe"
    fi

    local output_file="${OUTPUT_DIR}/${APP_NAME}-${goos}-${goarch}${suffix}"
    local ldflags="-s -w ${VERSION_LDFLAGS}"

    log_info "编译 ${goos}/${goarch} → ${output_file}"
    log_dbg "CGO_ENABLED=0 GOOS=${goos} GOARCH=${goarch}"
    log_dbg "ldflags: ${VERSION_LDFLAGS}"

    CGO_ENABLED=0 \
    GOOS="${goos}" \
    GOARCH="${goarch}" \
    go build ${BUILD_FLAGS} \
        -ldflags="${ldflags}" \
        -o "${output_file}" \
        "${MAIN_PKG}"

    if [[ -f "${output_file}" ]]; then
        local size
        size=$(du -h "${output_file}" | cut -f1)
        log_info "完成: ${output_file} (${size})"
    else
        log_error "编译失败: ${goos}/${goarch}"
        return 1
    fi
}

# ============ 准备输出目录 ============
prepare_output() {
    rm -rf "${OUTPUT_DIR}"
    mkdir -p "${OUTPUT_DIR}"
    log_info "输出目录: ${OUTPUT_DIR}/"
}

# ============ 同步配置文件 ============
sync_configs() {
    local target_dir="$1"
    local copied=0 missing=0

    for fname in "${CONFIG_FILES[@]}"; do
        local src="${CONFIG_SRC_DIR}/${fname}"
        local dst="${target_dir}/${fname}"
        if [[ -f "${src}" ]]; then
            cp "${src}" "${dst}"
            copied=$((copied + 1))
            log_dbg "+ ${fname}"
        else
            missing=$((missing + 1))
            log_warn "配置文件不存在（跳过）: ${src}"
        fi
    done

    log_info "配置文件同步完成: ${copied} 个已复制, ${missing} 个缺失"

    # 确保日志目录存在（systemd WorkingDirectory 下 ./logs/）
    mkdir -p "${target_dir}/logs"
}

# ============ 打包 ============
package_release() {
    local platform="$1"
    local arch="$2"
    local suffix=""
    [[ "${platform}" == "windows" ]] && suffix=".exe"

    local pkg_name="${APP_NAME}-${platform}-${arch}"
    local staging_dir="${OUTPUT_DIR}/staging/${pkg_name}"

    rm -rf "${staging_dir}"
    mkdir -p "${staging_dir}"

    # 1. 复制二进制
    cp "${OUTPUT_DIR}/${APP_NAME}-${platform}-${arch}${suffix}" \
       "${staging_dir}/${APP_NAME}${suffix}"

    # 2. 同步配置文件
    sync_configs "${staging_dir}"

    # 3. 复制部署脚本（Linux 包才需要）
    if [[ "${platform}" == "linux" ]] && [[ -d "deploy" ]]; then
        # 排除 build.sh（构建脚本不需要在包里）
        mkdir -p "${staging_dir}/deploy"
        cp deploy/start.sh "${staging_dir}/deploy/" 2>/dev/null || true
        cp deploy/metric-agent.service "${staging_dir}/deploy/" 2>/dev/null || true
        chmod +x "${staging_dir}/deploy/start.sh" 2>/dev/null || true
    fi

    # 4. 生成 VERSION 文件
    cat > "${staging_dir}/VERSION" << EOF
MetricAgent
Version: $(date +%Y%m%d)-$(git rev-parse --short HEAD 2>/dev/null || echo local)
Platform: ${platform}/${arch}
GoVersion: $(go version)
BuildTime: $(date '+%Y-%m-%d %H:%M:%S')
EOF

    # 5. 压缩
    local archive_ext="tar.gz"
    local archive_cmd="tar czf"
    [[ "${platform}" == "windows" ]] && {
        archive_ext="zip"
        archive_cmd="zip -r -q"
    }

    local archive_file="${OUTPUT_DIR}/${pkg_name}.${archive_ext}"
    rm -f "${archive_file}"

    (
        cd "${OUTPUT_DIR}/staging"
        ${archive_cmd} "../${pkg_name}.${archive_ext}" "${pkg_name}/"
    )

    local size
    size=$(du -h "${archive_file}" | cut -f1)
    log_info "打包完成: ${archive_file} (${size})"

    rm -rf "${OUTPUT_DIR}/staging"
}

# ============ 帮助 ============
print_help() {
    cat << 'HELPEOF'
MetricAgent 编译打包脚本
=========================

用法:
  ./build.sh [target]

目标:
  linux (默认)       Linux amd64 + 打包
  windows            Windows amd64 + 打包
  darwin             macOS amd64 + 打包
  arm64              Linux arm64 + 打包
  all                编译并打包所有支持平台
  local              编译当前平台（含调试信息，不打包）
  check-env          仅做环境检查，不编译
  help / -h / --help 显示此帮助

输出:
  dist/metric-agent-{os}-{arch}       可执行文件
  dist/metric-agent-{os}-{arch}.tar.gz 部署包（含二进制 + 配置 + 部署脚本）

部署包目录结构:
  metric-agent-{os}-{arch}/
  ├── metric-agent           # 可执行文件
  ├── metricAgent.yml        # 主配置
  ├── crontab.yml            # 进程守护配置
  ├── scheduledConfig.yml    # 定时任务配置
  ├── metricFileConfig.yml   # 公共配置清单（可选）
  ├── logs/                  # 日志目录（空）
  ├── deploy/                # 部署脚本（仅Linux包）
  │   ├── start.sh
  │   └── metric-agent.service
  └── VERSION                # 版本信息

编译选项（硬编码在脚本中）:
  CGO_ENABLED=0              静态编译，无外部依赖
  -ldflags="-s -w"           去除调试信息，减小体积
  -ldflags="-X ...AppVersion" 版本号注入（日期 + git short hash）
  -mod=mod                   Go module 模式

说明:
  - 静态编译的二进制不依赖任何外部库，可直接部署到目标节点
  - 目标节点无需安装 Go 环境
  - 建议在 CI 环境中执行 all 目标生成完整发布包
HELPEOF
}

# ============ 主入口 ============
main() {
    local target="${1:-linux}"

    case "${target}" in
        check-env)
            log_info "========== 环境检查 =========="
            check_environment
            log_info "环境检查通过 ✓"
            ;;
        help|-h|--help)
            print_help
            ;;
        *)
            check_environment
            prepare_output

            case "${target}" in
                linux)
                    build_platform "linux" "amd64"
                    package_release "linux" "amd64"
                    ;;
                windows)
                    build_platform "windows" "amd64"
                    package_release "windows" "amd64"
                    ;;
                darwin)
                    build_platform "darwin" "amd64"
                    package_release "darwin" "amd64"
                    ;;
                arm64)
                    build_platform "linux" "arm64"
                    package_release "linux" "arm64"
                    ;;
                all)
                    log_info "编译所有平台..."
                    build_platform "linux" "amd64"
                    build_platform "linux" "arm64"
                    build_platform "windows" "amd64"
                    build_platform "darwin" "amd64"
                    for combo in "linux amd64" "linux arm64" "windows amd64" "darwin amd64"; do
                        local os arch
                        os=$(echo "${combo}" | cut -d' ' -f1)
                        arch=$(echo "${combo}" | cut -d' ' -f2)
                        package_release "${os}" "${arch}"
                    done
                    ;;
                local)
                    log_info "编译当前平台（含调试信息）..."
                    go build ${BUILD_FLAGS} -o "${OUTPUT_DIR}/${APP_NAME}" "${MAIN_PKG}"
                    if [[ -f "${OUTPUT_DIR}/${APP_NAME}" ]]; then
                        log_info "完成: ${OUTPUT_DIR}/${APP_NAME}"
                    fi
                    ;;
                *)
                    log_error "未知目标: ${target}"
                    print_help
                    exit 1
                    ;;
            esac

            echo ""
            log_info "========== 编译完成 =========="
            log_info "输出目录: ${OUTPUT_DIR}/"
            ls -lh "${OUTPUT_DIR}/" 2>/dev/null || true
            ;;
    esac
}

main "$@"
