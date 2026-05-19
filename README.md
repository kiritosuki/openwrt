# OpenWrt 网络应用实验代码

本项目用于完成“基于 OpenWrt 的网络应用程序开发”实验。

最终部署结构：

```text
Windows x86 宿主机：
  netlab-server.exe        Web 前端 + Go 后端

Windows 上的 OpenWrt x86 虚拟机：
  openwrt-agent            OpenWrt 轻量 HTTP agent
  traffic-monitor          C/libpcap 流量监控程序
  scripts/firewall.sh      OpenWrt 防火墙脚本
```

Windows 后端通过 HTTP 访问 OpenWrt agent，不需要配置 SSH。

## 目录

```text
cmd/server/                Windows 上运行的 Web 后端，内嵌前端页面
cmd/agent/                 OpenWrt 上运行的 HTTP agent
cmd/server/web/            HTML/CSS/JavaScript 前端
monitor/traffic_monitor.c  C/libpcap 流量监控程序
scripts/firewall.sh        OpenWrt 防火墙脚本
Makefile                   编译入口
```

## 编译目标

需要得到三个产物：

```text
netlab-server.exe          运行在 Windows x86 宿主机
openwrt-agent              运行在 OpenWrt x86 虚拟机
traffic-monitor            运行在 OpenWrt x86 虚拟机
```

下面默认 Windows 和 OpenWrt 都是常见的 64 位 x86，即 `amd64/x86_64`。如果你的 Windows 是 32 位系统，把 Go 的 `GOARCH=amd64` 改为 `GOARCH=386`。

## 1. 编译 netlab-server.exe

`netlab-server.exe` 是 Windows 上运行的 Web 后端。

### 方式 A：在 macOS 上交叉编译

在 macOS 项目根目录执行：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags="-s -w" \
  -o build/windows-amd64/netlab-server.exe ./cmd/server
```

或者直接执行：

```bash
make windows-amd64
```

生成：

```text
build/windows-amd64/netlab-server.exe
```

把这个文件复制到 Windows 宿主机。

### 方式 B：在 Windows 本机编译

在 Windows 上安装 Go，进入项目目录，在 PowerShell 执行：

```powershell
go build -trimpath -ldflags="-s -w" -o netlab-server.exe .\cmd\server
```

生成：

```text
netlab-server.exe
```

## 2. 编译 openwrt-agent

`openwrt-agent` 是 Go 程序，运行在 OpenWrt x86 虚拟机上，不依赖 CGO，可以直接交叉编译。

### 方式 A：在 macOS 上交叉编译

在 macOS 项目根目录执行：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags="-s -w" \
  -o build/openwrt-x86_64/openwrt-agent ./cmd/agent
```

也可以执行：

```bash
make openwrt-x86_64
```

生成：

```text
build/openwrt-x86_64/openwrt-agent
```

### 方式 B：在 Windows 本机编译

在 Windows 上安装 Go，进入项目目录，在 PowerShell 执行：

```powershell
$env:GOOS="linux"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"
go build -trimpath -ldflags="-s -w" -o openwrt-agent .\cmd\agent
```

生成：

```text
openwrt-agent
```

把这个文件复制到 OpenWrt 虚拟机。

## 3. 编译 traffic-monitor

`traffic-monitor` 是 C 程序，依赖 OpenWrt 的 `libpcap`。它不能用 macOS 或普通 Ubuntu 的本地 gcc 直接编译给 OpenWrt 用，必须使用 OpenWrt SDK/toolchain 交叉编译。

推荐在那台 Windows 电脑的 WSL / Ubuntu x86_64 中编译。

### 3.1 准备 WSL / Ubuntu

在 WSL / Ubuntu 中安装依赖：

```bash
sudo apt update
sudo apt install -y build-essential clang flex bison g++ gawk gcc-multilib gettext git libncurses-dev libssl-dev python3-distutils rsync unzip zlib1g-dev file wget curl zstd
```

### 3.2 下载 OpenWrt SDK

下载与你 OpenWrt 虚拟机版本一致的 SDK。以 `24.10.0 x86/64` 为例：

```bash
curl -LO https://downloads.openwrt.org/releases/24.10.0/targets/x86/64/openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64.tar.zst
tar --use-compress-program=unzstd -xf openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64.tar.zst
```

如果你的 OpenWrt 版本不是 `24.10.0`，需要下载对应版本的 `x86/64` SDK。

