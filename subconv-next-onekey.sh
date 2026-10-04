#!/usr/bin/env bash
# SubConv Next 原生安装与管理；Debian / Ubuntu amd64 / arm64，无需 Docker。
set -Eeuo pipefail

SCRIPT_VERSION="1.2.0"
SOURCE_REPO="4444654/subconv-next"
UPSTREAM_REPO="Earl9/subconv-next"
BIN="/usr/local/bin/subconv-next"
MANAGER="/usr/local/bin/scn"
CONF_DIR="/etc/subconv-next"
ENV_FILE="${CONF_DIR}/subconv-next.env"
CONFIG_JSON="${CONF_DIR}/config.json"
DATA_DIR="/var/lib/subconv-next"
SERVICE="subconv-next.service"
UNIT_FILE="/etc/systemd/system/${SERVICE}"
RUN_USER="subconv-next"
GO_DIR="/opt/subconv-next/go"
WORK_DIR=""
TRANSACTION=0
WAS_ACTIVE=0
WAS_ENABLED=0
SCN_MANAGER_MODE="${SCN_MANAGER_MODE:-0}"

info() { printf '[信息] %s\n' "$*"; }
warn() { printf '[提示] %s\n' "$*" >&2; }
die() { printf '[错误] %s\n' "$*" >&2; exit 1; }

check_system() {
  [[ ${EUID:-$(id -u)} -eq 0 ]] || die "请用 root 或 sudo bash 运行。"
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID:-}" in debian|ubuntu) ;; *) die "仅支持 Debian / Ubuntu。" ;; esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "仅支持 amd64 / arm64，当前架构：$(uname -m)" ;;
  esac
  command -v systemctl >/dev/null || die "需要 systemd，请在服务器的 SSH 终端中运行。"
  [[ -d /run/systemd/system ]] || die "systemd 未运行；此脚本适用于原生 VPS，不适用于容器内。"
}

cleanup() {
  local result=$?
  trap - EXIT ERR INT TERM
  if (( TRANSACTION )); then
    warn "操作未完成，恢复原程序、配置和服务。"
    if ! rollback; then
      warn "自动恢复未完全成功，请运行 scn repair 并检查 scn logs。"
    fi
    result=1
  fi
  [[ -z "$WORK_DIR" ]] || rm -rf -- "$WORK_DIR"
  exit "$result"
}

on_error() {
  local result=$1 line=$2
  printf '[错误] 第 %s 行失败（退出码 %s）。可运行 scn install 重试或 scn repair 修复。\n' "$line" "$result" >&2
  exit "$result"
}

install_deps() {
  local cmd missing=0
  for cmd in curl jq openssl tar gzip sha256sum useradd groupadd ss flock; do
    command -v "$cmd" >/dev/null || missing=1
  done
  [[ -s /etc/ssl/certs/ca-certificates.crt ]] || missing=1
  (( missing )) || return 0
  info "自动安装 curl、jq、证书及系统工具……"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends ca-certificates curl jq openssl tar gzip coreutils passwd iproute2 util-linux
}

download() {
  curl -fLSs --proto '=https' --proto-redir '=https' --retry 2 \
    --connect-timeout 15 --max-time 300 "$1" -o "$2"
}

write_manager() {
  # 从已加载的函数生成独立脚本，不复制 /dev/fd，也不再次下载安装脚本。
  local tmp name
  mkdir -p /usr/local/bin
  tmp=$(mktemp "${MANAGER}.XXXXXX")
  {
    printf '#!/usr/bin/env bash\nset -Eeuo pipefail\nSCN_MANAGER_MODE=1\n'
    for name in SCRIPT_VERSION SOURCE_REPO UPSTREAM_REPO BIN MANAGER CONF_DIR ENV_FILE CONFIG_JSON DATA_DIR SERVICE UNIT_FILE RUN_USER GO_DIR; do
      printf '%s=%q\n' "$name" "${!name}"
    done
    printf 'WORK_DIR=""\nTRANSACTION=0\nWAS_ACTIVE=0\nWAS_ENABLED=0\n'
    declare -f
    printf '\nmain "$@"\n'
  } > "$tmp"
  bash -n "$tmp"
  chmod 0755 "$tmp"
  mv -f -- "$tmp" "$MANAGER"
  # 少数 root PATH 不含 /usr/local/bin；不覆盖其它程序。
  if [[ ! -e /usr/bin/scn && ! -L /usr/bin/scn ]]; then
    ln -s "$MANAGER" /usr/bin/scn
  fi
}

