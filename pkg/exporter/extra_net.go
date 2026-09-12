package exporter

import (
	"log"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/example/zte-sr1010-exporter/pkg/client"
)

// collectWAN gathers per-WAN configuration, counters and IPv6 details from the
// main WAN (WAN1) and dual WAN (WAN2) endpoints.
func (e *ZTEExporter) collectWAN(ch chan<- prometheus.Metric) {
	m := e.x

	sources := []struct {
		index string
		fetch func() (string, error)
	}{
		{"1", e.client.GetMainWAN},
		{"2", e.client.GetDualWAN},
	}

	for _, s := range sources {
		body, err := s.fetch()
		if err != nil {
			log.Printf("WAN %s fetch failed: %v", s.index, err)
			continue
		}
		cfg := client.SectionFirst(body, "ID_WAN_COMFIG")
		if cfg == nil {
			continue
		}

		e.gauge(ch, m.wanInfo, 1,
			s.index,
			client.Clean(cfg.Get("WANCName")),
			client.Clean(cfg.Get("ConnType")),
			client.Clean(cfg.Get("TransType")),
			client.Clean(cfg.Get("IPAddress")),
			client.Clean(cfg.Get("GateWay")),
			client.Clean(cfg.Get("DNS1")),
			client.Clean(cfg.Get("DNS2")),
			client.NormalizeMAC(cfg.Get("WorkIFMac")),
			client.Clean(cfg.Get("ConnStatus")),
			client.Clean(cfg.Get("VLANID")),
		)

		if v, ok := parseFloat(cfg.Get("UpTime")); ok {
			e.gauge(ch, m.wanUptime, v, s.index)
		}

		mtu := client.Clean(cfg.Get("MTU"))
		if mtu == "" {
			mtu = client.Clean(cfg.Get("MRU"))
		}
		if v, ok := parseFloat(mtu); ok {
			e.gauge(ch, m.wanMTU, v, s.index)
		}

		if v, ok := parseFloat(cfg.Get("RxBytes")); ok {
			e.counter(ch, m.wanRxBytes, v, s.index)
		}
		if v, ok := parseFloat(cfg.Get("TxBytes")); ok {
			e.counter(ch, m.wanTxBytes, v, s.index)
		}
		if v, ok := parseFloat(cfg.Get("RxPackets")); ok {
			e.counter(ch, m.wanRxPkts, v, s.index)
		}
		if v, ok := parseFloat(cfg.Get("TxPackets")); ok {
			e.counter(ch, m.wanTxPkts, v, s.index)
		}

		gua := client.Clean(cfg.Get("Gua1"))
		if gua != "" && gua != "::" {
			e.gauge(ch, m.wanIPv6Info, 1,
				s.index,
				gua,
				client.Clean(cfg.Get("Gua1PrefixLen")),
				client.Clean(cfg.Get("Gateway6")),
				client.Clean(cfg.Get("Pd")),
				client.Clean(cfg.Get("PdLen")),
				client.Clean(cfg.Get("ConnStatus6")),
			)
		}
	}
}

// collectEthernet gathers physical port link state, negotiated speed code and
// per-port line rates, joining the INFO and STATE tables by instance id.
func (e *ZTEExporter) collectEthernet(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetEthPortData()
	if err != nil {
		log.Printf("GetEthPortData failed: %v", err)
		return
	}

	states := map[string]client.Params{}
	for _, p := range client.Section(body, "OBJ_ETHPORT_STATE_ID") {
		states[p.Get("_InstID")] = p
	}

	for _, p := range client.Section(body, "OBJ_ETHPORT_INFO_ID") {
		id := p.Get("_InstID")
		port := instSuffix(id)
		name := client.Clean(p.Get("EthPortAliasName"))

		e.gauge(ch, m.ethIsWAN, bool01(p.IntOr("WanType", 0) > 0), port, name)
		e.gauge(ch, m.ethUpstream, bool01(p.IntOr("EthPortUpStream", 0) != 0), port, name)
		if v, ok := parseFloat(p.Get("EthPortMaxSpeed")); ok {
			e.gauge(ch, m.ethMaxSpeed, v, port, name)
		}
		if v, ok := parseFloat(p.Get("EthPortSpeed")); ok {
			e.gauge(ch, m.ethSpeed, v, port, name)
		}

		if st, ok := states[id]; ok {
			// EthPortStatus: 0 = link up.
			e.gauge(ch, m.ethUp, bool01(st.Get("EthPortStatus") == "0"), port, name)
			e.gauge(ch, m.ethRxBps, client.ParseSpeed(st.Get("EthPortRecvRate")), port, name)
			e.gauge(ch, m.ethTxBps, client.ParseSpeed(st.Get("EthPortSendRate")), port, name)
		}
	}
}

