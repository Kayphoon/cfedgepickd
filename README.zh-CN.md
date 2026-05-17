# cfpick

[English](README.md)

`cfpick` 是一个面向 `cloudflared` Tunnel 边缘 IP 选择的小型控制面守护进程和命令行工具。

它本身不代理流量。它会探测 Cloudflare 边缘候选 IP，观察 `cloudflared` 的健康状态和空闲窗口，更新 `/etc/hosts`，在 blue/green 两个 `cloudflared` 槽位之间热切换，并在新槽位没有变健康时自动回滚。

## 适用范围

v1 支持 Linux systemd、macOS 系统 LaunchDaemon、`cloudflared tunnel run`、基于 `/etc/hosts` 的边缘主机名固定，以及 `auto`、`quic`、`http2` 三种协议模式。`auto` 模式下会优先用 QUIC 探测排序，并在需要时回退到 HTTP/2 TCP 探测。

QUIC 探测使用和 `cloudflared` 相同的边缘 TLS Server Name，也就是 `quic.cftunnel.com`。`region*.v2.argotunnel.com` 是写入 `/etc/hosts` 的边缘主机名，不是 QUIC TLS SNI。

Cloudflare Tunnel 的 QUIC 边缘会呈现 Cloudflare Origin 证书，所以探测逻辑会验证预期的 `argotunnel` ALPN 和 Cloudflare Origin 证书形态，而不是要求公网 Web CA 链。

当配置为 `protocol: auto` 时，`cfpick` 可以用 QUIC 探测给 IP 排名，但会继续让生产 `cloudflared` 保持 `auto`，这样 UDP/QUIC 后续异常时仍可回退到 HTTP/2。

v1 暂不支持 OpenWrt 或其他服务管理器、纯 Docker 模式的 `cloudflared` 管理，也不支持用 DNS 服务替代 `/etc/hosts`。

## 安装

安装最新版本并启动守护进程：

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh
```

安装时把状态输出默认设为中文：

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --zh
```

安装时启用 100ms 的应急热切换阈值：

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --emergency-rtt-ms 100
```

安装指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --version v0.2.13
```

安装器会自动识别 Linux/macOS 和 `amd64`/`arm64`，下载匹配的发布包，在可用时校验 `checksums.txt`，安装二进制文件，写入 `/etc/cfpick/config.json`，写入平台对应的服务定义，启用守护进程并启动它。

使用 `--dry-run` 可以只预览发现结果和计划写入内容，不改动机器。使用 `--no-start` 可以只安装而不启动服务。安装时传 `--lang zh` 或 `--zh` 会让 `cfpick status` 默认使用中文标签；不传或传 `--lang en` 则默认英文。

## 常用命令

```bash
cfpick status
cfpick status --lang zh
cfpick status --metric error_delta --since 24h
cfpick discover
cfpick probe --protocol auto
cfpick once --config /etc/cfpick/config.json
cfpick switch --config /etc/cfpick/config.json
cfpick switch --apply --config /etc/cfpick/config.json
cfpick switch --apply --ips 198.41.200.227,198.41.200.132 --config /etc/cfpick/config.json
cfpick switch --apply --mode restart --config /etc/cfpick/config.json
cfpick run --config /etc/cfpick/config.json
```

一行安装脚本是正常安装路径。它会写入 `/etc/cfpick/config.json`，安装 systemd unit 或 launchd plist，启用守护进程并启动它。

`switch` 是手动替换命令。不带 `--apply` 时，它只探测并打印计划中的 blue/green 切换。带 `--apply` 时，它会写入选中的 IP，启动非活跃槽位，等待 `readyConnections >= 2`，然后优雅停止旧的活跃槽位；如果新槽位没有变健康，则自动回滚。传 `--ips` 会跳过探测，直接应用指定 IP。`--mode restart` 会使用旧的重启式切换行为。

## 历史和图表

守护进程每轮都会向 `/var/lib/cfpick/history.jsonl` 追加一条 JSONL 记录。默认每 5 分钟采样一次，由 `switching.probe_interval_seconds` 控制。历史文件路径可通过 `runtime.history_file` 配置。

历史记录默认保留 30 天，由 `runtime.history_retention_days` 控制。每次成功追加后，旧记录会被清理。把这个值设为负数可以关闭清理。

使用 `cfpick status` 可以查看终端仪表盘。它会展示统一状态总览、当前边缘连接、边缘 IP 对比、最近决策状态和按时间排序的折线图。默认图表会同时显示请求率和错误率。默认语言来自安装器写入配置中的 `runtime.language`，也可以在单次命令中用 `--lang` 或 `--zh` 覆盖：

```bash
cfpick status --metric request_rate --since 24h
cfpick status --metric response_5xx_delta --since 24h
cfpick status --metric rss_mb --since 24h
cfpick status --metric request_delta --since 24h
cfpick status --metric error_delta --since 24h
cfpick status --metric rtt --since 24h
cfpick status --metric ready --since 7d --width 100 --height 16
```

支持的指标包括 `request_rate`、`request_delta`、`error_rate`、`error_delta`、`response_5xx_delta`、`response_5xx_rate`、`rss_mb`、`heap_mb`、`goroutines`、`cpu_percent`、`network_rx_rate`、`network_tx_rate`、`rtt`、`ready`、`ha`、`concurrent`、`degraded` 和 `idle`。

## 构建和发布

```bash
make test
make dist VERSION=v0.2.13
```

`make dist` 会为 Linux 和 macOS 构建静态二进制：

```text
dist/cfpick-linux-amd64.tar.gz
dist/cfpick-linux-arm64.tar.gz
dist/cfpick-darwin-amd64.tar.gz
dist/cfpick-darwin-arm64.tar.gz
dist/checksums.txt
dist/install.sh
```

每个压缩包都包含 `cfpick`、`cfedgepickd`、`cfedgepickctl`、`install.sh`、两个 systemd service 文件和两个示例配置文件。`cfpick` 仍是默认兼容安装入口，`cfedgepickd` 守护进程和 `cfedgepickctl` 辅助工具也会一起打包。

GitHub Actions 会在 pull request 和推送到 `main` 时运行同一套发布构建，并上传短期可检查的构建产物。推送版本标签会创建 GitHub Release，并上传平台压缩包、`checksums.txt` 和 `install.sh`：

```bash
git tag v0.2.13
git push origin v0.2.13
```

标签构建会把标签名嵌入 `cfpick version`、`cfedgepickd version` 和 `cfedgepickctl version`。非标签 CI 构建会嵌入分支或 ref 名称以及短提交 SHA。

## 安全策略

守护进程只会在存在更好的 TopN IP 集合、当前连接看起来已变差、冷却期未激活，并且配置的空闲窗口已满足时进行切换。

当 `readyConnections < 2` 时，应急切换可以绕过空闲要求。把 `switching.emergency_rtt_threshold_ms` 设为正数，例如 `100`，还可以在当前边缘 IP 的探测中位 RTT 高于该阈值时立即热切换。默认值 `0` 表示关闭这个延迟保险。

手动执行 `cfpick switch --apply` 会有意绕过 degraded、cooldown 和 idle 门槛，因为这是明确的人工操作。它默认使用 blue/green 热切换；需要旧的重启式路径时可使用 `--mode restart`。

所有 hosts/config 写入都会先备份。如果非活跃槽位在超时前没有达到 `readyConnections >= 2`，`cfpick` 会停止它，恢复备份，并保持旧的活跃槽位继续运行。
