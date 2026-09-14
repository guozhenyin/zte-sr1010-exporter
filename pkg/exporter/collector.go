package exporter

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/example/zte-sr1010-exporter/pkg/client"
)

// Options controls optional (potentially heavier) collection.
type Options struct {
	// CollectClients enables the rich LAN client table (largest response).
	CollectClients bool
	// CollectARP enables the ARP table (needs a POST with the session token).
	CollectARP bool
}

// ZTEExporter implements prometheus.Collector for ZXSLC SR1010.
type ZTEExporter struct {
	client *client.ZTEClient
	opts   Options

	descs []*prometheus.Desc

	up             *prometheus.Desc
	scrapeSuccess  *prometheus.Desc
	scrapeDuration *prometheus.Desc

	// home page (kept for backwards compatibility)
	wanUpBps     *prometheus.Desc
	wanDownBps   *prometheus.Desc
	wanSpeed     *prometheus.Desc
	wanStatus    *prometheus.Desc
	accessDevNum *prometheus.Desc
	topoAPNum    *prometheus.Desc
	dualWAN      *prometheus.Desc
	info         *prometheus.Desc

	// additional metrics live in extra.go to keep files readable.
	x *extraMetrics

	mu sync.Mutex

	// lastLoginErr de-duplicates repeated login-failure logs across scrapes.
	lastLoginErr string
}

// New builds the exporter. All metric descriptors are registered through
// (*ZTEExporter).desc so Describe() always stays in sync.
func New(c *client.ZTEClient, opts Options) *ZTEExporter {
	e := &ZTEExporter{client: c, opts: opts}

	e.up = e.desc("zte_up", "1 if the last scrape against the router succeeded")
	e.scrapeSuccess = e.desc("zte_scrape_success", "1 if the last scrape succeeded")
	e.scrapeDuration = e.desc("zte_scrape_duration_seconds", "Duration of the last scrape")

	e.wanUpBps = e.desc("zte_wan_up_bps", "WAN uplink rate in bits per second", "wan")
	e.wanDownBps = e.desc("zte_wan_down_bps", "WAN downlink rate in bits per second", "wan")
	e.wanSpeed = e.desc("zte_wan_link_speed_mbps", "WAN link negotiated speed in Mbps")
	e.wanStatus = e.desc("zte_wan_connected", "1 if WAN status is Connected", "wan")
	e.accessDevNum = e.desc("zte_access_device_count", "Number of connected access devices")
	e.topoAPNum = e.desc("zte_topo_ap_count", "Number of mesh/topo APs reported by the home page")
	e.dualWAN = e.desc("zte_dual_wan_enabled", "1 if DualWAN is enabled")
	e.info = e.desc("zte_router_info", "Router info (value always 1)",
		"model", "software_ver", "dev_name", "ip", "mac", "mode")

	e.x = e.newExtraMetrics()

	return e
}

// desc registers a descriptor and returns it.
func (e *ZTEExporter) desc(name, help string, labels ...string) *prometheus.Desc {
	d := prometheus.NewDesc(name, help, labels, nil)
	e.descs = append(e.descs, d)
	return d
}

func (e *ZTEExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range e.descs {
		ch <- d
	}
}

func (e *ZTEExporter) Collect(ch chan<- prometheus.Metric) {
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()
	success := 0.0
	defer func() {
		e.gauge(ch, e.scrapeDuration, time.Since(start).Seconds())
		e.gauge(ch, e.scrapeSuccess, success)
		e.gauge(ch, e.up, success)
	}()

	if err := e.client.EnsureLogin(); err != nil {
		// No usable session: stop here instead of firing every data request
		// and letting the login backoff flood the log on each scrape.
		e.logLoginErr(err)
		return
	}
	e.clearLoginErr()

	body, err := e.client.GetHomeDeviceData()
	if err != nil {
		log.Printf("GetHomeDeviceData failed: %v", err)
		return
	}

	// Session-expired signature: no WANUpRate / OBJ_HOME_BASICINFO_ID.
	// GetHomeDeviceData already invalidated the session in that case.
	if !client.HasBasicInfo(body) {
		log.Printf("session appears expired (no WANUpRate, len=%d), re-login...", len(body))
		if err := e.client.Login(); err != nil {
			e.logLoginErr(err)
			return
		}
		body, err = e.client.GetHomeDeviceData()
		if err != nil {
			log.Printf("GetHomeDeviceData after re-login failed: %v", err)
			return
		}
		if !client.HasBasicInfo(body) {
			log.Printf("still no basic info after re-login (len=%d)", len(body))
			return
		}
	}

	e.client.Heartbeat() // keep the session alive
	success = 1.0

	e.collectHome(ch, body)
	e.collectSSIDs(ch, body)
	e.collectSystem(ch)
	e.collectTime(ch)
	e.collectAddrManager(ch)
	e.collectWAN(ch)
	e.collectEthernet(ch)
	e.collectWiFi(ch)
	e.collectMesh(ch)
	e.collectRoutes(ch)
	e.collectServices(ch)
	if e.opts.CollectClients {
		e.collectClients(ch)
	}
	if e.opts.CollectARP {
		e.collectARP(ch)
	}
}

