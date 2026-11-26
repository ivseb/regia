package commitsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/contract"
)

type fakeSource struct {
	commits []ghapi.Commit
	pulls   []ghapi.PullRequest
	since   time.Time
	err     error
}

func (f *fakeSource) Commits(_ context.Context, since time.Time, each func(ghapi.Commit) (bool, error)) error {
	f.since = since
	if f.err != nil {
		return f.err
	}
	for _, c := range f.commits {
		if c.When().Before(since) {
			continue
		}
		if stop, err := each(c); err != nil || stop {
			return err
		}
	}
	return nil
}

func (f *fakeSource) ClosedPulls(_ context.Context, each func(ghapi.PullRequest) (bool, error)) error {
	for _, p := range f.pulls {
		if stop, err := each(p); err != nil || stop {
			return err
		}
	}
	return nil
}

func commit(sha, msg string, at time.Time) ghapi.Commit {
	var c ghapi.Commit
	c.SHA, c.Commit.Message = sha, msg
	c.Commit.Committer.Date, c.Commit.Author.Date = at, at
	c.Commit.Author.Name = "Ana Rossi"
	return c
}

func TestReadCursorAndKeys(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc := contract.Service{Name: "ordini"}
	merged := now.Add(-48 * time.Hour)
	src := &fakeSource{
		commits: []ghapi.Commit{ // dal più recente
			commit("c3", "HL-12 e HL-13 insieme\n\ncorpo", now.Add(-1*time.Hour)),
			commit("c2", "senza chiave", now.Add(-24*time.Hour)),
			commit("c1", "HL-12 primo passo", now.Add(-72*time.Hour)),
			commit("c0", "HL-1 troppo vecchio", now.Add(-100*24*time.Hour)),
		},
		pulls: []ghapi.PullRequest{
			{Number: 9, Title: "HL-14 unita", MergedAt: &merged, MergeCommitSHA: "m9", UpdatedAt: merged},
			{Number: 8, Title: "HL-15 chiusa e basta", UpdatedAt: now.Add(-60 * time.Hour)},
			{Number: 7, Title: "HL-16 troppo vecchia", MergedAt: &merged, MergeCommitSHA: "m7", UpdatedAt: now.Add(-100 * 24 * time.Hour)},
		},
	}
	res, err := Read(context.Background(), src, svc, Cursor{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	// Prima lettura: da 90 giorni fa, c0 escluso; c2 senza chiave non si consegna.
	if got := src.since; !got.Equal(now.Add(-Lookback)) {
		t.Errorf("since alla prima lettura = %v", got)
	}
	var shas []string
	for _, c := range res.Commits {
		shas = append(shas, c.SHA)
	}
	if len(shas) != 3 || shas[0] != "c3" || shas[1] != "c1" || shas[2] != "m9" {
		t.Fatalf("commit consegnati: %v", shas)
	}
	if pr := res.Commits[2]; pr.Source != "pull_request" || pr.PRNumber != 9 || pr.Author != "" {
		t.Errorf("PR unita: %+v", pr)
	}
	if res.Commits[0].Author != "Ana Rossi" || res.Commits[0].Source != "commit" {
		t.Errorf("commit: %+v", res.Commits[0])
	}
	if res.Read != 3 || !res.Next.At.Equal(now.Add(-time.Hour)) || res.Next.SHA != "c3" || !res.Next.PRsAt.Equal(merged) {
		t.Fatalf("cursore: %+v (letti %d)", res.Next, res.Read)
	}

	// Seconda lettura dal cursore: since = data dell'ultimo commit, il cursore non arretra.
	src.commits = src.commits[:1]
	res2, err := Read(context.Background(), src, svc, res.Next, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if !src.since.Equal(res.Next.At) {
		t.Errorf("since dal cursore = %v", src.since)
	}
	if res2.Next != res.Next {
		t.Errorf("il cursore doveva restare: %+v", res2.Next)
	}

	// Full ignora il cursore.
	if _, err := Read(context.Background(), src, svc, res.Next, true, now); err != nil || !src.since.Equal(now.Add(-Lookback)) {
		t.Errorf("Full: since = %v, %v", src.since, err)
	}

	// Un errore non fa avanzare niente.
	src.err = errors.New("500")
	res3, err := Read(context.Background(), src, svc, Cursor{}, false, now)
	if err == nil || res3.Next != (Cursor{}) || len(res3.Commits) != 0 {
		t.Errorf("errore: %+v, %v", res3, err)
	}
}

func TestReadTruncated(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{}
	for i := 0; i < MaxCommits+5; i++ {
		src.commits = append(src.commits, commit("s", "HL-1", now.Add(-time.Duration(i)*time.Minute)))
	}
	res, err := Read(context.Background(), src, contract.Service{}, Cursor{}, false, now)
	if err != nil || res.Read != MaxCommits || len(res.Warnings) != 1 {
		t.Fatalf("tetto: letti %d, avvisi %v, %v", res.Read, res.Warnings, err)
	}
}
