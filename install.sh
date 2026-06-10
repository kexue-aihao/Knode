#!/usr/bin/env bash
set -euo pipefail

REPO="${KNODE_REPO:-kexue-aihao/Knode}"
GITHUB_API="${KNODE_GITHUB_API:-https://api.github.com}"
GITHUB_BASE="${KNODE_GITHUB_BASE:-https://github.com}"

INSTALL_DIR="${KNODE_INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="${KNODE_BIN_NAME:-knode}"
BIN_PATH="${INSTALL_DIR}/${BIN_NAME}"
CONFIG_PATH="${KNODE_CONFIG:-/etc/knode/knode.json}"
SERVICE_NAME="${KNODE_SERVICE_NAME:-knode}"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

DEFAULT_NODE_ID="${KNODE_NODE_ID:-knode-a}"
DEFAULT_ADMIN_ADDR="${KNODE_ADMIN_ADDR:-127.0.0.1:8080}"
DEFAULT_INBOUND_NAME="${KNODE_INBOUND_NAME:-local-tcp}"
DEFAULT_INBOUND_LISTEN="${KNODE_INBOUND_LISTEN:-127.0.0.1:7000}"
DEFAULT_MAX_CONNECTIONS="${KNODE_MAX_CONNECTIONS:-1024}"
DEFAULT_UPSTREAM_NAME="${KNODE_UPSTREAM_NAME:-kray-primary}"
DEFAULT_UPSTREAM_TRANSPORT="${KNODE_TRANSPORT:-tcp}"
DEFAULT_UPSTREAM_ADDRESS="${KNODE_UPSTREAM_ADDR:-127.0.0.1:9000}"
DEFAULT_UPSTREAM_URL="${KNODE_UPSTREAM_URL:-}"

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
  KNODE_INBOUND_LISTEN       local TCP listen address, default: 127.0.0.1:7000
  KNODE_ADMIN_ADDR           admin listen address, default: 127.0.0.1:8080
  KNODE_CONFIG               config path, default: /etc/knode/knode.json
  KNODE_SYSTEMD=0            install binary and config only, skip systemd

Examples:
  bash install.sh install
  bash install.sh upgrade
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

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
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

write_env_config() {
  local dir endpoint client_id
  dir="$(dirname "$CONFIG_PATH")"
  client_id="${KNODE_CLIENT_ID:-$DEFAULT_NODE_ID}"

  [ -n "${KNODE_CLIENT_SECRET:-}" ] || return 1
  [ -n "${KNODE_SERVER_SIGNING_KEY:-}" ] || return 1
  endpoint="$(endpoint_json)"
  case "$DEFAULT_MAX_CONNECTIONS" in
    ''|*[!0-9]*)
      die "KNODE_MAX_CONNECTIONS must be a positive integer"
      ;;
  esac

  install -d -m 0755 "$dir"
  cat > "$CONFIG_PATH" <<EOF
{
  "node_id": "$(json_escape "$DEFAULT_NODE_ID")",
  "admin": {
    "address": "$(json_escape "$DEFAULT_ADMIN_ADDR")"
  },
  "shutdown_grace": "10s",
  "upstreams": [
    {
      "name": "$(json_escape "$DEFAULT_UPSTREAM_NAME")",
      "transport": "$(json_escape "$DEFAULT_UPSTREAM_TRANSPORT")",
      ${endpoint},
      "dial_timeout": "10s",
      "kless": {
        "client_id": "$(json_escape "$client_id")",
        "client_secret": "$(json_escape "$KNODE_CLIENT_SECRET")",
        "server_signing_key": "$(json_escape "$KNODE_SERVER_SIGNING_KEY")",
        "max_frame_payload": 16384,
        "handshake_timeout": "10s"
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
    log "keeping existing config ${CONFIG_PATH}"
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

do_install() {
  need_root
  install_binary "${KNODE_VERSION:-}"
  ensure_config
  install_service
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

  if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
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
  if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
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

main() {
  local action="${1:-install}"
  case "$action" in
    install)
      do_install
      ;;
    upgrade|update)
      do_upgrade
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
