package chat01

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

type chartSeries struct {
	Name string     `json:"name"`
	Data []*float64 `json:"data"`
}
type chartProps struct {
	Type   string        `json:"type"`
	Title  string        `json:"title,omitempty"`
	X      []string      `json:"x"`
	Series []chartSeries `json:"series"`
}

// Only observed declarative chart data crosses this boundary. Runtime, actions,
// formulas and upstream component names never enter the site's UI registry.
func chartPropsFor(widget map[string]any) (*chartProps, string) {
	content, _ := widget["content"].(map[string]any)
	typeName := safeString(content["chartType"])
	if typeName != "line" && typeName != "bar" && typeName != "pie" {
		return nil, ""
	}
	rows, _ := content["data"].([]any)
	series, _ := content["series"].([]any)
	if len(rows) == 0 || len(rows) > 256 || len(series) == 0 || len(series) > 8 {
		return nil, ""
	}
	xKey := safeString(content["xKey"])
	if typeName == "pie" {
		xKey = safeString(content["nameKey"])
	}
	if xKey == "" {
		return nil, ""
	}
	props := &chartProps{Type: typeName, Title: safeString(content["title"])}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, ""
		}
		switch label := m[xKey].(type) {
		case string:
			props.X = append(props.X, safeString(label))
		case json.Number:
			props.X = append(props.X, label.String())
		default:
			return nil, ""
		}
	}
	for _, item := range series {
		m, _ := item.(map[string]any)
		key := safeString(m["dataKey"])
		if key == "" {
			return nil, ""
		}
		s := chartSeries{Name: safeString(m["label"])}
		if s.Name == "" {
			s.Name = key
		}
		// Keep units without assigning one series' currency to another series.
		if unit := safeString(m["valuePrefix"]) + safeString(m["valueSuffix"]); unit != "" {
			s.Name += "（" + strings.TrimSpace(unit) + "）"
		}
		for _, row := range rows {
			v := row.(map[string]any)[key]
			if v == nil {
				s.Data = append(s.Data, nil)
				continue
			}
			n, ok := v.(json.Number)
			if !ok {
				return nil, ""
			}
			value, err := n.Float64()
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, ""
			}
			s.Data = append(s.Data, &value)
		}
		props.Series = append(props.Series, s)
	}
	if typeName == "pie" && len(props.Series) != 1 {
		return nil, ""
	}
	meta, _ := content["meta"].(map[string]any)
	return props, safeString(meta["footer"])
}

func renderChart(block Block, props *chartProps, footer string) string {
	encoded, _ := json.Marshal(struct {
		Component string      `json:"component"`
		Version   int         `json:"version"`
		ID        string      `json:"id"`
		Props     *chartProps `json:"props"`
	}{"chart", 1, "chat01-" + block.ID, props})
	text := "```deeix-ui\n" + string(encoded) + "\n```"
	if footer != "" {
		text += "\n\n" + footer
	}
	return text
}

// Kept separate from rendering so an incomplete upstream payload can still
// expose its verified rows without claiming to be an intact chart.
func chartTable(props *chartProps) string {
	cell := func(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s) }
	var b strings.Builder
	b.WriteString("| 项目 |")
	for _, s := range props.Series {
		b.WriteString(" " + cell(s.Name) + " |")
	}
	b.WriteString("\n| --- |" + strings.Repeat(" --- |", len(props.Series)) + "\n")
	for i, x := range props.X {
		b.WriteString("| " + cell(x) + " |")
		for _, s := range props.Series {
			value := ""
			if s.Data[i] != nil {
				value = strconv.FormatFloat(*s.Data[i], 'g', -1, 64)
			}
			b.WriteString(" " + value + " |")
		}
		b.WriteByte('\n')
	}
	return b.String()
}
