# zte-sr1010-exporter

面向 **ZTE ZXSLC SR1010（星云 MAX）** 及同类 Vue 界面 ZTE 家用路由器的 Prometheus Exporter。

它直接调用路由器 Web 页面背后的 Vue AJAX 接口（`_type=vueData&_tag=...`），将系统信息、WAN、LAN/DHCP、以太网口、WiFi 射频与 SSID、Mesh 组网、接入终端、路由表、ARP、DDNS、端口映射等数据暴露为 Prometheus 指标。

- Go 1.21+，零 CGO，单二进制
- 共 77 个指标，约 20 个路由器接口
- 附带 Grafana 面板 `grafana-dashboard-sr1010.json`

## 特性

- **完整登录流程**：实现抓包确认的 3 步登录，密码为 `SHA256(password + logintoken)`。
- **会话复用与保活**：跨抓取复用会话，成功后发 `heartbeat_lua`；超过 5 分钟强制重登。
- **自动恢复**：响应判定为未认证时自动重登并重试；首页数据异常时标记会话失效。
- **失败退避**：登录失败后 65 秒内不再尝试，避免触发路由器约 60 秒的账号锁定。
- **容错采集**：单个子接口失败只记日志并跳过其指标，不影响 `zte_up`。
- **可裁剪**：可用开关关闭最重的两项采集以降低抓取耗时。

## 快速开始

### 本地构建运行

```bash
go mod tidy
go build -o zte-sr1010-exporter .

export ZTE_URL=http://192.168.5.1
export ZTE_PASSWORD=你的路由器管理密码   # 必填
export DEBUG=true                        # 排障时开启

./zte-sr1010-exporter --listen :9100

curl -s http://127.0.0.1:9100/health     # ok
curl -s http://127.0.0.1:9100/metrics | head
```

SR1010 页面只要求密码，接口层固定传 `Username=admin`，通常无需设置 `ZTE_USERNAME`。

### Docker Compose

```bash
# 先修改 docker-compose.yml 中的 ZTE_PASSWORD（默认为 CHANGE_ME）
docker compose up -d --build
curl -s http://127.0.0.1:9100/metrics
```

> Compose 显式设置了 `LISTEN=:9100` 并映射 `9100:9100`。若直接 `docker run` 且不传 `LISTEN`，容器内监听的是默认的 `:9100`，需映射 `9100` 或加 `-e LISTEN=:9100`。

## 配置项

| 命令行 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--url` | `ZTE_URL` | `http://192.168.5.1` | 路由器管理地址 |
| `--username` | `ZTE_USERNAME` | `admin` | 登录用户名（SR1010 固定为 admin） |
| `--password` | `ZTE_PASSWORD` | – | 登录密码，**必填**，为空则进程退出 |
| `--listen` | `LISTEN` | `:9100` | 监听地址 |
| `--timeout` | `TIMEOUT` | `8` | 单个 HTTP 请求超时（秒） |
| `--debug` | `DEBUG` | `false` | 详细日志（含 URL、token、响应片段） |
| `--disable-clients` | `DISABLE_CLIENTS` | `false` | 跳过 LAN 终端明细表（体积最大的一项） |
| `--disable-arp` | `DISABLE_ARP` | `false` | 跳过 ARP 表（需 POST） |

低端路由器或对延迟敏感时建议开启后两个开关，此时 `zte_client_*`、`zte_arp_*` 不再产生。

## HTTP 端点

| 路径 | 说明 |
|---|---|
| `/metrics` | Prometheus 指标（独立 Registry，不含 Go 运行时自带指标） |
| `/health` | 固定返回 `200 ok`，仅表示进程存活，不探测路由器 |

## Prometheus 接入

```yaml
scrape_configs:
  - job_name: zte
    scrape_interval: 60s
    scrape_timeout: 30s
    static_configs:
      - targets: ['192.168.10.55:9100']
```

- 一轮抓取会串行发起近 20 个请求，**间隔建议 ≥ 30s**，过短会加重路由器负担甚至触发会话抢占。
- `scrape_timeout` 留足余量：`--timeout` 只管单个请求，不是整轮耗时。
- 配套面板通过顶部 `$job` 变量过滤（默认值 `zte`），`job_name` 保持一致即可；如需采集多台路由器，用不同 `job_name` 即可在同一下拉框中切换。
- 告警推荐 `zte_up == 0`、`zte_scrape_duration_seconds > 20`。

