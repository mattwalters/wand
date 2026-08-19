package linear

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoSendsRawKeyNoBearer(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "lin_api_test", Endpoint: srv.URL}
	if err := c.Do(context.Background(), "query { viewer { id } }", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	// Linear wants the key raw; a Bearer prefix is rejected as a bad key.
	if gotAuth != "lin_api_test" {
		t.Errorf("Authorization header %q, want the raw key", gotAuth)
	}
}

func TestDoDecodesData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Variables["key"] != "WSPK" {
			t.Errorf("variables not sent: %v", req.Variables)
		}
		w.Write([]byte(`{"data":{"teams":{"nodes":[{"id":"t1","key":"WSPK"}]}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	team, err := c.TeamByKey(context.Background(), "WSPK")
	if err != nil {
		t.Fatalf("teamByKey: %v", err)
	}
	if team.ID != "t1" {
		t.Errorf("team id %q, want t1", team.ID)
	}
}

func TestDoSurfacesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Shaped like the live API's plan-gating error from the spike.
		w.WriteHeader(400)
		w.Write([]byte(`{"errors":[{"message":"Not allowed to access feature 'privateTeams'",` +
			`"extensions":{"code":"FEATURE_NOT_ACCESSIBLE",` +
			`"userPresentableMessage":"Subscribe to the Business plan to access private teams in your workspace."}}]}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	err := c.Do(context.Background(), "mutation { x }", nil, nil)
	var apiErr *APIError
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	// The user-presentable message wins: it is the one written for humans.
	if !strings.Contains(err.Error(), "Business plan") {
		t.Errorf("error %q should carry the user-presentable message", err.Error())
	}
}
