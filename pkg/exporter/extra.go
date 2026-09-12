package exporter

import (
	"log"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/example/zte-sr1010-exporter/pkg/client"
)

// extraMetrics holds the descriptors of the metrics added on top of the
// original home-page set. They are created via (*ZTEExporter).desc so that
// Describe() reports them automatically.
type extraMetrics struct {
	// system
	sysUptime *prometheus.Desc
	sysInfo   *prometheus.Desc
	sysTime   *prometheus.Desc
	ntpSync   *prometheus.Desc
	ntpPoll   *prometheus.Desc
	ntpInfo   *prometheus.Desc
	natMode   *prometheus.Desc

	// LAN / DHCP
	lanInfo    *prometheus.Desc
	dhcpEnable *prometheus.Desc
	dhcpLease  *prometheus.Desc
	dhcpPool   *prometheus.Desc
	dhcpRange  *prometheus.Desc

	// WAN detail
	wanUptime   *prometheus.Desc
	wanInfo     *prometheus.Desc
	wanIPv6Info *prometheus.Desc
	wanMTU      *prometheus.Desc
	wanRxBytes  *prometheus.Desc
	wanTxBytes  *prometheus.Desc
	wanRxPkts   *prometheus.Desc
	wanTxPkts   *prometheus.Desc

	// ethernet ports
	ethUp       *prometheus.Desc
	ethSpeed    *prometheus.Desc
	ethMaxSpeed *prometheus.Desc
	ethRxBps    *prometheus.Desc
	ethTxBps    *prometheus.Desc
	ethIsWAN    *prometheus.Desc
	ethUpstream *prometheus.Desc

	// wifi radios
	wifiRadioUp   *prometheus.Desc
	wifiChannel   *prometheus.Desc
	wifiMaxRate   *prometheus.Desc
	wifiTxPower   *prometheus.Desc
	wifiStandard  *prometheus.Desc
	wifiBandwidth *prometheus.Desc

	// wifi SSIDs
	ssidInfo    *prometheus.Desc
	ssidEnabled *prometheus.Desc

	// mesh topology
	meshInfo      *prometheus.Desc
	meshUp        *prometheus.Desc
	meshLinkSpeed *prometheus.Desc
	meshChannel   *prometheus.Desc
	meshAffSta    *prometheus.Desc
	meshRSSI      *prometheus.Desc
	meshSNR       *prometheus.Desc
	meshChild     *prometheus.Desc
	meshCount     *prometheus.Desc

	// LAN clients
	clientInfo       *prometheus.Desc
	clientOnline     *prometheus.Desc
	clientRSSI       *prometheus.Desc
	clientRxBytes    *prometheus.Desc
	clientTxBytes    *prometheus.Desc
	clientUpload     *prometheus.Desc
	clientDownload   *prometheus.Desc
	clientOnlineSecs *prometheus.Desc
	clientCount      *prometheus.Desc
	clientOnlineCnt  *prometheus.Desc
	clientCountByNet *prometheus.Desc

	// routing / tables
	routeV4Count *prometheus.Desc
	routeV6Count *prometheus.Desc
	arpCount     *prometheus.Desc
	arpInfo      *prometheus.Desc

	// services
	ddnsInfo      *prometheus.Desc
	ddnsCount     *prometheus.Desc
	portFwdCount  *prometheus.Desc
	portFwdInfo   *prometheus.Desc
	fwMACFilter   *prometheus.Desc
	fwURLFilter   *prometheus.Desc
	staticBindCnt *prometheus.Desc
}

