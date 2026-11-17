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
	// stamps is the X-Expected-Updated-At header of each write, "" when none was sent.
	stamps []string
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
			if len(raw) > 0 && json.Unmarshal(raw, &body) != nil {
				t.Errorf("%s: body is not a JSON object: %s", key, raw)
			}

			api.writes = append(api.writes, body)
			api.stamps = append(api.stamps, r.Header.Get("X-Expected-Updated-At"))
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

	if len(api.stamps) != 1 || api.stamps[0] != t1 {
		t.Errorf("version header = %v, want %s", api.stamps, t1)
	}

	want := map[string]any{"name": "x-renamed"}
	if !reflect.DeepEqual(api.writes[0], want) {
		t.Errorf("write body = %v, want %v: the version is a header, not a body field", api.writes[0], want)
	}
}

// A delete sends the version it read too, in the same header.
func TestDeleteSendsTheVersionItRead(t *testing.T) {
	t.Parallel()

	issuer := projectPath + "/kya-issuer-policy/skyfire-kyapay"

	srv, api := newFakeAPI(t, map[string]reply{
		"GET " + issuer:    {http.StatusOK, `{"issuerName":"skyfire-kyapay","updatedAt":"` + t1 + `"}`},
		"DELETE " + issuer: {http.StatusNoContent, ``},
	})

	_, err := runAigw(t, srv.URL, projects, "kya-issuers", "remove", "skyfire-kyapay", "--project-id", "p1", "--confirm", apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	if len(api.stamps) != 1 || api.stamps[0] != t1 {
		t.Errorf("version header = %v, want %s", api.stamps, t1)
	}
}

// A version the caller passed is sent as given, with no read. "null" says the
// row was never updated, and the API checks it as that.
func TestAGivenVersionIsKept(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		flag, want string
	}{
		{t0, t0},
		{"null", "null"},
	} {
		srv, api := newFakeAPI(t, map[string]reply{
			"PATCH " + projectPath: {http.StatusOK, `{}`},
		})

		_, err := runAigw(t, srv.URL, projects, "update", "p1", "--name", "y", "--expected-updated-at", tc.flag, apiKey, "k")
		if err != nil {
			t.Fatal(err)
		}

		if got := api.stamps[0]; got != tc.want {
			t.Errorf("--expected-updated-at %s sent %q, want %q", tc.flag, got, tc.want)
		}

		if len(api.calls) != 1 {
			t.Errorf("--expected-updated-at %s: want the write only, got %v", tc.flag, api.calls)
		}
	}
}

// A row that does not exist yet (404) has no version to send: the write is not checked.
// A row that exists with no updatedAt sends null, so it is refused if it changed since.
func TestNoVersionToReadSendsNone(t *testing.T) {
	t.Parallel()

	issuer := projectPath + "/kya-issuer-policy/skyfire-kyapay"

	for name, tc := range map[string]struct {
		get  reply
		want string
	}{
		"404":          {reply{http.StatusNotFound, `{"msg":"Not found","msgCode":"not_found"}`}, ""},
		"no updatedAt": {reply{http.StatusOK, `{"issuerName":"skyfire-kyapay","minTrust":"KNOWN"}`}, "null"},
	} {
		srv, api := newFakeAPI(t, map[string]reply{
			"GET " + issuer: tc.get,
			"PUT " + issuer: {http.StatusOK, `{}`},
		})

		_, err := runAigw(t, srv.URL, projects, "kya-issuers", "set", "skyfire-kyapay", "--project-id", "p1", apiKey, "k")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if got := api.stamps[0]; got != tc.want {
			t.Errorf("%s: sent version %q, want %q", name, got, tc.want)
		}
	}
}

func TestReplaceSendsAnEntryPerHeldEntry(t *testing.T) {
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
		map[string]any{"issuerName": "a", "updatedAt": t1},
		map[string]any{"issuerName": "b", "updatedAt": ""},
	}
	if got := api.writes[0]["seen"]; !reflect.DeepEqual(got, want) {
		t.Errorf("seen = %v, want %v", got, want)
	}

	if api.stamps[0] != "" {
		t.Errorf("a list replace sends its versions in the body, got header %q", api.stamps[0])
	}
}

