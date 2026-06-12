# Knode

Knode 是面向 Kboard 的 KLESS 节点后端，基于 [kexue-aihao/kray](https://github.com/kexue-aihao/kray) 的 `kray/pkg/kless` 核心库实现。Knode 负责节点进程、配置校验、入站监听、KLESS 握手、用户同步、流量上报和 systemd 管理；Kboard 负责节点资料、用户权限、订阅下发和计费控制。

## 当前能力

- 支持 KLESS Server 公网入口模式，krayN 可以直接连接 Knode 入站端口。
- 支持旧的 TCP 转发模式，用于本地 TCP inbound 转发到上游 kray/KLESS server。
- 支持从 Kboard 控制面同步动态用户，字段兼容 `users` 和 `data.users` 返回结构。
- 支持用户凭据字段 `kless_client_id`、`kless_client_secret`，并兼容旧字段 `client_id`、`client_secret`。
- 支持向 Kboard 上报 alive、traffic、access logs 控制端点。
- 提供管理接口 `/healthz`、`/readyz`、`/metrics`、`/status`。
- 提供配置校验、上游连通性检查、样例配置生成和 KLESS 密钥生成命令。
- 提供 Linux 一键安装脚本、交互式管理菜单、systemd 服务和防火墙端口放行辅助。
- GitHub Actions 在推送 `v*` tag 后自动测试、交叉编译、打包并发布 Release。

## 模式说明

### KLESS Server 模式

正式对接 Kboard 和 krayN 时推荐使用这个模式。

```text
krayN -> Knode public inbound -> target site
```

配置特征：

- inbound `mode` 为 `kless-server`
- inbound 监听公网端口，例如 `0.0.0.0:36514`
- 需要 `server_signing_private`
- Knode 从 Kboard `users_endpoint` 同步用户凭据
- krayN 订阅中的 `server_public_key` 必须等于 Knode 配置里的 `server_signing_key`

### TCP 转发模式

这个模式主要用于调试或兼容旧架构。

```text
plain TCP inbound -> Knode as KLESS client -> upstream kray/KLESS server
```

配置特征：

- inbound `mode` 为 `tcp`
- inbound 必须指定 `upstream`
- upstream 使用 KLESS client 配置连接上游
- krayN 不应直接把这个 inbound 当作 KLESS Server 连接

## 快速开始

本地开发：

```powershell
go test ./...
go vet ./...
go run ./cmd/knode gen-keys
go run ./cmd/knode init -config knode.json
go run ./cmd/knode check -config knode.json
go run ./cmd/knode run -config knode.json
```

常用命令：

```bash
knode
knode menu
knode version
knode gen-keys
knode check -config /etc/knode/knode.json
knode check-upstreams -config /etc/knode/knode.json
knode run -config /etc/knode/knode.json
```

不带参数运行 `knode` 会尝试打开 `/usr/local/bin/knode-manager` 管理菜单。

## 一键安装

安装最新 Release：

```bash
curl -fsSL "https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh" | sudo bash -s -- install
```

升级最新 Release 并重启服务：

```bash
curl -fsSL "https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh" | sudo bash -s -- upgrade
```

打开管理菜单：

```bash
sudo knode
```

也可以直接运行远程菜单：

```bash
curl -fsSL "https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh" | sudo bash -s -- menu
```

生成或覆盖配置：

```bash
curl -fsSL "https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh" | sudo env \
  KNODE_INBOUND_MODE="kless-server" \
  KNODE_INBOUND_NAME="public-kless" \
  KNODE_INBOUND_LISTEN="0.0.0.0:36514" \
  KNODE_CLIENT_ID="kboard-node-1" \
  KNODE_CLIENT_SECRET="replace-with-client-secret" \
  KNODE_SERVER_SIGNING_KEY="replace-with-server-public-key" \
  KNODE_SERVER_SIGNING_PRIVATE="replace-with-server-private-key" \
  bash -s -- gen-config
```

注意：`install` 在已有配置且配置校验通过时会保留 `/etc/knode/knode.json`。如果需要按新的环境变量重建配置，请使用 `gen-config` 或管理菜单的“生成 Knode 配置文件”。

## 安装脚本参数

常用环境变量：

```text
KNODE_CONFIG                  config path, default /etc/knode/knode.json
KNODE_INSTALL_DIR             binary install dir, default /usr/local/bin
KNODE_MANAGER_PATH            manager shortcut path, default /usr/local/bin/knode-manager
KNODE_SYSTEMD                 set 0 to skip systemd

KNODE_NODE_ID                 runtime node id
KNODE_ADMIN_ADDR              admin listen address, default 127.0.0.1:8080
KNODE_SHUTDOWN_GRACE          graceful shutdown timeout, default 10s

KNODE_INBOUND_MODE            tcp or kless-server
KNODE_INBOUND_NAME            inbound name
KNODE_INBOUND_LISTEN          inbound listen address
KNODE_MAX_CONNECTIONS         max inbound connections

KNODE_TRANSPORT               tcp, tls, httpupgrade, httpupdate, websocket, httpstream, grpc, xhttp
KNODE_UPSTREAM_NAME           upstream name
KNODE_UPSTREAM_ADDR           upstream address for tcp/tls
KNODE_UPSTREAM_URL            upstream URL for HTTP/WebSocket/gRPC/XHTTP transports
KNODE_SERVER_NAME             TLS/server name
KNODE_CA_FILE                 CA file path
KNODE_INSECURE_SKIP_VERIFY    true or false
KNODE_HEADERS_JSON            upstream request headers JSON
KNODE_DIAL_TIMEOUT            upstream dial timeout

KNODE_CLIENT_ID               KLESS client id
KNODE_CLIENT_SECRET           KLESS client secret
KNODE_SERVER_SIGNING_KEY      KLESS server Ed25519 public key
KNODE_SERVER_SIGNING_PRIVATE  KLESS server Ed25519 private key, required for kless-server
KNODE_KLESS_CAPABILITIES      comma-separated capabilities
KNODE_MAX_FRAME_PAYLOAD       max KLESS frame payload
KNODE_PADDING_MIN             handshake padding min
KNODE_PADDING_MAX             handshake padding max
KNODE_HANDSHAKE_TIMEOUT       handshake timeout
KNODE_MAX_HANDSHAKE_SKEW      max handshake clock skew
KNODE_USERS_REFRESH_INTERVAL  dynamic users refresh interval
```

Kboard 对接变量：

```text
KBOARD_PUBLIC_URL
KBOARD_API_PREFIX
KBOARD_NODE_ID
KBOARD_NODE_SHARED_SECRET
KBOARD_KNODE_CONFIG_ENDPOINT
KBOARD_KNODE_USERS_ENDPOINT
KBOARD_KNODE_TRAFFIC_ENDPOINT
KBOARD_KNODE_ALIVE_ENDPOINT
KBOARD_KNODE_ACCESS_LOGS_ENDPOINT
KBOARD_REPORT_INTERVAL
```

## KLESS Server 配置示例

适合父节点、单节点或实际后端节点：

```json
{
  "node_id": "kboard-node-4",
  "admin": {
    "address": "127.0.0.1:8080"
  },
  "shutdown_grace": "10s",
  "kboard": {
    "public_url": "https://kboard.example.com",
    "node_id": "4",
    "node_shared_secret": "replace-with-node-shared-secret",
    "config_endpoint": "/kb-prefix/knode/control/config",
    "users_endpoint": "/kb-prefix/knode/control/users",
    "traffic_endpoint": "/kb-prefix/knode/control/traffic",
    "alive_endpoint": "/kb-prefix/knode/control/alive",
    "access_logs_endpoint": "/kb-prefix/knode/control/access-logs",
    "report_interval": "30s"
  },
  "upstreams": [],
  "inbounds": [
    {
      "name": "public-kless",
      "listen": "0.0.0.0:36514",
      "mode": "kless-server",
      "max_connections": 50000,
      "kless": {
        "client_id": "kboard-node-4",
        "client_secret": "replace-with-client-secret",
        "server_signing_key": "replace-with-server-public-key",
        "server_signing_private": "replace-with-server-private-key",
        "max_frame_payload": 16384,
        "padding_min": 0,
        "padding_max": 0,
        "handshake_timeout": "10s",
        "max_handshake_skew": "2m",
        "users_refresh_interval": "30s"
      }
    }
  ]
}
```

正常启动日志类似：

```text
knode kboard-node-4 started
inbound public-kless listening on [::]:36514
kboard users sync loaded 2 kless clients
```

## TCP 转发配置示例

适合旧链路或调试：

```json
{
  "node_id": "knode-a",
  "admin": {
    "address": "127.0.0.1:8080"
  },
  "shutdown_grace": "10s",
  "upstreams": [
    {
      "name": "kray-primary",
      "transport": "tcp",
      "address": "127.0.0.1:9000",
      "dial_timeout": "10s",
      "kless": {
        "client_id": "knode-a",
        "client_secret": "replace-with-client-secret",
        "server_signing_key": "replace-with-server-public-key",
        "max_frame_payload": 16384,
        "handshake_timeout": "10s"
      }
    }
  ],
  "inbounds": [
    {
      "name": "local-tcp",
      "listen": "127.0.0.1:7000",
      "mode": "tcp",
      "upstream": "kray-primary",
      "max_connections": 1024
    }
  ]
}
```

## Kboard 父子节点说明

Knode 只需要部署在实际后端节点，也就是父节点或单节点。子节点如果只是中转入口，通常由其他转发工具把子节点公网端口透明转发到父节点 Knode 入站端口。

```text
krayN -> child public endpoint -> TCP forward -> parent Knode public-kless
```

在这种拓扑中：

- 父节点运行 Knode，使用 `kless-server` 模式。
- 子节点不需要运行 Knode。
- Kboard 订阅应下发子节点的前台展示地址和端口。
- 父节点的 Knode 从 Kboard 拉取用户并上报流量。
- 防火墙需要放行子节点入口端口，以及父节点允许子节点访问的后端端口。

## 管理菜单

安装后执行：

```bash
sudo knode
```

菜单包含：

- 修改配置
- 安装、更新、卸载 Knode
- 启动、停止、重启、查看状态、查看日志
- 设置或取消开机自启
- 查看版本
- 升级维护脚本
- 生成 Knode 配置文件
- 放行配置中的网络端口

## 排障

配置校验：

```bash
sudo /usr/local/bin/knode check -config /etc/knode/knode.json
```

查看监听端口：

```bash
sudo ss -lntp | grep knode
sudo ss -lntp | grep 36514
```

查看日志：

```bash
sudo journalctl -u knode -f
```

常见日志含义：

```text
kboard users sync loaded 0 kless clients
```

Knode 成功请求了 Kboard users 接口，但没有加载到可用 KLESS 用户。检查用户是否启用、套餐是否包含节点、Kboard 是否下发 `kless_client_id` 和 `kless_client_secret`。

```text
kless server handshake failed: kless: authentication failed
```

客户端已经连接到 Knode，但 `client_id` 或 `client_secret` 与 Knode 动态用户表不匹配。检查 krayN 订阅和 Kboard users 接口返回是否一致。

```text
dial tcp 127.0.0.1:9000: connect: connection refused
```

当前配置仍在 TCP 转发模式，并且上游没有监听。正式 KLESS Server 节点应确认 inbound 中存在 `"mode": "kless-server"`。

```text
connectex: actively refused
```

客户端连不到目标端口，通常是公网 IP、端口、云安全组、防火墙或中转规则问题。

## 发布

推送 `v*` tag 会触发 `.github/workflows/release.yml`：

```bash
git tag v0.1.8
git push origin v0.1.8
```

Release 工作流会执行：

- `go test ./...`
- 多平台交叉编译
- 生成 `knode-*.zip`
- 生成 `knode-*.zip.dgst`
- 上传到 GitHub Releases