func (e *ZTEExporter) newExtraMetrics() *extraMetrics {
	m := &extraMetrics{}

	m.sysUptime = e.desc("zte_system_uptime_seconds", "Router uptime in seconds")
	m.sysInfo = e.desc("zte_system_info", "Router hardware/software identity (value always 1)",
		"model", "hardware_ver", "boot_ver", "software_ver", "serial", "manufacturer", "mode")
	m.sysTime = e.desc("zte_system_time_seconds", "Router local clock (unix seconds)")
	m.ntpSync = e.desc("zte_ntp_sync_status", "1 if the router clock is synchronised with NTP")
	m.ntpPoll = e.desc("zte_ntp_poll_interval_seconds", "NTP poll interval in seconds")
	m.ntpInfo = e.desc("zte_ntp_info", "Configured NTP servers (value always 1)",
		"server1", "server2", "server3", "server4", "server5")
	m.natMode = e.desc("zte_nat_mode", "NAT mode as configured on the router")

	m.lanInfo = e.desc("zte_lan_info", "LAN/bridge configuration (value always 1)", "ip", "subnet_mask")
	m.dhcpEnable = e.desc("zte_dhcp_server_enabled", "1 if the LAN DHCP server is enabled")
	m.dhcpLease = e.desc("zte_dhcp_lease_seconds", "DHCP lease time in seconds")
	m.dhcpPool = e.desc("zte_dhcp_pool_addresses", "Number of addresses in the DHCP pool")
	m.dhcpRange = e.desc("zte_dhcp_range_info", "DHCP pool/range (value always 1)",
		"min_address", "max_address", "subnet_mask", "gateway", "dns1", "dns2")

	m.wanUptime = e.desc("zte_wan_uptime_seconds", "WAN connection uptime in seconds", "wan")
	m.wanInfo = e.desc("zte_wan_info", "Per-WAN connection details (value always 1)",
		"wan", "name", "conn_type", "trans_type", "ip", "gateway", "dns1", "dns2", "mac", "status", "vlan")
	m.wanIPv6Info = e.desc("zte_wan_ipv6_info", "Per-WAN IPv6 details (value always 1)",
		"wan", "ipv6", "ipv6_prefix_len", "gateway", "pd", "pd_len", "status")
	m.wanMTU = e.desc("zte_wan_mtu", "WAN MTU / MRU", "wan")
	m.wanRxBytes = e.desc("zte_wan_receive_bytes_total", "WAN received bytes", "wan")
	m.wanTxBytes = e.desc("zte_wan_transmit_bytes_total", "WAN transmitted bytes", "wan")
	m.wanRxPkts = e.desc("zte_wan_receive_packets_total", "WAN received packets", "wan")
	m.wanTxPkts = e.desc("zte_wan_transmit_packets_total", "WAN transmitted packets", "wan")

	m.ethUp = e.desc("zte_eth_port_up", "1 if the Ethernet port has link", "port", "name")
	m.ethSpeed = e.desc("zte_eth_port_speed_code", "Negotiated port speed code (router enum)", "port", "name")
	m.ethMaxSpeed = e.desc("zte_eth_port_max_speed_code", "Maximum port speed code (router enum)", "port", "name")
	m.ethRxBps = e.desc("zte_eth_port_receive_bps", "Port receive rate in bits per second", "port", "name")
	m.ethTxBps = e.desc("zte_eth_port_transmit_bps", "Port transmit rate in bits per second", "port", "name")
	m.ethIsWAN = e.desc("zte_eth_port_is_wan", "1 if the port is WAN-capable", "port", "name")
	m.ethUpstream = e.desc("zte_eth_port_upstream", "1 if the port is configured as WAN uplink", "port", "name")

	m.wifiRadioUp = e.desc("zte_wifi_radio_up", "1 if the radio is enabled", "radio", "band")
	m.wifiChannel = e.desc("zte_wifi_radio_channel", "Current radio channel", "radio", "band")
	m.wifiMaxRate = e.desc("zte_wifi_radio_max_rate_bps", "Radio maximum PHY rate in bits per second", "radio", "band")
	m.wifiTxPower = e.desc("zte_wifi_radio_tx_power_percent", "Radio transmit power percent", "radio", "band")
	m.wifiStandard = e.desc("zte_wifi_radio_standard_info", "Radio supported 802.11 standard (value always 1)",
		"radio", "band", "standard")
	m.wifiBandwidth = e.desc("zte_wifi_radio_bandwidth_info", "Radio channel bandwidth setting (value always 1)",
		"radio", "band", "bandwidth")

	m.ssidInfo = e.desc("zte_wifi_ssid_info", "WiFi SSID details (value always 1)",
		"ssid", "band", "type", "radio", "security")
	m.ssidEnabled = e.desc("zte_wifi_ssid_enabled", "1 if the SSID is enabled", "ssid", "band", "type")

	m.meshInfo = e.desc("zte_mesh_ap_info", "Mesh node identity (value always 1)",
		"mac", "name", "model", "role", "ip", "ipv6", "access_type", "software_ver", "inst_id", "parent", "ap_type")
	m.meshUp = e.desc("zte_mesh_ap_up", "1 if the mesh node is online", "mac", "name")
	m.meshLinkSpeed = e.desc("zte_mesh_ap_link_speed_mbps", "Mesh node negotiated link speed in Mbps", "mac", "name")
	m.meshChannel = e.desc("zte_mesh_ap_channel", "Mesh node operating channel", "mac", "name", "band")
	m.meshAffSta = e.desc("zte_mesh_ap_affiliated_stations", "Stations associated to the mesh node", "mac", "name")
	m.meshRSSI = e.desc("zte_mesh_ap_backhaul_rssi", "Mesh backhaul RSSI in dBm", "mac", "name")
	m.meshSNR = e.desc("zte_mesh_ap_backhaul_snr", "Mesh backhaul SNR in dB", "mac", "name")
	m.meshChild = e.desc("zte_mesh_ap_child_count", "Downstream 1905 devices reported for the mesh node", "mac", "name")
	m.meshCount = e.desc("zte_mesh_ap_count", "Number of mesh nodes reported by the topology API")

	m.clientInfo = e.desc("zte_client_info", "LAN client details (value always 1)",
		"mac", "ip", "hostname", "dev_name", "interface", "link", "band", "vendor", "type", "ssid", "parent_dev", "guest")
	m.clientOnline = e.desc("zte_client_online", "1 if the client is currently online", "mac", "hostname")
	m.clientRSSI = e.desc("zte_client_rssi_dbm", "Wireless client RSSI in dBm", "mac", "hostname")
	m.clientRxBytes = e.desc("zte_client_receive_bytes_total", "Client received bytes (since association)", "mac")
	m.clientTxBytes = e.desc("zte_client_transmit_bytes_total", "Client transmitted bytes (since association)", "mac")
	m.clientUpload = e.desc("zte_client_upload_rate", "Client upload rate in router-reported units", "mac")
	m.clientDownload = e.desc("zte_client_download_rate", "Client download rate in router-reported units", "mac")
	m.clientOnlineSecs = e.desc("zte_client_online_seconds", "Client online time in seconds", "mac")
	m.clientCount = e.desc("zte_client_count", "Number of known LAN clients")
	m.clientOnlineCnt = e.desc("zte_client_online_count", "Number of currently online LAN clients")
	m.clientCountByNet = e.desc("zte_client_count_by_link", "Number of LAN clients per link type", "link")

	m.routeV4Count = e.desc("zte_route_ipv4_count", "Number of IPv4 routes")
	m.routeV6Count = e.desc("zte_route_ipv6_count", "Number of IPv6 routes")
	m.arpCount = e.desc("zte_arp_entry_count", "Number of ARP table entries")
	m.arpInfo = e.desc("zte_arp_info", "ARP table entry (value always 1)", "ip", "mac", "interface", "status")

	m.ddnsInfo = e.desc("zte_ddns_client_info", "DDNS client entry (value always 1)",
		"domain", "sub_domain", "service", "interface", "status", "enabled")
	m.ddnsCount = e.desc("zte_ddns_client_count", "Number of configured DDNS clients")
	m.portFwdCount = e.desc("zte_port_forward_count", "Number of port-forwarding rules")
	m.portFwdInfo = e.desc("zte_port_forward_info", "Port-forwarding rule (value always 1)",
		"alias", "protocol", "external_port", "internal_client", "internal_port", "interface", "enabled")
	m.fwMACFilter = e.desc("zte_firewall_mac_filter_enabled", "1 if MAC filtering is enabled")
	m.fwURLFilter = e.desc("zte_firewall_url_filter_enabled", "1 if URL filtering is enabled")
	m.staticBindCnt = e.desc("zte_static_binding_count", "Number of DHCP static bindings")

	return m
}

