package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ZTEClient talks to ZXSLC SR1010 (and similar Vue-based ZTE home routers).
type ZTEClient struct {
	BaseURL  string
	Username string
	Password string
	Timeout  time.Duration
	Debug    bool

	mu           sync.Mutex
	client       *http.Client
	logged       bool
	lastLoginAt  time.Time
	lastFailAt   time.Time
	loginBackoff time.Duration // avoid lockout
	sessionToken string        // reused for POST endpoints (e.g. ARP table)
}

func New(baseURL, user, pass string, timeout time.Duration, debug bool) *ZTEClient {
	jar, _ := cookiejar.New(nil)
	return &ZTEClient{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		Username:     user,
		Password:     pass,
		Timeout:      timeout,
		Debug:        debug,
		loginBackoff: 65 * time.Second, // router locks ~60s after failed attempts
		client: &http.Client{
			Jar:     jar,
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *ZTEClient) debugf(format string, args ...interface{}) {
	if c.Debug {
		log.Printf("[debug] "+format, args...)
	}
}

// InvalidateSession marks session as expired so next EnsureLogin will re-auth.
func (c *ZTEClient) InvalidateSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logged = false
	c.debugf("session invalidated")
}

// Login performs the 3-step login observed in packet capture / HAR:
//  1. GET  /?_type=loginData&_tag=login_entry
//  2. GET  /?_type=loginsceneData&_tag=login_token_json
//     → {"_sessionToken":"...","logintoken":"11880034"}
//  3. POST /?_type=loginData&_tag=login_entry
//     Password = SHA256(password + logintoken)
func (c *ZTEClient) Login() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// respect backoff after lock / consecutive failures
	if !c.lastFailAt.IsZero() && time.Since(c.lastFailAt) < c.loginBackoff {
		remain := c.loginBackoff - time.Since(c.lastFailAt)
		return fmt.Errorf("login backoff, wait %s", remain.Round(time.Second))
	}

	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)

	entryURL := fmt.Sprintf("%s/?_type=loginData&_tag=login_entry&_=%s", c.BaseURL, ts)
	body1, err := c.doGet(entryURL)
	if err != nil {
		return fmt.Errorf("login_entry: %w", err)
	}
	c.debugf("login_entry response: %s", truncate(body1, 200))

	tokenURL := fmt.Sprintf("%s/?_type=loginsceneData&_tag=login_token_json", c.BaseURL)
	body2, err := c.doGet(tokenURL)
	if err != nil {
		return fmt.Errorf("login_token_json: %w", err)
	}
	c.debugf("login_token_json: %s", body2)

	loginToken := extract(body2, `"logintoken"\s*:\s*"([^"]+)"`)
	sessionToken := extract(body2, `"_sessionToken"\s*:\s*"([^"]+)"`)
	if sessionToken == "" {
		sessionToken = extract(body2, `"_sessionTOKEN"\s*:\s*"([^"]+)"`)
	}
	if sessionToken == "" {
		sessionToken = extract(body1, `"sess_token"\s*:\s*"([^"]+)"`)
	}
	if sessionToken == "" {
		sessionToken = extract(body1, `"_sessionToken"\s*:\s*"([^"]+)"`)
	}
	c.sessionToken = sessionToken

	c.debugf("extracted logintoken=%s  _sessionToken=%s", loginToken, sessionToken)
	if loginToken == "" {
		c.lastFailAt = time.Now()
		return fmt.Errorf("failed to extract logintoken from: %s", truncate(body2, 200))
	}

	hashed := sha256Hex(c.Password + loginToken)
	c.debugf("password hash (sha256(pwd+logintoken))=%s", hashed)

	username := c.Username
	if username == "" {
		username = "admin"
	}

	form := url.Values{}
	form.Set("Username", username)
	form.Set("Password", hashed)
	form.Set("action", "login")
	form.Set("Frm_Logintoken", "")
	form.Set("captchaCode", "")
	if sessionToken != "" {
		form.Set("_sessionTOKEN", sessionToken)
	}

	postURL := fmt.Sprintf("%s/?_type=loginData&_tag=login_entry", c.BaseURL)
	respBody, err := c.doPostForm(postURL, form)
	if err != nil {
		return fmt.Errorf("login POST: %w", err)
	}
	c.debugf("login POST response: %s", respBody)

	// ---- 明确的错误判断（必须在"成功判断"之前） ----

	if strings.Contains(respBody, "e_invalid_user_pwd") || strings.Contains(respBody, "用户名或密码") {
		c.logged = false
		c.lastFailAt = time.Now()
		return fmt.Errorf("login failed (bad password/token): %s", truncate(respBody, 200))
	}
	if strings.Contains(respBody, "e_login_locked") {
		c.logged = false
		c.lastFailAt = time.Now()
		return fmt.Errorf("login locked: %s", truncate(respBody, 200))
	}
	// ↓↓↓ 关键修复：e_exceed_max_user_preempt 必须视为登录失败
	if strings.Contains(respBody, "e_exceed_max_user_preempt") {
		c.logged = false
		c.lastFailAt = time.Now()
		return fmt.Errorf("login preempted by another session (e_exceed_max_user_preempt): %s", truncate(respBody, 200))
	}

	// ---- 只有 loginErrType 为空 + 有 sess_token 才算成功 ----
	if strings.Contains(respBody, `"loginErrType":""`) && strings.Contains(respBody, "sess_token") {
		c.logged = true
		c.lastLoginAt = time.Now()
		c.lastFailAt = time.Time{}
		c.debugf("login succeeded")

		// 登录后访问一次首页，让路由器完成 session 绑定
		homeTS := strconv.FormatInt(time.Now().UnixMilli(), 10)
		homeURL := fmt.Sprintf("%s/?_type=vueData&_tag=vue_home_device_data_no_update_sess&IF_OP=refresh&_=%s", c.BaseURL, homeTS)
		homeBody, homeErr := c.doGet(homeURL)
		if homeErr == nil {
			c.debugf("post-login home probe: len=%d, hasBasicInfo=%v", len(homeBody), HasBasicInfo(homeBody))
		} else {
			c.debugf("post-login home probe failed: %v", homeErr)
		}

		return nil
	}

	c.logged = false
	c.lastFailAt = time.Now()
	return fmt.Errorf("login unexpected response: %s", truncate(respBody, 200))
}

