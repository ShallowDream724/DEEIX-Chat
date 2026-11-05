package chat01

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Resource describes a reference, not a fetched or persisted artifact. An image
// in Markdown is not evidence that an image generation tool was called.
type Resource struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Name     string `json:"name"`
	BlockID  string `json:"blockId"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Evidence string `json:"evidence"`
	// OriginKind preserves whether the reference appeared in an answer, process,
	// or component. Discovering an artifact does not move its surrounding text.
	OriginKind Kind   `json:"originKind"`
	SourceURL  string `json:"sourceUrl,omitempty"`
}

var markdownLinkRE = regexp.MustCompile(`^(!?)\[([^\]\n]*)\]\(([^\s)]+)\)`)

// ResourcesFor finds explicit answer/process links and known component fields.
// It ignores fenced and inline code,
// keeps separate occurrences in source order, and never constructs URLs from
// internal citation references or an unavailable sandbox path.
func ResourcesFor(blocks []Block) []Resource {
	var out []Resource
	var fence byte
	fenceLength := 0
	inline := 0
	for _, b := range blocks {
		if b.Kind == KindComponent {
			out = append(out, componentResources(b)...)
			continue
		}
		if b.Kind != KindAnswer && b.Kind != KindReasoning && b.Kind != KindSearch && b.Kind != KindToolNote {
			continue
		}
		text := b.Text
		lineStart := true
		for i := 0; i < len(text); {
			if lineStart {
				indent := 0
				for i+indent < len(text) && indent < 4 && text[i+indent] == ' ' {
					indent++
				}
				if indent <= 3 && i+indent < len(text) && (text[i+indent] == '`' || text[i+indent] == '~') {
					ch := text[i+indent]
					n := run(text[i+indent:], ch)
					if n >= 3 && (fence == 0 || fence == ch && n >= fenceLength) {
						if fence == 0 {
							fence = ch
							fenceLength = n
						} else {
							fence = 0
						}
						end := strings.IndexByte(text[i:], '\n')
						if end < 0 {
							break
						}
						i += end + 1
						lineStart = true
						continue
					}
				}
				if indent >= 4 || text[i] == '\t' {
					end := strings.IndexByte(text[i:], '\n')
					if end < 0 {
						break
					}
					i += end + 1
					continue
				}
			}
			lineStart = text[i] == '\n'
			if fence != 0 {
				i++
				continue
			}
			if text[i] == '\\' {
				i += min(2, len(text)-i)
				continue
			}
			if text[i] == '`' {
				n := run(text[i:], '`')
				if inline == 0 {
					inline = n
				} else if inline == n {
					inline = 0
				}
				i += n
				continue
			}
			if inline == 0 && (text[i] == '[' || text[i] == '!') {
				m := markdownLinkRE.FindStringSubmatchIndex(text[i:min(i+8192, len(text))])
				if m != nil && m[0] == 0 {
					raw := text[i+m[6] : i+m[7]]
					u, err := url.Parse(raw)
					if err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "sandbox") && (u.Scheme == "sandbox" || u.Hostname() != "") {
						image := m[3] > m[2]
						kind := "source"
						evidence := "answer-link"
						if image {
							kind = "external-image"
							evidence = "answer-image"
						}
						if u.Scheme == "sandbox" {
							kind = "unavailable"
							evidence = "unmapped-sandbox"
						}
						// The captured python-generations namespace is explicit code
						// output provenance. A generic CDN host or extension is not.
						if u.Scheme == "https" && strings.EqualFold(u.Host, "files.chat01.ai") && u.User == nil && strings.HasPrefix(u.Path, "/python-generations/") {
							kind = "file"
							if image {
								kind = "image"
							}
							evidence = "chat01-python-generations"
						}
						if IsGeneratedImageURL(raw) {
							kind = "image"
							evidence = "chat01-generated-image-namespace"
						}
						name := path.Base(u.Path)
						if decoded, e := url.PathUnescape(name); e == nil {
							name = decoded
						}
						out = append(out, Resource{Kind: kind, URL: raw, Title: text[i+m[4] : i+m[5]], Name: name, BlockID: b.ID, Start: b.Start + int64(i), End: b.Start + int64(i+m[1]), Evidence: evidence, OriginKind: b.Kind})
					}
					i += m[1]
					continue
				}
			}
			i++
		}
	}
	return out
}

