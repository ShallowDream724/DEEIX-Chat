package extraction

import (
	"encoding/json"
	"fmt"
	"io"
)

func DecodeBounded(reader io.Reader, target any, limit int64) error {
	data, err := ReadBounded(reader, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

const DefaultReadMaxBytes int64 = 50 * 1024 * 1024

func ReadMaxBytes(value int64) int64 {
	if value > 0 {
		return value
	}
	return DefaultReadMaxBytes
}

// ReadBounded returns the complete payload or an error; a truncated prefix is
// never accepted as a successful extraction response.
func ReadBounded(reader io.Reader, limit int64) ([]byte, error) {
	limit = ReadMaxBytes(limit)
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("extraction response exceeds %d bytes", limit)
	}
	return data, nil
}