env_get() {
  local key=$1
  [[ -f "$ENV_FILE" ]] || return 0
  awk -v key="$key" 'index($0, key "=") == 1 {value=substr($0,length(key)+2)} END {print value}' "$ENV_FILE"
}

env_set() {
  # 不 source 环境文件；URL 中的 &、引号、$ 和反斜杠都不会变成 Shell 命令。
  local key=$1 value=$2 tmp
  [[ "$key" =~ ^SUBCONV_[A-Z_]+$ ]] || die "配置项无效。"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* && "$value" != *'"'* && "$value" != *"'"* && "$value" != *"\\"* && "$value" != *[[:space:]]* ]] || die "配置值不能包含空白、引号或反斜杠。"
  tmp=$(mktemp "${CONF_DIR}/.env.XXXXXX")
  awk -v key="$key" 'index($0, key "=") != 1' "$ENV_FILE" > "$tmp"
  printf '%s=%s\n' "$key" "$value" >> "$tmp"
  chown root:"$RUN_USER" "$tmp"
  chmod 0640 "$tmp"
  mv -f -- "$tmp" "$ENV_FILE"
}

make_config() {
  getent group "$RUN_USER" >/dev/null || groupadd --system "$RUN_USER"
  id "$RUN_USER" >/dev/null 2>&1 || useradd --system --gid "$RUN_USER" --no-create-home --shell /usr/sbin/nologin "$RUN_USER"
  install -d -m 0750 -o root -g "$RUN_USER" "$CONF_DIR"
  install -d -m 0700 -o "$RUN_USER" -g "$RUN_USER" "$DATA_DIR"
  if [[ ! -e "$ENV_FILE" ]]; then
    cat > "$ENV_FILE" <<EOF
SUBCONV_HOST=127.0.0.1
SUBCONV_PORT=9876
SUBCONV_DATA_DIR=${DATA_DIR}
SUBCONV_LOG_LEVEL=info
SUBCONV_ACCESS_TOKEN=$(openssl rand -hex 24)
SUBCONV_PUBLIC_BASE_URL=
SUBCONV_PUBLIC_CONVERTER=false
SUBCONV_TRUST_PROXY_HEADERS=false
EOF
  fi
  # 由程序补齐默认值，不依赖 Raw 下载；已有配置一律保留。
  [[ -e "$CONFIG_JSON" ]] || printf '{}\n' > "$CONFIG_JSON"
  jq -e 'type == "object"' "$CONFIG_JSON" >/dev/null || die "config.json 不是有效的 JSON 对象，请修正后重试。"
  chown root:"$RUN_USER" "$ENV_FILE" "$CONFIG_JSON"
  chmod 0640 "$ENV_FILE" "$CONFIG_JSON"
  [[ -n "$(env_get SUBCONV_ACCESS_TOKEN)" ]] || env_set SUBCONV_ACCESS_TOKEN "$(openssl rand -hex 24)"
  [[ -n "$(env_get SUBCONV_HOST)" ]] || env_set SUBCONV_HOST 127.0.0.1
  [[ -n "$(env_get SUBCONV_PORT)" ]] || env_set SUBCONV_PORT 9876
  [[ -n "$(env_get SUBCONV_DATA_DIR)" ]] || env_set SUBCONV_DATA_DIR "$DATA_DIR"
  valid_port "$(env_get SUBCONV_PORT)" || die "配置中的端口无效，请修正后运行 scn repair。"
  [[ $(env_get SUBCONV_DATA_DIR) == "$DATA_DIR" ]] || die "检测到自定义数据目录，请在 unit 的 ReadWritePaths 中配置相同路径；现有配置已保留。"
}

write_unit() {
  cat > "$UNIT_FILE" <<EOF
[Unit]
Description=SubConv Next Subscription Converter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_USER}
EnvironmentFile=${ENV_FILE}
ExecStart=${BIN} serve --config ${CONFIG_JSON}
WorkingDirectory=${DATA_DIR}
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "$UNIT_FILE"
  systemctl daemon-reload
}

