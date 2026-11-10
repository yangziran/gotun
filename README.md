# Gotun

> **维护模式声明**：Gotun 自 v1.2.0 起进入维护模式——功能冻结，仅接受安全与正确性修复，不再新增功能。后续新能力开发请见 [OxiTun](https://github.com/yangziran/oxitun)（Rust 重写的下一代 SSH 隧道工具）。

![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.26.2-00ADD8?style=flat-square&logo=go)
![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-brightgreen?style=flat-square)
![License](https://img.shields.io/badge/License-MIT-blue?style=flat-square)
![Status](https://img.shields.io/badge/Status-Maintenance%20v1.2.0-success?style=flat-square)

Gotun (Go Tunnel) 是一款基于 Go 语言编写的轻量级、声明式且极具韧性的跨平台**企业级 SSH 隧道与边缘网络网关工具**。

通过一纸 YAML 配置文件，Gotun 能够为您在后台并发管理多条复杂的端口映射与内网穿透隧道。其底层独创的 **ServerPool 连接池**不仅消灭了握手延迟，还带来了平滑热加载、多路复用与多节点容灾等企业级能力。结合最新引入的**极速防污染 DNS**与**全链路原生双栈 IPv6**特性，确保您的网络在复杂环境下保持稳定且极致安全。

## 🌟 核心企业级特性

- **声明式配置**：告别繁琐且难以记忆的 `ssh -L / -D / -R` 长串命令，所有规则统一在 YAML 文件中优雅管理。
- **智能分流路由 (Smart Routing)**：内置基于 `netip` 的毫秒级分流引擎，支持 `pac` 内置分流模式（按大陆 IP 与局域网规则自动直连，非浏览器 PAC 文件），精准把控跨境流量。
- **极速防污染 DNS 代理**：内置高性能 DNS 缓存层，配合内置分流引擎实现国内外解析分流。原生支持基于策略模式的 `UDP`、`DoT`、`DoH` 乃至 `国密 DoH (TLCP)` 多协议上游解析。
- **全链路双栈 IPv6 原生兼容**：底层完全抛弃单栈局限，智能开启双栈监听。全面支持 IPv6-Only 节点拨号、SOCKS5 纯 IPv6 寻址以及 AAAA 记录独立缓存隔离。
- **极速多路并发池 (Pool Size)**：独创 `ServerPool` 架构，支持自定义并发 N 条 SSH 物理连接分摊流量，彻底打破单通道跨国传输的队头阻塞 (HoL Blocking) 瓶颈。
- **TCP 深度缓冲调优**：禁用 Nagle 算法，强制扩张底层 TCP 收发缓冲至 4MB，暴力抗击高延迟与网络抖动，极限压榨出口带宽。
- **三位一体的隧道支持**：
  - **Local (本地转发)**：将远程内网服务映射到本地端口，等同于 `ssh -L`。
  - **Dynamic (动态代理)**：在本地建立 SOCKS5 代理实现全局路由转发，等同于 `ssh -D`（内置高性能协议解析器）。
  - **Remote (反向转发)**：支持内网穿透，将本地开发服务暴露至公网服务器，等同于 `ssh -R`。
- **多出口负载均衡与容灾 (HA)**：支持多出口节点轮询 (Round-Robin)；Remote 远端映射模式下，主节点连接失败或断开时将按序尝试备用节点 (Failover)，切换期间该映射会短暂中断。
- **链式堡垒机穿透 (ProxyJump)**：原生支持配置 `jump_host`，直指企业深层内网靶机。
- **极简原生状态栏 (Systray)**：摒弃重型跨平台 GUI，引入纯原生后台托盘，提供免终端常驻、可视化启停与一键热重载，仅占 16MB 内存。
- **平滑热加载 (Hot Reload)**：修改配置后一键重载（或发送 `SIGHUP`）：未变更的隧道与底层连接原样保留；仅配置有变化的隧道会被重启，其上的活跃连接将断开。
- **断线自动恢复**：解耦本地监听与底层网络，配合 KeepAlive 与指数退避重连，网络恢复后自动重连。
- **监控探针与密码加密**：暴露 Prometheus 指标，完美对接企业监控大屏；支持服务器密码 AES-GCM 密文存储，告别明文泄露。
- **主机密钥可信校验 (TOFU)**：替代无校验连接，known_hosts 持久化 + 防中间人变更检测；支持预置 `host_key` 指纹实现无人值守信任。

---

## 🚀 安装与编译

请确保您的系统中已安装 Go (1.26.2+，与 go.mod 要求一致)。本项目自带一键跨平台打包的 `Makefile`。

```bash
# 1. 克隆项目
git clone https://github.com/yangziran/gotun.git
cd gotun

# 2. 拉取依赖
go mod tidy

# 3. 使用 Make 一键编译对应平台二进制文件
make build         # 编译当前平台版本
make build-mac     # 编译 macOS (amd64 & arm64) 版本 (开启 CGO 以支持状态栏)
make build-linux   # 编译 Linux 版本
make build-win     # 编译 Windows (.exe) 版本

# 4. 清理编译产物
make clean         # 清理 bin/ 目录下的所有二进制文件
```

---

## 📖 快速上手

### 1. 准备企业级混合配置

复制示例配置文件并进行修改：

```bash
cp configs/config.example.yaml configs/config.yaml
```

打开 `configs/config.yaml`，填入您的服务器信息和期望的隧道规则。以下是一个集成了**智能分流、多路并发、跳板机穿透与负载均衡**的高级示例：

```yaml
routing:
  mode: 'pac' # 内置分流模式：自动绕过局域网与国内 IP（非浏览器 PAC 文件）
  bypass_lan: true

dns:
  enable: true # 开启极速防污染 DNS (双栈并发隔离缓存)
  listen_addr: '127.0.0.1:53'
  upstream: 'https://doh.pub/dns-query' # 支持 DoH / DoT / 国密 gmdoh://

metrics_addr: '127.0.0.1:9090' # 开启 Prometheus 探针

servers:
  - name: 'tokyo_node'
    host: '8.211.x.x'
    port: 22
    user: 'root'
    password: 'your_password'
    pool_size: 8 # 开启 8 条并发连接，打破高延迟队头阻塞

  - name: 'jump_server'
    host: '100.20.0.1'
    port: 22
    user: 'admin'
    key_path: '~/.ssh/id_rsa'

  - name: 'target_server'
    host: '192.168.1.100'
    port: 22
    user: 'root'
    key_path: '~/.ssh/id_rsa'
    jump_host: 'jump_server' # 链式堡垒机穿透

tunnels:
  # 场景 1: 动态代理 (绑定东京节点做跨境分流)
  - name: 'tokyo_proxy'
    server_names: ['tokyo_node']
    type: 'dynamic'
    local_addr: '127.0.0.1:1080'

  # 场景 2: 本地端口转发 (穿透跳板机访问内网数据库)
  - name: 'db_tunnel'
    server_names: ['target_server']
    type: 'local'
    local_addr: '127.0.0.1:33060'
    remote_addr: '10.0.0.5:3306'
```

### 2. (可选) 生成密文密码

如果您的服务器必须使用密码登录，强烈建议不要在配置文件中明文保存密码。您可以使用内置的 `encrypt` 命令进行加密：

```bash
./bin/gotun encrypt -p "您的服务器密码" -k "您的自定义加解密密钥"
```

将其输出的 Base64 密文填入 `configs/config.yaml`，并开启加密标识：

```yaml
# 方式 2: 使用密文密码认证
password: '刚才生成的 Base64 密文'
encrypted: true
```

在后续启动时，通过环境变量注入解密密钥即可自动完成内存级别的解密：

```bash
export GOTUN_SECRET_KEY="您的自定义加解密密钥"
```

### 3. 启动隧道 (纯后台 CLI)

使用 `start` 命令并指定您的配置文件即可启动所有定义的隧道：

```bash
./bin/gotun start -c configs/config.yaml
```

> **热加载 (Hot Reload) 操作**：
> 在服务运行时，如果您修改了 `configs/config.yaml`，无需重启进程，只需新开一个终端并执行：
>
> ```bash
> ./bin/gotun reload
> ```
>
> 服务将跨进程自动寻找后台守护程序并重新加载最新配置：未变更的隧道与连接保持运行，配置有变化的隧道会重启（其活跃连接将断开）。**注意**：`metrics_addr` 不支持热切换，修改监听地址需重启进程（重载时会输出警告日志）。

### 4. 状态栏模式 (Systray GUI) —— 极简桌面体验 🌟

如果您是桌面用户（Mac/Windows），强烈推荐使用全新的 `tray` 模式！它会在您的系统顶部菜单栏生成一个小图标，提供纯原生的图形化控制菜单（包含启动/停止、一键热重载配置、打开配置目录等），且内存占用极低（~16MB）。

为了让状态栏程序**完全脱离终端运行**（即使关闭终端窗口，它也能在后台安静常驻），请在终端执行以下后台挂起命令：

```bash
# macOS / Linux (脱离终端在后台运行状态栏)
nohup ./bin/gotun tray -c configs/config.yaml > /dev/null 2>&1 &
```

执行后您可以直接关闭终端窗口，从此以后直接点击右上角的状态栏小图标进行管理即可！完全退出请点击菜单里的【退出 Gotun】。

### 5. 系统级后台守护进程 (Service) 操作指南

如果您希望工具在服务器后台静默运行甚至开机自启，可以使用以下高阶命令将程序注册为操作系统的底层服务。

> **⚠️ 注意**：注册系统服务时，`-c` 传入的配置文件路径**必须使用绝对路径**！

#### 🍎 macOS / 🐧 Linux 平台

涉及到系统底层服务注册，必须使用 `sudo` 提权执行。

```bash
# 1. 注册开机自启系统服务 (仅需执行一次)
sudo ./bin/gotun service install -c /绝对路径/configs/config.yaml

# 2. 启动服务与查看状态
sudo ./bin/gotun service start
sudo ./bin/gotun service status

# 3. 优雅停止与彻底卸载服务
sudo ./bin/gotun service stop
sudo ./bin/gotun service uninstall
```

#### 🪟 Windows 平台

在 Windows 系统下，您需要**以管理员身份运行命令提示符 (CMD) 或 PowerShell**。

```cmd
# 1. 注册开机自启系统服务 (仅需执行一次)
.\bin\gotun_windows_amd64.exe service install -c C:\绝对路径\configs\config.yaml

# 2. 启动服务与查看状态
.\bin\gotun_windows_amd64.exe service start
.\bin\gotun_windows_amd64.exe service status

# 3. 优雅停止与彻底卸载服务
.\bin\gotun_windows_amd64.exe service stop
.\bin\gotun_windows_amd64.exe service uninstall
```

---

## 🔐 安全机制

### 主机密钥校验（TOFU）

所有 SSH 连接均按 TOFU（首次使用即信任）语义校验主机密钥，**跳板与最终节点独立校验**：

| 场景 | 行为 |
|---|---|
| known_hosts 已有记录且一致 | 直接通过 |
| 首次连接（有 TTY） | 交互显示指纹，输入 `y` 信任并持久化 |
| 首次连接（无 TTY，如系统服务/状态栏） | 在服务器配置中预置 `host_key: "SHA256:..."`（可用 `ssh-keyscan` 获取），否则**明确失败** |
| 密钥与记录不一致 | **拒绝连接**，绝不覆盖（防中间人） |

- known_hosts 默认使用 `~/.ssh/known_hosts`（与 OpenSSH 互通），可通过环境变量 `GOTUN_KNOWN_HOSTS` 显式指定；系统服务场景建议显式设置。
- 可在服务器配置中预置 `host_key: "SHA256:AbCdEf…"` 指纹，实现无人值守信任。首次连接报错信息会打印实际指纹，核对后可复制到配置中。

### 密文密码

服务器密码可使用 AES-GCM 加密后存储，并在对应服务器配置中设置 `encrypted: true`：

- 启动时必须通过 `GOTUN_SECRET_KEY` 环境变量提供与加密时相同的密钥；未设置时**启动直接报错退出**。
- 使用 `gotun encrypt` 生成密文时，必须通过 `-k` 参数或 `GOTUN_SECRET_KEY` 环境变量显式提供加密密钥。
- 不要将真实密钥写入配置文件或提交到版本库。

---

## 📜 许可协议

本项目基于 **MIT** 开源许可协议发布。您可以自由地使用、修改、分发甚至用于商业项目，只需保留原作者版权声明即可。

欢迎提交安全与正确性修复 PR。