// EnsureLogin re-logins if needed.
// Session older than 5 minutes will be forced to re-login.
func (c *ZTEClient) EnsureLogin() error {
	c.mu.Lock()
	logged := c.logged
	var age time.Duration
	if !c.lastLoginAt.IsZero() {
		age = time.Since(c.lastLoginAt)
	}
	c.mu.Unlock()

	// session 超过 5 分钟就强制重新登录
	if logged && age < 5*time.Minute {
		return nil
	}
	if logged {
		c.debugf("session age=%v, forcing re-login", age.Round(time.Second))
		c.InvalidateSession()
	}
	return c.Login()
}

// Heartbeat keeps the session alive (observed in browser traffic).
func (c *ZTEClient) Heartbeat() {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	u := fmt.Sprintf("%s/?_type=vueData&_tag=heartbeat_lua&isReturnEnv=0&_=%s", c.BaseURL, ts)
	_, _ = c.doGet(u)
}

// GetHomeDeviceData fetches the main home/WAN stats.
// Prefer the no-update-sess variant used by ZTE-Stat_Max.
func (c *ZTEClient) GetHomeDeviceData() (string, error) {
	_ = c.EnsureLogin()

	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	u := fmt.Sprintf("%s/?_type=vueData&_tag=vue_home_device_data_no_update_sess&IF_OP=refresh&_=%s", c.BaseURL, ts)
	body, err := c.doGet(u)
	if err != nil {
		u2 := fmt.Sprintf("%s/?_type=vueData&_tag=vue_home_device_data&_=%s", c.BaseURL, ts)
		body, err = c.doGet(u2)
	}

	// 如果没拿到有效数据，标记 session 失效（下次 scrape 会重新登录）
	if err == nil && !HasBasicInfo(body) {
		c.debugf("no basic info in response (len=%d), invalidating session", len(body))
		c.InvalidateSession()
	}

	return body, err
}

// HasBasicInfo reports whether the XML contains authenticated home metrics.
// Unauthenticated responses lack OBJ_HOME_BASICINFO_ID / WANUpRate.
func HasBasicInfo(xml string) bool {
	return strings.Contains(xml, "WANUpRate") || strings.Contains(xml, "OBJ_HOME_BASICINFO_ID")
}

