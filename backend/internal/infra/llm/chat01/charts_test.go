package chat01

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChartDataUsesExistingDeclarativeChart(t *testing.T) {
	for _, kind := range []string{"line", "bar", "pie"} {
		raw := componentOpen + `{"charts_widget_v2":{"content":{"chartType":"` + kind + `","xKey":"month","nameKey":"month","series":[{"dataKey":"revenue","label":"营收"}],"data":[{"month":"1月","revenue":32},{"month":"2月","revenue":null}]}}}` + componentClose
		b := checkReplay(t, raw)[0]
		text := RenderComponentFallback(b)
		if !strings.HasPrefix(text, "```deeix-ui\n") {
			t.Fatalf("missing chart: %s", text)
		}
		var block struct {
			Component string     `json:"component"`
			Version   int        `json:"version"`
			Props     chartProps `json:"props"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(text, "```deeix-ui\n"), "\n```")), &block); err != nil {
			t.Fatal(err)
		}
		if block.Component != "chart" || block.Version != 1 || block.Props.Type != kind || *block.Props.Series[0].Data[0] != 32 || block.Props.Series[0].Data[1] != nil {
			t.Fatalf("invalid chart conversion: %+v", block)
		}
	}
}

func TestChartDoesNotForwardRuntimeOrInvalidData(t *testing.T) {
	for _, value := range []string{`"alert(1)"`, `1e9999`} {
		raw := componentOpen + `{"charts_widget_v2":{"content":{"chartType":"bar","xKey":"name","series":[{"dataKey":"y"}],"data":[{"name":"A","y":` + value + `}]},"code":"alert(1)"}}` + componentClose
		text := RenderComponentFallback(parsed(raw, 0)[0])
		if strings.Contains(text, "deeix-ui") || strings.Contains(text, "alert") {
			t.Fatalf("unsafe chart: %q", text)
		}
	}
}
