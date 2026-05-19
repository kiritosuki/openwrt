# OpenWrt 网络应用实验代码

本项目实现实验二要求的两个模块：

- `traffic-monitor`：C + libpcap 实时抓包统计，支持命令行展示，并周期性写出 JSON。
- `netlab-server`：Go 后端，运行在 Windows 宿主机，提供 Web 页面、流量数据 API、防火墙 API。
- `openwrt-agent`：Go 轻量 agent，运行在 OpenWrt，只负责读取流量 JSON 和本地调用防火墙脚本。
- `scripts/firewall.sh`：OpenWrt 防火墙规则脚本，通过 UCI 新增、查看、删除、清空实验规则。
- `cmd/server/web`：原生 HTML/CSS/JavaScript 前端，无 CDN 依赖。

## 目录结构

```text
cmd/server/main.go        Go 后端
cmd/server/web/           前端页面，已通过 go:embed 打进后端二进制
cmd/agent/main.go         OpenWrt 轻量 HTTP agent
monitor/traffic_monitor.c C/libpcap 流量监控程序
scripts/firewall.sh       OpenWrt 防火墙脚本
Makefile                  本地和交叉编译入口
```

## 推荐部署结构

你的 Windows 宿主机和 OpenWrt 虚拟机通过 VMware 虚拟局域网互通时，推荐这样部署：

```text
Windows 宿主机：
  netlab-server.exe
  浏览器访问 http://127.0.0.1:8080/

OpenWrt 虚拟机：
  traffic-monitor
  openwrt-agent
  scripts/firewall.sh
```

Windows 后端通过 HTTP 请求访问 OpenWrt agent，例如：

```text
http://OpenWrt虚拟机IP:9090
```

这个方案不需要配置 SSH。

## macOS 本地构建检查

Go 后端可直接构建：

```bash
make server
```

C 监控程序在 macOS 上需要先安装 libpcap 开发环境。macOS 系统一般自带 libpcap，可尝试：

```bash
make monitor
```

如果只想构建 OpenWrt 可运行的 Go 后端：

```bash
make openwrt-x86_64
```

产物位于：

```text
build/openwrt-x86_64/netlab-server
build/openwrt-x86_64/openwrt-agent
```

构建 Windows 宿主机运行的 Web 后端：

```bash
make windows-amd64
```

产物位于：

```text
build/windows-amd64/netlab-server.exe
```

## OpenWrt x86_64 交叉编译 C 监控程序

`traffic-monitor` 依赖 OpenWrt 的 `libpcap`，建议使用 OpenWrt SDK 交叉编译。注意：OpenWrt 官方 SDK 通常是 Linux x86_64 可执行工具链，macOS 不能直接运行其中的编译器。推荐在 macOS 上使用 Docker 运行 Linux 容器，并把当前项目目录挂载进去。

1. 在 macOS 安装 Docker Desktop，然后进入本项目目录，启动一个 Linux x86_64 容器：

```bash
docker run --rm -it --platform linux/amd64 \
  -v "$PWD":/work -w /work \
  ubuntu:24.04 bash
```

后续命令都在容器内执行。

2. 安装 SDK 需要的基础工具：

```bash
apt update
apt install -y build-essential clang flex bison g++ gawk gcc-multilib gettext git libncurses-dev libssl-dev python3-distutils rsync unzip zlib1g-dev file wget curl zstd
```

3. 下载与你虚拟机版本一致的 OpenWrt SDK，例如 x86/64：

```bash
curl -LO https://downloads.openwrt.org/releases/24.10.0/targets/x86/64/openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64.tar.zst
```

如果课程镜像版本不同，请进入对应版本目录下载 `openwrt-sdk-...x86-64...tar.zst`。

4. 解压 SDK：

```bash
tar --use-compress-program=unzstd -xf openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64.tar.zst
```

5. 进入 SDK，安装 libpcap 到 staging 环境：

```bash
cd openwrt-sdk-24.10.0-x86-64_gcc-13.3.0_musl.Linux-x86_64
./scripts/feeds update packages
./scripts/feeds install libpcap
make package/libpcap/compile V=s
```

6. 回到本项目目录，使用 SDK toolchain 编译。请把 `SDK` 改成你的实际 SDK 路径：

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

OpenWrt agent 不依赖 CGO，可直接交叉编译：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags="-s -w" \
  -o build/openwrt-x86_64/openwrt-agent ./cmd/agent
```

Windows Web 后端也不依赖 CGO，可从 macOS 交叉编译：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags="-s -w" \
  -o build/windows-amd64/netlab-server.exe ./cmd/server
```

## 复制到 OpenWrt

将以下文件复制到 OpenWrt，例如 `/root/netlab`：

```text
build/openwrt-x86_64/openwrt-agent
build/openwrt-x86_64/traffic-monitor
scripts/firewall.sh
```

如果不配置 SSH，建议使用实验指导书里的 Samba 共享目录。假设共享目录映射到 OpenWrt 的 `/mnt/p0`，在 Windows 中把文件复制进去后，在 OpenWrt 控制台执行：

