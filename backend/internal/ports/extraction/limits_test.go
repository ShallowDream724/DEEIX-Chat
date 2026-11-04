package extraction

import (
	"strings"
	"testing"
)

func TestReadBoundedRejectsTruncatedSuccess(t *testing.T) {
	for _, value := range []string{"abcd", "abcde"} {
		data, err := ReadBounded(strings.NewReader(value), 4)
		if len(value) == 4 && (err != nil || string(data) != value) {
			t.Fatalf("exact boundary: %q, %v", data, err)
		}
		if len(value) > 4 && (err == nil || data != nil) {
			t.Fatalf("oversize accepted: %q, %v", data, err)
		}
	}
}

func TestDecodeBoundedRejectsValidPrefixWithOversizedTail(t *testing.T) {
	var result map[string]any
	if err := DecodeBounded(strings.NewReader(`{}          `), &result, 4); err == nil {
		t.Fatal("accepted oversized response with valid JSON prefix")
	}
}
