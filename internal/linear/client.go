// Package linear is a minimal client for Linear's GraphQL API.
//
// It is deliberately dependency-free: wand's use of the API is a fixed set of
// typed queries, which a GraphQL client library would not make shorter, and
// wand ships as a standalone open-source binary where every dependency is
// weight. The transport is one function; everything else is typed operations
// over it.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Endpoint is Linear's public GraphQL endpoint.
const Endpoint = "https://api.linear.app/graphql"

// Client speaks GraphQL to one Linear workspace.
//
// Linear personal API keys are sent raw in the Authorization header — no
// "Bearer" prefix. That is Linear's documented convention, and getting it
// wrong produces an authentication error that looks like a bad key.
type Client struct {
	APIKey   string
	Endpoint string       // defaults to Endpoint when empty
	HTTP     *http.Client // defaults to a 30s-timeout client when nil
}

// GraphQLError is one error entry from a GraphQL response.
type GraphQLError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code                   string `json:"code"`
		UserPresentableMessage string `json:"userPresentableMessage"`
	} `json:"extensions"`
}

// APIError is a non-empty errors array in an otherwise well-formed response.
type APIError struct {
	Errors []GraphQLError
}

func (e *APIError) Error() string {
	if len(e.Errors) == 0 {
		return "linear: unknown API error"
	}
	msg := e.Errors[0].Message
	if p := e.Errors[0].Extensions.UserPresentableMessage; p != "" {
		msg = p
	}
	if len(e.Errors) > 1 {
		return fmt.Sprintf("linear: %s (and %d more errors)", msg, len(e.Errors)-1)
	}
	return "linear: " + msg
}

// Do executes one GraphQL request and decodes the "data" object into out.
// A response with GraphQL errors returns *APIError, even alongside data.
func (c *Client) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("linear: encode request: %w", err)
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("linear: build request: %w", err)
	}
	req.Header.Set("Authorization", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	defer resp.Body.Close()

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []GraphQLError  `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("linear: decode response (HTTP %d): %w", resp.StatusCode, err)
	}
	if len(envelope.Errors) > 0 {
		return &APIError{Errors: envelope.Errors}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear: HTTP %d with no GraphQL errors", resp.StatusCode)
	}
	if out != nil && envelope.Data != nil {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("linear: decode data: %w", err)
		}
	}
	return nil
}
