package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

const generatedFileDownloadVariant = "generated-file:"

var (
	errGeneratedFileRedirect     = errors.New("generated file redirects are not allowed")
	openAIContainerIDPattern     = regexp.MustCompile(`^cntr_[A-Za-z0-9_-]{1,200}$`)
	openAIContainerFileIDPattern = regexp.MustCompile(`^cfile_[A-Za-z0-9_-]{1,200}$`)
	sandboxFileLinkPattern       = regexp.MustCompile("sandbox:[^\\s<>()\\[\\]\\\"'`]+")
)

var _ portllm.GeneratedFileReader = (*Client)(nil)

// OpenGeneratedFile downloads an explicitly cited container file using the exact
// generation route. The response remains streaming until its caller closes Body.
func (c *Client) OpenGeneratedFile(ctx context.Context, route portllm.RouteConfig, file portllm.GeneratedFile) (*portllm.GeneratedFileContent, error) {
	if portllm.NormalizeAdapter(route.Protocol) == portllm.AdapterChat01ChatCompletions {
		return c.openChat01GeneratedFile(ctx, route, file)
	}
	if portllm.NormalizeAdapter(route.Protocol) != portllm.AdapterOpenAIResponses {
		return nil, fmt.Errorf("generated file source is not supported for this adapter")
	}
	containerID, fileID, ok := parseOpenAIContainerFileSource(file.ID)
	if !ok {
		return nil, fmt.Errorf("invalid generated file source identifier")
	}
	base, err := url.Parse(strings.TrimSpace(route.BaseURL))
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("invalid generated file route URL")
	}
	requestURL := buildVersionedEndpointURL(route.BaseURL, "v1", "containers/"+containerID+"/files/"+fileID+"/content")
	requestCtx, cancel := context.WithTimeout(ctx, resolveReadTimeout(route.ReadTimeoutMS))
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	if key := strings.TrimSpace(route.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	setAdditionalHeaders(req, route.HeadersJSON)
	variant := generatedFileDownloadVariant + strconv.Itoa(normalizeConnectTimeoutMS(route.ConnectTimeoutMS))
	var resp *http.Response
	if route.UntrustedEndpoint {
		resp, err = c.doUntrustedRequest(route, req, variant)
	} else {
		resp, err = c.httpClients.Do(req, route.BaseURL, variant)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer cancel()
		defer resp.Body.Close() //nolint:errcheck
		body, readErr := readUpstreamBody(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, parseUpstreamError(resp.StatusCode, body, upstreamDebugSnapshot(req, nil, resp, body))
	}
	mimeType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return &portllm.GeneratedFileContent{
		Body:      &generatedFileBody{ReadCloser: resp.Body, cancel: cancel},
		SizeBytes: resp.ContentLength,
		MimeType:  mimeType,
	}, nil
}

type generatedFileBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *generatedFileBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

func parseOpenAIContainerFileSource(source string) (string, string, bool) {
	parts := strings.Split(source, ":")
	if len(parts) != 3 || parts[0] != "openai-container" || !openAIContainerIDPattern.MatchString(parts[1]) || !openAIContainerFileIDPattern.MatchString(parts[2]) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

type responsesFileAnnotation struct {
	file       portllm.GeneratedFile
	annotation map[string]any
	text       string
}

func parseResponsesGeneratedFiles(raw map[string]any) []portllm.GeneratedFile {
	var annotations []responsesFileAnnotation
	collectResponsesFileAnnotations(raw, "", &annotations)
	nameIDs := make(map[string]map[string]struct{})
	for _, item := range annotations {
		if nameIDs[item.file.FileName] == nil {
			nameIDs[item.file.FileName] = make(map[string]struct{})
		}
		nameIDs[item.file.FileName][item.file.ID] = struct{}{}
	}
	var files []portllm.GeneratedFile
	for _, item := range annotations {
		if start, startOK := item.annotation["start_index"]; startOK {
			if end, endOK := item.annotation["end_index"]; endOK {
				runes := []rune(item.text)
				first, last := toInt64(start), toInt64(end)
				if first >= 0 && last > first && last <= int64(len(runes)) {
					if links := matchingSandboxFileLinks(string(runes[first:last]), item.file.FileName); len(links) == 1 {
						item.file.SourceLinks = links
					}
				}
			}
		}
		if len(item.file.SourceLinks) == 0 && len(nameIDs[item.file.FileName]) == 1 {
			links := matchingSandboxFileLinks(item.text, item.file.FileName)
			// Multiple paths with the same basename are ambiguous without a span.
			if len(links) == 1 {
				item.file.SourceLinks = links
			}
		}
		files = mergeGeneratedFiles(files, item.file)
	}
	return files
}

func collectResponsesFileAnnotations(raw any, text string, result *[]responsesFileAnnotation) {
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			collectResponsesFileAnnotations(item, text, result)
		}
	case map[string]any:
		if ownText, ok := value["text"].(string); ok {
			text = ownText
		}
		if getString(value["type"]) == "container_file_citation" {
			containerID, fileID := getString(value["container_id"]), getString(value["file_id"])
			fileName := path.Base(strings.ReplaceAll(getString(value["filename"]), "\\", "/"))
			if openAIContainerIDPattern.MatchString(containerID) && openAIContainerFileIDPattern.MatchString(fileID) && fileName != "" && fileName != "." && fileName != ".." && fileName != "/" {
				*result = append(*result, responsesFileAnnotation{file: portllm.GeneratedFile{ID: "openai-container:" + containerID + ":" + fileID, FileName: fileName}, annotation: value, text: text})
			}
			return
		}
		for _, key := range []string{"output", "content", "annotations"} {
			collectResponsesFileAnnotations(value[key], text, result)
		}
	}
}

