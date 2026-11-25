package importer

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"

	"github.com/jackc/pgx/v5"
)

// Dataset sono i dati letti dall'origine, prima della mappatura. Ha due forme:
//   - un CSV (Header e Rows così come sono nel file): l'interpretazione delle colonne
//     dipende dallo strumento e, per il CSV generico, dalla mappatura delle colonne;
//   - le issue già lette da un'API (Issues, Sprints).
//
// Si salva compresso in import_datasets e si rilegge a ogni passo (analisi, anteprima,
// esecuzione): il file si carica una volta sola.
type Dataset struct {
	System   string     `json:"system"` // jira | youtrack | csv
	Format   string     `json:"format"` // csv | api
	Instance string     `json:"instance,omitempty"`
	Header   []string   `json:"header,omitempty"`
	Rows     [][]string `json:"rows,omitempty"`
	// Lines: riga del file di ogni record (i campi su più righe la spostano).
	Lines   []int        `json:"lines,omitempty"`
	Issues  []Issue      `json:"issues,omitempty"`
	Sprints []SprintInfo `json:"sprints,omitempty"`
	// Users: nomi ed email noti dall'API, per chiave (accountId, login...).
	Users map[string]UserRef `json:"users,omitempty"`
	// canDownload: gli allegati dell'API si possono scaricare (c'è l'archivio dei file).
	canDownload bool
}

// Issue è un elemento d'origine, nella forma comune a tutti gli strumenti. Le date restano
// testo: il formato si decide (o si sceglie) dopo, sui valori di tutto il file.
type Issue struct {
	Line        int      `json:"line,omitempty"` // riga del CSV (0 per l'API)
	Key         string   `json:"key"`
	ID          string   `json:"id,omitempty"` // id interno dello strumento (Jira: Issue id)
	Project     string   `json:"project,omitempty"`
	Type        string   `json:"type,omitempty"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status,omitempty"`
	StatusCat   string   `json:"status_category,omitempty"` // To Do | In Progress | Done
	Priority    string   `json:"priority,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
	Assignee    *UserRef `json:"assignee,omitempty"`
	Reporter    *UserRef `json:"reporter,omitempty"`
	Created     string   `json:"created,omitempty"`
	Updated     string   `json:"updated,omitempty"`
	Resolved    string   `json:"resolved,omitempty"`
	Due         string   `json:"due,omitempty"`
	Start       string   `json:"start,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Sprints     []string `json:"sprints,omitempty"`
	// Parent: chiave o id del genitore (sottotask, Parent, Epic Link, subtask of).
	Parent string `json:"parent,omitempty"`
	// Estimate e Spent in minuti (stima originale, tempo registrato in totale).
	Estimate    *float64            `json:"estimate,omitempty"`
	Links       []Link              `json:"links,omitempty"`
	Comments    []Comment           `json:"comments,omitempty"`
	Worklogs    []Worklog           `json:"worklogs,omitempty"`
	Attachments []Attachment        `json:"attachments,omitempty"`
	Custom      map[string][]string `json:"custom,omitempty"`
	// Problems: valori che non si sono potuti leggere (diventano errori della riga).
	Problems []string `json:"problems,omitempty"`
}

// UserRef è una persona dell'origine. Ref è la chiave stabile (accountId, login, nome).
type UserRef struct {
	Ref   string `json:"ref"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Link è un collegamento con un'altra issue. Out = questa issue è la sorgente del verso
// "in avanti" del tipo (Jira: outward, YouTrack: OUTWARD; es. A blocks B).
type Link struct {
	Type   string `json:"type"`
	Out    bool   `json:"out"`
	Target string `json:"target"`
}

type Comment struct {
	ID      string   `json:"id,omitempty"`
	Author  *UserRef `json:"author,omitempty"`
	Created string   `json:"created,omitempty"`
	Body    string   `json:"body"`
}

type Worklog struct {
	ID      string   `json:"id,omitempty"`
	Author  *UserRef `json:"author,omitempty"`
	Started string   `json:"started,omitempty"`
	Minutes float64  `json:"minutes"`
	Comment string   `json:"comment,omitempty"`
}

type Attachment struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name"`
	URL     string   `json:"url,omitempty"`
	Author  *UserRef `json:"author,omitempty"`
	Created string   `json:"created,omitempty"`
	Size    int64    `json:"size,omitempty"`
}

// SprintInfo è uno sprint letto dall'API, con le date e lo stato.
type SprintInfo struct {
	Name  string `json:"name"`
	State string `json:"state,omitempty"` // future | active | closed
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	Goal  string `json:"goal,omitempty"`
}

func saveDataset(ctx context.Context, tx pgx.Tx, runID string, ds *Dataset) error {
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err := json.NewEncoder(gz).Encode(ds); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `insert into import_datasets (tenant_id, run_id, content, size_bytes) values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, run_id) do update set content = excluded.content, size_bytes = excluded.size_bytes`,
		runID, buf.Bytes(), buf.Len())
	return err
}

func loadDataset(ctx context.Context, tx pgx.Tx, runID string) (*Dataset, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, "select content from import_datasets where run_id = $1", runID).Scan(&raw); err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	ds := &Dataset{}
	if err := json.NewDecoder(io.Reader(gz)).Decode(ds); err != nil {
		return nil, err
	}
	return ds, nil
}
