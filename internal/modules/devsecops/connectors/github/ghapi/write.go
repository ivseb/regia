package ghapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Le uniche chiamate che scrivono su GitHub (adozione guidata, replica/devsecops.md §3.4):
// un branch, i file di adozione e la pull request. Servono i permessi facoltativi
// «Contents» e «Pull requests» in scrittura della GitHub App; senza, la procedura offre i
// file da scaricare. Nessuna di queste chiamate si ripete da sola: una scrittura non è
// idempotente.

// WritePermissions sono i permessi in scrittura che servono all'adozione guidata.
var WritePermissions = []string{"contents", "pull_requests"}

// ErrNoWrite dice che le credenziali non hanno il permesso di scrivere.
var ErrNoWrite = errors.New("le credenziali di GitHub non permettono di scrivere nel repository: alla GitHub App servono i permessi «Contents» e «Pull requests» in scrittura")

// IsNoWrite dice se l'errore è la mancanza dei permessi di scrittura.
func IsNoWrite(err error) bool { return errors.Is(err, ErrNoWrite) }

// Pull è una pull request.
type Pull struct {
	Number    int        `json:"number"`
	State     string     `json:"state"` // open | closed
	Merged    bool       `json:"merged"`
	HTMLURL   string     `json:"html_url"`
	Title     string     `json:"title"`
	MergedAt  *time.Time `json:"merged_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	HeadRef   string     `json:"-"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// write invia una richiesta con un corpo JSON e legge la risposta in dst.
func (c *Client) write(ctx context.Context, method, path string, body, dst any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	auth, err := c.authorization(ctx, false)
	if err != nil {
		return err
	}
	if err := c.respectReserve(ctx); err != nil {
		return err
	}
	full := c.resolve(path, nil)
	req, err := http.NewRequestWithContext(ctx, method, full, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("User-Agent", "regia-connector")
	req.Header.Set("Authorization", auth)
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return networkError(err)
	}
	defer resp.Body.Close()
	c.observe(resp.Header)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if dst != nil {
			if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst); err != nil {
				return fmt.Errorf("risposta di GitHub non leggibile (%s): %v", pathOf(full), err)
			}
		}
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if wait, limited := c.rateWait(resp, b); limited {
		reset := c.o.Now().Add(wait)
		return &Error{Status: resp.StatusCode, Path: pathOf(full), RateLimited: true, Reset: reset,
			Msg: fmt.Sprintf("limite di velocità di GitHub raggiunto: riprova dopo le %s", reset.Local().Format("15:04"))}
	}
	var gh struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &gh)
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound) &&
		strings.Contains(strings.ToLower(gh.Message), "not accessible by") {
		return ErrNoWrite
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return &Error{Status: resp.StatusCode, Path: pathOf(full), Msg: "GitHub ha rifiutato la richiesta: " + gh.Message}
	}
	return apiError(resp.StatusCode, pathOf(full), b)
}

// CreateBranch crea un branch a partire da uno sha.
func (c *Client) CreateBranch(ctx context.Context, full, branch, sha string) error {
	p, err := repoPath(full)
	if err != nil {
		return err
	}
	return c.write(ctx, http.MethodPost, p+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": sha}, nil)
}

// PutFile crea o aggiorna un file su un branch con un commit. sha è quello del file
// esistente (vuoto per un file nuovo).
func (c *Client) PutFile(ctx context.Context, full, branch, file, message string, content []byte, sha string) error {
	p, err := repoPath(full)
	if err != nil {
		return err
	}
	body := map[string]any{"message": message, "branch": branch, "content": base64.StdEncoding.EncodeToString(content)}
	if sha != "" {
		body["sha"] = sha
	}
	return c.write(ctx, http.MethodPut, p+"/contents/"+escPath(file), body, nil)
}

// CreatePull apre una pull request da head verso base.
func (c *Client) CreatePull(ctx context.Context, full, title, head, base, body string) (*Pull, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var pr Pull
	if err := c.write(ctx, http.MethodPost, p+"/pulls", map[string]any{"title": title, "head": head, "base": base, "body": body}, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// GetPull legge lo stato di una pull request.
func (c *Client) GetPull(ctx context.Context, full string, number int) (*Pull, error) {
	p, err := repoPath(full)
	if err != nil {
		return nil, err
	}
	var pr Pull
	if err := c.Get(ctx, fmt.Sprintf("%s/pulls/%d", p, number), url.Values{}, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// FileOrNil legge un file; (nil, "", nil) se non esiste.
func (c *Client) FileOrNil(ctx context.Context, full, file, ref string) ([]byte, string, error) {
	b, sha, err := c.File(ctx, full, file, ref)
	if IsNotFound(err) {
		return nil, "", nil
	}
	return b, sha, err
}

// HasWrite dice se i permessi dichiarati dall'installazione (GitHub App) comprendono la
// scrittura di contenuti e pull request.
func HasWrite(perms map[string]string) bool {
	for _, p := range WritePermissions {
		if v := perms[p]; v != "write" && v != "admin" {
			return false
		}
	}
	return true
}
