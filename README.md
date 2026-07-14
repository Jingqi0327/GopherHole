# GopherHole

[![Go Version](https://img.shields.io/badge/Go-1.26.5-blue.svg)](https://golang.org)

**GopherHole** 是一款基于 Go 语言开发的轻量级 P2P 虚拟局域网（VPN）/ 内网穿透互联工具。它通过在本地创建虚拟网卡（TUN 设备）接管网络流量，并基于端到端加密的 UDP 通道，将处于不同局域网（NAT）之后的节点直接打通，构建安全、高效、低延迟 of 虚拟专用网络。

针对 P2P 行业中公认最棘手的 **NAT3 (端口限制锥形) × NAT4 (对称型)** 这一难点组合，GopherHole 创新性地引入了 **“两阶段端口预测与多 Socket 扫射碰撞”** 策略，实测能在数秒内达到 99% 以上 of 直连打洞成功率，极大减少了传统方案因打洞失败回退到中继服务器（Relay）所导致的带宽瓶颈与延迟飙升。

---

## 📖 目录

- [🌟 核心特性](#-核心特性)
- [🛠️ 编译与安装](#️-编译与安装)
- [🚀 快速开始](#-快速开始)
- [⚙️ 配置文件参数说明](#️-配置文件参数说明)

---

## 🌟 核心特性

- ⚡ **突破复杂 NAT 限制**：智能检测双方 NAT 类型，针对最棘手的 `NAT3 × NAT4` 组合自主执行“两阶段扫射打洞策略”，直连成功率极高。
- 🔒 **WireGuard 级别的加密安全**：控制面采用安全认证，数据面使用基于非对称密钥对（公钥加密，私钥解密）的端到端（E2E）加密，确保传输数据无法被监听或篡改。
- 🔑 **双向鉴权与接入控制**：通过 Token 机制或密钥对配对进行身份认证，防止未授权节点接入网络。
- 🌐 **内置 mDNS / 动态 Hosts 接管**：内置网络名称解析，自动维护本地 `/etc/hosts`，支持通过节点主机名直接互访（例如 `ssh user@MacBook-Pro`）。
- 🏎️ **零拷贝与性能优化**：实现报文原地加密（In-place Encryption）与零拷贝，有效降低在高吞吐量下的 CPU 负载与 GC 压力；引入待发包暂存机制，完美解决“第一包丢失”引起的连接延迟。
- 📦 **轻量无依赖，跨平台**：无复杂外部依赖，支持 Linux (amd64, arm64, armv7)、macOS、Windows 的一键编译与部署。

---

## 🛠️ 编译与安装

确保本地已安装 [Go 1.20+](https://golang.org/dl/)。如果需要重新编译 Protobuf 文件，还需要安装 `protoc` 以及 Go 插件：

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

### 使用 Makefile 构建

克隆仓库后，可以通过 `Makefile` 方便地进行编译：

```bash
# 1. 重新生成 Protobuf 代码（若修改了 signaling.proto）
make proto

# 2. 编译当前平台的 Server 和 Client 进 bin/ 目录
make build

# 3. 交叉编译所有支持的平台和架构（Linux, macOS, Windows）
make build-all

# 4. 清理编译产物
make clean
```

编译出的二进制文件会放置在 `bin/` 目录下：
- **服务端**：`bin/server`
- **客户端**：`bin/client`

---

## 🚀 快速开始

### 第一步：部署协调中心 (Signaling Server)

在一台拥有**固定公网 IP** 的服务器上部署服务端。

1. **生成/配置环境变量**：拷贝并配置 `.env` 文件。
   ```bash
   cp .env.example bin/.env
   ```
   编辑 `bin/.env`，填写认证 Token 并配置服务端口（例如默认的 `:8086`）。

2. **启动服务端**：
   ```bash
   # 直接运行
   ./bin/server
   ```
   *首次启动时，服务端会自动生成公私钥对，并在控制台打印出来，请妥善保存。*

---

### 第二步：部署边缘节点 (Client)

客户端必须运行在有**管理员权限**的机器上，以支持创建虚拟网卡和修改 `/etc/hosts`。

1. **准备配置文件**：
   在客户端运行目录下拷贝 `.env` 文件：
   ```bash
   cp .env.example .env
   ```
   修改配置项：
   ```ini
   # 协调中心公钥（从服务端启动日志获取）
   SERVER_PUBLIC_KEY="服务端生成的公钥内容"
   # 鉴权 Token
   TOKEN="与服务端一致的鉴权Token"
   # 协调中心地址
   SERVER="服务器IP:8086"
   # 自定义本机在网络中的主机名
   HOSTNAME="MyLaptop"
   ```

2. **启动客户端**：
   - **Linux** / **macOS** (需要 sudo 权限创建 TUN 网卡)：
     ```bash
     sudo ./bin/client
     ```
   - **Windows**：
     > [!IMPORTANT]
     > Windows 系统运行客户端需要提前安装虚拟网卡驱动，请先下载并完成安装：  
     > 🔗 [Windows 虚拟网卡驱动官方下载 (tap-windows-9.24.2)](https://build.openvpn.net/downloads/releases/tap-windows-9.24.2-I601-Win10.exe)  
     > 安装完成后，使用**管理员权限**打开 PowerShell 或 CMD，运行：
     ```powershell
     .\bin\client.exe
     ```

3. **测试连通性**：
   客户端上线并打洞成功后，你可以通过分配的虚拟 IP 直接访问对端：
   ```bash
   # ping 对端节点（例如 10.8.0.222）
   ping 10.8.0.222
   
   # 或者通过内置 hosts 映射直接使用主机名访问
   ssh root@Nas
   ```

---

## ⚙️ 配置文件参数说明

编辑 `.env` 文件，可配置的参数列表如下：

| 参数名 | 适用模式 | 默认值 | 描述说明 |
| :--- | :--- | :--- | :--- |
| `PRIVATE_KEY` | Server | - | 服务端私钥（自动生成或手动指定） |
| `PUBLIC_KEY` | Server | - | 服务端公钥，用于自检与展示 |
| `TOKEN` | Both | - | 鉴权密钥，加入同一个网络的客户端必须与服务端保持一致 |
| `BIND_ADDR` | Server | `:8086` | 服务端 gRPC 心跳和控制面监听端口 |
| `VIRTUAL_SUBNET` | Server | `10.8.0` | 自动分配的局域网网段前缀（当前固定 `/24` 掩码） |
| `SERVER_PUBLIC_KEY` | Client | - | 协调中心的公钥，用于控制面通信的加密自检 |
| `STUN_SERVERS` | Client | - | 外部公共 STUN 服务器列表，用逗号分隔，用于客户端自测 NAT 类型 |
| `SERVER` | Client | `127.0.0.1:8086` | 协调中心的连接地址（`IP:Port`） |
| `HOSTNAME` | Client | 本机 Hostname | 自定义该节点在局域网内显示的主机名 |
| `IP` | Client | - | 期望绑定的固定虚拟 IP，留空则由 Server 自动随机分配 |
| `KEEPALIVE_INTERVAL` | Client | `15` | 保活心跳发送间隔（秒），用于防止防火墙/路由器公网端口映射老化 |