## Grafana 面板

Grafana → Dashboards → Import → 上传 `grafana-dashboard-sr1010.json`。面板标题为「ZTE ZXSLC SR1010 监控」，UID 为 `zte-sr1010-exporter`。导入后顶部有两个变量：**数据源**（自动列出 Prometheus 数据源，无需改 JSON）与 **任务 `$job`**（默认 `zte`，取自 `label_values(zte_up, job)`）。

面板共 47 个，分 5 组：

| 分组 | 内容 |
|---|---|
| 概览（原有） | 导出器状态、WAN 状态与实时速率、当日流量、接入设备/Mesh AP 数、采集耗时与健康度、路由器信息 |
| 系统 / 网络概览 | 运行时长、NTP 同步、NAT 模式、双 WAN、DHCP 地址池、终端数量、路由/ARP 条数；设备硬件固件信息表、WAN 连接详情表 |
| 以太网口 / 无线 | 端口连接状态与速率、端口实时速率曲线、射频状态表、信道变化、SSID 列表、支持标准与频宽 |
| Mesh 组网 | 节点拓扑表、回传链路速率条图、在线节点与关联终端趋势、回传 RSSI/SNR 与信道 |
| 接入终端 / 网络服务 | 终端列表（在线/RSSI/在线时长）、接入方式环图、终端流量 Top5、端口映射、DDNS、防火墙开关、LAN/DHCP 网段 |

端口表格、Mesh 拓扑表等多查询面板使用 `joinByField` 按 `name` / `mac` / `radio` 合并，需 Prometheus 数据源以 `format: table` + `instant` 返回，Grafana 版本建议 ≥ 10。

> 面板中的「当日累计流量」由 `increase(zte_wan_up_bps[24h])` 近似估算，而该指标是瞬时速率 gauge，抓取失败或路由器重启会产生偏差，仅作趋势参考。精确流量建议改用 `zte_wan_receive_bytes_total` / `zte_wan_transmit_bytes_total`。

## 工作原理

### 登录流程（抓包确认）

1. `GET /?_type=loginData&_tag=login_entry`
2. `GET /?_type=loginsceneData&_tag=login_token_json` → `{"_sessionToken":"...","logintoken":"..."}`
3. `POST /?_type=loginData&_tag=login_entry`，表单含 `Username`、`action=login`、`_sessionTOKEN`，其中 `Password = SHA256(明文密码 + logintoken)`

成功判定严格：响应须同时包含 `"loginErrType":""` 与 `sess_token`。以下签名一律判为失败并进入退避：

| 签名 | 含义 |
|---|---|
| `e_invalid_user_pwd` / `用户名或密码` | 密码错误 |
| `e_login_locked` | 账号锁定 |
| `e_exceed_max_user_preempt` | 会话被其他登录抢占 |

登录成功后额外访问一次首页（`vue_home_device_data_no_update_sess`）以完成会话绑定。

### 会话管理

- 会话年龄小于 5 分钟直接复用，否则强制重登；
- 抓取成功后发送 `heartbeat_lua` 保活；
- 子接口响应既非 JSON 又不含 `<ajax_response_xml_root` 时，自动重登并重试一次；
- 首页响应缺少 `WANUpRate` / `OBJ_HOME_BASICINFO_ID` 时标记会话失效，下轮抓取重登。

### 数据解析

路由器返回 GoAhead/Vue 风格扁平 XML：

```xml
<ajax_response_xml_root>
  <OBJ_DEVINFO_ID>
    <Instance>
      <ParaName>ModelName</ParaName><ParaValue>ZXSLC SR1010</ParaValue>
    </Instance>
  </OBJ_DEVINFO_ID>
</ajax_response_xml_root>
```

`pkg/client/parse.go` 将 `<Instance>` 解析为 `ParaName → ParaValue` 映射（重复键取首个），并处理 HTML 实体、MAC 规范化、空白折叠。Mesh 拓扑接口返回 JSON，由 `ParseTopo` 单独解析。