begin_transaction() {
  local path index=0
  [[ -n "$WORK_DIR" ]] || WORK_DIR=$(mktemp -d /tmp/subconv-next.XXXXXX)
  mkdir -m 0700 "$WORK_DIR/backup"
  for path in "$BIN" "$ENV_FILE" "$CONFIG_JSON" "$UNIT_FILE"; do
    if [[ -e "$path" ]]; then cp -a -- "$path" "$WORK_DIR/backup/$index"; fi
    index=$((index + 1))
  done
  systemctl is-active --quiet "$SERVICE" && WAS_ACTIVE=1
  systemctl is-enabled --quiet "$SERVICE" && WAS_ENABLED=1
  TRANSACTION=1
}

rollback() {
  local path index=0 failed=0
  systemctl stop "$SERVICE" >/dev/null 2>&1 || true
  for path in "$BIN" "$ENV_FILE" "$CONFIG_JSON" "$UNIT_FILE"; do
    if [[ -e "$WORK_DIR/backup/$index" ]]; then
      cp -a -- "$WORK_DIR/backup/$index" "$path" || failed=1
    else
      rm -f -- "$path" || failed=1
    fi
    index=$((index + 1))
  done
  systemctl daemon-reload || failed=1
  if (( WAS_ENABLED )); then systemctl enable "$SERVICE" >/dev/null 2>&1 || failed=1
  else systemctl disable "$SERVICE" >/dev/null 2>&1 || true; fi
  if (( WAS_ACTIVE )); then systemctl start "$SERVICE" || failed=1; fi
  TRANSACTION=0
  return "$failed"
}

release_binary() {
  local repo=$1 asset="subconv-next-linux-${ARCH}" url sums expected
  download "https://api.github.com/repos/${repo}/releases/latest" "$WORK_DIR/release.json" || return 1
  url=$(jq -er --arg name "$asset" '.assets[] | select(.name == $name) | .browser_download_url' "$WORK_DIR/release.json") || return 1
  sums=$(jq -er '.assets[] | select(.name == "checksums.txt") | .browser_download_url' "$WORK_DIR/release.json") || return 1
  [[ "$url" == "https://github.com/${repo}/releases/download/"* && "$sums" == "https://github.com/${repo}/releases/download/"* ]] || return 1
  download "$url" "$WORK_DIR/$asset" || return 1
  download "$sums" "$WORK_DIR/checksums.txt" || return 1
  expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset {print $1}' "$WORK_DIR/checksums.txt")
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || return 1
  printf '%s  %s\n' "$expected" "$WORK_DIR/$asset" | sha256sum -c - >/dev/null || return 1
  chmod 0755 "$WORK_DIR/$asset" || return 1
  "$WORK_DIR/$asset" version >/dev/null 2>&1 || return 1
  mv -f "$WORK_DIR/$asset" "$WORK_DIR/candidate" || return 1
  info "已下载并校验 ${repo} $(jq -r '.tag_name' "$WORK_DIR/release.json")。"
}