func (c *ZTEClient) GetTopoData() (string, error) {
	_ = c.EnsureLogin()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	u := fmt.Sprintf("%s/?_type=vueData&_tag=vue_topo_data&Action=GetALLAP&_=%s", c.BaseURL, ts)
	return c.doGet(u)
}

func (c *ZTEClient) GetClientData() (string, error) {
	_ = c.EnsureLogin()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	u := fmt.Sprintf("%s/?_type=vueData&_tag=vue_client_data&_=%s", c.BaseURL, ts)
	return c.doGet(u)
}

// ---------- additional data endpoints (discovered via HAR analysis) ----------

// API tags for the Vue data endpoints used by the exporter.
const (
	tagHomeManageReg = "home_managreg_lua"
	tagMainWAN       = "vue_mainwan_data"
	tagDualWAN       = "vue_dualwan_data"
	tagEthPortData   = "vue_internet_ethport_data"
	tagWLANConfig    = "wlanConfig_data"
	tagLanInfo       = "localnet_lan_info_lua"
	tagRouteIPv4     = "vue_routeipv4_table_data"
	tagRouteIPv6     = "vue_routeipv6_table_data"
	tagSNTP          = "sntp_lua"
	tagAddrManager   = "addr_manager_data"
	tagDDNS          = "ddns_data"
	tagPortForward   = "localnet_portforwarding_lua"
	tagSecurityGlob  = "security_globalctl_data"
	tagStaticBind    = "static_bindAddr_data"
	tagNatMode       = "natmode_data"
	tagARPTable      = "arp_arptable_lua"
)

// GetHomeManageReg returns device management info (model/versions/uptime/LAN IP).
func (c *ZTEClient) GetHomeManageReg() (string, error) { return c.vueGet(tagHomeManageReg, "") }

// GetMainWAN returns the primary WAN (WAN1) configuration and counters.
func (c *ZTEClient) GetMainWAN() (string, error) { return c.vueGet(tagMainWAN, "") }

// GetDualWAN returns the secondary WAN (WAN2) configuration and counters.
func (c *ZTEClient) GetDualWAN() (string, error) { return c.vueGet(tagDualWAN, "") }

// GetEthPortData returns physical Ethernet port status and line rates.
func (c *ZTEClient) GetEthPortData() (string, error) { return c.vueGet(tagEthPortData, "") }

// GetWLANConfig returns per-radio WLAN configuration (channel/rate/power/standard).
func (c *ZTEClient) GetWLANConfig() (string, error) { return c.vueGet(tagWLANConfig, "") }

// GetLanInfo returns the rich LAN client table (per-device traffic, RSSI, band...).
func (c *ZTEClient) GetLanInfo() (string, error) { return c.vueGet(tagLanInfo, "") }

// GetRouteIPv4 returns the IPv4 routing table.
func (c *ZTEClient) GetRouteIPv4() (string, error) { return c.vueGet(tagRouteIPv4, "") }

// GetRouteIPv6 returns the IPv6 routing table.
func (c *ZTEClient) GetRouteIPv6() (string, error) { return c.vueGet(tagRouteIPv6, "") }

// GetSNTP returns NTP/SNTP sync state and the router clock.
func (c *ZTEClient) GetSNTP() (string, error) { return c.vueGet(tagSNTP, "") }

// GetAddrManager returns the DHCP server / LAN address pool configuration.
func (c *ZTEClient) GetAddrManager() (string, error) { return c.vueGet(tagAddrManager, "") }

// GetDDNS returns DDNS client configuration and status.
func (c *ZTEClient) GetDDNS() (string, error) { return c.vueGet(tagDDNS, "") }

// GetPortForward returns the port-forwarding (NAT) rule table.
func (c *ZTEClient) GetPortForward() (string, error) { return c.vueGet(tagPortForward, "") }

// GetSecurityGlobal returns the global firewall switches.
func (c *ZTEClient) GetSecurityGlobal() (string, error) { return c.vueGet(tagSecurityGlob, "") }

// GetStaticBind returns DHCP static bindings.
func (c *ZTEClient) GetStaticBind() (string, error) { return c.vueGet(tagStaticBind, "") }

