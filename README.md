# Knode

Knode 是 Kboard 面板的配套节点后端。当前实现依照
[kexue-aihao/kray](https://github.com/kexue-aihao/kray) 的 KLESS 核心库构建：
Knode 负责节点生命周期、配置加载、连接管理、健康检查和转发，KLESS 握手、认证、
密钥派生与记录层加密全部交给 `kray/pkg/kless`。

## 功能

- 本地 TCP 入站监听，并将业务流量通过 KLESS 安全连接转发到 kray 上游。
- 支持 TCP、TLS、HTTPUpgrade/HTTPUpdate、WebSocket、HTTPStream、gRPC、XHTTP 上游传输配置。
- 提供 `/healthz`、`/readyz`、`/metrics`、`/status` 管理接口。
- 提供配置校验、上游连通性检查、样例配置生成和 KLESS 密钥生成命令。
- GitHub Actions 支持推送 `v*` tag 后自动测试、交叉编译、打包并发布 Release。

## 快速开始

```powershell
go test ./...
go run ./cmd/knode gen-keys
go run ./cmd/knode init -config knode.json
go run ./cmd/knode check -config knode.json
go run ./cmd/knode run -config knode.json
```

## 一键安装

Linux 节点可以直接使用仓库中的 `install.sh` 安装最新 GitHub Release：

```bash
curl -fsSL https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh | sudo bash
```

升级到最新 Release：

```bash
curl -fsSL https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh | sudo bash -s -- upgrade
```

打开快捷管理菜单：

```bash
sudo knode-manager
```

或直接从 GitHub 进入菜单：

```bash
curl -fsSL https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh | sudo bash -s -- menu
```

对接 Kboard 时可以通过环境变量下发 KLESS 配置：

```bash
KNODE_CLIENT_SECRET="..." \
KNODE_SERVER_SIGNING_KEY="..." \
KNODE_UPSTREAM_ADDR="127.0.0.1:9000" \
sudo -E bash install.sh install
```

`gen-keys` 会输出服务端 Ed25519 公私钥和客户端 secret。Knode 配置中使用
`server_signing_public` 作为 `server_signing_key`，kray 服务端使用对应私钥。

## 配置示例

```json
{
  "node_id": "knode-a",
  "admin": {
    "address": "127.0.0.1:8080"
  },
  "upstreams": [
    {
      "name": "kray-primary",
      "transport": "tcp",
      "address": "127.0.0.1:9000",
      "kless": {
        "client_id": "knode-a",
        "client_secret": "base64url-client-secret",
        "server_signing_key": "base64url-ed25519-public-key"
      }
    }
  ],
  "inbounds": [
    {
      "name": "local-tcp",
      "listen": "127.0.0.1:7000",
      "upstream": "kray-primary",
      "max_connections": 1024
    }
  ]
}
```

## 发版

推送 tag 会触发 `.github/workflows/release.yml`：

```powershell
git tag v0.1.0
git push origin v0.1.0
```

工作流会生成多平台 `knode-*.zip` 和对应 `knode-*.zip.dgst`，并上传到 GitHub
Release。
