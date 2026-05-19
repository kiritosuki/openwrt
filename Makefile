APP_NAME := netlab-server
AGENT_NAME := openwrt-agent
MONITOR_NAME := traffic-monitor
BUILD_DIR := build

.PHONY: all server agent monitor clean openwrt-x86_64 windows-amd64

all: server agent monitor

server:
	mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME) ./cmd/server

agent:
	mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/$(AGENT_NAME) ./cmd/agent

monitor:
	mkdir -p $(BUILD_DIR)
	$(CC) -O2 -Wall -Wextra -o $(BUILD_DIR)/$(MONITOR_NAME) monitor/traffic_monitor.c -lpcap -lpthread

openwrt-x86_64:
	mkdir -p $(BUILD_DIR)/openwrt-x86_64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/openwrt-x86_64/$(AGENT_NAME) ./cmd/agent
	@echo "Build C monitor with OpenWrt SDK/toolchain, see README.md"

windows-amd64:
	mkdir -p $(BUILD_DIR)/windows-amd64
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BUILD_DIR)/windows-amd64/$(APP_NAME).exe ./cmd/server

clean:
	rm -rf $(BUILD_DIR)
