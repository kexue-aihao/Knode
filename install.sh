#!/usr/bin/env bash
set -euo pipefail

REPO="${KNODE_REPO:-kexue-aihao/Knode}"
GITHUB_API="${KNODE_GITHUB_API:-https://api.github.com}"
GITHUB_BASE="${KNODE_GITHUB_BASE:-https://github.com}"
GITHUB_RAW="${KNODE_GITHUB_RAW:-https://raw.githubusercontent.com}"

INSTALL_DIR="${KNODE_INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="${KNODE_BIN_NAME:-knode}"
BIN_PATH="${INSTALL_DIR}/${BIN_NAME}"
MANAGER_PATH="${KNODE_MANAGER_PATH:-/usr/local/bin/knode-manager}"
CONFIG_PATH="${KNODE_CONFIG:-/etc/knode/knode.json}"
SERVICE_NAME="${KNODE_SERVICE_NAME:-knode}"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

DEFAULT_NODE_ID="${KNODE_NODE_ID:-knode-a}"
DEFAULT_ADMIN_ADDR="${KNODE_ADMIN_ADDR:-127.0.0.1:8080}"
DEFAULT_SHUTDOWN_GRACE="${KNODE_SHUTDOWN_GRACE:-10s}"
DEFAULT_INBOUND_NAME="${KNODE_INBOUND_NAME:-local-tcp}"
DEFAULT_INBOUND_LISTEN="${KNODE_INBOUND_LISTEN:-127.0.0.1:7000}"
DEFAULT_MAX_CONNECTIONS="${KNODE_MAX_CONNECTIONS:-1024}"
DEFAULT_UPSTREAM_NAME="${KNODE_UPSTREAM_NAME:-kray-primary}"
DEFAULT_UPSTREAM_TRANSPORT="${KNODE_TRANSPORT:-tcp}"
DEFAULT_UPSTREAM_ADDRESS="${KNODE_UPSTREAM_ADDR:-127.0.0.1:9000}"
DEFAULT_UPSTREAM_URL="${KNODE_UPSTREAM_URL:-}"
DEFAULT_SERVER_NAME="${KNODE_SERVER_NAME:-}"
DEFAULT_CA_FILE="${KNODE_CA_FILE:-}"
DEFAULT_INSECURE_SKIP_VERIFY="${KNODE_INSECURE_SKIP_VERIFY:-false}"
DEFAULT_HEADERS_JSON="${KNODE_HEADERS_JSON:-}"
DEFAULT_DIAL_TIMEOUT="${KNODE_DIAL_TIMEOUT:-10s}"
DEFAULT_KLESS_CAPABILITIES="${KNODE_KLESS_CAPABILITIES:-}"
DEFAULT_MAX_FRAME_PAYLOAD="${KNODE_MAX_FRAME_PAYLOAD:-16384}"
DEFAULT_PADDING_MIN="${KNODE_PADDING_MIN:-0}"
DEFAULT_PADDING_MAX="${KNODE_PADDING_MAX:-0}"
DEFAULT_HANDSHAKE_TIMEOUT="${KNODE_HANDSHAKE_TIMEOUT:-10s}"

KBOARD_PUBLIC_URL="${KBOARD_PUBLIC_URL:-}"
KBOARD_API_PREFIX="${KBOARD_API_PREFIX:-}"
KBOARD_NODE_ID="${KBOARD_NODE_ID:-}"
KBOARD_NODE_SHARED_SECRET="${KBOARD_NODE_SHARED_SECRET:-}"
KBOARD_KNODE_CONFIG_ENDPOINT="${KBOARD_KNODE_CONFIG_ENDPOINT:-${KBOARD_API_PREFIX:+${KBOARD_API_PREFIX}/knode/control/config}}"
KBOARD_KNODE_USERS_ENDPOINT="${KBOARD_KNODE_USERS_ENDPOINT:-${KBOARD_API_PREFIX:+${KBOARD_API_PREFIX}/knode/control/users}}"
KBOARD_KNODE_TRAFFIC_ENDPOINT="${KBOARD_KNODE_TRAFFIC_ENDPOINT:-${KBOARD_API_PREFIX:+${KBOARD_API_PREFIX}/knode/control/traffic}}"
KBOARD_KNODE_ALIVE_ENDPOINT="${KBOARD_KNODE_ALIVE_ENDPOINT:-${KBOARD_API_PREFIX:+${KBOARD_API_PREFIX}/knode/control/alive}}"
KBOARD_KNODE_ACCESS_LOGS_ENDPOINT="${KBOARD_KNODE_ACCESS_LOGS_ENDPOINT:-${KBOARD_API_PREFIX:+${KBOARD_API_PREFIX}/knode/control/access-logs}}"
KBOARD_REPORT_INTERVAL="${KBOARD_REPORT_INTERVAL:-30s}"