// This exact namespace appears in independent native-generation captures 01,
// 03 and 22. The 24-character user ID, two UUIDs, and PNG filename are structural
// evidence; a CDN host, user-uploads path or extension alone is insufficient.
var generatedImagePathRE = regexp.MustCompile(`^/user-[A-Za-z0-9]{24}__[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.png$`)

func IsGeneratedImageURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Host, "files.chat01.ai") && u.User == nil && u.RawPath == "" && u.RawQuery == "" && u.Fragment == "" && generatedImagePathRE.MatchString(u.Path)
}

func componentResources(block Block) []Resource {
	if !strings.HasPrefix(block.Text, componentOpen) {
		return nil
	}
	// Only explicit fields of the observed map cards are interpreted. Generic
	// strings (including code, constants or callback text) are never URL-mined.
	info := ComponentInfoFor(block)
	var out []Resource
	for _, card := range info.Cards {
		if card.URL == "" {
			continue
		}
		kind, evidence := "source", "component-map-card-link"
		if IsGeneratedImageURL(card.URL) {
			kind = "image"
			evidence = "chat01-generated-image-namespace"
		}
		u, _ := url.Parse(card.URL)
		// JSON may escape a URL; the containing component is the honest source
		// range rather than a guessed exact offset into its encoded payload.
		out = append(out, Resource{Kind: kind, URL: card.URL, Title: card.Title, Name: path.Base(u.Path), BlockID: block.ID, Start: block.Start, End: block.End, Evidence: evidence, OriginKind: KindComponent})
	}
	// The public site's captured opGenui results identify image content and
	// source pages separately. Only these exact declarative fields are read;
	// runtime code/constants and arbitrary callback values remain opaque.
	payload := strings.TrimSuffix(strings.TrimPrefix(block.Text, componentOpen), componentClose)
	if len(payload) > maxComponent {
		payload = payload[:maxComponent]
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	budget := 2048
	value, _, _ := readJSONPrefix(dec, 0, &budget)
	root, _ := value.(map[string]any)
	runtime, _ := root["runtime"].(map[string]any)
	appData, _ := runtime["appData"].(map[string]any)
	opGenui, _ := appData["opGenui"].(map[string]any)
	results, _ := opGenui["componentResults"].(map[string]any)
	appendURL := func(raw, title, source, kind, evidence string) {
		if !validSourceURL(raw) || len(out) >= 128 {
			return
		}
		if IsGeneratedImageURL(raw) {
			kind = "image"
			evidence = "chat01-generated-image-namespace"
		}
		if !validSourceURL(source) {
			source = ""
		}
		u, _ := url.Parse(raw)
		out = append(out, Resource{Kind: kind, URL: raw, Title: title, Name: path.Base(u.Path), BlockID: block.ID, Start: block.Start, End: block.End, Evidence: evidence, OriginKind: KindComponent, SourceURL: source})
	}
	for _, key := range sortedKeys(results) {
		entry, _ := results[key].(map[string]any)
		state, _ := entry["state"].(map[string]any)
		switch entry["componentName"] {
		case "AsyncImage", "AsyncImageGroup":
			images, _ := state["images"].([]any)
			for _, item := range images {
				image, _ := item.(map[string]any)
				raw, _ := image["content_url"].(string)
				if raw == "" {
					raw, _ = image["original_content_url"].(string)
				}
				source, _ := image["url"].(string)
				appendURL(raw, safeString(image["title"]), source, "external-image", "component-async-image")
			}
		case "Entity":
			raw, _ := state["image_url"].(string)
			appendURL(raw, safeString(state["value"]), "", "external-image", "component-entity-image")
		case "Link":
			raw, _ := state["url"].(string)
			appendURL(raw, safeString(state["title"]), "", "source", "component-link")
		case "Cite":
			items, _ := state["items"].([]any)
			for _, item := range items {
				citation, _ := item.(map[string]any)
				raw, _ := citation["url"].(string)
				appendURL(raw, safeString(citation["title"]), "", "source", "component-citation")
			}
		}
	}
	return out
}