## 已确认的路由器接口

| 方法 | `_tag` | 用途 |
|---|---|---|
| GET | `loginData&_tag=login_entry` | 登录第 1 步 |
| GET | `loginsceneData&_tag=login_token_json` | 获取 logintoken / sessionToken |
| POST | `loginData&_tag=login_entry` | 登录第 3 步（提交哈希密码） |
| GET | `heartbeat_lua` | 会话保活 |
| GET | `vue_home_device_data_no_update_sess` | 首页概览（首选，`IF_OP=refresh`） |
| GET | `vue_home_device_data` | 首页概览（回退） |
| GET | `vue_topo_data&Action=GetALLAP` | Mesh 拓扑（JSON） |
| GET | `home_managreg_lua` | 设备信息 / 运行时长 |
| GET | `natmode_data` | NAT 模式 |
| GET | `sntp_lua` | NTP 状态与系统时间 |
| GET | `addr_manager_data` | LAN/网桥 + DHCP 配置 |
| GET | `vue_mainwan_data` / `vue_dualwan_data` | WAN1 / WAN2 配置与计数 |
| GET | `vue_internet_ethport_data` | 以太网口状态与速率 |
| GET | `wlanConfig_data` | WiFi 射频配置 |
| GET | `localnet_lan_info_lua` | LAN 终端明细表 |
| GET | `vue_routeipv4_table_data` / `vue_routeipv6_table_data` | IPv4 / IPv6 路由表 |
| GET | `ddns_data` | DDNS 客户端 |
| GET | `localnet_portforwarding_lua` | 端口映射规则 |
| GET | `security_globalctl_data` | 防火墙全局开关 |
| GET | `static_bindAddr_data` | DHCP 静态绑定 |
| POST | `arp_arptable_lua` | ARP 表（`_sessionTOKEN` + `IF_ACTION=DISPPART`） |

`vueData` 系列 GET 均附加毫秒时间戳 `_=<ms>` 以规避缓存。请求统一携带 `User-Agent: zte-sr1010-exporter/0.2` 与 `Referer`。

## 指标清单

全部以 `zte_` 为前缀。**信息类指标值恒为 1，实际数据在标签中**。

### 抓取状态

| 指标 | 标签 |
|---|---|
| `zte_up` | – 上次抓取是否成功 |
| `zte_scrape_success` | – 同上 |
| `zte_scrape_duration_seconds` | – 本轮耗时（秒） |
| `zte_router_info` | `model,software_ver,dev_name,ip,mac,mode` |

### 首页概览

| 指标 | 标签 / 说明 |
|---|---|
| `zte_wan_up_bps` | `wan` — 上行速率 bps，`wan` 为 `1`/`2` |
| `zte_wan_down_bps` | `wan` — 下行速率 bps |
| `zte_wan_link_speed_mbps` | – 协商速率 Mbps |
| `zte_wan_connected` | `wan` — 是否 Connected |
| `zte_access_device_count` | – 接入设备数 |
| `zte_topo_ap_count` | – 拓扑上报 AP 数 |
| `zte_dual_wan_enabled` | – 是否启用双 WAN |

### 系统 / 时间

| 指标 | 标签 / 说明 |
|---|---|
| `zte_system_uptime_seconds` | – 运行时长（秒） |
| `zte_system_info` | `model,hardware_ver,boot_ver,software_ver,serial,manufacturer,mode` |
| `zte_system_time_seconds` | – 路由器本地时间（Unix 秒） |
| `zte_ntp_sync_status` | – 是否已同步 |
| `zte_ntp_poll_interval_seconds` | – 轮询间隔（秒） |
| `zte_ntp_info` | `server1..server5` |
| `zte_nat_mode` | – NAT 模式取值 |

### LAN / DHCP

| 指标 | 标签 / 说明 |
|---|---|
| `zte_lan_info` | `ip,subnet_mask` |
| `zte_dhcp_server_enabled` | – DHCP 是否启用 |
| `zte_dhcp_lease_seconds` | – 租约时长（秒） |
| `zte_dhcp_pool_addresses` | – 地址池容量（按起止地址计算） |
| `zte_dhcp_range_info` | `min_address,max_address,subnet_mask,gateway,dns1,dns2` |

