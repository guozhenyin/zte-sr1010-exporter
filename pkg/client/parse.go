package client

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// XML <Instance> parsing helpers
//
// The Vue/GoAhead style API answers every request with a flat XML document:
//
//	<ajax_response_xml_root>
//	  <OBJ_SOMETHING_ID>
//	    <Instance>
//	      <ParaName>Key</ParaName><ParaValue>Value</ParaValue>
//	      ...
//	    </Instance>
//	  </OBJ_SOMETHING_ID>
//	</ajax_response_xml_root>
//
// The helpers below turn such a document into easy-to-consume maps.
// ---------------------------------------------------------------------------

var (
	reInstance   = regexp.MustCompile(`(?s)<Instance>(.*?)</Instance>`)
	reParaPair   = regexp.MustCompile(`(?s)<ParaName>([^<]*)</ParaName>\s*<ParaValue>([^<]*)</ParaValue>`)
	reNumEntity  = regexp.MustCompile(`&#([0-9]+);`)
	reHexEntity  = regexp.MustCompile(`&#x([0-9a-fA-F]+);`)
	reWhitespace = regexp.MustCompile(`\s+`)

	sectionCache = map[string]*regexp.Regexp{}
)

// Params holds the ParaName/ParaValue pairs of one XML <Instance>.
// When a name is repeated (e.g. WorkIFMac appears twice) the first wins.
type Params map[string]string

// Get returns the value for name, or "" when absent.
func (p Params) Get(name string) string {
	if p == nil {
		return ""
	}
	return p[name]
}

// Int parses an integer value; ok is false when missing or invalid.
func (p Params) Int(name string) (int64, bool) {
	s := strings.TrimSpace(p.Get(name))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Float parses a float value; ok is false when missing or invalid.
func (p Params) Float(name string) (float64, bool) {
	s := strings.TrimSpace(p.Get(name))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// IntOr returns an integer or def when missing/invalid.
func (p Params) IntOr(name string, def int64) int64 {
	if v, ok := p.Int(name); ok {
		return v
	}
	return def
}

// FloatOr returns a float or def when missing/invalid.
func (p Params) FloatOr(name string, def float64) float64 {
	if v, ok := p.Float(name); ok {
		return v
	}
	return def
}

// Bool01 maps common truthy encodings to 1 and everything else to 0.
func (p Params) Bool01(name string) float64 {
	switch strings.ToLower(strings.TrimSpace(p.Get(name))) {
	case "1", "true", "yes", "on", "enable", "enabled", "connected", "up":
		return 1
	}
	return 0
}

// Is reports whether the value equals want (case-insensitive).
func (p Params) Is(name, want string) bool {
	return strings.EqualFold(strings.TrimSpace(p.Get(name)), want)
}

// Section returns every <Instance> found under <rootTag>.
func Section(xml, rootTag string) []Params {
	m := sectionRe(rootTag).FindStringSubmatch(xml)
	if len(m) < 2 {
		return nil
	}
	return parseInstances(m[1])
}

// SectionFirst returns the first <Instance> under <rootTag>, or nil.
func SectionFirst(xml, rootTag string) Params {
	all := Section(xml, rootTag)
	if len(all) == 0 {
		return nil
	}
	return all[0]
}

func sectionRe(rootTag string) *regexp.Regexp {
	if re, ok := sectionCache[rootTag]; ok {
		return re
	}
	re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(rootTag) + `>(.*?)</` + regexp.QuoteMeta(rootTag) + `>`)
	sectionCache[rootTag] = re
	return re
}

func parseInstances(inner string) []Params {
	matches := reInstance.FindAllStringSubmatch(inner, -1)
	out := make([]Params, 0, len(matches))
	for _, m := range matches {
		pairs := reParaPair.FindAllStringSubmatch(m[1], -1)
		if len(pairs) == 0 {
			continue
		}
		p := make(Params, len(pairs))
		for _, kv := range pairs {
			k := strings.TrimSpace(kv[1])
			if k == "" {
				continue
			}
			if _, exists := p[k]; !exists { // keep first occurrence
				p[k] = Unescape(kv[2])
			}
		}
		out = append(out, p)
	}
	return out
}

// Unescape decodes the HTML/XML entities used by the router responses
// (e.g. "&#32;" -> " ", "&quot;" -> '"').
func Unescape(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	if reNumEntity.MatchString(s) {
		s = reNumEntity.ReplaceAllStringFunc(s, func(m string) string {
			n, err := strconv.Atoi(m[2 : len(m)-1])
			if err != nil || n <= 0 || n > 0x10FFFF {
				return m
			}
			return string(rune(n))
		})
	}
	if reHexEntity.MatchString(s) {
		s = reHexEntity.ReplaceAllStringFunc(s, func(m string) string {
			n, err := strconv.ParseInt(m[3:len(m)-1], 16, 32)
			if err != nil || n <= 0 || n > 0x10FFFF {
				return m
			}
			return string(rune(n))
		})
	}
	if strings.Contains(s, "&amp;") {
		s = strings.ReplaceAll(s, "&amp;", "\x00")
	}
	s = strings.NewReplacer(
		"&quot;", `"`,
		"&apos;", "'",
		"&lt;", "<",
		"&gt;", ">",
		"&nbsp;", " ",
	).Replace(s)
	if strings.Contains(s, "\x00") {
		s = strings.ReplaceAll(s, "\x00", "&")
	}
	return s
}

// NormalizeMAC lower-cases a MAC address and trims spaces. Empty/zero MACs
// return "".
func NormalizeMAC(mac string) string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" || mac == "00:00:00:00:00:00" || mac == "000000000000" {
		return ""
	}
	return mac
}

// Clean collapses all whitespace runs (incl. newlines) into single spaces and
// trims the result. Handy for values that embed padding.
func Clean(s string) string {
	return strings.TrimSpace(reWhitespace.ReplaceAllString(s, " "))
}

// ---------------------------------------------------------------------------
// Mesh topology JSON parsing (vue_topo_data&Action=GetALLAP)
// ---------------------------------------------------------------------------

// MeshAP is one node reported by the mesh topology endpoint.
type MeshAP struct {
	Index  string
	Fields map[string]string
}

// Get returns a field value ("" when absent).
func (m MeshAP) Get(name string) string {
	if m.Fields == nil {
		return ""
	}
	return m.Fields[name]
}

// ParseTopo decodes the "apData" object returned by the mesh topology API.
func ParseTopo(body string) ([]MeshAP, error) {
	var env struct {
		ApData map[string]json.RawMessage `json:"apData"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return nil, err
	}

	out := make([]MeshAP, 0, len(env.ApData))
	for k, raw := range env.ApData {
		if k == "MGET_INST_NUM" {
			continue
		}
		var fields map[string]interface{}
		if err := json.Unmarshal(raw, &fields); err != nil {
			continue
		}
		conv := make(map[string]string, len(fields))
		for fk, fv := range fields {
			conv[fk] = scalarString(fv)
		}
		out = append(out, MeshAP{Index: k, Fields: conv})
	}

	sort.Slice(out, func(i, j int) bool {
		ai, aerr := strconv.Atoi(out[i].Index)
		aj, jerr := strconv.Atoi(out[j].Index)
		if aerr == nil && jerr == nil {
			return ai < aj
		}
		return out[i].Index < out[j].Index
	})
	return out, nil
}

func scalarString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
