// Package chat01 incrementally classifies text decoded from Chat01 responses.
// It performs no I/O and never executes the server's reported tool activity.
package chat01

// Version identifies the interpretation used for both streaming and persistence.
const Version = "chat01-text-v1"

type Kind string

const (
	KindAnswer       Kind = "answer"
	KindReasoning    Kind = "reasoning"
	KindPython       Kind = "python"
	KindShell        Kind = "shell"
	KindCodeFragment Kind = "code-fragment"
	KindSearch       Kind = "search"
	KindToolNote     Kind = "tool-note"
	KindComponent    Kind = "component"
	KindControl      Kind = "control"
	KindMeta         Kind = "meta"
)

// Block is an ordered, source-preserving segment. Offsets are UTF-8 byte offsets
// into the concatenated input. Complete describes a delimited component only;
// it never means that a reported tool succeeded or returned a result.
type Block struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	Text      string `json:"text"`
	Reason    string `json:"reason,omitempty"`
	Section   int    `json:"section"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	Complete  bool   `json:"complete,omitempty"`
	Oversized bool   `json:"oversized,omitempty"`
}

// Delta appends Text to BlockID. A metadata-only update may have empty Text.
// The stable local ID and offsets allow consumers to persist exactly the same
// blocks used for streaming without reparsing the final answer.
type Delta struct {
	BlockID   string `json:"blockId"`
	Kind      Kind   `json:"kind"`
	Text      string `json:"text"`
	Reason    string `json:"reason,omitempty"`
	Section   int    `json:"section"`
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	Complete  bool   `json:"complete,omitempty"`
	Oversized bool   `json:"oversized,omitempty"`
}