go_sufficient() {
  local version
  version=$("$1" version 2>/dev/null | awk '{sub(/^go/, "", $3); print $3}') || return 1
  [[ "$version" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || return 1
  [[ "$(printf '%s\n%s\n' "$2" "$version" | sort -V | head -n 1)" == "$2" ]]
}

ensure_go() {
  local required=$1 go_cmd filename checksum url
  go_cmd=$(command -v go || true)
  if [[ -n "$go_cmd" ]] && go_sufficient "$go_cmd" "$required"; then GO_BIN=$go_cmd; return; fi
  if [[ -x "$GO_DIR/bin/go" ]] && go_sufficient "$GO_DIR/bin/go" "$required"; then GO_BIN="$GO_DIR/bin/go"; return; fi
  info "源码编译需要 Go >= ${required}，自动安装 Go……"
  if apt-get install -y --no-install-recommends golang-go; then
    go_cmd=$(command -v go || true)
    if [[ -n "$go_cmd" ]] && go_sufficient "$go_cmd" "$required"; then GO_BIN=$go_cmd; return; fi
  fi
  download 'https://go.dev/dl/?mode=json' "$WORK_DIR/go.json"
  filename=$(jq -er --arg arch "$ARCH" '.[0].files[] | select(.os == "linux" and .arch == $arch and .kind == "archive") | .filename' "$WORK_DIR/go.json")
  checksum=$(jq -er --arg name "$filename" '.[0].files[] | select(.filename == $name) | .sha256' "$WORK_DIR/go.json")
  [[ "$filename" =~ ^go[0-9.]+\.linux-(amd64|arm64)\.tar\.gz$ && "$checksum" =~ ^[0-9a-fA-F]{64}$ ]] || die "Go 下载信息无效。"
  url="https://go.dev/dl/${filename}"
  download "$url" "$WORK_DIR/go.tar.gz"
  printf '%s  %s\n' "$checksum" "$WORK_DIR/go.tar.gz" | sha256sum -c - >/dev/null
  mkdir -p "$WORK_DIR/toolchain" /opt/subconv-next
  tar --no-same-owner -xzf "$WORK_DIR/go.tar.gz" -C "$WORK_DIR/toolchain"
  go_sufficient "$WORK_DIR/toolchain/go/bin/go" "$required" || die "Go 版本不足或无法运行。"
  rm -rf -- "$GO_DIR"
  mv "$WORK_DIR/toolchain/go" "$GO_DIR"
  GO_BIN="$GO_DIR/bin/go"
}

build_binary() {
  local required
  info "Release 不可用，改从 ${SOURCE_REPO} 源码编译（可能需要几分钟）……"
  download "https://github.com/${SOURCE_REPO}/archive/refs/heads/main.tar.gz" "$WORK_DIR/source.tar.gz"
  mkdir "$WORK_DIR/source"
  tar --no-same-owner -xzf "$WORK_DIR/source.tar.gz" -C "$WORK_DIR/source" --strip-components=1
  required=$(awk '$1 == "go" {print $2; exit}' "$WORK_DIR/source/go.mod")
  [[ "$required" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || die "源码的 Go 版本声明无效。"
  ensure_go "$required"
  (
    cd "$WORK_DIR/source"
    export CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" GOTOOLCHAIN=local
    "$GO_BIN" build -trimpath -ldflags='-s -w -X main.version=source-main' -o "$WORK_DIR/candidate" ./cmd/subconv-next
  )
  "$WORK_DIR/candidate" version >/dev/null || die "编译后的程序无法运行。"
}

prepare_binary() {
  WORK_DIR=$(mktemp -d /tmp/subconv-next.XXXXXX)
  if release_binary "$SOURCE_REPO"; then return; fi
  warn "本仓库暂无可用 Release，尝试上游 Release。"
  if release_binary "$UPSTREAM_REPO"; then return; fi
  build_binary
}

valid_port() { [[ "$1" =~ ^[0-9]{1,5}$ ]] && (( 10#$1 >= 1 && 10#$1 <= 65535 )); }

port_available() {
  local port=$1 listeners
  listeners=$(ss -H -ltn "sport = :${port}")
  [[ -z "$listeners" ]] || die "端口 ${port} 已被占用，请先释放端口或选择其它端口。"
}

health_check() {
  local host port _
  host=$(env_get SUBCONV_HOST); port=$(env_get SUBCONV_PORT)
  case "${host:-127.0.0.1}" in 0.0.0.0) host=127.0.0.1 ;; '::'|'[::]') host='[::1]' ;; esac
  [[ "$host" != *:* || "$host" == '['*']' ]] || host="[$host]"
  valid_port "${port:-9876}" || return 1
  for _ in {1..20}; do
    if systemctl is-active --quiet "$SERVICE" && curl --noproxy '*' -fsS --connect-timeout 2 --max-time 3 "http://${host:-127.0.0.1}:${port:-9876}/healthz" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}

activate() {
  systemctl enable "$SERVICE"
  systemctl restart "$SERVICE"
  if ! health_check; then
    systemctl --no-pager -l status "$SERVICE" >&2 || true
    die "服务健康检查失败；本次变更将自动恢复。详细日志：scn logs。"
  fi
  TRANSACTION=0
}

show_access() {
  local host port
  host=$(env_get SUBCONV_HOST); port=$(env_get SUBCONV_PORT)
  printf '\n监听地址：%s:%s\n' "${host:-127.0.0.1}" "${port:-9876}"
  if [[ "$host" == 0.0.0.0 ]]; then
    printf '浏览器访问：http://服务器IP:%s/（需要安全组/防火墙放行）\n' "$port"
  else
    printf '默认仅本机访问；已有 Caddy 可反代至 127.0.0.1:%s。\n' "$port"
    printf '需要 IP:端口 访问：运行 scn bind public。\n'
  fi
  printf '查看登录 Token：scn token\n管理菜单：scn\n\n'
}

install_app() {
  info "SubConv Next 原生安装 v${SCRIPT_VERSION} / ${ARCH}"
  write_manager
  info "管理命令已保存；安装中断后可用 scn install 重试。"
  install_deps
  exec 9>/run/lock/subconv-next.lock
  flock -n 9 || die "另一个安装/管理操作正在运行。"
  prepare_binary
  begin_transaction
  make_config
  if (( ! WAS_ACTIVE )); then port_available "$(env_get SUBCONV_PORT)"; fi
  # 原子替换，避免覆盖正在运行的可执行文件产生 Text file busy。
  install -m 0755 "$WORK_DIR/candidate" "${BIN}.new"
  mv -f -- "${BIN}.new" "$BIN"
  write_unit
  activate
  info "安装/更新成功，已启用开机启动。"
  "$BIN" version
  show_access
}

need_install() {
  [[ -x "$BIN" && -f "$ENV_FILE" && -f "$CONFIG_JSON" ]] || die "安装尚未完成。请运行 scn install；无需另外安装 scn。"
}

lock_operation() {
  exec 9>/run/lock/subconv-next.lock
  flock -n 9 || die "另一个安装/管理操作正在运行。"
}

repair_app() {
  if [[ ! -x "$BIN" ]]; then install_app; return; fi
  install_deps
  lock_operation
  begin_transaction
  make_config
  write_unit
  activate
  write_manager
  info "服务和管理命令已修复。"
  show_access
}

change_setting() {
  local key=$1 value=$2 current token
  need_install
  lock_operation
  if [[ "$key" == SUBCONV_PORT ]]; then
    valid_port "$value" || die "端口必须为 1–65535。"
    value=$((10#$value))
    current=$(env_get SUBCONV_PORT)
    [[ "$value" == "$current" ]] || port_available "$value"
  fi
  if [[ "$key" == SUBCONV_HOST && "$value" == 0.0.0.0 ]]; then
    token=$(env_get SUBCONV_ACCESS_TOKEN)
    [[ ${#token} -ge 24 ]] || die "公网监听需要至少 24 字符的 Token，请先运行 scn reset-token。"
  fi
  begin_transaction
  env_set "$key" "$value"
  systemctl restart "$SERVICE"
  if ! health_check; then die "修改后健康检查失败，将恢复原设置。"; fi
  TRANSACTION=0
  info "设置已更新。"
  show_access
}

set_public_url() {
  local value=${1%/}
  [[ -z "$value" || "$value" =~ ^https?://[^/?#[:space:]]+(:[0-9]+)?(/[^[:space:]]*)?$ ]] || die "请输入完整的 http:// 或 https:// 地址，留空可清除。"
  # 空环境变量不会清除 config.json 中已有的基础地址，必要时同步清除。
  if [[ -z "$value" ]]; then
    need_install
    lock_operation
    begin_transaction
    jq 'del(.service.public_base_url)' "$CONFIG_JSON" > "$WORK_DIR/config.json"
    install -m 0640 -o root -g "$RUN_USER" "$WORK_DIR/config.json" "$CONFIG_JSON"
    env_set SUBCONV_PUBLIC_BASE_URL ""
    systemctl restart "$SERVICE"
    health_check || die "修改后健康检查失败，将恢复原设置。"
    TRANSACTION=0
    info "公网基础地址已清除。"
  else change_setting SUBCONV_PUBLIC_BASE_URL "$value"; fi
}

uninstall_app() {
  local answer
  printf '卸载程序和服务，保留 %s 与 %s。\n' "$CONF_DIR" "$DATA_DIR"
  read -r -p '输入 UNINSTALL 确认：' answer || return 0
  [[ "$answer" == UNINSTALL ]] || return 0
  lock_operation
  systemctl disable --now "$SERVICE" >/dev/null 2>&1 || true
  rm -f -- "$UNIT_FILE" "$BIN" "$MANAGER"
  if [[ -L /usr/bin/scn && $(readlink /usr/bin/scn) == "$MANAGER" ]]; then rm -f /usr/bin/scn; fi
  systemctl daemon-reload
  info "已卸载。配置、数据和系统用户已保留，重新安装可继续使用。"
}

menu_action() {
  local choice=$1 value
  case "$choice" in
    1) main status ;;
    2) main start ;;
    3) main stop ;;
    4) main restart ;;
    5) main logs ;;
    6) main token ;;
    7) main reset-token ;;
    8) read -r -p '请输入端口 [1-65535]：' value; main port "$value" ;;
    9) if [[ $(env_get SUBCONV_HOST) == 0.0.0.0 ]]; then main bind local; else main bind public; fi ;;
    10) read -r -p '公网地址（留空清除）：' value; main url "$value" ;;
    11) main update ;;
    12) main repair ;;
    13) main uninstall ;;
    *) warn "无效选择。" ;;
  esac
}

