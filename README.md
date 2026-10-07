# Gotun

Gotun (Go Tunnel) 是一款基于 Go 语言编写的轻量级、声明式且极具韧性的跨平台**企业级 SSH 隧道与边缘网络网关工具**。

通过一纸 YAML 配置文件，Gotun 能够为您在后台并发管理多条复杂的端口映射与内网穿透隧道。其底层独创的 **ServerPool 连接池**不仅消灭了握手延迟，还带来了零停机热加载、多路复用与主备容灾的顶级企业级能力，确保您的隧道在任何复杂网络环境下都能“永不掉线”。

## 🌟 核心企业级特性

- **声明式配置**：告别繁琐且难以记忆的 `ssh -L / -D / -R` 长串命令，所有规则统一在 YAML 文件中优雅管理。
- **三位一体的隧道支持**：
  - **Local (本地转发)**：将远程内网服务映射到本地端口，等同于 `ssh -L`。
  - **Dynamic (动态代理)**：在本地建立 SOCKS5 代理实现全局路由转发，等同于 `ssh -D`（内置高性能协议解析器）。
  - **Remote (反向转发)**：支持内网穿透，将本地开发服务暴露至公网服务器，等同于 `ssh -R`。
- **SSH 连接多路复用 (Multiplexing)**：所有同目标的隧道共享同一条底层的 TCP 物理连接，大幅降低服务器连接数与握手耗损。
- **多出口负载均衡与容灾 (HA)**：
  - 支持多出口节点轮询 (Round-Robin)。
  - Remote 远端映射模式下支持跨机房自动主备容灾切换 (Failover)。
- **链式堡垒机穿透 (ProxyJump)**：原生支持配置 `jump_host`，直指企业深层内网靶机。
- **不死神盾 (断线自动恢复)**：解耦本地监听与底层网络，配合 KeepAlive 与指数退避重连，网络恢复后瞬间满血复活。
- **零停机热加载 (Hot Reload)**：修改配置后发送 `SIGHUP` 信号，底层无感平滑切换，已活跃连接一秒不掉。
- **Prometheus 监控探针 (Metrics)**：暴露网络 I/O 吞吐与并发状态，完美对接企业级监控大屏。
- **配置密码安全加密**：支持将服务器密码通过 AES-GCM 加密存储，彻底告别明文泄漏。
- **系统级守护进程**：自带开机自启系统服务安装工具，支持 macOS(Launchd)/Linux(Systemd)/Windows(Services)。

---

## 🚀 安装与编译

请确保您的系统中已安装 Go (1.20+)。本项目自带一键跨平台打包的 `Makefile`。

```bash
# 1. 克隆项目
git clone https://github.com/yangziran/gotun.git
cd gotun

# 2. 拉取依赖
go mod tidy

# 3. 使用 Make 一键编译对应平台二进制文件
make build         # 编译当前平台版本
make build-mac     # 编译 macOS (amd64 & arm64) 版本
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
cp configs/config.example.yaml config.yaml
```

打开 `config.yaml`，填入您的服务器信息和期望的隧道规则。以下是一个集成了**跳板机、负载均衡与 Prometheus 监控**的高级示例：

```yaml
metrics_addr: "127.0.0.1:9090" # 开启 Prometheus 探针

servers:
  - name: "bastion"
    host: "100.20.0.1"
    port: 22
    user: "admin"
    key_path: "~/.ssh/id_rsa"
    
  - name: "target_server_1"
    host: "192.168.1.100"
    port: 22
    user: "root"
    key_path: "~/.ssh/id_rsa"
    jump_host: "bastion" # 链式堡垒机穿透
    
  - name: "target_server_2"
    host: "192.168.1.101"
    port: 22
    user: "root"
    key_path: "~/.ssh/id_rsa"
    jump_host: "bastion"

tunnels:
  # 场景 1: 本地端口转发 (轮询负载均衡访问内网数据库)
  - name: "db_forward"
    server_names: ["target_server_1", "target_server_2"]
    type: "local"
    local_addr: "127.0.0.1:33060"
    remote_addr: "10.0.0.5:3306"

  # 场景 2: 动态代理 (通过靶机 1 的 SOCKS5)
  - name: "global_proxy"
    server_names: ["target_server_1"]
    type: "dynamic"
    local_addr: "127.0.0.1:1080"
```

### 2. (可选) 生成密文密码
如果您的服务器必须使用密码登录，强烈建议不要在配置文件中明文保存密码。您可以使用内置的 `encrypt` 命令进行加密：
```bash
./bin/gotun encrypt -p "您的服务器密码" -k "您的自定义加解密密钥"
```
将其输出的 Base64 密文填入 `config.yaml`，并开启加密标识：
```yaml
    # 方式 2: 使用密文密码认证
    password: "刚才生成的 Base64 密文"
    encrypted: true
```
在后续启动时，通过环境变量注入解密密钥即可自动完成内存级别的解密：
```bash
export GOTUN_SECRET_KEY="您的自定义加解密密钥"
```

### 3. 启动隧道
使用 `start` 命令并指定您的配置文件即可启动所有定义的隧道：
```bash
./bin/gotun start -c config.yaml
```

> **热加载 (Hot Reload) 操作**：
> 在服务运行时，如果您修改了 `config.yaml`，无需重启进程，只需新开一个终端并执行：
> ```bash
> ./bin/gotun reload
> ```
> 服务将跨进程自动寻找后台守护程序，并毫无波澜地重新加载最新配置！

### 4. 后台守护进程 (Service) 操作指南

如果您希望工具在后台静默运行甚至开机自启，可以使用以下高阶命令将程序注册为操作系统的底层服务。

> **⚠️ 注意**：注册系统服务时，`-c` 传入的配置文件路径**必须使用绝对路径**！

#### 🍎 macOS / 🐧 Linux 平台
涉及到系统底层服务注册，必须使用 `sudo` 提权执行。
```bash
# 1. 注册开机自启系统服务 (仅需执行一次)
sudo ./bin/gotun service install -c /绝对路径/configs/config.example.yaml

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
.\bin\gotun_windows_amd64.exe service install -c C:\绝对路径\configs\config.example.yaml

# 2. 启动服务与查看状态
.\bin\gotun_windows_amd64.exe service start
.\bin\gotun_windows_amd64.exe service status

# 3. 优雅停止与彻底卸载服务
.\bin\gotun_windows_amd64.exe service stop
.\bin\gotun_windows_amd64.exe service uninstall
```

---

## 📜 许可协议
Gotun 遵循开源许可协议。欢迎提交 PR 一起参与共建！

---

## 🔮 探索路线
Gotun v1.0.0 已经完成了全线企业级能力（ServerPool、Hot Reload、HA、128KB缓冲池）的底层核心构建。在接下来的 `v1.1.0` 中，我们将探索构建基于 Wails / Tauri 桌面可视化的 GUI，为非技术用户提供极致优雅的系统托盘交互体验。