```sh
mkdir -p /root/netlab/scripts
cp /mnt/p0/openwrt-agent /root/netlab/
cp /mnt/p0/traffic-monitor /root/netlab/
cp /mnt/p0/firewall.sh /root/netlab/scripts/
chmod +x /root/netlab/openwrt-agent /root/netlab/traffic-monitor /root/netlab/scripts/firewall.sh
```

OpenWrt 上需要安装运行依赖：

```sh
opkg update
opkg install libpcap
```

## OpenWrt 运行方式

1. 查看网卡名称：

```sh
ip addr
```

常见接口是 `br-lan`、`eth0`、`eth1`。如果想监控 LAN 侧流量，通常选择 `br-lan`。

2. 启动流量监控：

```sh
cd /root/netlab
./traffic-monitor -i br-lan -o /tmp/traffic_stats.json
```

后台运行：

```sh
nohup ./traffic-monitor -i br-lan -o /tmp/traffic_stats.json -q >/tmp/traffic-monitor.log 2>&1 &
```

3. 启动 OpenWrt agent：

```sh
cd /root/netlab
./openwrt-agent -listen 0.0.0.0:9090 -stats /tmp/traffic_stats.json -firewall-script /root/netlab/scripts/firewall.sh
```

后台运行：

```sh
nohup ./openwrt-agent -listen 0.0.0.0:9090 -stats /tmp/traffic_stats.json -firewall-script /root/netlab/scripts/firewall.sh >/tmp/openwrt-agent.log 2>&1 &
```

4. 在 Windows 宿主机测试 OpenWrt agent：

```text
http://OpenWrt虚拟机IP:9090/agent/health
http://OpenWrt虚拟机IP:9090/agent/traffic
```

## Windows 宿主机运行 Web 后端

把 `build/windows-amd64/netlab-server.exe` 复制到 Windows 后，在 PowerShell 中运行：

```powershell
.\netlab-server.exe -listen 0.0.0.0:8080 -openwrt-agent http://OpenWrt虚拟机IP:9090
```

然后在 Windows 浏览器打开：

```text
http://127.0.0.1:8080/
```

## API 说明

流量数据：

```bash
curl http://Windows宿主机IP:8080/api/traffic
```

也可以直接测试 OpenWrt agent：

```bash
curl http://OpenWrt虚拟机IP:9090/agent/traffic
```

新增防火墙规则：

```bash
curl -X POST http://Windows宿主机IP:8080/api/firewall/add \
  -H 'Content-Type: application/json' \
  -d '{"proto":"tcp","src_ip":"192.168.1.100","dest_ip":"8.8.8.8","port":"80","action":"reject"}'
```

查看规则：

```bash
curl http://Windows宿主机IP:8080/api/firewall/list
```

删除规则：

```bash
curl -X POST http://Windows宿主机IP:8080/api/firewall/delete \
  -H 'Content-Type: application/json' \
  -d '{"name":"cnslab_规则名称"}'
```

清空本实验创建的规则：

```bash
curl -X POST http://Windows宿主机IP:8080/api/firewall/clear
```

连接验证：

```bash
curl -X POST http://Windows宿主机IP:8080/api/firewall/verify \
  -H 'Content-Type: application/json' \
  -d '{"host":"8.8.8.8","port":"53"}'
```

## 防火墙规则说明

脚本通过 OpenWrt UCI 写入 `config rule`，规则名称统一以 `cnslab_` 开头。`clear` 只删除本实验创建的规则，不会清空系统原有防火墙配置。

默认规则方向为：

```text
src=lan
dest=wan
```

这适合验证“LAN 主机经过 OpenWrt 访问外网”的控制效果。如果你的虚拟机网络区域名称不是 `lan/wan`，请按实际 `/etc/config/firewall` 调整 `scripts/firewall.sh` 中的 `src` 和 `dest`。

## 演示建议

1. 展示 OpenWrt 虚拟机 IP、网络连通性和接口名称。
2. 启动 `traffic-monitor`，命令行中能看到源 IP、目的 IP、累计流量、峰值和平均速率。
3. 启动 `netlab-server`，浏览器访问 Web 页面，刷新流量监控表格和曲线。
4. 在 Web 页面添加一条 TCP/UDP/ICMP 防火墙规则，展示后端返回结果和规则列表变化。
5. 使用页面中的连接验证，或在 OpenWrt 命令行使用 `ping`、`nc`、`curl` 验证规则生效。

## AI 使用说明建议写法

实验报告中可说明：使用 AI 辅助完成项目结构设计、Go 后端接口、C/libpcap 抓包统计、防火墙 Shell 脚本和前端页面初稿；人工检查了参数校验、防火墙脚本执行边界、OpenWrt 运行命令，并在虚拟机环境中完成编译部署和规则验证。报告中需要附上关键交互截图和你对生成代码的理解说明。