// collectSSIDs parses the main/guest SSID tables embedded in the home page.
func (e *ZTEExporter) collectSSIDs(ch chan<- prometheus.Metric, body string) {
	m := e.x
	sections := []struct{ tag, kind string }{
		{"OBJ_WLANMAINSSID_ID", "main"},
		{"OBJ_WLANGUESTSSID_ID", "guest"},
	}
	for _, s := range sections {
		for _, p := range client.Section(body, s.tag) {
			ssid := client.Clean(p.Get("ESSID"))
			if ssid == "" {
				continue
			}
			band := client.Clean(p.Get("CardBand"))
			radio := instSuffix(p.Get("_InstID"))
			e.gauge(ch, m.ssidInfo, 1, ssid, band, s.kind, radio, client.Clean(p.Get("BeaconType")))
			e.gauge(ch, m.ssidEnabled, p.Bool01("Enable"), ssid, band, s.kind)
		}
	}
}

// collectSystem fetches device info + NAT mode.
func (e *ZTEExporter) collectSystem(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetHomeManageReg()
	if err != nil {
		log.Printf("GetHomeManageReg failed: %v", err)
		return
	}
	if dev := client.SectionFirst(body, "OBJ_DEVINFO_ID"); dev != nil {
		e.gauge(ch, m.sysUptime, float64(dev.IntOr("UpTime", 0)))
		model := client.Clean(dev.Get("ModelName"))
		if model == "" {
			model = client.Clean(dev.Get("DisplayModelName"))
		}
		e.gauge(ch, m.sysInfo, 1,
			model,
			client.Clean(dev.Get("HardwareVer")),
			client.Clean(dev.Get("BootVer")),
			client.Clean(dev.Get("SoftwareVer")),
			client.Clean(dev.Get("SerialNumber")),
			client.Clean(dev.Get("ManuFacturer")),
			client.Clean(client.ExtractPara(body, "Mode")),
		)
	}

	if nm, err := e.client.GetNatMode(); err == nil {
		if p := client.SectionFirst(nm, "OBJ_FW_NATMODE_ID"); p != nil {
			e.gauge(ch, m.natMode, float64(p.IntOr("NatMode", 0)))
		}
	} else {
		log.Printf("GetNatMode failed: %v", err)
	}
}