log() {
  printf '[knode-install] %s\n' "$*"
}

die() {
  printf '[knode-install] error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Knode installer

Usage:
  bash install.sh install     Install latest Knode release
  bash install.sh upgrade     Upgrade to latest Knode release and restart service
  bash install.sh menu        Open interactive management menu
  bash install.sh start       Start systemd service
  bash install.sh stop        Stop systemd service
  bash install.sh restart     Restart systemd service
  bash install.sh logs        Show systemd logs
  bash install.sh status      Show installed and latest versions
  bash install.sh uninstall   Remove systemd service and binary
  bash install.sh help        Show this help

Kboard/env integration:
  KNODE_CLIENT_ID            KLESS client id, default: knode-a
  KNODE_CLIENT_SECRET        KLESS client secret from kray/Kboard
  KNODE_SERVER_SIGNING_KEY   kray Ed25519 public signing key
  KNODE_TRANSPORT            tcp, tls, httpupgrade, websocket, httpstream, grpc, xhttp
  KNODE_UPSTREAM_ADDR        kray address for tcp/tls, default: 127.0.0.1:9000
  KNODE_UPSTREAM_URL         kray URL for HTTP/WebSocket transports
  KNODE_HEADERS_JSON         optional JSON object for upstream request headers
  KNODE_INBOUND_LISTEN       local TCP listen address, default: 127.0.0.1:7000
  KNODE_ADMIN_ADDR           admin listen address, default: 127.0.0.1:8080
  KNODE_CONFIG               config path, default: /etc/knode/knode.json
  KNODE_SYSTEMD=0            install binary and config only, skip systemd
  KNODE_MANAGER_PATH         manager shortcut path, default: /usr/local/bin/knode-manager

Examples:
  bash install.sh install
  bash install.sh upgrade
  bash install.sh menu
  KNODE_CLIENT_SECRET=... KNODE_SERVER_SIGNING_KEY=... bash install.sh install
EOF
}

need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    die "please run as root"
  fi
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

http_get() {
  local url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --connect-timeout 20 "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$url"
  else
    die "missing required command: curl or wget"
  fi
}

http_download() {
  local url="$1"
  local output="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fL --retry 3 --connect-timeout 20 -o "$output" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "$output" "$url"
  else
    die "missing required command: curl or wget"
  fi
}

latest_tag() {
  http_get "${GITHUB_API}/repos/${REPO}/releases/latest" |
    grep -m1 '"tag_name"' |
    sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
}

install_script_url() {
  printf '%s/%s/master/install.sh' "$GITHUB_RAW" "$REPO"
}

