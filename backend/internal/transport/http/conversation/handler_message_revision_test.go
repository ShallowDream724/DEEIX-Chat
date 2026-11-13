package conversation

import (
	"encoding/json"
	"github.com/gin-gonic/gin/binding"
	"testing"
)

func TestSaveMessageRevisionRequestRequiresExplicitFinalAttachmentCollection(t *testing.T) {
	for _, scenario := range []struct {
		name, body string
		valid      bool
	}{
		{"remove all", `{"content":"keep text","fileIDs":[],"expectedUpdatedAt":"2026-11-13T00:00:00Z","revisionID":"4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0"}`, true},
		{"attachment only", `{"content":"","fileIDs":["file-one"],"expectedUpdatedAt":"2026-11-13T00:00:00Z","revisionID":"4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0"}`, true},
		{"missing collection", `{"content":"text","expectedUpdatedAt":"2026-11-13T00:00:00Z","revisionID":"4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0"}`, false},
		{"null collection", `{"content":"text","fileIDs":null,"expectedUpdatedAt":"2026-11-13T00:00:00Z","revisionID":"4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0"}`, false},
		{"no revision identity", `{"content":"text","fileIDs":[],"expectedUpdatedAt":"2026-11-13T00:00:00Z"}`, false},
		{"no optimistic version", `{"content":"text","fileIDs":[],"revisionID":"4dcc136c-cc03-41fc-9ebd-eb5f50d2b0a0"}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var request SaveMessageRevisionRequest
			if err := json.Unmarshal([]byte(scenario.body), &request); err != nil {
				t.Fatal(err)
			}
			err := binding.Validator.ValidateStruct(request)
			if (err == nil) != scenario.valid {
				t.Fatalf("validation error = %v, valid = %v", err, scenario.valid)
			}
		})
	}
}
