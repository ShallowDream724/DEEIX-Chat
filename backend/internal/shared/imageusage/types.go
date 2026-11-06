// Package imageusage describes observed image-model usage independently of a
// transport or pricing policy. Inputs include their cached subsets.
package imageusage

type Usage struct {
	Model                  string `json:"model,omitempty"`
	Present                bool   `json:"present"`
	Complete               bool   `json:"complete"`
	CacheKnown             bool   `json:"cacheKnown"`
	TextInputTokens        int64  `json:"textInputTokens"`
	ImageInputTokens       int64  `json:"imageInputTokens"`
	CachedTextInputTokens  int64  `json:"cachedTextInputTokens"`
	CachedImageInputTokens int64  `json:"cachedImageInputTokens"`
	ImageOutputTokens      int64  `json:"imageOutputTokens"`
	TextOutputTokens       int64  `json:"textOutputTokens"`
}

// Add combines separate calls. A missing or ambiguous breakdown remains
// incomplete; it must not become an apparently exact bill after aggregation.
func Add(a, b Usage) Usage {
	if !a.Present {
		return b
	}
	if !b.Present {
		return a
	}
	r := Usage{
		Model: a.Model, Present: true, Complete: a.Complete && b.Complete,
		CacheKnown:             a.CacheKnown && b.CacheKnown,
		TextInputTokens:        a.TextInputTokens + b.TextInputTokens,
		ImageInputTokens:       a.ImageInputTokens + b.ImageInputTokens,
		CachedTextInputTokens:  a.CachedTextInputTokens + b.CachedTextInputTokens,
		CachedImageInputTokens: a.CachedImageInputTokens + b.CachedImageInputTokens,
		ImageOutputTokens:      a.ImageOutputTokens + b.ImageOutputTokens,
		TextOutputTokens:       a.TextOutputTokens + b.TextOutputTokens,
	}
	if r.Model == "" {
		r.Model = b.Model
	} else if b.Model != "" && r.Model != b.Model {
		r.Complete = false
	}
	return r
}

// Delta extracts newly observed counters from cumulative events for one call.
// An identical terminal usage event therefore never charges a second time.
func Delta(current, previous Usage) Usage {
	if !current.Present {
		return Usage{}
	}
	if !previous.Present {
		return current
	}
	r := current
	r.TextInputTokens = max(0, current.TextInputTokens-previous.TextInputTokens)
	r.ImageInputTokens = max(0, current.ImageInputTokens-previous.ImageInputTokens)
	r.CachedTextInputTokens = max(0, current.CachedTextInputTokens-previous.CachedTextInputTokens)
	r.CachedImageInputTokens = max(0, current.CachedImageInputTokens-previous.CachedImageInputTokens)
	r.ImageOutputTokens = max(0, current.ImageOutputTokens-previous.ImageOutputTokens)
	r.TextOutputTokens = max(0, current.TextOutputTokens-previous.TextOutputTokens)
	return r
}
