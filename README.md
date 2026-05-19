# OpenWrt 网络应用实验代码

本项目用于完成“基于 OpenWrt 的网络应用程序开发”实验。

部署方式：

```text
Windows 宿主机：
  netlab-server.exe        Web 前端 + Go 后端

OpenWrt 虚拟机：
  openwrt-agent            轻量 HTTP agent
  traffic-monitor          C/libpcap 流量监控
  scripts/firewall.sh      防火墙规则脚本
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

## 编译

### 1. 编译 Windows Web 后端

在 macOS 项目根目录执行：

```bash
make windows-amd64
```

生成：

```text
build/windows-amd64/netlab-server.exe
```

把这个文件复制到 Windows 宿主机。

### 2. 编译 OpenWrt agent

在 macOS 项目根目录执行：

```bash
make openwrt-x86_64
```

生成：

```text
build/openwrt-x86_64/openwrt-agent
```

这个文件复制到 OpenWrt。

### 3. 编译 OpenWrt 流量监控程序

`traffic-monitor` 依赖 OpenWrt 的 `libpcap`，不能使用 macOS 本地 `make monitor` 的产物放到 OpenWrt 运行。

需要使用 OpenWrt SDK 交叉编译。核心命令如下，`SDK` 改成你的 OpenWrt SDK 实际路径：

```bash
SDK=/path/to/openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64
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

最终需要得到：

```text
build/openwrt-x86_64/traffic-monitor
```

## 复制到 OpenWrt

需要复制到 OpenWrt 的文件：

```text
build/openwrt-x86_64/openwrt-agent
build/openwrt-x86_64/traffic-monitor
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

OpenWrt 需要安装 libpcap：

```sh
opkg update
opkg install libpcap
```

## OpenWrt 运行

查看网卡名称：

```sh
ip addr
```

常见监控接口是 `br-lan`。启动流量监控：

```sh
cd /root/netlab
./traffic-monitor -i br-lan -o /tmp/traffic_stats.json -q
```

启动 OpenWrt agent：

```sh
cd /root/netlab
./openwrt-agent -listen 0.0.0.0:9090 -stats /tmp/traffic_stats.json -firewall-script /root/netlab/scripts/firewall.sh
```

也可以后台运行：

```sh
nohup ./traffic-monitor -i br-lan -o /tmp/traffic_stats.json -q >/tmp/traffic-monitor.log 2>&1 &
nohup ./openwrt-agent -listen 0.0.0.0:9090 -stats /tmp/traffic_stats.json -firewall-script /root/netlab/scripts/firewall.sh >/tmp/openwrt-agent.log 2>&1 &
```

在 Windows 浏览器测试：

```text
http://OpenWrt虚拟机IP:9090/agent/health
```

## Windows 运行

在 Windows PowerShell 中运行：

```powershell
.\netlab-server.exe -listen 0.0.0.0:8080 -openwrt-agent http://OpenWrt虚拟机IP:9090
```

然后浏览器打开：

```text
http://127.0.0.1:8080/
```

## 使用说明

页面包含两个模块：

- 流量监控：周期性读取 OpenWrt 上 `/tmp/traffic_stats.json`，展示源 IP、目的 IP、累计流量、峰值和平均速率。
- 防火墙配置：通过 Windows 后端调用 OpenWrt agent，agent 在 OpenWrt 本机执行 `scripts/firewall.sh`，完成规则新增、查看、删除、清空和验证。

防火墙脚本默认规则方向：

```text
src=lan
dest=wan
```

如果你的 OpenWrt 防火墙区域名称不是 `lan/wan`，需要修改 [scripts/firewall.sh](scripts/firewall.sh) 中对应的 `uci set firewall.$section.src=lan` 和 `uci set firewall.$section.dest=wan`。

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
