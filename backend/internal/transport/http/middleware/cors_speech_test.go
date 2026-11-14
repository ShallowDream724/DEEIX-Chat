package middleware

import "testing"

func TestSharedCORSOriginPolicyForUpgradedConnections(t *testing.T) {
	for _, tt := range []struct {
		origin, configured string
		allowed            bool
	}{{"https://chat.example", "https://chat.example", true}, {"https://CHAT.example/", "https://chat.example", true}, {"tauri://localhost", "https://chat.example", true}, {"http://tauri.localhost", "https://chat.example", true}, {"https://evil.example", "https://chat.example", false}, {"https://evil.example", "*", true}, {"http://localhost:3100", "http://localhost:3100,http://localhost:8080", true}} {
		t.Run(tt.origin+"_"+tt.configured, func(t *testing.T) {
			if got := IsCORSOriginAllowed(tt.origin, tt.configured); got != tt.allowed {
				t.Fatalf("allowed=%t wanted %t", got, tt.allowed)
			}
		})
	}
}