detect_asset_suffix() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"

  [ "$os" = "Linux" ] || die "this installer currently supports Linux nodes only"

  case "$arch" in
    x86_64|amd64)
      printf 'linux-64'
      ;;
    i386|i686)
      printf 'linux-32'
      ;;
    aarch64|arm64)
      printf 'linux-arm64-v8a'
      ;;
    armv8l|armv7l|armv7*)
      printf 'linux-arm32-v7a'
      ;;
    armv6l|armv6*)
      printf 'linux-arm32-v6'
      ;;
    armv5l|armv5*)
      printf 'linux-arm32-v5'
      ;;
    mips64el|mips64le)
      printf 'linux-mips64le'
      ;;
    mips64)
      printf 'linux-mips64'
      ;;
    mipsel|mipsle)
      printf 'linux-mips32le'
      ;;
    mips)
      printf 'linux-mips32'
      ;;
    ppc64le)
      printf 'linux-ppc64le'
      ;;
    ppc64)
      printf 'linux-ppc64'
      ;;
    riscv64)
      printf 'linux-riscv64'
      ;;
    s390x)
      printf 'linux-s390x'
      ;;
    *)
      die "unsupported Linux architecture: ${arch}"
      ;;
  esac
}

installed_version() {
  if [ -x "$BIN_PATH" ]; then
    "$BIN_PATH" version 2>/dev/null | awk '{print $2}' || true
  fi
}

verify_digest() {
  local archive="$1"
  local digest_file="$2"
  local expected actual
  need_cmd sha256sum

  expected="$(awk 'NR == 1 {print $1}' "$digest_file")"
  actual="$(sha256sum "$archive" | awk '{print $1}')"
  [ -n "$expected" ] || die "empty digest file"
  [ "$expected" = "$actual" ] || die "sha256 mismatch for ${archive}"
}

install_binary() {
  local tag suffix asset tmp archive digest url
  tag="${1:-}"
  if [ -z "$tag" ]; then
    tag="$(latest_tag 2>/dev/null || true)"
  fi

  suffix="$(detect_asset_suffix)"
  asset="knode-${suffix}.zip"
  tmp="$(mktemp -d)"
  archive="${tmp}/${asset}"
  digest="${tmp}/${asset}.dgst"
  if [ -n "$tag" ]; then
    url="${GITHUB_BASE}/${REPO}/releases/download/${tag}/${asset}"
  else
    url="${GITHUB_BASE}/${REPO}/releases/latest/download/${asset}"
  fi

  cleanup_tmp() {
    rm -rf "$tmp"
  }
  trap cleanup_tmp EXIT

  need_cmd unzip
  log "latest release: ${tag:-GitHub latest}"
  log "downloading ${asset}"
  http_download "$url" "$archive"
  http_download "${url}.dgst" "$digest"
  verify_digest "$archive" "$digest"

  unzip -oq "$archive" -d "$tmp"
  [ -x "${tmp}/knode" ] || chmod +x "${tmp}/knode"
  install -d -m 0755 "$INSTALL_DIR"
  install -m 0755 "${tmp}/knode" "$BIN_PATH"
  cleanup_tmp
  trap - EXIT
  log "installed ${BIN_PATH}"
  "$BIN_PATH" version || true
}

install_manager_script() {
  local tmp url
  tmp="$(mktemp)"
  url="$(install_script_url)"
  if http_download "$url" "$tmp"; then
    install -d -m 0755 "$(dirname "$MANAGER_PATH")"
    install -m 0755 "$tmp" "$MANAGER_PATH"
    log "installed manager shortcut ${MANAGER_PATH}"
  else
    log "manager script update skipped"
  fi
  rm -f "$tmp"
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

env_config_available() {
  [ -n "${KNODE_CLIENT_SECRET:-}" ] && [ -n "${KNODE_SERVER_SIGNING_KEY:-}" ]
}

kboard_config_available() {
  [ -n "$KBOARD_PUBLIC_URL" ] &&
    [ -n "$KBOARD_NODE_ID" ] &&
    [ -n "$KBOARD_NODE_SHARED_SECRET" ] &&
    [ -n "$KBOARD_KNODE_ALIVE_ENDPOINT" ]
}

ensure_uint() {
  local value="$1"
  local name="$2"
  case "$value" in
    ''|*[!0-9]*)
      die "${name} must be a non-negative integer"
      ;;
  esac
}

