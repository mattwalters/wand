package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProjectsReturnsATeamsProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"teams":{"nodes":[{"projects":{"nodes":[
			{"id":"p1","name":"Onboarding","description":"New user flow"}
		]}}]}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	projects, err := c.Projects(context.Background(), "WND")
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "Onboarding" {
		t.Fatalf("projects = %+v", projects)
	}
}

func TestProjectsWithUnknownTeamReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"teams":{"nodes":[]}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	projects, err := c.Projects(context.Background(), "NOPE")
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects = %+v, want none", projects)
	}
}

func TestCreateProjectSendsTeamIDsAndReturnsTheCreated(t *testing.T) {
	var input map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables struct {
				Input map[string]json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		input = req.Variables.Input
		w.Write([]byte(`{"data":{"projectCreate":{"success":true,"project":{"id":"p1","name":"Onboarding","description":"d"}}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	p, err := c.CreateProject(context.Background(), "team-1", "Onboarding", "d")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "p1" || p.Name != "Onboarding" {
		t.Fatalf("project = %+v", p)
	}
	var teamIDs []string
	if err := json.Unmarshal(input["teamIds"], &teamIDs); err != nil {
		t.Fatalf("decoding teamIds: %v", err)
	}
	if len(teamIDs) != 1 || teamIDs[0] != "team-1" {
		t.Errorf("teamIds = %v", teamIDs)
	}
}

func TestCreateProjectRefusedReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"projectCreate":{"success":false,"project":null}}}`))
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", Endpoint: srv.URL}
	if _, err := c.CreateProject(context.Background(), "team-1", "Onboarding", "d"); err == nil {
		t.Fatal("expected an error on refusal")
	}
}
