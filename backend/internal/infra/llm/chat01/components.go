package chat01

import (
	"encoding/json"
	"io"
	"net/url"
	"sort"
	"strings"
)

type ComponentCard struct {
	Title string `json:"title,omitempty"`
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

type ComponentPoint struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	ID      string `json:"id,omitempty"`
}

// ComponentInfo is a safe, bounded read-only fallback, never a widget runtime.
type ComponentInfo struct {
	Type       string           `json:"type,omitempty"`
	State      string           `json:"state"`
	Title      string           `json:"title,omitempty"`
	Cards      []ComponentCard  `json:"cards,omitempty"`
	References []string         `json:"references,omitempty"`
	Points     []ComponentPoint `json:"points,omitempty"`
	Narrative  string           `json:"narrative,omitempty"`
}

// ComponentInfoFor interprets only the observed map_widget_v2 shape. It accepts
// complete JSON fields from a truncated prefix but never repairs JSON, invents
// coordinates, evaluates code, or treats an internal reference as a URL.
func ComponentInfoFor(block Block) ComponentInfo {
	info := ComponentInfo{State: "unsupported"}
	if block.Kind != KindComponent || !strings.HasPrefix(block.Text, componentOpen) {
		return info
	}
	payload := strings.TrimPrefix(block.Text, componentOpen)
	if strings.HasSuffix(payload, componentClose) {
		payload = strings.TrimSuffix(payload, componentClose)
	}
	info.State = "complete"
	if !block.Complete {
		info.State = "incomplete"
	}
	if block.Oversized || len(payload) > maxComponent {
		info.State = "oversized"
	}
	if len(payload) > maxComponent {
		payload = payload[:maxComponent]
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	budget := 2048
	value, _, err := readJSONPrefix(dec, 0, &budget)
	if err != nil && info.State == "complete" {
		info.State = "invalid"
	}
	if err == nil {
		if _, e := dec.Token(); e != io.EOF {
			if info.State == "complete" {
				info.State = "invalid"
			}
		}
	}
	root, _ := value.(map[string]any)
	widget, ok := root["map_widget_v2"].(map[string]any)
	if !ok {
		if info.State == "complete" {
			info.State = "unsupported"
		}
		return info
	}
	info.Type = "map_widget_v2"
	info.Title = safeString(widget["title"])
	info.Narrative = safeString(widget["narrative"])
	seen := map[string]bool{}
	if points, ok := widget["points"].([]any); ok {
		for _, point := range points {
			if m, ok := point.(map[string]any); ok {
				p := ComponentPoint{Name: safeString(m["name"]), Address: safeString(m["address"]), ID: safeString(m["id"])}
				if (p.Name != "" || p.Address != "") && len(info.Points) < 32 {
					info.Points = append(info.Points, p)
				}
				ref := safeString(m["ref"])
				if ref != "" && !seen[ref] && len(info.References) < 32 {
					info.References = append(info.References, ref)
					seen[ref] = true
				}
			}
		}
	}
	carousel, _ := widget["carousel"].(map[string]any)
	cards, _ := carousel["cards"].(map[string]any)
	// Point order is the only observed explicit card order. Extra cards retain
	// deterministic JSON-key order below, rather than Go map iteration order.
	refs := append([]string(nil), info.References...)
	for _, ref := range sortedKeys(cards) {
		if !seen[ref] {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		if len(info.Cards) >= 16 {
			break
		}
		if card, ok := cards[ref].(map[string]any); ok {
			c := ComponentCard{Title: safeString(card["title"]), Label: safeString(card["label"]), Ref: ref}
			for _, key := range []string{"url", "href"} {
				if u := safeString(card[key]); validSourceURL(u) {
					c.URL = u
					break
				}
			}
			if c.Title != "" || c.Label != "" || c.URL != "" {
				info.Cards = append(info.Cards, c)
			}
		}
	}
	return info
}

// RenderComponentFallback emits text and verified links, without private wire
// delimiters, raw JSON, internal point IDs, inferred maps or executable actions.
func RenderComponentFallback(block Block) string {
	info := ComponentInfoFor(block)
	var text strings.Builder
	resources := componentResources(block)
	if info.State == "unsupported" && len(resources) > 0 {
		info.State = "complete"
	}
	if info.Title != "" {
		text.WriteString(info.Title)
		text.WriteByte('\n')
	}
	for _, card := range info.Cards {
		label := card.Title
		if label == "" {
			label = card.Label
		}
		text.WriteString("- ")
		text.WriteString(label)
		if card.Label != "" && card.Label != label {
			text.WriteString("：")
			text.WriteString(card.Label)
		}
		if card.URL != "" {
			text.WriteString("（")
			text.WriteString(card.URL)
			text.WriteString("）")
		}
		text.WriteByte('\n')
	}
	for _, point := range info.Points {
		text.WriteString("- ")
		text.WriteString(point.Name)
		if point.Address != "" {
			if point.Name != "" {
				text.WriteString("：")
			}
			text.WriteString(point.Address)
		}
		text.WriteByte('\n')
	}
	if info.Narrative != "" {
		text.WriteString(info.Narrative)
		text.WriteByte('\n')
	}
	for _, resource := range resources {
		if resource.Evidence == "component-map-card-link" {
			continue
		}
		label := resource.Title
		if label == "" {
			label = "图片"
		}
		label = markdownLabel(label)
		if resource.Kind == "image" || resource.Kind == "external-image" {
			text.WriteString("![")
			text.WriteString(label)
			text.WriteString("](")
			text.WriteString(markdownURL(resource.URL))
			text.WriteString(")\n")
			if resource.SourceURL != "" {
				text.WriteString("[图片来源](")
				text.WriteString(markdownURL(resource.SourceURL))
				text.WriteString(")\n")
			}
		} else {
			if resource.Title == "" {
				label = "来源"
			}
			text.WriteString("[")
			text.WriteString(label)
			text.WriteString("](")
			text.WriteString(markdownURL(resource.URL))
			text.WriteString(")\n")
		}
	}
	switch info.State {
	case "oversized":
		text.WriteString("（上游组件过大，已保留可读取的信息）")
	case "incomplete":
		text.WriteString("（上游组件内容不完整）")
	case "invalid":
		text.WriteString("（上游组件数据无法完整解析）")
	case "unsupported":
		text.WriteString("（暂不支持此上游组件）")
	default:
		if info.Type == "map_widget_v2" {
			text.WriteString("（此地图组件暂以文字展示）")
		}
	}
	return strings.TrimSpace(text.String())
}

// readJSONPrefix retains only fully decoded scalar fields and bounded container
// prefixes. A truncated string is never accepted as a value.
func readJSONPrefix(dec *json.Decoder, depth int, budget *int) (any, bool, error) {
	if depth > 32 || *budget <= 0 {
		return nil, false, io.ErrShortBuffer
	}
	*budget--
	token, err := dec.Token()
	if err != nil {
		return nil, false, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, true, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for dec.More() {
			k, e := dec.Token()
			if e != nil {
				return m, false, e
			}
			key, ok := k.(string)
			if !ok {
				return m, false, io.ErrUnexpectedEOF
			}
			v, complete, e := readJSONPrefix(dec, depth+1, budget)
			if v != nil || complete {
				m[key] = v
			}
			if e != nil {
				return m, false, e
			}
		}
		close, e := dec.Token()
		if e != nil {
			return m, false, e
		}
		if close != json.Delim('}') {
			return m, false, io.ErrUnexpectedEOF
		}
		return m, true, nil
	case '[':
		var list []any
		for dec.More() {
			v, complete, e := readJSONPrefix(dec, depth+1, budget)
			if v != nil || complete {
				list = append(list, v)
			}
			if e != nil {
				return list, false, e
			}
		}
		close, e := dec.Token()
		if e != nil {
			return list, false, e
		}
		if close != json.Delim(']') {
			return list, false, io.ErrUnexpectedEOF
		}
		return list, true, nil
	default:
		return nil, false, io.ErrUnexpectedEOF
	}
}

func safeString(v any) string {
	s, _ := v.(string)
	runes := []rune(s)
	if len(runes) > 512 {
		return string(runes[:512]) + "…"
	}
	return s
}
func validSourceURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func markdownLabel(s string) string {
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\r", " ", "\n", " ").Replace(s)
}
func markdownURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	return strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(u.String())
}