endpoint_json() {
  case "$DEFAULT_UPSTREAM_TRANSPORT" in
    tcp|tls)
      printf '"address": "%s"' "$(json_escape "$DEFAULT_UPSTREAM_ADDRESS")"
      ;;
    *)
      [ -n "$DEFAULT_UPSTREAM_URL" ] || die "KNODE_UPSTREAM_URL is required for ${DEFAULT_UPSTREAM_TRANSPORT}"
      printf '"url": "%s"' "$(json_escape "$DEFAULT_UPSTREAM_URL")"
      ;;
  esac
}

upstream_optional_json() {
  if [ -n "$DEFAULT_SERVER_NAME" ]; then
    printf '      "server_name": "%s",\n' "$(json_escape "$DEFAULT_SERVER_NAME")"
  fi
  if [ -n "$DEFAULT_CA_FILE" ]; then
    printf '      "ca_file": "%s",\n' "$(json_escape "$DEFAULT_CA_FILE")"
  fi
  case "$DEFAULT_INSECURE_SKIP_VERIFY" in
    true|false)
      printf '      "insecure_skip_verify": %s,\n' "$DEFAULT_INSECURE_SKIP_VERIFY"
      ;;
    *)
      die "KNODE_INSECURE_SKIP_VERIFY must be true or false"
      ;;
  esac
  if [ -n "$DEFAULT_HEADERS_JSON" ]; then
    printf '      "headers": %s,\n' "$DEFAULT_HEADERS_JSON"
  fi
}

capabilities_json() {
  local raw item first old_ifs
  raw="$DEFAULT_KLESS_CAPABILITIES"
  [ -n "$raw" ] || return 0
  case "$raw" in
    \[*\])
      printf '%s' "$raw"
      return
      ;;
  esac

  first=1
  printf '['
  old_ifs="$IFS"
  IFS=','
  for item in $raw; do
    item="$(printf '%s' "$item" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
    [ -n "$item" ] || continue
    if [ "$first" -eq 0 ]; then
      printf ', '
    fi
    printf '"%s"' "$(json_escape "$item")"
    first=0
  done
  IFS="$old_ifs"
  printf ']'
}

kless_optional_json() {
  if [ -n "$DEFAULT_KLESS_CAPABILITIES" ]; then
    printf '        "capabilities": %s,\n' "$(capabilities_json)"
  fi
}

kboard_config_json() {
  kboard_config_available || return 0
  cat <<EOF
  "kboard": {
    "public_url": "$(json_escape "$KBOARD_PUBLIC_URL")",
    "node_id": "$(json_escape "$KBOARD_NODE_ID")",
    "node_shared_secret": "$(json_escape "$KBOARD_NODE_SHARED_SECRET")",
    "config_endpoint": "$(json_escape "$KBOARD_KNODE_CONFIG_ENDPOINT")",
    "users_endpoint": "$(json_escape "$KBOARD_KNODE_USERS_ENDPOINT")",
    "traffic_endpoint": "$(json_escape "$KBOARD_KNODE_TRAFFIC_ENDPOINT")",
    "alive_endpoint": "$(json_escape "$KBOARD_KNODE_ALIVE_ENDPOINT")",
    "access_logs_endpoint": "$(json_escape "$KBOARD_KNODE_ACCESS_LOGS_ENDPOINT")",
    "report_interval": "$(json_escape "$KBOARD_REPORT_INTERVAL")"
  },
EOF
}

backup_config() {
  local backup
  backup="${CONFIG_PATH}.bak.$(date +%Y%m%d%H%M%S)"
  cp -a "$CONFIG_PATH" "$backup"
  log "backed up existing config to ${backup}"
}

