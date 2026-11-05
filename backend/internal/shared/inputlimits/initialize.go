package inputlimits

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// InitializeUpstreamDefaults fills only absent fields of one upstream override.
// Both documents use the ordinary administrator schema: omitted fields inherit,
// and existing explicit values (including zero and null) are preserved.
func InitializeUpstreamDefaults(raw string, upstreamID uint, presetJSON string) (string, error) {
	if upstreamID == 0 {
		return "", fmt.Errorf("input limits upstream ID must be positive")
	}
	key := strconv.FormatUint(uint64(upstreamID), 10)
	var preset map[string]json.RawMessage
	if err := json.Unmarshal([]byte(presetJSON), &preset); err != nil || preset == nil {
		return "", fmt.Errorf("input limits preset must be an object")
	}
	probe, _ := json.Marshal(map[string]any{"upstreams": map[string]any{key: preset}})
	if err := Validate(string(probe)); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		raw = DefaultConfigJSON()
	}
	if err := Validate(raw); err != nil {
		return "", err
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &root)
	upstreams := map[string]map[string]json.RawMessage{}
	if value, exists := root["upstreams"]; exists {
		_ = json.Unmarshal(value, &upstreams)
	}
	if upstreams[key] == nil {
		upstreams[key] = map[string]json.RawMessage{}
	}
	for field, value := range preset {
		if _, exists := upstreams[key][field]; !exists {
			upstreams[key][field] = value
		}
	}
	root["upstreams"], _ = json.Marshal(upstreams)
	result, err := json.Marshal(root)
	return string(result), err
}
