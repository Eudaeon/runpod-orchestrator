// Package runpod is a thin client for RunPod's GraphQL API (api.runpod.io).
//
// Requests are authenticated with a short-lived Bearer JWT obtained from a
// TokenSource (see the clerk package).
package runpod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Sentinel errors returned when an operation succeeds at the transport level
// but the API returns no usable data.
var (
	errNotAuthenticated = errors.New("runpod: not authenticated (check credentials)")
	errTemplateNotFound = errors.New("runpod: template not found")
	errDeployFailed     = errors.New("runpod: deploy returned no pod")
)

const (
	defaultEndpoint = "https://api.runpod.io/graphql"
	userAgent       = "runpod-orchestrator/0.1"
	origin          = "https://console.runpod.io"
	referer         = "https://console.runpod.io/"
)

// TokenSource supplies a valid Bearer token for each request. The clerk.Client
// satisfies this interface.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Client talks to the RunPod GraphQL API.
type Client struct {
	httpClient *http.Client
	endpoint   string
	tokens     TokenSource
}

// New returns a Client that authenticates using tokens from the given source.
func New(tokens TokenSource) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		endpoint:   defaultEndpoint,
		tokens:     tokens,
	}
}

// graphQLError is a single error entry returned by the GraphQL API.
type graphQLError struct {
	Message string `json:"message"`
}

func (e graphQLError) Error() string { return e.Message }

// do executes a GraphQL operation and unmarshals the "data" field into out.
func (c *Client) do(ctx context.Context, operationName, query string, variables map[string]any, out any) error {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}

	reqBody, err := json.Marshal(map[string]any{
		"operationName": operationName,
		"query":         query,
		"variables":     variables,
	})
	if err != nil {
		return err
	}

	// The console tags the request with the operation name in the query string.
	endpoint := c.endpoint
	if operationName != "" {
		endpoint += "?operation=" + operationName
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "*/*")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("runpod: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("runpod: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runpod: %s: %s", resp.Status, string(data))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("runpod: decoding response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("runpod: graphql error: %s", envelope.Errors[0].Message)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("runpod: decoding data: %w", err)
		}
	}
	return nil
}