write_env_config() {
  local dir endpoint client_id
  dir="$(dirname "$CONFIG_PATH")"
  client_id="${KNODE_CLIENT_ID:-$DEFAULT_NODE_ID}"

  env_config_available || return 1
  ensure_uint "$DEFAULT_MAX_CONNECTIONS" "KNODE_MAX_CONNECTIONS"
  ensure_uint "$DEFAULT_MAX_FRAME_PAYLOAD" "KNODE_MAX_FRAME_PAYLOAD"
  ensure_uint "$DEFAULT_PADDING_MIN" "KNODE_PADDING_MIN"
  ensure_uint "$DEFAULT_PADDING_MAX" "KNODE_PADDING_MAX"
  endpoint="$(endpoint_json)"

  install -d -m 0755 "$dir"
  cat > "$CONFIG_PATH" <<EOF
{
  "node_id": "$(json_escape "$DEFAULT_NODE_ID")",
  "admin": {
    "address": "$(json_escape "$DEFAULT_ADMIN_ADDR")"
  },
  "shutdown_grace": "$(json_escape "$DEFAULT_SHUTDOWN_GRACE")",
$(kboard_config_json)  "upstreams": [
    {
      "name": "$(json_escape "$DEFAULT_UPSTREAM_NAME")",
      "transport": "$(json_escape "$DEFAULT_UPSTREAM_TRANSPORT")",
      ${endpoint},
$(upstream_optional_json)      "dial_timeout": "$(json_escape "$DEFAULT_DIAL_TIMEOUT")",
      "kless": {
        "client_id": "$(json_escape "$client_id")",
        "client_secret": "$(json_escape "$KNODE_CLIENT_SECRET")",
        "server_signing_key": "$(json_escape "$KNODE_SERVER_SIGNING_KEY")",
$(kless_optional_json)        "max_frame_payload": ${DEFAULT_MAX_FRAME_PAYLOAD},
        "padding_min": ${DEFAULT_PADDING_MIN},
        "padding_max": ${DEFAULT_PADDING_MAX},
        "handshake_timeout": "$(json_escape "$DEFAULT_HANDSHAKE_TIMEOUT")"
      }
    }
  ],
  "inbounds": [
    {
      "name": "$(json_escape "$DEFAULT_INBOUND_NAME")",
      "listen": "$(json_escape "$DEFAULT_INBOUND_LISTEN")",
      "upstream": "$(json_escape "$DEFAULT_UPSTREAM_NAME")",
      "max_connections": ${DEFAULT_MAX_CONNECTIONS}
    }
  ]
}
EOF
  chmod 0600 "$CONFIG_PATH"
}

ensure_config() {
  if [ -f "$CONFIG_PATH" ]; then
    if config_ready; then
      if kboard_config_available && ! grep -q '"kboard"[[:space:]]*:' "$CONFIG_PATH"; then
        backup_config
        write_env_config
        log "recreated config with Kboard integration: ${CONFIG_PATH}"
        return
      fi
      log "keeping existing config ${CONFIG_PATH}"
      return
    fi
    if env_config_available; then
      backup_config
      write_env_config
      log "recreated config from environment: ${CONFIG_PATH}"
      return
    fi
    log "existing config is not ready: ${CONFIG_PATH}"
    log "keeping it because Kboard/KNODE secret env is incomplete"
    return
  fi

  if write_env_config; then
    log "created config from environment: ${CONFIG_PATH}"
    return
  fi

  install -d -m 0755 "$(dirname "$CONFIG_PATH")"
  "$BIN_PATH" init -config "$CONFIG_PATH" >/dev/null || true
  chmod 0600 "$CONFIG_PATH" 2>/dev/null || true
  log "created sample config ${CONFIG_PATH}"
  log "set KNODE_CLIENT_SECRET and KNODE_SERVER_SIGNING_KEY, or edit the config before starting"
}

config_ready() {
  [ -f "$CONFIG_PATH" ] || return 1
  "$BIN_PATH" check -config "$CONFIG_PATH" >/dev/null 2>&1
}

install_service() {
  [ "${KNODE_SYSTEMD:-1}" = "1" ] || {
    log "KNODE_SYSTEMD=0, skip systemd service"
    return
  }
  command -v systemctl >/dev/null 2>&1 || {
    log "systemctl not found, skip systemd service"
    return
  }

  cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=Knode node backend
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=${BIN_PATH} run -config ${CONFIG_PATH}
Restart=on-failure
RestartSec=3s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME" >/dev/null

  if config_ready; then
    systemctl restart "$SERVICE_NAME"
    log "service ${SERVICE_NAME} started"
  else
    log "config is not ready; service is installed but not started"
    log "after updating ${CONFIG_PATH}, run: systemctl restart ${SERVICE_NAME}"
  fi
}

