package cmd_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"gitlab.com/loginid/software/loginid-aigw-cli.git/internal/apicmd"
)

// fakeAPI stands in for the real API, so no test reaches a real host, not
// even login's key check. It rejects any key containing "rejected". It records
// each request's Authorization header.
type fakeAPI struct {
	mu   sync.Mutex
	hits []string
}

func (f *fakeAPI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")

		f.mu.Lock()
		f.hits = append(f.hits, auth)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(auth, "rejected") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"msgCode":"unauthorized","msg":"invalid api key"}`))

			return
		}

		_, _ = w.Write([]byte(`[]`))
	}
}

// take returns the requests since the last call.
func (f *fakeAPI) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	hits := f.hits
	f.hits = nil

	return hits
}

var fake = &fakeAPI{}

// TestMain swaps in go-keyring's in-memory store, so no test in this package
// can reach the real OS keyring, even through a future bug, and points the
// API at fake.
func TestMain(m *testing.M) {
	keyring.MockInit()

	// Never closed: it dies with the process.
	apicmd.APIBase = httptest.NewServer(fake.handler()).URL

	os.Exit(m.Run())
}
