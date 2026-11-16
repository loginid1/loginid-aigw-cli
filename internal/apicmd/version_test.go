package apicmd_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

type reply struct {
	status int
	body   string
}

// fakeAPI answers each "METHOD path" with its reply, and records every request
// and the body of each write. A request with no reply fails the test.
type fakeAPI struct {
	mu     sync.Mutex
	calls  []string
	writes []map[string]any
}

func newFakeAPI(t *testing.T, replies map[string]reply) (*httptest.Server, *fakeAPI) {
	t.Helper()

	api := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()

		api.mu.Lock()
		api.calls = append(api.calls, key)

		if r.Method != http.MethodGet {
			raw, _ := io.ReadAll(r.Body)

			var body map[string]any
			if json.Unmarshal(raw, &body) != nil {
				t.Errorf("%s: body is not a JSON object: %s", key, raw)
			}

			api.writes = append(api.writes, body)
		}
		api.mu.Unlock()

		rep, ok := replies[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusTeapot)

			return
		}

		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(srv.Close)

	return srv, api
}

const (
	projectPath = "/v1/projects/p1"
	t1          = "2026-11-12T10:00:00.123456Z"
	t0          = "2026-11-01T09:00:00.000001Z"
)

func TestUpdateSendsTheVersionItRead(t *testing.T) {
	t.Parallel()

	srv, api := newFakeAPI(t, map[string]reply{
		"GET " + projectPath:   {http.StatusOK, `{"id":"p1","name":"x","updatedAt":"` + t1 + `"}`},
		"PATCH " + projectPath: {http.StatusOK, `{"id":"p1"}`},
	})

	_, err := runAigw(t, srv.URL, projects, "update", "p1", "--name", "x-renamed", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"name": "x-renamed", "expectedUpdatedAt": t1}
	if len(api.writes) != 1 || !reflect.DeepEqual(api.writes[0], want) {
		t.Errorf("write body = %v, want %v", api.writes, want)
	}
}

// A version the caller passed is sent as given, with no read; "null" is JSON null.
func TestAGivenVersionIsKept(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		flag string
		want any
	}{
		{t0, t0},
		{"null", nil},
	} {
		srv, api := newFakeAPI(t, map[string]reply{
			"PATCH " + projectPath: {http.StatusOK, `{}`},
		})

		_, err := runAigw(t, srv.URL, projects, "update", "p1", "--name", "y", "--expected-updated-at", tc.flag, apiKey, "k")
		if err != nil {
			t.Fatal(err)
		}

		if got, ok := api.writes[0]["expectedUpdatedAt"]; !ok || got != tc.want {
			t.Errorf("--expected-updated-at %s sent %v (present %v)", tc.flag, got, ok)
		}

		if len(api.calls) != 1 {
			t.Errorf("--expected-updated-at %s: want the write only, got %v", tc.flag, api.calls)
		}
	}
}

// Never saved (404), or a build that does not report updatedAt: the write says
// null.
func TestNoVersionToReadSendsNull(t *testing.T) {
	t.Parallel()

	issuer := projectPath + "/kya-issuer-policy/skyfire-kyapay"

	for name, get := range map[string]reply{
		"404":          {http.StatusNotFound, `{"msg":"Not found","msgCode":"not_found"}`},
		"no updatedAt": {http.StatusOK, `{"issuerName":"skyfire-kyapay","minTrust":"KNOWN"}`},
	} {
		srv, api := newFakeAPI(t, map[string]reply{
			"GET " + issuer: get,
			"PUT " + issuer: {http.StatusOK, `{}`},
		})

		_, err := runAigw(t, srv.URL, projects, "kya-issuers", "set", "skyfire-kyapay", "--project-id", "p1", apiKey, "k")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if got, ok := api.writes[0]["expectedUpdatedAt"]; !ok || got != nil {
			t.Errorf("%s: expectedUpdatedAt = %v (present %v), want null", name, got, ok)
		}
	}
}

func TestReplaceSendsAVersionPerHeldEntry(t *testing.T) {
	t.Parallel()

	list := projectPath + "/kya-issuer-policy"

	srv, api := newFakeAPI(t, map[string]reply{
		"GET " + list: {http.StatusOK, `[{"issuerName":"a","updatedAt":"` + t1 + `"},{"issuerName":"b"}]`},
		"PUT " + list: {http.StatusOK, `[]`},
	})

	_, err := runAigw(t, srv.URL, projects, "kya-issuers", "replace", "--project-id", "p1",
		"-d", "entries[0][issuerName]=c", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	want := []any{
		map[string]any{"id": "a", "expectedUpdatedAt": t1},
		map[string]any{"id": "b", "expectedUpdatedAt": nil},
	}
	if got := api.writes[0]["versions"]; !reflect.DeepEqual(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
}

// A read that fails stops the write, and keeps the read's exit code.
func TestAFailedReadStopsTheWrite(t *testing.T) {
	t.Parallel()

	srv, api := newFakeAPI(t, map[string]reply{
		"GET " + projectPath: {http.StatusForbidden, `{"msg":"api key is missing a required scope","msgCode":"forbidden"}`},
	})

	_, err := runAigw(t, srv.URL, projects, "update", "p1", "--name", "z", apiKey, "k")

	var exitErr *apicmd.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != apicmd.ExitMissingScope {
		t.Errorf("err = %v, want exit %d", err, apicmd.ExitMissingScope)
	}

	if err == nil || !strings.Contains(err.Error(), "--expected-updated-at") {
		t.Errorf("err %v does not say how to pass the version", err)
	}

	if len(api.writes) != 0 {
		t.Errorf("wrote %v after a failed read", api.writes)
	}
}

// A stale write shows aigw's own words, which say what happened and what to do
// (loginid-aigw internal/errors ErrStaleUpdate).
func TestConflictShowsTheServersMessage(t *testing.T) {
	t.Parallel()

	const stale = "Someone else changed this since you loaded it. Reload to see their changes."

	srv, _ := newFakeAPI(t, map[string]reply{
		"PATCH " + projectPath: {http.StatusConflict, `{"msg":"` + stale + `","msgCode":"conflict"}`},
	})

	_, err := runAigw(t, srv.URL, projects, "update", "p1", "--name", "z", "--expected-updated-at", t0, apiKey, "k")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409: conflict: "+stale) {
		t.Errorf("err = %v", err)
	}
}

// Help does not ask for the version the CLI fills in.
func TestHelpDoesNotRequireTheVersion(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{projects, "update", "--help"},
		{projects, "kya-issuers", "replace", "--help"},
	} {
		out, err := runAigw(t, "http://unused", args...)
		if err != nil {
			t.Fatal(err)
		}

		for _, bad := range []string{"<expected-updated-at>", "[versions]", "versions[0]"} {
			if strings.Contains(out, bad) {
				t.Errorf("%v help mentions %q:\n%s", args, bad, out)
			}
		}

		if strings.Contains(args[1], "update") && !strings.Contains(out, "the CLI reads the current one") {
			t.Errorf("%v help does not explain --expected-updated-at:\n%s", args, out)
		}
	}
}