require_systemctl() {
  command -v systemctl >/dev/null 2>&1 || die "systemctl not found"
}

service_exists() {
  command -v systemctl >/dev/null 2>&1 || return 1
  systemctl list-unit-files "${SERVICE_NAME}.service" 2>/dev/null | grep -q "^${SERVICE_NAME}.service"
}

service_state() {
  if ! service_exists; then
    printf '服务未安装'
    return
  fi
  if systemctl is-active --quiet "$SERVICE_NAME"; then
    printf '已运行'
  else
    printf '未运行'
  fi
}

autostart_state() {
  if service_exists && systemctl is-enabled --quiet "$SERVICE_NAME"; then
    printf '是'
  else
    printf '否'
  fi
}

choose_editor() {
  if [ -n "${EDITOR:-}" ] && command -v "$EDITOR" >/dev/null 2>&1; then
    printf '%s' "$EDITOR"
    return
  fi
  for editor in nano vim vi; do
    if command -v "$editor" >/dev/null 2>&1; then
      printf '%s' "$editor"
      return
    fi
  done
  return 1
}

read_tty() {
  local prompt="$1"
  local answer=""
  if [ "${KNODE_MENU_TTY:-1}" != "0" ] && [ -e /dev/tty ] && { : </dev/tty; } 2>/dev/null; then
    printf '%s' "$prompt" >/dev/tty
    IFS= read -r answer </dev/tty || true
  else
    printf '%s' "$prompt" >&2
    IFS= read -r answer || true
  fi
  answer="${answer%$'\r'}"
  printf '%s' "$answer"
}

pause_menu() {
  read_tty "按回车键继续..." >/dev/null
}

confirm() {
  local prompt answer
  prompt="$1"
  answer="$(read_tty "${prompt} [y/N]: ")"
  case "$answer" in
    y|Y|yes|YES)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

do_edit_config() {
  need_root
  local editor
  if [ ! -f "$CONFIG_PATH" ]; then
    install -d -m 0755 "$(dirname "$CONFIG_PATH")"
    if [ -x "$BIN_PATH" ]; then
      "$BIN_PATH" init -config "$CONFIG_PATH" >/dev/null || true
    else
      : > "$CONFIG_PATH"
    fi
    chmod 0600 "$CONFIG_PATH" 2>/dev/null || true
  fi
  editor="$(choose_editor)" || die "no editor found; install nano/vim/vi or set EDITOR"
  "$editor" "$CONFIG_PATH"
  if config_ready; then
    log "config check passed"
    if service_exists && confirm "是否重启 ${SERVICE_NAME} 使配置生效"; then
      systemctl restart "$SERVICE_NAME"
      log "service ${SERVICE_NAME} restarted"
    fi
  else
    log "config check failed; run: ${BIN_PATH} check -config ${CONFIG_PATH}"
  fi
}

do_generate_config() {
  need_root
  if [ -f "$CONFIG_PATH" ]; then
    if confirm "配置文件已存在，是否备份并覆盖 ${CONFIG_PATH}"; then
      backup_config
    else
      log "cancelled"
      return
    fi
  fi
  if write_env_config; then
    log "created config from environment: ${CONFIG_PATH}"
  elif [ -x "$BIN_PATH" ]; then
    install -d -m 0755 "$(dirname "$CONFIG_PATH")"
    "$BIN_PATH" init -config "$CONFIG_PATH" >/dev/null || true
    chmod 0600 "$CONFIG_PATH" 2>/dev/null || true
    log "created sample config ${CONFIG_PATH}"
  else
    die "Knode binary is not installed; install first"
  fi
  if config_ready; then
    log "config check passed"
  else
    log "config needs editing before service start"
  fi
}

