package exporter

import (
	"log"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/example/zte-sr1010-exporter/pkg/client"
)

// collectClients parses the rich LAN client table (localnet_lan_info_lua).
func (e *ZTEExporter) collectClients(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetLanInfo()
	if err != nil {
		log.Printf("GetLanInfo failed: %v", err)
		return
	}

	insts := client.Section(body, "OBJ_LAN_INFO_ID")
	total, online := 0, 0
	byLink := map[string]int{}

	for _, p := range insts {
		mac := client.NormalizeMAC(p.Get("MACAddress"))
		if mac == "" {
			continue
		}
		total++

		hostName := client.Clean(p.Get("HostName"))
		if hostName == "" {
			hostName = client.Clean(p.Get("DevName"))
		}
		ip := client.Clean(p.Get("IPAddress"))
		iface := client.Clean(p.Get("Interface"))
		ssid := client.Clean(p.Get("IFAliasName"))
		rssi := client.Clean(p.Get("DirectRssi"))

		band := client.Clean(p.Get("Band"))
		if band == "" {
			band = bandFromDirectBand(p.Get("DirectBand"))
		}

		link := "ethernet"
		if (rssi != "" && rssi != "0") || strings.HasPrefix(ssid, "SSID") {
			link = "wifi"
		}

		isOnline := p.Get("Active") == "1"
		if isOnline {
			online++
		}
		byLink[link]++

		e.gauge(ch, m.clientInfo, 1,
			mac,
			ip,
			hostName,
			client.Clean(p.Get("DevName")),
			iface,
			link,
			band,
			client.Clean(p.Get("DisplayedVendor")),
			client.Clean(p.Get("DisplayedType")),
			ssid,
			client.Clean(p.Get("ParentDeviceName")),
			client.Clean(p.Get("isGuestAcessDev")),
		)

		e.gauge(ch, m.clientOnline, bool01(isOnline), mac, hostName)

		if v, ok := parseFloat(rssi); ok && v != 0 {
			e.gauge(ch, m.clientRSSI, v, mac, hostName)
		}
		if v, ok := parseFloat(p.Get("BytesReceived")); ok {
			e.counter(ch, m.clientRxBytes, v, mac)
		}
		if v, ok := parseFloat(p.Get("BytesSend")); ok {
			e.counter(ch, m.clientTxBytes, v, mac)
		}
		if v, ok := parseFloat(p.Get("UploadSpeed")); ok {
			e.gauge(ch, m.clientUpload, v, mac)
		}
		if v, ok := parseFloat(p.Get("DownloadSpeed")); ok {
			e.gauge(ch, m.clientDownload, v, mac)
		}
		if v, ok := parseFloat(p.Get("OnlineTime")); ok {
			e.gauge(ch, m.clientOnlineSecs, v, mac)
		}
	}

	e.gauge(ch, m.clientCount, float64(total))
	e.gauge(ch, m.clientOnlineCnt, float64(online))
	for link, n := range byLink {
		e.gauge(ch, m.clientCountByNet, float64(n), link)
	}
}

// collectARP parses the ARP table (POST arp_arptable_lua).
func (e *ZTEExporter) collectARP(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetARPTable()
	if err != nil {
		log.Printf("GetARPTable failed: %v", err)
		return
	}

	insts := client.Section(body, "OBJ_GETARPINST_ID")
	if len(insts) == 0 {
		return
	}
	e.gauge(ch, m.arpCount, float64(len(insts)))

	for _, p := range insts {
		ip := client.Clean(p.Get("DestIP"))
		mac := client.NormalizeMAC(p.Get("MACAddr"))
		if ip == "" && mac == "" {
			continue
		}
		e.gauge(ch, m.arpInfo, 1, ip, mac, client.Clean(p.Get("Interface")), client.Clean(p.Get("Status")))
	}
}

// collectRoutes counts IPv4/IPv6 route table entries.
func (e *ZTEExporter) collectRoutes(ch chan<- prometheus.Metric) {
	m := e.x

	if body, err := e.client.GetRouteIPv4(); err == nil {
		e.gauge(ch, m.routeV4Count, float64(len(client.Section(body, "OBJ_ROUTETABLE_ID"))))
	} else {
		log.Printf("GetRouteIPv4 failed: %v", err)
	}

	if body, err := e.client.GetRouteIPv6(); err == nil {
		e.gauge(ch, m.routeV6Count, float64(len(client.Section(body, "OBJ_ROUTETABLE6_ID"))))
	} else {
		log.Printf("GetRouteIPv6 failed: %v", err)
	}
}

// collectServices gathers DDNS, port-forwarding, firewall and static-binding
// information.
func (e *ZTEExporter) collectServices(ch chan<- prometheus.Metric) {
	m := e.x

	if body, err := e.client.GetDDNS(); err == nil {
		clients := client.Section(body, "OBJ_DDNSCLIENT_ID")
		e.gauge(ch, m.ddnsCount, float64(len(clients)))
		for _, p := range clients {
			e.gauge(ch, m.ddnsInfo, 1,
				client.Clean(p.Get("DomainName")),
				client.Clean(p.Get("SubDomain")),
				client.Clean(p.Get("Service")),
				client.Clean(p.Get("Interface")),
				client.Clean(p.Get("Status")),
				client.Clean(p.Get("Enable")),
			)
		}
	} else {
		log.Printf("GetDDNS failed: %v", err)
	}

	if body, err := e.client.GetPortForward(); err == nil {
		rules := client.Section(body, "OBJ_FWPM_ID")
		e.gauge(ch, m.portFwdCount, float64(len(rules)))
		for _, p := range rules {
			e.gauge(ch, m.portFwdInfo, 1,
				client.Clean(p.Get("Alias")),
				client.Clean(p.Get("Protocol")),
				client.Clean(p.Get("ExternalPort")),
				client.Clean(p.Get("InternalClient")),
				client.Clean(p.Get("InternalPort")),
				client.Clean(p.Get("Interface")),
				client.Clean(p.Get("Enable")),
			)
		}
	} else {
		log.Printf("GetPortForward failed: %v", err)
	}

	if body, err := e.client.GetSecurityGlobal(); err == nil {
		if p := client.SectionFirst(body, "OBJ_FWBASE_ID"); p != nil {
			e.gauge(ch, m.fwMACFilter, p.Bool01("MacFilterEnable"))
			e.gauge(ch, m.fwURLFilter, p.Bool01("UrlFilterEnable"))
		}
	} else {
		log.Printf("GetSecurityGlobal failed: %v", err)
	}

	if body, err := e.client.GetStaticBind(); err == nil {
		e.gauge(ch, m.staticBindCnt, float64(len(client.Section(body, "OBJ_DHCPBIND_ID"))))
	} else {
		log.Printf("GetStaticBind failed: %v", err)
	}
}
