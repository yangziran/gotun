# Gotun - 跨平台 SSH 隧道工具设计与开发指南

> **文档说明**：本文档为 `gotun` 项目的系统架构与设计指南。旨在为开发者提供清晰的模块划分、数据结构设计、核心流程说明以及跨平台开发环境的初始化标准。本指南已更新至 v1.0.0 首个稳定企业级架构。

---

## 一、 项目概述

- **项目名称**：gotun
- **开发语言**：Go 1.20+
- **核心依赖**：`golang.org/x/crypto/ssh`, `github.com/spf13/cobra`, `github.com/kardianos/service`, `github.com/prometheus/client_golang`
- **目标平台**：macOS / Linux / Windows
- **核心定位**：一个轻量级、高可用、支持声明式配置的跨平台企业级 SSH 隧道与边缘网络网关工具。解决开发者日常工作中频繁维护 SSH 端口转发以及隧道容易因网络波动断开的痛点，提供零停机热加载、多路复用与负载均衡容灾能力。

---

## 二、 系统架构设计

### 2.1 模块依赖关系

系统采用自定向下的分层架构，底层网络与业务层完全解耦，确保代码的高内聚与低耦合。

```text
[ 表现层 ]  cmd/gotun (启动入口) ── internal/cli (Cobra解析、SIGHUP监听、系统守护进程)
                 │
[ 逻辑层 ]  internal/manager (全局调度与配置热加载 Reload) ── internal/metrics (Prometheus探针)
                 │
[ 路由层 ]  internal/tunnel (隧道业务: Local/Dynamic/Remote 分发、轮询负载均衡)
                 │
[ 连接层 ]  internal/pool (ServerPool: SSH连接池、多路复用、堡垒机ProxyJump、心跳重连)
                 │
[ 基础层 ]  internal/config (解析) ── pkg/logger (日志) ── pkg/crypto (AES-GCM 加解密)
```

### 2.2 目录结构规范 (Standard Go Layout)

按照 Go 社区推荐规范，针对 `gotun` 定制的目录结构：

```text
gotun/
├── cmd/
│   └── gotun/              # 程序入口
│       └── main.go
├── internal/               # 私有核心业务逻辑
│   ├── cli/                # 命令行交互与系统后台 Service 逻辑
│   ├── config/             # YAML 配置解析与结构体验证
│   ├── manager/            # 隧道实例调度器与热加载协调
│   ├── pool/               # [基础设施] SSH 连接池 (处理拨号、重连、多跳)
│   ├── tunnel/             # [业务连接] 具体隧道实现与轮询分发
│   └── metrics/            # Prometheus 监控指标上报模块
├── pkg/                    # 可复用的公共组件
│   ├── logger/             # 统一日志封装
│   └── crypto/             # AES-GCM 配置密码加密工具
├── configs/                # 配置文件示例 (config.example.yaml)
└── Makefile                # 跨平台一键编译脚本
```

---

## 三、 核心数据模型与状态机

### 3.1 核心配置模型 (YAML 映射)

设计声明式的配置文件，支持同时管理多台服务器和多条不同类型的隧道规则：

```go
// internal/config/config.go
type Config struct {
    Servers     []ServerConfig `yaml:"servers"`
    Tunnels     []TunnelConfig `yaml:"tunnels"`
    MetricsAddr string         `yaml:"metrics_addr,omitempty"` // Prometheus 探针地址
}

type ServerConfig struct {
    Name      string `yaml:"name"`

    Host      string `yaml:"host"`
    Port      int    `yaml:"port"`
    User      string `yaml:"user"`
    Password  string `yaml:"password,omitempty"`
    KeyPath   string `yaml:"key_path,omitempty"`
    JumpHost  string `yaml:"jump_host,omitempty"` // 链式堡垒机穿透
    Encrypted bool   `yaml:"encrypted"`
}

type TunnelConfig struct {
    Name        string   `yaml:"name"`
    ServerNames []string `yaml:"server_names"`   // 多节点负载均衡
    Type        string   `yaml:"type"`           // "local" / "dynamic" / "remote"
    LocalAddr   string   `yaml:"local_addr"`
    RemoteAddr  string   `yaml:"remote_addr"`
}
```

### 3.2 隧道生命周期管理 (基于 Interface)

通过定义统一的 `Tunnel` 接口，Manager 模块可以抹平不同隧道的底层差异，进行统一的多态管理：

```go
type Tunnel interface {
    Start(ctx context.Context) error
    Stop()
    GetName() string
}
```

---

## 四、 关键流程实现规范

### 4.1 ServerPool 连接池机制 (基础设施)

底层网络维护从 Tunnel 中被完全剥离。`ServerPool` 负责全局的 SSH Transport 管理：