do_start() {
  need_root
  require_systemctl
  service_exists || install_service
  config_ready || die "config is not ready: ${CONFIG_PATH}"
  systemctl start "$SERVICE_NAME"
  log "service ${SERVICE_NAME} started"
}

do_stop() {
  need_root
  require_systemctl
  service_exists || die "service ${SERVICE_NAME} is not installed"
  systemctl stop "$SERVICE_NAME"
  log "service ${SERVICE_NAME} stopped"
}

do_restart() {
  need_root
  require_systemctl
  service_exists || install_service
  config_ready || die "config is not ready: ${CONFIG_PATH}"
  systemctl restart "$SERVICE_NAME"
  log "service ${SERVICE_NAME} restarted"
}

do_enable() {
  need_root
  require_systemctl
  service_exists || install_service
  systemctl enable "$SERVICE_NAME" >/dev/null
  log "service ${SERVICE_NAME} enabled"
}

do_disable() {
  need_root
  require_systemctl
  service_exists || die "service ${SERVICE_NAME} is not installed"
  systemctl disable "$SERVICE_NAME" >/dev/null
  log "service ${SERVICE_NAME} disabled"
}

do_logs() {
  require_systemctl
  service_exists || die "service ${SERVICE_NAME} is not installed"
  journalctl -u "$SERVICE_NAME" -e --no-pager -n "${KNODE_LOG_LINES:-200}"
}

do_version() {
  local latest current
  latest="$(latest_tag 2>/dev/null || true)"
  current="$(installed_version || true)"
  printf '当前版本: %s\n' "${current:-未安装}"
  printf '最新版本: %s\n' "${latest:-unknown}"
  if [ -x "$BIN_PATH" ]; then
    "$BIN_PATH" version || true
  fi
}

config_ports() {
  [ -f "$CONFIG_PATH" ] || return 0
  grep -E '"(listen|address)"[[:space:]]*:' "$CONFIG_PATH" |
    sed -nE 's/.*"[^"]*:([0-9]+)".*/\1/p' |
    sort -n -u
}

open_port() {
  local port="$1"
  if command -v ufw >/dev/null 2>&1; then
    ufw allow "${port}/tcp"
    return
  fi
  if command -v firewall-cmd >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="${port}/tcp"
    firewall-cmd --reload
    return
  fi
  if command -v iptables >/dev/null 2>&1; then
    iptables -C INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null ||
      iptables -I INPUT -p tcp --dport "$port" -j ACCEPT
    return
  fi
  die "no supported firewall tool found: ufw/firewall-cmd/iptables"
}

do_open_ports() {
  need_root
  local ports port
  ports="$(config_ports)"
  [ -n "$ports" ] || die "no ports found in ${CONFIG_PATH}"
  printf '%s\n' "$ports" | while IFS= read -r port; do
    [ -n "$port" ] || continue
    log "allow tcp/${port}"
    open_port "$port"
  done
  log "firewall rules updated"
}

do_install() {
  need_root
  install_binary "${KNODE_VERSION:-}"
  ensure_config
  install_service
  install_manager_script
}

do_upgrade() {
  need_root
  local tag current
  tag="$(latest_tag 2>/dev/null || true)"
  current="$(installed_version)"

  if [ -z "$current" ]; then
    log "Knode is not installed; running install"
    do_install
    return
  fi

  if [ -n "$tag" ] && [ "$current" = "$tag" ]; then
    log "already on latest version ${tag}"
  else
    log "upgrading ${current:-not-installed} -> ${tag:-GitHub latest}"
    install_binary "$tag"
  fi
  install_manager_script

  if command -v systemctl >/dev/null 2>&1; then
    if ! service_exists; then
      ensure_config
      install_service
      return
    fi
    if config_ready; then
      systemctl restart "$SERVICE_NAME"
      log "service ${SERVICE_NAME} restarted"
    else
      log "config is not ready; binary upgraded but service not restarted"
    fi
  fi
}

