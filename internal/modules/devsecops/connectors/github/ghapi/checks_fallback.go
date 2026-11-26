package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// IsForbidden dice se l'errore è un 403 che non dipende dal limite di velocità: di solito un
// permesso che il token non ha (per esempio «Checks», che i token fine-grained non offrono).
func IsForbidden(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusForbidden && !e.RateLimited
}

// WorkflowRuns elenca gli esiti delle GitHub Actions di un commit, nella forma dei check run.
// Serve quando il token non legge i check run: basta il permesso «Actions» in lettura.
func (c *Client) WorkflowRuns(ctx context.Context, full, sha string) ([]CheckRun, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var out []CheckRun
	err = c.paginate(ctx, p+"/actions/runs", url.Values{"head_sha": {sha}, "per_page": {"50"}}, "workflow_runs", 2, func(raw json.RawMessage) (bool, error) {
		var w struct {
			ID           int64     `json:"id"`
			Name         string    `json:"name"`
			DisplayTitle string    `json:"display_title"`
			HeadSHA      string    `json:"head_sha"`
			Status       string    `json:"status"`
			Conclusion   string    `json:"conclusion"`
			HTMLURL      string    `json:"html_url"`
			UpdatedAt    time.Time `json:"updated_at"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return true, err
		}
		r := CheckRun{ID: w.ID, Name: w.Name, HeadSHA: w.HeadSHA, Status: w.Status, Conclusion: w.Conclusion, HTMLURL: w.HTMLURL}
		r.Output.Title = w.DisplayTitle
		if w.Status == "completed" && !w.UpdatedAt.IsZero() {
			at := w.UpdatedAt
			r.CompletedAt = &at
		}
		out = append(out, r)
		return false, nil
	})
	return out, err
}

// CommitStatuses legge gli stati di un commit (la vecchia API «statuses»), nella forma dei
// check run. È l'ultimo ripiego: basta il permesso «Commit statuses» in lettura.
func (c *Client) CommitStatuses(ctx context.Context, full, sha string) ([]CheckRun, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var st struct {
		Statuses []struct {
			ID          int64     `json:"id"`
			Context     string    `json:"context"`
			State       string    `json:"state"` // pending | success | failure | error
			TargetURL   string    `json:"target_url"`
			Description string    `json:"description"`
			UpdatedAt   time.Time `json:"updated_at"`
		} `json:"statuses"`
	}
	if err := c.Get(ctx, p+"/commits/"+esc(sha)+"/status", nil, &st); err != nil {
		return nil, err
	}
	out := make([]CheckRun, 0, len(st.Statuses))
	for _, s := range st.Statuses {
		r := CheckRun{ID: s.ID, Name: s.Context, HeadSHA: sha, HTMLURL: s.TargetURL, Status: "completed"}
		r.Output.Title = s.Description
		switch s.State {
		case "success":
			r.Conclusion = "success"
		case "failure", "error":
			r.Conclusion = "failure"
		default:
			r.Status, r.Conclusion = "in_progress", ""
		}
		if r.Status == "completed" && !s.UpdatedAt.IsZero() {
			at := s.UpdatedAt
			r.CompletedAt = &at
		}
		out = append(out, r)
	}
	return out, nil
}

// ChecksWithFallback legge i check run del commit; se il token non ha il permesso (403),
// ripiega sugli esiti delle Actions e poi sugli stati del commit. via dice da dove vengono:
// "checks", "actions" o "statuses".
func (c *Client) ChecksWithFallback(ctx context.Context, full, sha string) (runs []CheckRun, via string, err error) {
	runs, err = c.CheckRuns(ctx, full, sha)
	if err == nil || !IsForbidden(err) {
		return runs, "checks", err
	}
	first := err
	if runs, err = c.WorkflowRuns(ctx, full, sha); err == nil && len(runs) > 0 {
		return runs, "actions", nil
	}
	if st, serr := c.CommitStatuses(ctx, full, sha); serr == nil {
		if len(st) > 0 || err != nil {
			return st, "statuses", nil
		}
	}
	if err == nil {
		// Actions leggibili ma senza esiti per questo commit: nessun controllo, non un errore.
		return runs, "actions", nil
	}
	return nil, "", first
}