- **连接复用 (Multiplexing)**：所有指向同一 Server 的 Tunnel 共享同一条底层的 TCP 物理连接，通过内部开辟 SSH Channel 转发数据。
- **多跳穿透 (ProxyJump)**：若检测到 `JumpHost` 字段，Pool 会优先连接堡垒机，然后在其之上进行 TCP 拨号连接靶机。
- **退避重连**：断线后由 Pool 独立执行指数退避重连。

### 4.2 本地与动态端口转发 (业务通道)

**实现逻辑**：Tunnel 纯粹作为“业务请求分发器”。监听本地请求 -> 被触发时向 ServerPool 获取 `ssh.Client` -> 申请 Channel -> 拷贝流量。

- **轮询负载均衡 (Round-Robin)**：当配置了多个 `ServerNames` 时，Tunnel 会自动轮询申请 Client，将并发压力分散到多个远端。
- **128KB 高吞吐零拷贝缓冲池 (Streaming Optimization)**：为了彻底解决流媒体 (如 YouTube 4K) 在起步阶段瞬间拉取大数据包导致的 GC 停顿与 CPU 飙升问题，我们在底层 (`internal/tunnel/buffer.go`) 设计了基于 `sync.Pool` 的全局 128KB 巨大内存池。所有的 `io.Copy` 均被重构为 `io.CopyBuffer`。由于连接之间完美复用了这块巨大的内存，Gotun 实现了在极高并发吞吐下的 **Zero Allocation (零内存分配)**。

### 4.3 远程内网穿透与容灾切换 (Failover)

**实现逻辑**：对于 Remote 模式，Tunnel 需要在远端发起 Listen。

- **主备容灾切换**：Tunnel 会向 Pool 申请某一个 Server 的连接并在远端建立 Listen，如果该底层节点宕机，Tunnel 会立即进入容灾模式，遍历选取 `ServerNames` 数组中的下一个可用节点，重新发起 Listen，实现**跨机房自动 Failover**。

### 4.4 零停机热加载与监控告警

- **Hot Reload**：我们选用了云原生标准的 `SIGHUP` 信号来触发热加载，以保证配置重载的绝对原子性，避免 `fsnotify` 带来的修改过程误触发问题。
  并且在 v1.0.0 中，我们不仅在底层实现了信号监听，还在上层 CLI 暴露了极其人性化的跨进程热重载命令：

```bash
gotun reload
```

当执行 `gotun start` 或是守护进程启动时，引擎会自动在操作系统的临时目录下 (如 `/tmp/gotun.pid`) 写入进程 ID。`gotun reload` 命令会智能读取该 PID，并向后台发射 `SIGHUP` 信号，使得热加载操作具备极高的交互体验，从此告别繁琐的 `ps` 与 `kill`。信号被捕获后会触发 `manager.Reload()`。底层会深层次比对新老配置，仅重启发生改变的 Tunnel，正在运行的稳定连接零影响。

- **Metrics**：所有的流量与重连行为都在运行时通过 `Prometheus` 进行累加打点，支持对接企业级可视化大屏。

---

## 五、 开发环境初始化指南

```bash
# 1. 确保在 gotun 根目录下
cd gotun

# 2. 下载或刷新核心依赖
go mod tidy

# 3. 跨平台编译 (Makefile)
make build         # 编译本平台
make build-mac     # 编译 macOS 双架构 (amd64 / arm64)
make build-linux   # 编译 Linux
make build-win     # 编译 Windows (自动加上 .exe 后缀)
make clean         # 清理 bin 目录
```

---

## 六、 演进路线规划 (Roadmap)

- **[x] v0.1.0**：完成核心架构，支持基于 YAML 的单/多端 Local 转发。
- **[x] v0.5.0**：增加心跳检测与智能断线重连机制，加入 Manager 调度器。
- **[x] v0.8.0**：支持 Remote、Dynamic 穿透，实现配置 AES-GCM 高级加密存储与守护进程化。
- **[x] v1.0.0**：(企业级网络网关架构 - 首次开源稳定版)
  - **配置热加载 (Hot Reload)**: 监听 `SIGHUP` 信号，实现配置动态生效与零停机 (Zero-Downtime) 演进。
  - **SSH 连接复用 (Multiplexing)**: 引入 ServerPool，多隧道共享物理 TCP 连接，大幅降低服务器连接数耗损。
  - **多跳堡垒机支持 (ProxyJump)**: 原生支持链式代理穿透，适应深层企业内网环境。
  - **极致流媒体缓冲池 (Zero-Allocation)**: 引入 128KB 全局内存池，消除 4K 视频缓冲时的 GC 停顿。
  - **监控指标探针 (Metrics)**: 暴露 Prometheus 兼容的 API，实时监控 I/O 吞吐、活跃连接与重连统计。
- **[ ] v1.1.0**：(探索阶段) 探索 Wails / Tauri 桌面可视化的 GUI，为非技术用户提供托盘图标管理交互。
