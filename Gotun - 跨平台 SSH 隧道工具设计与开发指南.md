# Gotun - 跨平台 SSH 隧道工具设计与开发指南

> **文档说明**：本文档为 `gotun` 项目的系统架构与设计指南。旨在为开发者提供清晰的模块划分、数据结构设计、核心流程说明以及跨平台开发环境的初始化标准。

---

## 一、 项目概述

- **项目名称**：gotun
- **开发语言**：Go 1.20+
- **核心依赖**：`golang.org/x/crypto/ssh`, `github.com/spf13/cobra`, `github.com/kardianos/service`, `github.com/prometheus/client_golang`, `github.com/getlantern/systray`
- **目标平台**：macOS / Linux / Windows
- **核心定位**：一个轻量级、高可用、支持声明式配置的跨平台企业级 SSH 隧道与边缘网络网关工具。解决开发者日常工作中频繁维护 SSH 端口转发的痛点，提供零停机热加载、多路并发复用、内置智能分流路由、极速防污染 DNS 以及全链路双栈 IPv6 原生架构。

---

## 二、 系统架构设计

### 2.1 模块依赖关系

系统采用自定向下的分层架构，底层网络与业务层完全解耦，确保代码的高内聚与低耦合。

系统采用自定向下的分层架构，底层网络与业务层完全解耦，确保代码的高内聚与低耦合。

```mermaid
graph TD
    %% 表现层
    subgraph 表现层 [表现层 (Presentation Layer)]
        A[cmd/gotun<br>启动入口] --> B[internal/cli<br>Cobra解析 / 守护进程 / 原生状态栏]
    end

    %% 逻辑层
    subgraph 逻辑层 [逻辑层 (Logic Layer)]
        B --> C[internal/manager<br>全局调度与 SIGHUP 热加载]
        C -.-> D[internal/metrics<br>Prometheus 监控探针]
        C --> E[internal/dns<br>智能防污染 DNS 与多策略协议]
    end

    %% 路由层
    subgraph 路由层 [路由层 (Routing Layer)]
        C --> F[internal/tunnel<br>隧道生命周期与多路复用调度]
        F --> G1(Local Tunnel<br>本地端口转发)
        F --> G2(Dynamic Tunnel<br>SOCKS5 / PAC 分流代理)
        F --> G3(Remote Tunnel<br>反向内网穿透 / 容灾 HA)
    end

    %% 连接层
    subgraph 连接层 [连接层 (Connection Layer)]
        F --> H[internal/pool - ServerPool<br>并发 SSH 连接池 / 堡垒机穿透]
    end

    %% 基础层
    subgraph 基础层 [基础层 (Infrastructure Layer)]
        C -.-> I1[internal/config<br>YAML 配置解析]
        C -.-> I2[pkg/logger<br>日志系统]
        C -.-> I3[pkg/crypto<br>AES-GCM 加解密]
    end

    style A fill:#2B3A42,stroke:#3F5159,stroke-width:2px,color:#fff
    style B fill:#3F5159,stroke:#2B3A42,stroke-width:2px,color:#fff
    style C fill:#4CAF50,stroke:#388E3C,stroke-width:2px,color:#fff
    style E fill:#00ACC1,stroke:#00838F,stroke-width:2px,color:#fff
    style F fill:#F57C00,stroke:#E65100,stroke-width:2px,color:#fff
    style H fill:#E53935,stroke:#C62828,stroke-width:2px,color:#fff
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
    Routing     RoutingConfig  `yaml:"routing,omitempty"`
    DNS         DNSConfig      `yaml:"dns,omitempty"`
    Servers     []ServerConfig `yaml:"servers"`
    Tunnels     []TunnelConfig `yaml:"tunnels"`
    MetricsAddr string         `yaml:"metrics_addr,omitempty"`
}

type DNSConfig struct {
    Enable     bool   `yaml:"enable"`
    ListenAddr string `yaml:"listen_addr"`
    Upstream   string `yaml:"upstream"`
}

type RoutingConfig struct {
    Mode      string `yaml:"mode"`       // "global" 或 "pac"
    BypassLan bool   `yaml:"bypass_lan"` // 是否绕过局域网
}

type ServerConfig struct {
    Name      string `yaml:"name"`
    Host      string `yaml:"host"`
    Port      int    `yaml:"port"`
    User      string `yaml:"user"`
    Password  string `yaml:"password,omitempty"`
    KeyPath   string `yaml:"key_path,omitempty"`
    JumpHost  string `yaml:"jump_host,omitempty"`
    PoolSize  int    `yaml:"pool_size,omitempty"` // 并发连接池大小
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

- **连接复用与多路并发 (Connection Pool)**：所有指向同一 Server 的 Tunnel 共享底层的多条 TCP 物理连接（通过 `pool_size` 决定连接数量，默认 1）。这打破了单一 SSH 连接在跨国长胖网络（LFN）下的队头阻塞（HoL Blocking）问题，极大提升了极限流媒体吞吐量。
- **TCP 底层深度调优**：底层的 `net.TCPConn` 强制关闭了 Nagle 算法（`TCP_NODELAY = true`），并手动扩张读写缓冲区至 4MB，以硬抗跨国高延迟抖动，榨干出口带宽。
- **多跳穿透 (ProxyJump)**：若检测到 `JumpHost` 字段，Pool 会优先连接堡垒机，然后在其之上进行 TCP 拨号连接靶机。
- **退避重连**：断线后由 Pool 独立执行指数退避重连。

### 4.2 本地与动态端口转发 (业务通道)

**实现逻辑**：Tunnel 纯粹作为“业务请求分发器”。监听本地请求 -> 经过**智能路由引擎**判定 -> 触发时向 ServerPool 获取 `ssh.Client` -> 申请 Channel -> 拷贝流量。

- **内置智能路由 (Smart Routing)**：原生内嵌基于 `netip` 的毫秒级国内/局域网分流引擎，支持 `global`（全局代理）与 `pac`（绕过国内大陆 IP 与局域网）模式。异步加载 `chnroutes2`，实现按需分流，不浪费一滴机场流量。

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

### 4.5 极速 DNS 网关与国密 DoH (TLCP) 劫持

借助 `miekg/dns`，Gotun 实现了一个透明的极速 DNS 缓存防污染代理：
- **策略模式 (Strategy Pattern)**：在 `internal/dns/resolver.go` 中抽象了 `UpstreamResolver` 接口。根据配置前缀 (`udp://`, `tls://`, `https://`, `gmdoh://`)，动态初始化分发 UDP、DoT、DoH 解析器，实现开闭原则 (OCP)。
- **国密 TLCP 劫持**：引入了开源的 `tjfoc/gmsm` 密码学库。对于 `gmdoh://` 协议，我们抛弃了 Go 官方的 `crypto/tls`，通过在 HTTP Transport 层劫持 `DialTLSContext`，手动调用 `gmtls.Client` 进行商密级 TLCP 双证书握手。实现了对业务层完全透明的国密 DoH 解析支持。
- **AAAA 与 A 记录隔离缓存**：对收到的 DNS Msg 按照 `Name + Qtype + Qclass` 进行联合散列，自然隔离 IPv4 与 IPv6 缓存。