// collectTime fetches NTP state and the router clock.
func (e *ZTEExporter) collectTime(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetSNTP()
	if err != nil {
		log.Printf("GetSNTP failed: %v", err)
		return
	}
	sn := client.SectionFirst(body, "OBJ_SNTP_ID")
	if sn == nil {
		return
	}

	e.gauge(ch, m.ntpSync, bool01(sn.Get("SynStatusCode") == "2"))
	if v, ok := parseFloat(sn.Get("PollTimeInterval")); ok {
		e.gauge(ch, m.ntpPoll, v)
	}
	e.gauge(ch, m.ntpInfo, 1,
		client.Clean(sn.Get("NtpServer1")),
		client.Clean(sn.Get("NtpServer2")),
		client.Clean(sn.Get("NtpServer3")),
		client.Clean(sn.Get("NtpServer4")),
		client.Clean(sn.Get("NtpServer5")),
	)

	if t, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(sn.Get("CurrentLocalTime")), time.Local); err == nil {
		e.gauge(ch, m.sysTime, float64(t.Unix()))
	}
}

// collectAddrManager fetches LAN/bridge and DHCP server configuration.
func (e *ZTEExporter) collectAddrManager(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetAddrManager()
	if err != nil {
		log.Printf("GetAddrManager failed: %v", err)
		return
	}

	br := client.SectionFirst(body, "OBJ_BRGRP_ID")
	if br != nil {
		e.gauge(ch, m.lanInfo, 1, client.Clean(br.Get("IPAddr")), client.Clean(br.Get("SubMask")))
	}

	dh := client.SectionFirst(body, "OBJ_DHCPHOST_ID")
	if dh == nil {
		return
	}
	e.gauge(ch, m.dhcpEnable, dh.Bool01("ServerEnable"))
	if v, ok := parseFloat(dh.Get("LeaseTime")); ok {
		e.gauge(ch, m.dhcpLease, v)
	}
	minAddr := client.Clean(dh.Get("MinAddress"))
	maxAddr := client.Clean(dh.Get("MaxAddress"))
	if size, ok := ipv4PoolSize(minAddr, maxAddr); ok {
		e.gauge(ch, m.dhcpPool, size)
	}
	e.gauge(ch, m.dhcpRange, 1,
		minAddr,
		maxAddr,
		client.Clean(dh.Get("SubnetMask")),
		client.Clean(dh.Get("IPRouters")),
		client.Clean(dh.Get("DNSServer1")),
		client.Clean(dh.Get("DNSServer2")),
	)
}