// collectWiFi gathers per-radio state. MaxRate is reported in kbps and is
// converted to bits/s here.
func (e *ZTEExporter) collectWiFi(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetWLANConfig()
	if err != nil {
		log.Printf("GetWLANConfig failed: %v", err)
		return
	}

	for _, p := range client.Section(body, "OBJ_WLANSETTING_ID") {
		radio := instSuffix(p.Get("_InstID"))
		band := client.Clean(p.Get("Band"))

		e.gauge(ch, m.wifiRadioUp, p.Bool01("RadioStatus"), radio, band)
		if v, ok := parseFloat(p.Get("Channel")); ok {
			e.gauge(ch, m.wifiChannel, v, radio, band)
		}
		if v, ok := parseFloat(p.Get("MaxRate")); ok {
			e.gauge(ch, m.wifiMaxRate, v*1000, radio, band)
		}
		if v, ok := parsePercent(p.Get("TxPower")); ok {
			e.gauge(ch, m.wifiTxPower, v, radio, band)
		}
		e.gauge(ch, m.wifiStandard, 1, radio, band, client.Clean(p.Get("Standard")))
		e.gauge(ch, m.wifiBandwidth, 1, radio, band, client.Clean(p.Get("BandWidth")))
	}
}

// collectMesh parses the mesh topology JSON (vue_topo_data&Action=GetALLAP).
func (e *ZTEExporter) collectMesh(ch chan<- prometheus.Metric) {
	m := e.x

	body, err := e.client.GetTopoData()
	if err != nil {
		log.Printf("GetTopoData failed: %v", err)
		return
	}
	aps, err := client.ParseTopo(body)
	if err != nil {
		log.Printf("ParseTopo failed: %v", err)
		return
	}

	e.gauge(ch, m.meshCount, float64(len(aps)))

	for _, ap := range aps {
		mac := client.NormalizeMAC(ap.Get("MacAddr"))
		name := client.Clean(ap.Get("DeviceName"))

		e.gauge(ch, m.meshInfo, 1,
			mac,
			name,
			client.Clean(ap.Get("ModelName")),
			client.Clean(ap.Get("role")),
			client.Clean(ap.Get("IpAddr")),
			client.Clean(ap.Get("IPv6Addr")),
			client.Clean(ap.Get("AccessType")),
			client.Clean(ap.Get("SoftwareVer")),
			client.Clean(ap.Get("instID")),
			client.Clean(ap.Get("instID_Parent")),
			client.Clean(ap.Get("ApType")),
		)

		e.gauge(ch, m.meshUp, bool01(ap.Get("MeshStatus") == "1"), mac, name)

		if v, ok := parseFloat(ap.Get("LinkSpeed")); ok {
			e.gauge(ch, m.meshLinkSpeed, v, mac, name)
		}

		channels := []struct{ field, band string }{
			{"Channel0", "2.4G"},
			{"Channel1", "5G"},
			{"Channel2", "6G"},
		}
		for _, c := range channels {
			if v, ok := parseFloat(ap.Get(c.field)); ok && v > 0 {
				e.gauge(ch, m.meshChannel, v, mac, name, c.band)
			}
		}

		if v, ok := parseFloat(ap.Get("AffStaNum")); ok {
			e.gauge(ch, m.meshAffSta, v, mac, name)
		}
		if v, ok := parseFloat(ap.Get("BhStaRssi")); ok && v != 0 && v != -1 {
			e.gauge(ch, m.meshRSSI, v, mac, name)
		}
		if v, ok := parseFloat(ap.Get("BhStaSnr")); ok && v != 0 && v != -1 {
			e.gauge(ch, m.meshSNR, v, mac, name)
		}
		if v, ok := parseFloat(ap.Get("t1905DevNum")); ok {
			e.gauge(ch, m.meshChild, v, mac, name)
		}
	}
}