### WAN 明细

| 指标 | 标签 / 说明 |
|---|---|
| `zte_wan_uptime_seconds` | `wan` — 在线时长（秒） |
| `zte_wan_info` | `wan,name,conn_type,trans_type,ip,gateway,dns1,dns2,mac,status,vlan` |
| `zte_wan_ipv6_info` | `wan,ipv6,ipv6_prefix_len,gateway,pd,pd_len,status`（地址为 `::` 时不产生） |
| `zte_wan_mtu` | `wan` — MTU（缺失时回退 MRU） |
| `zte_wan_receive_bytes_total` | `wan` — 接收字节（counter） |
| `zte_wan_transmit_bytes_total` | `wan` — 发送字节（counter） |
| `zte_wan_receive_packets_total` | `wan` — 接收包数（counter） |
| `zte_wan_transmit_packets_total` | `wan` — 发送包数（counter） |

### 以太网口

| 指标 | 标签 / 说明 |
|---|---|
| `zte_eth_port_up` | `port,name` — 源字段 `EthPortStatus=0` 表示已连接 |
| `zte_eth_port_speed_code` | `port,name` — 当前速率枚举码（**固件枚举，未换算 Mbps**） |
| `zte_eth_port_max_speed_code` | `port,name` — 最大速率枚举码 |
| `zte_eth_port_receive_bps` | `port,name` — 接收速率 bps（已从 `1.4Kbps` 等换算） |
| `zte_eth_port_transmit_bps` | `port,name` — 发送速率 bps |
| `zte_eth_port_is_wan` | `port,name` — 是否具备 WAN 能力 |
| `zte_eth_port_upstream` | `port,name` — 是否配置为 WAN 上联口 |

### WiFi 射频 / SSID

| 指标 | 标签 / 说明 |
|---|---|
| `zte_wifi_radio_up` | `radio,band` — 射频是否开启 |
| `zte_wifi_radio_channel` | `radio,band` — 当前信道 |
| `zte_wifi_radio_max_rate_bps` | `radio,band` — 最大 PHY 速率 bps（源值为 kbps） |
| `zte_wifi_radio_tx_power_percent` | `radio,band` — 发射功率百分比 |
| `zte_wifi_radio_standard_info` | `radio,band,standard` |
| `zte_wifi_radio_bandwidth_info` | `radio,band,bandwidth` |
| `zte_wifi_ssid_info` | `ssid,band,type,radio,security` — `type` 为 `main`/`guest` |
| `zte_wifi_ssid_enabled` | `ssid,band,type` |

### Mesh 组网

| 指标 | 标签 / 说明 |
|---|---|
| `zte_mesh_ap_info` | `mac,name,model,role,ip,ipv6,access_type,software_ver,inst_id,parent,ap_type` |
| `zte_mesh_ap_up` | `mac,name` — 是否在线 |
| `zte_mesh_ap_link_speed_mbps` | `mac,name` — 协商速率 |
| `zte_mesh_ap_channel` | `mac,name,band` — `band` 为 `2.4G`/`5G`/`6G`，值为 0 时不产生 |
| `zte_mesh_ap_affiliated_stations` | `mac,name` — 关联终端数 |
| `zte_mesh_ap_backhaul_rssi` | `mac,name` — 回传 RSSI dBm（0 或 -1 时不产生） |
| `zte_mesh_ap_backhaul_snr` | `mac,name` — 回传 SNR dB |
| `zte_mesh_ap_child_count` | `mac,name` — 下游 1905 设备数 |
| `zte_mesh_ap_count` | – 节点总数 |

### LAN 终端（`--disable-clients` 可关闭）

| 指标 | 标签 / 说明 |
|---|---|
| `zte_client_info` | `mac,ip,hostname,dev_name,interface,link,band,vendor,type,ssid,parent_dev,guest`，`link` 为 `wifi`/`ethernet` |
| `zte_client_online` | `mac,hostname` |
| `zte_client_rssi_dbm` | `mac,hostname` — 为 0 时不产生 |
| `zte_client_receive_bytes_total` | `mac` — 自关联以来接收字节（counter） |
| `zte_client_transmit_bytes_total` | `mac` — 自关联以来发送字节（counter） |
| `zte_client_upload_rate` | `mac` — **固件原始单位，未换算** |
| `zte_client_download_rate` | `mac` — **固件原始单位，未换算** |
| `zte_client_online_seconds` | `mac` — 在线时长（秒） |
| `zte_client_count` | – 已知终端总数 |
| `zte_client_online_count` | – 当前在线数 |
| `zte_client_count_by_link` | `link` — 按接入方式统计 |

