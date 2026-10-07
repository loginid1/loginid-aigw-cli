package client_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/client"
)

func TestDoSendsRequest(t *testing.T) {
	t.Parallel()

	var (
		gotPath, gotQuery, gotAuth, gotType string
		gotBody                             map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		gotAuth, gotType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")

		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)

	body, err := client.New(srv.URL+"/", "k-1").Do(t.Context(), &client.Request{
		Method:     http.MethodPost,
		Path:       "/projects/{projectId}/resources",
		PathParams: map[string]string{"projectId": "a/b"},
		Query:      map[string][]string{"limit": {"5"}},
		Body:       map[string]any{"name": "shop"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != `{"ok":true}` {
		t.Errorf("body = %s", body)
	}

	// The slash in the id is escaped, so it cannot reach another path.
	if gotPath != "/projects/a%2Fb/resources" || gotQuery != "limit=5" {
		t.Errorf("path = %s?%s", gotPath, gotQuery)
	}

	if gotAuth != "Bearer k-1" || gotType != "application/json" || gotBody["name"] != "shop" {
		t.Errorf("auth %q, type %q, body %v", gotAuth, gotType, gotBody)
	}
}

func TestDoRejectsUnfilledPathParam(t *testing.T) {
	t.Parallel()

	_, err := client.New("http://unused.invalid", "k").Do(t.Context(), &client.Request{
		Method: http.MethodGet,
		Path:   "/projects/{projectId}",
	})
	if err == nil || !strings.Contains(err.Error(), "{projectId}") {
		t.Fatalf("want an error naming {projectId}, got %v", err)
	}
}

func TestDoReturnsAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"msg":"api key is missing a required scope","msgCode":"forbidden"}`)
	}))
	t.Cleanup(srv.Close)

	_, err := client.New(srv.URL, "k").Do(t.Context(), &client.Request{
		Method: http.MethodPost, Path: "/projects", Scope: "project:write",
	})

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("want a 403 *APIError, got %v", err)
	}

	want := "POST /projects: HTTP 403: forbidden: api key is missing a required scope (requires scope project:write)"
	if err.Error() != want {
		t.Errorf("error = %q\n want %q", err.Error(), want)
	}
}
