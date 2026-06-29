package runpod

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// hapiBaseURL serves pod logs and other per-pod operations. It is a different
// host from the GraphQL API but accepts the same Bearer token.
const hapiBaseURL = "https://hapi.runpod.net"

// PodLogs holds a pod's log lines. container holds the container's stdout/stderr
// (the start command's output); system holds RunPod's lifecycle events (image
// pull, scheduling).
type PodLogs struct {
	Container []string `json:"container"`
	System    []string `json:"system"`
}

// PodLogs fetches the current logs for a pod.
func (c *Client) PodLogs(ctx context.Context, podID string) (*PodLogs, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1/pod/%s/logs", hapiBaseURL, podID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", referer)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runpod: fetching logs: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("runpod: reading logs: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runpod: logs request failed: %s", resp.Status)
	}

	var out PodLogs
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("runpod: decoding logs: %w", err)
	}
	return &out, nil
}