do_status() {
  local latest current
  latest="$(latest_tag 2>/dev/null || true)"
  current="$(installed_version || true)"
  printf 'repo=%s\n' "$REPO"
  printf 'latest=%s\n' "${latest:-unknown}"
  printf 'installed=%s\n' "${current:-not-installed}"
  printf 'binary=%s\n' "$BIN_PATH"
  printf 'config=%s\n' "$CONFIG_PATH"
  printf 'service=%s\n' "$(service_state)"
  printf 'autostart=%s\n' "$(autostart_state)"
  if service_exists; then
    systemctl --no-pager --full status "$SERVICE_NAME" || true
  fi
}

do_uninstall() {
  need_root
  if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
    systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
    rm -f "$SERVICE_PATH"
    systemctl daemon-reload
  fi
  rm -f "$BIN_PATH"
  log "removed ${BIN_PATH}"
  log "kept config ${CONFIG_PATH}"
}

show_menu() {
  local current latest state autostart
  current="$(installed_version || true)"
  latest="$(latest_tag 2>/dev/null || true)"
  state="$(service_state)"
  autostart="$(autostart_state)"

  if [ -t 1 ]; then
    clear || true
  fi
  cat <<EOF
Knode 节点后端管理脚本
------ github.com/kexue-aihao/Knode ------
0. 修改配置
-------------------------------------------
1. 安装 Knode
2. 更新 Knode
3. 卸载 Knode
-------------------------------------------
4. 启动 Knode
5. 停止 Knode
6. 重启 Knode
7. 查看 Knode 状态
8. 查看 Knode 日志
-------------------------------------------
9. 设置 Knode 开机自启
10. 取消 Knode 开机自启
-------------------------------------------
11. 查看 Knode 版本
12. 升级 Knode 维护脚本
13. 生成 Knode 配置文件
14. 放行 Knode 配置中的网络端口
15. 退出脚本

Knode服务状态: ${state}
是否开机自启: ${autostart}
当前版本: ${current:-未安装}
最新版本: ${latest:-unknown}
配置文件: ${CONFIG_PATH}
EOF
}

run_menu_action() {
  local choice="$1"
  case "$choice" in
    0)
      do_edit_config
      ;;
    1)
      do_install
      ;;
    2)
      do_upgrade
      ;;
    3)
      if confirm "确认卸载 Knode"; then
        do_uninstall
      else
        log "cancelled"
      fi
      ;;
    4)
      do_start
      ;;
    5)
      do_stop
      ;;
    6)
      do_restart
      ;;
    7)
      do_status
      ;;
    8)
      do_logs
      ;;
    9)
      do_enable
      ;;
    10)
      do_disable
      ;;
    11)
      do_version
      ;;
    12)
      need_root
      install_manager_script
      ;;
    13)
      do_generate_config
      ;;
    14)
      do_open_ports
      ;;
    15|q|Q|exit)
      exit 0
      ;;
    *)
      log "unknown option: ${choice}"
      ;;
  esac
}

do_menu() {
  local choice
  while true; do
    show_menu
    choice="$(read_tty "请输入选项 [0-15]: ")"
    printf '\n'
    if ! run_menu_action "$choice"; then
      log "operation failed"
    fi
    printf '\n'
    pause_menu
  done
}

main() {
  local action="${1:-install}"
  case "$action" in
    install)
      do_install
      ;;
    upgrade|update)
      do_upgrade
      ;;
    menu|manage)
      do_menu
      ;;
    edit-config|config)
      do_edit_config
      ;;
    start)
      do_start
      ;;
    stop)
      do_stop
      ;;
    restart)
      do_restart
      ;;
    logs|log)
      do_logs
      ;;
    enable)
      do_enable
      ;;
    disable)
      do_disable
      ;;
    version)
      do_version
      ;;
    update-script)
      need_root
      install_manager_script
      ;;
    gen-config)
      do_generate_config
      ;;
    open-ports)
      do_open_ports
      ;;
    status)
      do_status
      ;;
    uninstall|remove)
      do_uninstall
      ;;
    help|-h|--help)
      usage
      ;;
    *)
      usage
      die "unknown action: ${action}"
      ;;
  esac
}

main "$@"
