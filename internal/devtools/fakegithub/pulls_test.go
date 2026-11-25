package fakegithub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
)

// Le scritture del finto GitHub, provate con lo stesso client che usa Regia: senza i
// permessi della App 403 chiaro, con i permessi branch, file con sha, pull request, unione.
func TestFakeWrites(t *testing.T) {
	s := New()
	Demo(s, time.Now())
	s.PATs["pat"] = true
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ctx := context.Background()
	app, err := ghapi.New(ghapi.Options{BaseURL: srv.URL, HTTP: srv.Client(),
		Auth: ghapi.Auth{Mode: "app", AppID: s.AppID, InstallationID: s.InstallationID, PrivateKeyPEM: s.PrivateKeyPEM()}})
	if err != nil {
		t.Fatal(err)
	}
	const repo = "esempio/strumenti-interni"
	head, err := app.BranchHead(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}

	// La App ha solo i permessi in lettura: nessuna scrittura.
	if err := app.CreateBranch(ctx, repo, "regia/x", head); !ghapi.IsNoWrite(err) {
		t.Fatalf("creazione del branch senza permessi: %v", err)
	}
	inst, err := app.Installation(ctx)
	if err != nil || ghapi.HasWrite(inst.Permissions) {
		t.Fatalf("permessi dichiarati: %v %v", inst, err)
	}

	s.GrantWrite()
	if inst, _ = app.Installation(ctx); !ghapi.HasWrite(inst.Permissions) {
		t.Fatalf("permessi dopo la concessione: %v", inst.Permissions)
	}
	if err := app.CreateBranch(ctx, repo, "regia/x", head); err != nil {
		t.Fatal(err)
	}
	if err := app.CreateBranch(ctx, repo, "regia/x", head); err == nil {
		t.Fatal("un branch esistente non si ricrea")
	}
	if err := app.PutFile(ctx, repo, "regia/x", ".github/pipeline.yml", "aggiunge", []byte("version: 1\n"), ""); err != nil {
		t.Fatal(err)
	}
	// Un file esistente si aggiorna solo con il suo sha.
	if err := app.PutFile(ctx, repo, "regia/x", ".github/pipeline.yml", "aggiorna", []byte("version: 2\n"), ""); err == nil {
		t.Fatal("aggiornamento senza sha")
	}
	_, sha, err := app.File(ctx, repo, ".github/pipeline.yml", "regia/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.PutFile(ctx, repo, "regia/x", ".github/pipeline.yml", "aggiorna", []byte("version: 2\n"), sha); err != nil {
		t.Fatal(err)
	}
	// Il branch principale non è stato toccato.
	if b, _, err := app.FileOrNil(ctx, repo, ".github/pipeline.yml", "main"); err != nil || b != nil {
		t.Fatalf("main: %q %v", b, err)
	}

	pr, err := app.CreatePull(ctx, repo, "Allinea la pipeline", "regia/x", "main", "corpo")
	if err != nil || pr.Number != 1 || pr.State != "open" {
		t.Fatalf("pull request: %+v %v", pr, err)
	}
	if _, err := app.CreatePull(ctx, repo, "Doppia", "non-esiste", "main", ""); err == nil {
		t.Fatal("pull request da un branch inesistente")
	}
	s.MergePull(repo, 1)
	got, err := app.GetPull(ctx, repo, 1)
	if err != nil || !got.Merged || got.State != "closed" || got.MergedAt == nil {
		t.Fatalf("dopo l'unione: %+v %v", got, err)
	}
	if b, _, _ := app.FileOrNil(ctx, repo, ".github/pipeline.yml", "main"); string(b) != "version: 2\n" {
		t.Fatalf("main dopo l'unione: %q", b)
	}
	if _, err := app.GetPull(ctx, repo, 9); !ghapi.IsNotFound(err) {
		t.Fatalf("pull request inesistente: %v", err)
	}

	// Un token personale scrive senza i permessi della App, e lo dice il codice di stato.
	pat, _ := ghapi.New(ghapi.Options{BaseURL: srv.URL, HTTP: srv.Client(), Auth: ghapi.Auth{Mode: "token", Token: "pat"}})
	s.mu.Lock()
	s.Permissions["contents"] = "read"
	s.mu.Unlock()
	if err := pat.CreateBranch(ctx, repo, "regia/y", s.repos[repo].Branches["main"]); err != nil {
		t.Fatal(err)
	}
	if resp, _ := http.Get(srv.URL + "/repos/" + repo + "/pulls/1"); resp != nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("senza credenziali: %d", resp.StatusCode)
		}
	}
	s.ClosePull(repo, 1) // nessun effetto su una pull request già unita
	if p := s.Pulls(repo)[0]; !p.Merged {
		t.Fatalf("pull request unita: %+v", p)
	}
}
