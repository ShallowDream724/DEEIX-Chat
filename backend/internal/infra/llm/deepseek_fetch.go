package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func (c *Client) FetchNativeURL(ctx context.Context, route portllm.RouteConfig, target string) (portllm.NativeURLPage, error) {
	parsed, err := url.Parse(target)
	if err != nil || len(target) > 8192 || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return portllm.NativeURLPage{}, errors.New("invalid native fetch URL")
	}
	body, _ := json.Marshal(map[string]any{"url": target})
	requestURL := strings.TrimSuffix(deepSeekFilesURL(route.BaseURL), "/files")
	requestURL = strings.TrimSuffix(requestURL, "/v1") + "/v1/tools/fetch"
	fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, _, err := c.deepSeekFileRequest(fetchCtx, route, http.MethodPost, requestURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return portllm.NativeURLPage{}, err
	}
	var page struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err = json.Unmarshal(raw, &page); err != nil {
		return portllm.NativeURLPage{}, err
	}
	if strings.TrimSpace(page.Content) == "" {
		return portllm.NativeURLPage{}, errors.New("native fetch returned no page content")
	}
	return portllm.NativeURLPage{Title: page.Title, Content: page.Content}, nil
}
