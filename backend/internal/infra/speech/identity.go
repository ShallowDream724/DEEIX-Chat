package speech

import (
	"os"
)

func readIdentity(p string) ([]byte, error)  { return os.ReadFile(p) }
func writeIdentity(p string, b []byte) error { return os.WriteFile(p, b, 0600) }
