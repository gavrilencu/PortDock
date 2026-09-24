package deploy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/config"
	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

func TestAzureDeploymentStatus(t *testing.T) {
	var mu sync.Mutex
	got := map[string]map[string]any{}
	az := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got[r.Method+" "+r.URL.Path] = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	defer az.Close()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, _ := secrets.LoadOrCreate("", dir)
	conn := store.AzureConnection{Name: "contoso", URL: az.URL + "/contoso", Token: box.Encrypt("pat"), CreatedAt: store.Now()}
	if _, err := store.Insert(t.Context(), db, "azure_connections", &conn); err != nil {
		t.Fatal(err)
	}
	e := New(config.Config{PublicURL: "https://portdock.example.com"}, db, box, nil, nil, nil, nil)
	app := store.Application{ID: 3, Name: "web", AzureConnectionID: conn.ID, AzureProjectID: "p1", AzureRepoID: "r1"}
	sha := "0123456789abcdef0123456789abcdef01234567"

	wait := func(n int) {
		for i := 0; i < 100; i++ {
			mu.Lock()
			l := len(got)
			mu.Unlock()
			if l >= n {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("expected %d status requests, got %v", n, got)
	}
	e.reportCommitStatus(app, store.Deployment{ID: 9, CommitSHA: sha}, "success", "Deployed successfully")
	wait(1)
	st := got["POST /contoso/p1/_apis/git/repositories/r1/commits/"+sha+"/statuses"]
	if st["state"] != "succeeded" || st["targetUrl"] != "https://portdock.example.com/applications/3/deployments/9" ||
		st["context"].(map[string]any)["genre"] != "portdock" || st["context"].(map[string]any)["name"] != "deploy" {
		t.Fatalf("commit status: %v", got)
	}

	// Previews report on the pull request too.
	e.reportCommitStatus(app, store.Deployment{ID: 10, CommitSHA: sha, PRNumber: 7}, "failure", "Build failed")
	wait(2)
	pr := got["POST /contoso/p1/_apis/git/repositories/r1/pullRequests/7/statuses"]
	if pr["state"] != "failed" || pr["context"].(map[string]any)["name"] != "preview" {
		t.Fatalf("pull request status: %v", got)
	}
}