### 4.6 全链路双栈 IPv6 原生兼容架构

底层完全打破单栈绑定局限，实现企业级双栈兼容：
- **智能双栈侦听**：对于 `local`、`dynamic` 隧道以及 DNS 网关，若用户配置了 `127.0.0.1:port` 或 `0.0.0.0:port`，底层的 `getDualStackAddrs` 函数会自动裂变产生 `[::1]:port` 和 `[::]:port`。程序会动态向内核申请多个并发监听器，自动适配系统级双栈网络。
- **底层格式强约束**：所有的远端地址与动态寻址拼接，强制弃用 `fmt.Sprintf`，全面改为 `net.JoinHostPort`，完美处理纯 IPv6 (IPv6-Only) 宿主机的网络特征，消除 `too many colons` 崩溃。

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

- **[x] v1.0.0**：(极速边缘网关与桌面守护版 - 首次开源稳定版)
  - **核心网络架构**：支持 Local/Remote/Dynamic 三位一体隧道穿透，内置心跳检测与断线智能重连不死神盾。
  - **多路复用与并发**：引入 ServerPool 架构，多隧道共享物理连接；支持自定义 N 条物理连接并发分摊流量，打破跨国高延迟瓶颈。
  - **网络底层暴改**：强制关闭 Nagle 算法并注入 4MB 巨大 TCP 读写缓冲；引入 128KB 零拷贝 (Zero-Allocation) 内存池消除 GC 停顿。
  - **企业穿透与路由**：支持多跳链式堡垒机 (ProxyJump) 与多节点负载均衡容灾；内置 PAC 智能分流，毫秒级绕过国内 IP 与局域网。
  - **桌面与运维体系**：引入极简原生跨平台状态栏 (Systray)；支持跨进程 `SIGHUP` 零停机热加载；提供 Prometheus 探针与配置 AES 加密。

- **[x] v1.1.0**：(极速双栈与防污染解析升级)
  - **极速防污染 DNS**：内置基于 `miekg/dns` 的透明缓存解析器。使用 Strategy 模式原生支持 UDP、DoT、DoH 协议上游。
  - **国密 TLCP 劫持**：引入 `tjfoc/gmsm`，在 HTTP 传输层接管握手，提供符合商密标准的国密 DoH 客户端能力。
  - **全链路原生 IPv6**：底层弃用脆弱单栈监听，智能根据宿主机地址裂变并发侦听双栈端口。彻底根除因 `too many colons` 导致的 IPv6-Only 节点拨号崩溃。