menu() {
  local choice
  while true; do
    printf '\n====== SubConv Next 管理 v%s ======\n' "$SCRIPT_VERSION"
    printf '1. 状态     2. 启动     3. 停止     4. 重启\n'
    printf '5. 日志     6. Token    7. 重置 Token\n'
    printf '8. 端口     9. 本机/公网监听    10. 公网域名\n'
    printf '11. 安装/更新    12. 修复    13. 卸载    0. 退出\n'
    read -r -p '请选择：' choice || return 0
    [[ "$choice" != 0 ]] || return 0
    # 每次操作使用独立进程，失败或中断日志不会退出整个菜单。
    bash "$MANAGER" _menu_action "$choice" || warn "操作未完成，可查看提示后重试。"
    read -r -p '按回车继续……' choice || return 0
  done
}

usage() {
  cat <<'EOF'
用法：scn [命令]
  无参数             中文管理菜单
  install / update   安装或更新程序（保留已有配置、Token 和数据）
  repair             修复服务与管理命令，未安装时执行安装
  status             查看状态与访问方式
  start / stop / restart
  logs               最近 100 行日志并持续跟随，Ctrl+C 结束
  token / reset-token
  port 9876          修改端口
  bind local|public  切换本机/公网监听
  url https://sub.example.com   设置公网基础地址
  url ""             清除公网基础地址
  uninstall          卸载程序，保留配置和数据
EOF
}

