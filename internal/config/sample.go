package config

const SampleJSON = `{
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
        "client_secret": "replace-with-base64url-client-secret",
        "server_signing_key": "replace-with-base64url-ed25519-public-key",
        "max_frame_payload": 16384,
        "handshake_timeout": "10s"
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
`