### 路由 / ARP / 服务

| 指标 | 标签 / 说明 |
|---|---|
| `zte_route_ipv4_count` | – IPv4 路由条数 |
| `zte_route_ipv6_count` | – IPv6 路由条数 |
| `zte_arp_entry_count` | – ARP 表项数（`--disable-arp` 时不产生） |
| `zte_arp_info` | `ip,mac,interface,status` |
| `zte_ddns_client_info` | `domain,sub_domain,service,interface,status,enabled` |
| `zte_ddns_client_count` | – DDNS 客户端数 |
| `zte_port_forward_info` | `alias,protocol,external_port,internal_client,internal_port,interface,enabled` |
| `zte_port_forward_count` | – 端口映射规则数 |
| `zte_firewall_mac_filter_enabled` | – MAC 过滤是否启用 |
| `zte_firewall_url_filter_enabled` | – URL 过滤是否启用 |
| `zte_static_binding_count` | – DHCP 静态绑定数 |

## 注意事项与限制

- **单位未换算的指标**：`zte_eth_port_speed_code`、`zte_eth_port_max_speed_code`、`zte_client_upload_rate`、`zte_client_download_rate` 按固件原值透传，固件未公开枚举含义，请以本机实测为准。
- **登录失败排查**：`DEBUG=true` 查看 token 响应与 POST 表单；若固件改版，需同步调整 `pkg/client/zte.go`。
- **会话被抢占**：路由器通常只允许一个管理会话，长期开着的 Web 页面会把 Exporter 挤下线，此时会返回 `e_exceed_max_user_preempt` 并进入 65 秒退避。
- **抓取失败只影响 `zte_up`**：子接口失败不会中断整轮抓取，日志中可见 `GetXxx failed`。
- **兼容性变更**：`zte_router_info` 已从 v0.1 的 `{key,value}` 形式改为独立标签（`model,software_ver,...`），升级时请同步更新面板与告警。

## 项目结构

```
.
├── main.go                              # 参数解析、Registry 注册、HTTP 服务
├── pkg/client/
│   ├── zte.go                           # 登录、会话管理、各接口请求
│   └── parse.go                         # XML/JSON 解析、实体转义、MAC 规范化
├── pkg/exporter/
│   ├── collector.go                     # Collector 主体 + 首页指标 + 通用工具
│   ├── extra.go                         # 指标描述符定义、SSID/系统/时间/LAN+DHCP
│   ├── extra_net.go                     # WAN / 以太网口 / WiFi / Mesh
│   └── extra_clients.go                 # LAN 终端 / ARP / 路由 / 服务
├── Dockerfile                           # 多阶段构建（golang:1.22-alpine → alpine:3.19）
├── docker-compose.yml                   # 一键部署
└── grafana-dashboard-sr1010.json        # Grafana 面板
```

## 开发与构建

```bash
# 交叉编译
GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/zte-sr1010-exporter-linux-amd64 .
GOOS=linux  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o dist/zte-sr1010-exporter-linux-arm64 .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o dist/zte-sr1010-exporter.exe .

# 注入版本号（未注入时源码内默认值为 0.1.0）
go build -ldflags "-s -w -X main.version=0.2.2 -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" .

# Docker 镜像
docker build -t zte-sr1010-exporter:0.2.2 .
```

> 模块路径当前为模板值 `github.com/example/zte-sr1010-exporter`（见 `go.mod`），正式发布前建议改为实际仓库地址，并同步修改 `main.go` 与 `pkg/exporter/*.go` 中的 import。

## 后续可改进

1. 为慢速固件增加抓取缓存或会话保活循环，减少每轮请求数。
2. 确认以太网速率枚举映射后，直接输出 `_mbps` 指标。
3. 增加分频段 / 分 SSID 的终端数量统计。