main() {
  local action=${1:-} value
  case "$action" in help|-h|--help) usage; return ;; esac
  check_system
  trap cleanup EXIT
  trap 'on_error "$?" "$LINENO"' ERR
  trap 'exit 130' INT
  trap 'exit 143' TERM
  if [[ -z "$action" ]]; then
    if [[ "$SCN_MANAGER_MODE" == 1 ]]; then menu; else install_app; fi
    return
  fi
  case "$action" in
    install|update) install_app ;;
    repair) repair_app ;;
    _menu_action) menu_action "${2:-}" ;;
    status) need_install; systemctl --no-pager -l status "$SERVICE" || true; show_access ;;
    start|restart) need_install; lock_operation; systemctl "$action" "$SERVICE"; health_check || die "健康检查失败，请查看 scn logs。"; show_access ;;
    stop) need_install; lock_operation; systemctl stop "$SERVICE" ;;
    logs) need_install; journalctl -u "$SERVICE" -n 100 -f --no-pager ;;
    token) need_install; printf '登录 Token：%s\n' "$(env_get SUBCONV_ACCESS_TOKEN)" ;;
    reset-token) change_setting SUBCONV_ACCESS_TOKEN "$(openssl rand -hex 24)" ;;
    port) change_setting SUBCONV_PORT "${2:-}" ;;
    bind)
      case "${2:-}" in local) value=127.0.0.1 ;; public) value=0.0.0.0 ;; *) die "用法：scn bind local|public" ;; esac
      change_setting SUBCONV_HOST "$value"
      ;;
    url) [[ $# -eq 2 ]] || die '用法：scn url https://sub.example.com 或 scn url ""'; set_public_url "$2" ;;
    uninstall) uninstall_app ;;
    *) usage; return 2 ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