// ---------------------------------------------------------------------------
// home page metrics
// ---------------------------------------------------------------------------

func (e *ZTEExporter) collectHome(ch chan<- prometheus.Metric, body string) {
	up1 := client.ParseSpeed(client.ExtractPara(body, "WANUpRate"))
	down1 := client.ParseSpeed(client.ExtractPara(body, "WANDownRate"))
	up2 := client.ParseSpeed(client.ExtractPara(body, "WANUpRate2"))
	down2 := client.ParseSpeed(client.ExtractPara(body, "WANDownRate2"))

	e.gauge(ch, e.wanUpBps, up1, "1")
	e.gauge(ch, e.wanUpBps, up2, "2")
	e.gauge(ch, e.wanDownBps, down1, "1")
	e.gauge(ch, e.wanDownBps, down2, "2")

	if v := client.ExtractPara(body, "WANSpeed"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			e.gauge(ch, e.wanSpeed, f)
		}
	}

	e.gauge(ch, e.wanStatus, bool01(client.ExtractPara(body, "WANStatus") == "Connected"), "1")
	e.gauge(ch, e.wanStatus, bool01(client.ExtractPara(body, "WANStatus2") == "Connected"), "2")

	if f, err := strconv.ParseFloat(client.ExtractPara(body, "AccessDevNum"), 64); err == nil {
		e.gauge(ch, e.accessDevNum, f)
	}
	if f, err := strconv.ParseFloat(client.ExtractPara(body, "TopoAPNum"), 64); err == nil {
		e.gauge(ch, e.topoAPNum, f)
	}
	e.gauge(ch, e.dualWAN, bool01(client.ExtractPara(body, "DualWANEnable") == "1"))

	model := client.Clean(client.ExtractPara(body, "WEBTitle"))
	if model == "" {
		model = "ZXSLC SR1010"
	}
	e.gauge(ch, e.info, 1,
		model,
		client.ExtractPara(body, "SoftwareVer"),
		client.ExtractPara(body, "DevName"),
		client.ExtractPara(body, "AdminIPAddr"),
		client.ExtractPara(body, "DevMac"),
		client.ExtractPara(body, "Mode"),
	)
}

// ---------------------------------------------------------------------------
// login failure log de-duplication
// ---------------------------------------------------------------------------

// logLoginErr prints a login error only when it differs from the previous one,
// so an unreachable or locked router does not emit an identical line on every
// single scrape.
func (e *ZTEExporter) logLoginErr(err error) {
	msg := err.Error()
	if msg == e.lastLoginErr {
		return
	}
	e.lastLoginErr = msg
	log.Printf("login failed: %v", err)
}

// clearLoginErr resets the de-duplication state after a successful login.
func (e *ZTEExporter) clearLoginErr() {
	e.lastLoginErr = ""
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func (e *ZTEExporter) gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

func (e *ZTEExporter) counter(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
}

func bool01(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// instSuffix turns "DEV.ETH.IF1" into "IF1".
func instSuffix(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

// parsePercent turns "100%" into 100.
func parsePercent(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseFloat parses s, returning ok=false when empty/invalid.
func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// bandFromDirectBand maps the DirectBand enum (1=2.4G, 2=5G, 3=6G).
func bandFromDirectBand(v string) string {
	switch strings.TrimSpace(v) {
	case "1":
		return "2.4G"
	case "2":
		return "5G"
	case "3":
		return "6G"
	}
	return ""
}

// ipv4PoolSize returns the number of addresses between two IPv4 strings.
func ipv4PoolSize(min, max string) (float64, bool) {
	lo, ok1 := ipv4ToUint(min)
	hi, ok2 := ipv4ToUint(max)
	if !ok1 || !ok2 || hi < lo {
		return 0, false
	}
	return float64(hi-lo) + 1, true
}

func ipv4ToUint(s string) (uint32, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 4 {
		return 0, false
	}
	var v uint32
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			return 0, false
		}
		v = v<<8 | uint32(n)
	}
	return v, true
}
