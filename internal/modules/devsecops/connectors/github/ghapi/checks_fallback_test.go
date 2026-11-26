package ghapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// Pilota «Home lab», difetto 4: con un token fine-grained i check run rispondono 403 e i
// controlli si leggono dalle Actions, poi dagli stati del commit.
func TestChecksWithFallback(t *testing.T) {
	actions := true
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/ivan/priv/commits/abc/check-runs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	})
	mux.HandleFunc("GET /repos/ivan/priv/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		if !actions {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
			return
		}
		if r.URL.Query().Get("head_sha") != "abc" {
			t.Errorf("head_sha = %q", r.URL.Query().Get("head_sha"))
		}
		_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[{"id":7,"name":"CI","head_sha":"abc","status":"completed","conclusion":"success","html_url":"https://x/7","updated_at":"2026-11-26T10:00:00Z"}]}`))
	})
	mux.HandleFunc("GET /repos/ivan/priv/commits/abc/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"state":"failure","statuses":[{"id":9,"context":"ci/build","state":"error","target_url":"https://x/9","updated_at":"2026-11-26T10:00:00Z"}]}`))
	})
	hs := httptest.NewServer(mux)
	defer hs.Close()
	hc, _ := ghapi.NewHTTPClient(ghapi.Any, "", 5*time.Second)
	c, err := ghapi.New(ghapi.Options{BaseURL: hs.URL, HTTP: hc, Auth: ghapi.Auth{Mode: "token", Token: "github_pat_x"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.CheckRuns(ctx, "ivan/priv", "abc"); !ghapi.IsForbidden(err) {
		t.Fatalf("CheckRuns: atteso 403, avuto %v", err)
	}
	runs, via, err := c.ChecksWithFallback(ctx, "ivan/priv", "abc")
	if err != nil || via != "actions" || len(runs) != 1 || runs[0].Name != "CI" || runs[0].Conclusion != "success" || runs[0].CompletedAt == nil {
		t.Fatalf("Actions: via=%s runs=%+v err=%v", via, runs, err)
	}
	actions = false
	runs, via, err = c.ChecksWithFallback(ctx, "ivan/priv", "abc")
	if err != nil || via != "statuses" || len(runs) != 1 || runs[0].Name != "ci/build" || runs[0].Conclusion != "failure" {
		t.Fatalf("stati: via=%s runs=%+v err=%v", via, runs, err)
	}
}