// GetNatMode returns the NAT mode setting.
func (c *ZTEClient) GetNatMode() (string, error) { return c.vueGet(tagNatMode, "") }

// GetARPTable returns the ARP table. This endpoint requires a POST carrying the
// session token; when the token is missing the router returns an empty table
// and the caller degrades gracefully.
func (c *ZTEClient) GetARPTable() (string, error) {
	_ = c.EnsureLogin()

	c.mu.Lock()
	token := c.sessionToken
	c.mu.Unlock()

	form := url.Values{}
	form.Set("IF_ACTION", "DISPPART")
	if token != "" {
		form.Set("_sessionTOKEN", token)
	}
	u := fmt.Sprintf("%s/?_type=vueData&_tag=%s", c.BaseURL, tagARPTable)
	return c.doPostForm(u, form)
}

// vueGet performs an authenticated GET against a Vue data tag, transparently
// re-logging in once when the router reports the session is gone.
func (c *ZTEClient) vueGet(tag, extra string) (string, error) {
	if err := c.EnsureLogin(); err != nil {
		c.debugf("ensure login before %s: %v", tag, err)
	}

	body, err := c.vueGetRaw(tag, extra)
	if err != nil {
		return body, err
	}
	if isAuthFailure(body) {
		c.debugf("%s: response looks unauthenticated, forcing re-login", tag)
		c.InvalidateSession()
		if lerr := c.Login(); lerr != nil {
			c.debugf("re-login before %s failed: %v", tag, lerr)
			return body, nil
		}
		return c.vueGetRaw(tag, extra)
	}
	return body, nil
}

func (c *ZTEClient) vueGetRaw(tag, extra string) (string, error) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	u := fmt.Sprintf("%s/?_type=vueData&_tag=%s&_=%s", c.BaseURL, tag, ts)
	if extra != "" {
		u = fmt.Sprintf("%s/?_type=vueData&_tag=%s&%s&_=%s", c.BaseURL, tag, extra, ts)
	}
	return c.doGet(u)
}

// isAuthFailure reports whether a non-empty body clearly is not a data answer
// (i.e. the router served the login page instead of XML/JSON data).
func isAuthFailure(body string) bool {
	t := strings.TrimSpace(body)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return false
	}
	return !strings.Contains(body, "<ajax_response_xml_root")
}

// ---------- helpers ----------

func (c *ZTEClient) doGet(u string) (string, error) {
	c.debugf("GET %s", u)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "zte-sr1010-exporter/0.2")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", c.BaseURL+"/")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return string(b), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(b), nil
}

func (c *ZTEClient) doPostForm(u string, form url.Values) (string, error) {
	c.debugf("POST %s  body=%s", u, form.Encode())
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "zte-sr1010-exporter/0.2")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", c.BaseURL+"/")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func extract(body, pattern string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(body)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ParseSpeed converts router rate strings to bits-per-second.
// Formats: "1.4Kbps", "133.5Kbps", "688bps", "2.0Kbps"
func ParseSpeed(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	re := regexp.MustCompile(`(?i)^([0-9.]+)\s*([KkMmGg]?)(?:bps|b/s)?`)
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(m[2])
	switch unit {
	case "K":
		return v * 1e3
	case "M":
		return v * 1e6
	case "G":
		return v * 1e9
	default:
		return v
	}
}

func ExtractPara(xml, name string) string {
	re := regexp.MustCompile(`(?s)<ParaName>` + regexp.QuoteMeta(name) + `</ParaName>\s*<ParaValue>([^<]*)</ParaValue>`)
	m := re.FindStringSubmatch(xml)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func ExtractAllInstances(xml, rootTag string) []string {
	reRoot := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(rootTag) + `>(.*?)</` + regexp.QuoteMeta(rootTag) + `>`)
	m := reRoot.FindStringSubmatch(xml)
	if len(m) < 2 {
		return nil
	}
	reInst := regexp.MustCompile(`(?s)<Instance>(.*?)</Instance>`)
	matches := reInst.FindAllStringSubmatch(m[1], -1)
	out := make([]string, 0, len(matches))
	for _, mm := range matches {
		out = append(out, mm[1])
	}
	return out
}