// Entries the caller gave are kept, with no read.
func TestAGivenSeenListIsKept(t *testing.T) {
	t.Parallel()

	list := projectPath + "/kya-issuer-policy"

	srv, api := newFakeAPI(t, map[string]reply{
		"PUT " + list: {http.StatusOK, `[]`},
	})

	_, err := runAigw(t, srv.URL, projects, "kya-issuers", "replace", "--project-id", "p1",
		"-d", "entries[0][issuerName]=c", "-d", "seen[0][issuerName]=a", "-d", "seen[0][updatedAt]="+t0, apiKey, "k")
	if err != nil {
		t.Fatal(err)
	}

	want := []any{map[string]any{"issuerName": "a", "updatedAt": t0}}
	if got := api.writes[0]["seen"]; !reflect.DeepEqual(got, want) || len(api.calls) != 1 {
		t.Errorf("seen = %v, calls = %v", got, api.calls)
	}
}

// A read that fails stops the write, and keeps the read's exit code.
func TestAFailedReadStopsTheWrite(t *testing.T) {
	t.Parallel()

	srv, api := newFakeAPI(t, map[string]reply{
		"GET " + projectPath: {http.StatusUnauthorized, `{"msg":"unauthorized","msgCode":"unauthorized"}`},
	})

	_, err := runAigw(t, srv.URL, projects, "update", "p1", "--name", "z", apiKey, "k")

	var exitErr *apicmd.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != apicmd.ExitNotAuthenticated {
		t.Errorf("err = %v, want exit %d", err, apicmd.ExitNotAuthenticated)
	}

	if err == nil || !strings.Contains(err.Error(), "--expected-updated-at") {
		t.Errorf("err %v does not say how to pass the version", err)
	}

	if len(api.writes) != 0 {
		t.Errorf("wrote %v after a failed read", api.writes)
	}
}

// A key that can write but not read gets a 403 on the read. The write goes out as it did
// before versions, unchecked, with one note on stderr.
func TestAWriteOnlyKeyWritesUnchecked(t *testing.T) {
	t.Parallel()

	const forbidden = `{"msg":"api key is missing a required scope","msgCode":"forbidden"}`

	list := projectPath + "/kya-issuer-policy"

	for name, tc := range map[string]struct {
		replies map[string]reply
		args    []string
	}{
		"one row": {
			map[string]reply{"GET " + projectPath: {http.StatusForbidden, forbidden}, "PATCH " + projectPath: {http.StatusOK, `{}`}},
			[]string{projects, "update", "p1", "--name", "z", apiKey, "k"},
		},
		"a list": {
			map[string]reply{"GET " + list: {http.StatusForbidden, forbidden}, "PUT " + list: {http.StatusOK, `[]`}},
			[]string{projects, "kya-issuers", "replace", "--project-id", "p1", "-d", "entries[0][issuerName]=c", apiKey, "k"},
		},
	} {
		srv, api := newFakeAPI(t, tc.replies)

		out, err := runAigw(t, srv.URL, tc.args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if len(api.writes) != 1 || api.stamps[0] != "" {
			t.Errorf("%s: writes %v, version header %q, want one unchecked write", name, api.writes, api.stamps)
		}

		if _, sent := api.writes[0]["seen"]; sent {
			t.Errorf("%s: sent a seen list it could not read", name)
		}

		if got := strings.Count(out, "was not version-checked"); got != 1 {
			t.Errorf("%s: the note appeared %d times, want once:\n%s", name, got, out)
		}
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

		for _, bad := range []string{"<expected-updated-at>", "[seen]", "seen[0]"} {
			if strings.Contains(out, bad) {
				t.Errorf("%v help mentions %q:\n%s", args, bad, out)
			}
		}

		if strings.Contains(args[1], "update") && !strings.Contains(out, "the CLI reads the current one") {
			t.Errorf("%v help does not explain --expected-updated-at:\n%s", args, out)
		}
	}
}
