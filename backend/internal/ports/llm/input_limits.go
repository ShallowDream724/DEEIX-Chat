package llm

import "errors"

// ErrInputTooLarge means a request exceeds the adapter's input budget before it
// reaches the upstream. It must not be treated as an upstream availability error.
var ErrInputTooLarge = errors.New("model input exceeds request limit")