### 3.3 编译 SDK 中的 libpcap

```bash
cd openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64
./scripts/feeds update packages
./scripts/feeds install libpcap
make package/libpcap/compile V=s
cd ..
```

### 3.4 编译 traffic-monitor

假设当前目录是项目根目录，`SDK` 指向刚才解压出的 SDK：

```bash
SDK=$PWD/openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64
TOOLCHAIN=$SDK/staging_dir/toolchain-x86_64_gcc-13.3.0_musl
TARGET=$SDK/staging_dir/target-x86_64_musl
export PATH="$TOOLCHAIN/bin:$PATH"

mkdir -p build/openwrt-x86_64
x86_64-openwrt-linux-musl-gcc \
  -O2 -Wall -Wextra \
  -I"$TARGET/usr/include" \
  -L"$TARGET/usr/lib" \
  -o build/openwrt-x86_64/traffic-monitor \
  monitor/traffic_monitor.c \
  -lpcap -lpthread
```

生成：

```text
build/openwrt-x86_64/traffic-monitor
```

## 复制到 OpenWrt

需要复制到 OpenWrt 的文件：

```text
openwrt-agent
traffic-monitor
scripts/firewall.sh
```

如果使用 Samba 共享目录，假设 Windows 复制后的文件在 OpenWrt 的 `/mnt/p0`，则在 OpenWrt 控制台执行：

```sh
mkdir -p /root/netlab/scripts
cp /mnt/p0/openwrt-agent /root/netlab/
cp /mnt/p0/traffic-monitor /root/netlab/
cp /mnt/p0/firewall.sh /root/netlab/scripts/
chmod +x /root/netlab/openwrt-agent /root/netlab/traffic-monitor /root/netlab/scripts/firewall.sh
```

OpenWrt 上安装运行依赖：

```sh
opkg update
opkg install libpcap
```

## OpenWrt 运行

查看接口名称：

```sh
ip addr
```

常见监控接口是 `br-lan`。如果你的接口不同，把下面命令里的 `br-lan` 换成实际接口。

启动流量监控：

```sh
cd /root/netlab
./traffic-monitor -i br-lan -o /tmp/traffic_stats.json -q
```

启动 OpenWrt agent：

```sh
cd /root/netlab
./openwrt-agent -listen 0.0.0.0:9090 -stats /tmp/traffic_stats.json -firewall-script /root/netlab/scripts/firewall.sh
```

后台运行方式：

```sh
nohup ./traffic-monitor -i br-lan -o /tmp/traffic_stats.json -q >/tmp/traffic-monitor.log 2>&1 &
nohup ./openwrt-agent -listen 0.0.0.0:9090 -stats /tmp/traffic_stats.json -firewall-script /root/netlab/scripts/firewall.sh >/tmp/openwrt-agent.log 2>&1 &
```

在 Windows 浏览器测试 OpenWrt agent：

```text
http://OpenWrt虚拟机IP:9090/agent/health
```

## Windows 运行 Web

在 Windows PowerShell 中运行：

```powershell
.\netlab-server.exe -listen 0.0.0.0:8080 -openwrt-agent http://OpenWrt虚拟机IP:9090
```

然后浏览器打开：

```text
http://127.0.0.1:8080/
```

如果其他设备也要访问 Web 页面，使用：

```text
http://Windows宿主机IP:8080/
```

## 功能说明

页面包含两个模块：

- 流量监控：读取 OpenWrt 上 `/tmp/traffic_stats.json`，展示源 IP、目的 IP、累计流量、峰值和平均速率。
- 防火墙配置：Windows 后端调用 OpenWrt agent，agent 在 OpenWrt 本机执行 `scripts/firewall.sh`，完成规则新增、查看、删除、清空和验证。

防火墙脚本默认规则方向：

```text
src=lan
dest=wan
```

如果你的 OpenWrt 防火墙区域名称不是 `lan/wan`，需要修改 [scripts/firewall.sh](scripts/firewall.sh) 中的：

```sh
uci set "firewall.$section.src=lan"
uci set "firewall.$section.dest=wan"
```

## 常用测试

测试 OpenWrt agent：

```bash
curl http://OpenWrt虚拟机IP:9090/agent/health
curl http://OpenWrt虚拟机IP:9090/agent/traffic
```

测试 Windows Web 后端：

```bash
curl http://Windows宿主机IP:8080/api/traffic
curl http://Windows宿主机IP:8080/api/firewall/list
```