func matchingSandboxFileLinks(text, fileName string) []string {
	var links []string
	for _, candidate := range sandboxFileLinkPattern.FindAllString(text, -1) {
		parsed, err := url.Parse(candidate)
		if err == nil && parsed.Scheme == "sandbox" && parsed.Host == "" && parsed.RawQuery == "" && parsed.Fragment == "" && strings.HasPrefix(parsed.Path, "/") && path.Base(parsed.Path) == fileName {
			links = appendUniqueStrings(links, candidate)
		}
	}
	return links
}

func mergeGeneratedFiles(files []portllm.GeneratedFile, incoming ...portllm.GeneratedFile) []portllm.GeneratedFile {
	for _, file := range incoming {
		found := false
		for index := range files {
			if files[index].ID == file.ID {
				if files[index].FileName == "" {
					files[index].FileName = file.FileName
				}
				files[index].SourceLinks = appendUniqueStrings(files[index].SourceLinks, file.SourceLinks...)
				found = true
				break
			}
		}
		if !found {
			file.SourceLinks = appendUniqueStrings(nil, file.SourceLinks...)
			files = append(files, file)
		}
	}
	// Never replace one textual link with two different generated files.
	owners := make(map[string]string)
	ambiguous := make(map[string]bool)
	for _, file := range files {
		for _, link := range file.SourceLinks {
			if owner, ok := owners[link]; ok && owner != file.ID {
				ambiguous[link] = true
			}
			owners[link] = file.ID
		}
	}
	for index := range files {
		links := files[index].SourceLinks[:0]
		for _, link := range files[index].SourceLinks {
			if !ambiguous[link] {
				links = append(links, link)
			}
		}
		files[index].SourceLinks = links
	}
	// A repeated stream event must not reintroduce a link after two same-named
	// files made it ambiguous. Keep associations only when every same-named file
	// has its own verified links; a later complete response can supply those.
	nameCounts := make(map[string]int)
	missingLinks := make(map[string]bool)
	for _, file := range files {
		nameCounts[file.FileName]++
		if len(file.SourceLinks) == 0 {
			missingLinks[file.FileName] = true
		}
	}
	for index := range files {
		if nameCounts[files[index].FileName] > 1 && missingLinks[files[index].FileName] {
			files[index].SourceLinks = nil
		}
	}
	return files
}
