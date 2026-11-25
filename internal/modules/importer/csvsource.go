package importer

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Lettura dei CSV esportati e interpretazione delle colonne.
//
// Le colonne hanno un ruolo (Role): chiave, titolo, stato, una colonna di commenti, un
// collegamento di un certo tipo, un campo personalizzato… Per Jira e YouTrack il ruolo si
// ricava dalle intestazioni documentate dei loro export (scritte qui da zero, dalla
// documentazione pubblica); per il CSV generico da nomi comuni in italiano e inglese, e
// l'utente lo corregge nella mappatura. Le colonne ripetute (Jira ripete Comment, Labels,
// Sprint, Attachment, Log Work) hanno lo stesso ruolo e si sommano.

// Ruoli delle colonne.
const (
	RoleIgnore      = "ignore"
	RoleKey         = "key"
	RoleID          = "id"
	RoleParent      = "parent"
	RoleEpicLink    = "epic_link"
	RoleSummary     = "summary"
	RoleDescription = "description"
	RoleType        = "type"
	RoleStatus      = "status"
	RoleStatusCat   = "status_category"
	RolePriority    = "priority"
	RoleResolution  = "resolution"
	RoleAssignee    = "assignee"
	RoleAssigneeID  = "assignee_id"
	RoleReporter    = "reporter"
	RoleReporterID  = "reporter_id"
	RoleCreated     = "created"
	RoleUpdated     = "updated"
	RoleResolved    = "resolved"
	RoleDue         = "due"
	RoleStart       = "start"
	RoleLabels      = "labels"
	RoleSprint      = "sprint"
	RoleComment     = "comment"
	RoleWorklog     = "worklog"
	RoleAttachment  = "attachment"
	RoleEstimate    = "estimate"         // durata come testo («1g 2h») o minuti
	RoleEstimateSec = "estimate_seconds" // Jira: Original Estimate in secondi
	RoleProject     = "project"
	RoleSubtaskOf   = "subtask_of" // YouTrack: genitore
	RoleParentFor   = "parent_for" // YouTrack: figli
	// Prefissi: link_out:<tipo>, link_in:<tipo>, custom:<nome del campo>.
	RoleLinkOut = "link_out:"
	RoleLinkIn  = "link_in:"
	RoleCustom  = "custom:"
)

// RoleNames descrive i ruoli per l'interfaccia (CSV generico).
var RoleNames = map[string]string{
	RoleIgnore: "Non importare", RoleKey: "Chiave (es. PROJ-123)", RoleID: "Id interno", RoleParent: "Genitore (chiave o id)",
	RoleEpicLink: "Epic (chiave)", RoleSummary: "Titolo", RoleDescription: "Descrizione", RoleType: "Tipo", RoleStatus: "Stato",
	RoleStatusCat: "Categoria dello stato", RolePriority: "Priorità", RoleResolution: "Risoluzione", RoleAssignee: "Assegnatario",
	RoleAssigneeID: "Id dell'assegnatario", RoleReporter: "Segnalatore", RoleReporterID: "Id del segnalatore", RoleCreated: "Creato il",
	RoleUpdated: "Aggiornato il", RoleResolved: "Risolto il", RoleDue: "Scadenza", RoleStart: "Inizio", RoleLabels: "Etichette",
	RoleSprint: "Sprint", RoleComment: "Commento", RoleWorklog: "Ore registrate", RoleAttachment: "Allegato", RoleEstimate: "Stima",
	RoleEstimateSec: "Stima in secondi", RoleProject: "Progetto", RoleSubtaskOf: "Sottotask di", RoleParentFor: "Genitore di",
}

// maxRows: oltre questo numero di righe conviene dividere l'export.
const maxRows = 200_000

// readCSV legge il file: BOM, separatore (virgola, punto e virgola o tabulazione, dalla
// prima riga), campi su più righe tra virgolette. Restituisce intestazione, righe e la riga
// del file di ogni record.
func readCSV(r io.Reader, delimiter string) ([]string, [][]string, []int, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	head, _ := br.Peek(4096)
	head = bytes.TrimPrefix(head, []byte("\ufeff"))
	if bytes.HasPrefix(head, []byte{0xff, 0xfe}) || bytes.HasPrefix(head, []byte{0xfe, 0xff}) {
		return nil, nil, nil, errors.New("il file è in UTF-16: esportalo in UTF-8")
	}
	if b, _ := br.Peek(3); bytes.Equal(b, []byte("\ufeff")) {
		_, _ = br.Discard(3)
	}
	comma := ','
	if delimiter != "" {
		comma, _ = utf8.DecodeRuneInString(delimiter)
	} else {
		first, _, _ := bytes.Cut(head, []byte("\n"))
		c, sc, tb := bytes.Count(first, []byte(",")), bytes.Count(first, []byte(";")), bytes.Count(first, []byte("\t"))
		switch {
		case tb > c && tb > sc:
			comma = '\t'
		case sc > c:
			comma = ';'
		}
	}
	cr := csv.NewReader(br)
	cr.Comma = comma
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = false
	header, err := cr.Read()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("file vuoto o non leggibile: %v", err)
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}
	var rows [][]string
	var lines []int
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return nil, nil, nil, fmt.Errorf("riga %d: %v", pe.StartLine, pe.Err)
			}
			return nil, nil, nil, err
		}
		if strings.TrimSpace(strings.Join(rec, "")) == "" {
			continue
		}
		line, _ := cr.FieldPos(0)
		rows = append(rows, rec)
		lines = append(lines, line)
		if len(rows) > maxRows {
			return nil, nil, nil, fmt.Errorf("troppe righe (massimo %d per import): dividi l'export per progetto o per periodo", maxRows)
		}
	}
	return header, rows, lines, nil
}

var (
	jiraCustomRe  = regexp.MustCompile(`(?i)^custom field \((.+)\)$`)
	jiraLinkOutRe = regexp.MustCompile(`(?i)^outward issue link \((.+)\)$`)
	jiraLinkInRe  = regexp.MustCompile(`(?i)^inward issue link \((.+)\)$`)
)

// jiraColumns: intestazioni dell'export CSV di Jira (Cloud e Data Center).
var jiraColumns = map[string]string{
	"issue key": RoleKey, "issue id": RoleID, "parent id": RoleParent, "parent": RoleParent, "parent key": RoleParent,
	"epic link": RoleEpicLink, "summary": RoleSummary, "description": RoleDescription, "issue type": RoleType,
	"status": RoleStatus, "status category": RoleStatusCat, "priority": RolePriority, "resolution": RoleResolution,
	"assignee": RoleAssignee, "assignee id": RoleAssigneeID, "reporter": RoleReporter, "reporter id": RoleReporterID,
	"created": RoleCreated, "updated": RoleUpdated, "resolved": RoleResolved, "due date": RoleDue, "start date": RoleStart,
	"labels": RoleLabels, "sprint": RoleSprint, "comment": RoleComment, "log work": RoleWorklog, "attachment": RoleAttachment,
	"original estimate": RoleEstimateSec, "project key": RoleProject,
	"component/s": RoleCustom + "Component/s", "fix version/s": RoleCustom + "Fix Version/s",
	"affects version/s": RoleCustom + "Affects Version/s", "environment": RoleCustom + "Environment",
}

// jiraCustomRoles: alcuni campi personalizzati di Jira hanno un ruolo proprio.
var jiraCustomRoles = map[string]string{
	"epic link": RoleEpicLink, "sprint": RoleSprint, "start date": RoleStart, "parent": RoleParent,
	"epic name": RoleIgnore, "rank": RoleIgnore, "epic status": RoleIgnore, "epic colour": RoleIgnore, "epic color": RoleIgnore,
	"development": RoleIgnore, "team": RoleCustom + "Team",
}

// youtrackColumns: intestazioni dell'export CSV di YouTrack (lista delle issue).
var youtrackColumns = map[string]string{
	"issue id": RoleKey, "id": RoleKey, "summary": RoleSummary, "description": RoleDescription, "project": RoleProject,
	"reporter": RoleReporter, "created": RoleCreated, "updated": RoleUpdated, "resolved": RoleResolved,
	"priority": RolePriority, "type": RoleType, "state": RoleStatus, "assignee": RoleAssignee, "tags": RoleLabels,
	"sprint": RoleSprint, "sprints": RoleSprint, "board": RoleIgnore, "estimation": RoleEstimate, "due date": RoleDue,
	"start date": RoleStart, "subtask of": RoleSubtaskOf, "parent for": RoleParentFor,
	"depends on": RoleLinkIn + "Depend", "is required for": RoleLinkOut + "Depend",
	"duplicates": RoleLinkOut + "Duplicate", "is duplicated by": RoleLinkIn + "Duplicate",
	"relates to": RoleLinkOut + "Relates", "spent time": RoleIgnore, "votes": RoleIgnore, "updater": RoleIgnore,
	"updated by": RoleIgnore, "visible to": RoleIgnore, "watchers": RoleIgnore, "number in project": RoleIgnore,
	"comments": RoleIgnore, "attachments": RoleIgnore, "resolved date": RoleResolved,
}

// genericColumns: nomi comuni (italiano e inglese) per il CSV generico.
var genericColumns = map[string]string{
	"chiave": RoleKey, "key": RoleKey, "id": RoleKey, "codice": RoleKey, "issue key": RoleKey, "issue id": RoleKey,
	"titolo": RoleSummary, "title": RoleSummary, "summary": RoleSummary, "oggetto": RoleSummary, "nome": RoleSummary,
	"descrizione": RoleDescription, "description": RoleDescription, "testo": RoleDescription,
	"tipo": RoleType, "type": RoleType, "issue type": RoleType,
	"stato": RoleStatus, "state": RoleStatus, "status": RoleStatus, "fase": RoleStatus,
	"priorità": RolePriority, "priorita": RolePriority, "priority": RolePriority,
	"assegnatario": RoleAssignee, "assignee": RoleAssignee, "responsabile": RoleAssignee,
	"segnalatore": RoleReporter, "reporter": RoleReporter, "autore": RoleReporter, "richiedente": RoleReporter,
	"creato": RoleCreated, "creato il": RoleCreated, "created": RoleCreated, "data creazione": RoleCreated, "data di creazione": RoleCreated,
	"aggiornato": RoleUpdated, "aggiornato il": RoleUpdated, "updated": RoleUpdated,
	"risolto": RoleResolved, "risolto il": RoleResolved, "resolved": RoleResolved, "chiuso il": RoleResolved,
	"scadenza": RoleDue, "due": RoleDue, "due date": RoleDue, "inizio": RoleStart, "start": RoleStart, "start date": RoleStart,
	"etichette": RoleLabels, "labels": RoleLabels, "tags": RoleLabels, "tag": RoleLabels,
	"sprint": RoleSprint, "genitore": RoleParent, "parent": RoleParent, "epic": RoleEpicLink, "epic link": RoleEpicLink,
	"stima": RoleEstimate, "estimate": RoleEstimate, "commento": RoleComment, "comment": RoleComment, "commenti": RoleComment,
	"progetto": RoleProject, "project": RoleProject,
}

// DetectRoles propone il ruolo di ogni colonna per lo strumento.
func DetectRoles(system string, header []string) []string {
	out := make([]string, len(header))
	for i, h := range header {
		k := strings.ToLower(strings.TrimSpace(h))
		var role string
		switch system {
		case "jira":
			if m := jiraCustomRe.FindStringSubmatch(h); m != nil {
				name := strings.TrimSpace(m[1])
				if r, ok := jiraCustomRoles[strings.ToLower(name)]; ok {
					role = r
				} else {
					role = RoleCustom + name
				}
			} else if m := jiraLinkOutRe.FindStringSubmatch(h); m != nil {
				role = RoleLinkOut + strings.TrimSpace(m[1])
			} else if m := jiraLinkInRe.FindStringSubmatch(h); m != nil {
				role = RoleLinkIn + strings.TrimSpace(m[1])
			} else if r, ok := jiraColumns[k]; ok {
				role = r
			} else {
				role = RoleIgnore // colonne di servizio: Project name, Votes, Watchers, Σ…
			}
		case "youtrack":
			if r, ok := youtrackColumns[k]; ok {
				role = r
			} else if h == "" {
				role = RoleIgnore
			} else {
				role = RoleCustom + h
			}
		default:
			if r, ok := genericColumns[k]; ok {
				role = r
			} else if h == "" {
				role = RoleIgnore
			} else {
				role = RoleCustom + h
			}
		}
		out[i] = role
	}
	return out
}

// ValidRole dice se un ruolo scritto dall'utente è ammesso.
func ValidRole(r string) bool {
	if _, ok := RoleNames[r]; ok {
		return true
	}
	for _, p := range []string{RoleLinkOut, RoleLinkIn, RoleCustom} {
		if strings.HasPrefix(r, p) && len(strings.TrimSpace(r[len(p):])) > 0 && len(r) <= 200 {
			return true
		}
	}
	return false
}

var keyLikeRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*)-(\d+)$`)

// interpretCSV trasforma le righe in issue secondo i ruoli delle colonne.
func interpretCSV(ds *Dataset, roles []string) ([]Issue, []string) {
	var warnings []string
	hasKey := false
	for _, r := range roles {
		hasKey = hasKey || r == RoleKey
	}
	if !hasKey {
		warnings = append(warnings, "nessuna colonna con la chiave: le righe si riconoscono dalla posizione, quindi un secondo import dello stesso file aggiorna solo se l'ordine non cambia")
	}
	multi := ds.System == "youtrack" || ds.System == "csv"
	issues := make([]Issue, 0, len(ds.Rows))
	for ri, rec := range ds.Rows {
		line := ri + 2
		if ri < len(ds.Lines) {
			line = ds.Lines[ri]
		}
		is := Issue{Line: line}
		var assigneeName, assigneeID, reporterName, reporterID string
		for ci, role := range roles {
			if ci >= len(rec) {
				break
			}
			v := strings.TrimSpace(rec[ci])
			if v == "" || role == RoleIgnore {
				continue
			}
			switch {
			case role == RoleKey:
				is.Key = v
			case role == RoleID:
				is.ID = v
			case role == RoleParent, role == RoleSubtaskOf:
				is.Parent = firstOf(v, multi)
			case role == RoleEpicLink:
				if is.Parent == "" {
					is.Parent = v
				}
			case role == RoleParentFor:
				// I figli dicono già chi è il genitore (subtask of): qui non serve altro.
			case role == RoleSummary:
				is.Summary = v
			case role == RoleDescription:
				is.Description = rec[ci]
			case role == RoleType:
				is.Type = v
			case role == RoleStatus:
				is.Status = v
			case role == RoleStatusCat:
				is.StatusCat = v
			case role == RolePriority:
				is.Priority = v
			case role == RoleResolution:
				is.Resolution = v
			case role == RoleAssignee:
				assigneeName = firstOf(v, false)
			case role == RoleAssigneeID:
				assigneeID = v
			case role == RoleReporter:
				reporterName = v
			case role == RoleReporterID:
				reporterID = v
			case role == RoleCreated:
				is.Created = v
			case role == RoleUpdated:
				is.Updated = v
			case role == RoleResolved:
				is.Resolved = v
			case role == RoleDue:
				is.Due = v
			case role == RoleStart:
				is.Start = v
			case role == RoleProject:
				is.Project = v
			case role == RoleLabels:
				is.Labels = appendUnique(is.Labels, splitLabels(v, ds.System)...)
			case role == RoleSprint:
				if multi {
					is.Sprints = appendUnique(is.Sprints, splitCell(v, ds.System)...)
				} else {
					is.Sprints = appendUnique(is.Sprints, v)
				}
			case role == RoleComment:
				if c, ok := parseCommentCell(v, ds.System); ok {
					is.Comments = append(is.Comments, c)
				}
			case role == RoleWorklog:
				if w, err := parseWorklogCell(v); err == nil {
					is.Worklogs = append(is.Worklogs, w)
				} else {
					is.Problems = append(is.Problems, fmt.Sprintf("ore registrate %q: %v", short(v), err))
				}
			case role == RoleAttachment:
				if a, ok := parseAttachmentCell(v); ok {
					is.Attachments = append(is.Attachments, a)
				}
			case role == RoleEstimate:
				if m, err := parseDurationText(v); err == nil {
					is.Estimate = &m
				} else {
					is.Problems = append(is.Problems, fmt.Sprintf("stima %q non leggibile", short(v)))
				}
			case role == RoleEstimateSec:
				if n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64); err == nil {
					m := n / 60
					is.Estimate = &m
				} else if m, err := parseDurationText(v); err == nil {
					is.Estimate = &m
				}
			case strings.HasPrefix(role, RoleLinkOut), strings.HasPrefix(role, RoleLinkIn):
				out := strings.HasPrefix(role, RoleLinkOut)
				typ := strings.TrimPrefix(strings.TrimPrefix(role, RoleLinkOut), RoleLinkIn)
				for _, t := range splitList(v) {
					is.Links = append(is.Links, Link{Type: typ, Out: out, Target: t})
				}
			case strings.HasPrefix(role, RoleCustom):
				name := strings.TrimPrefix(role, RoleCustom)
				if is.Custom == nil {
					is.Custom = map[string][]string{}
				}
				vals := []string{v}
				if multi {
					vals = splitCell(v, ds.System)
				}
				is.Custom[name] = appendUnique(is.Custom[name], vals...)
			}
		}
		if assigneeName != "" || assigneeID != "" {
			is.Assignee = userOf(assigneeID, assigneeName)
		}
		if reporterName != "" || reporterID != "" {
			is.Reporter = userOf(reporterID, reporterName)
		}
		if is.Key == "" {
			if is.ID != "" {
				is.Key = is.ID
			} else {
				is.Key = "riga-" + strconv.Itoa(line)
			}
		}
		if is.Project == "" {
			if m := keyLikeRe.FindStringSubmatch(is.Key); m != nil {
				is.Project = strings.ToUpper(m[1])
			}
		}
		issues = append(issues, is)
	}
	// Jira: gli autori dei commenti e delle ore sono id (accountId o nome utente); i nomi si
	// ricavano dalle coppie Assignee/Assignee Id e Reporter/Reporter Id dello stesso file.
	names := map[string]string{}
	for _, is := range issues {
		for _, u := range []*UserRef{is.Assignee, is.Reporter} {
			if u != nil && u.Name != "" && u.Ref != u.Name {
				names[u.Ref] = u.Name
			}
		}
	}
	for i := range issues {
		for j := range issues[i].Comments {
			fillName(issues[i].Comments[j].Author, names)
		}
		for j := range issues[i].Worklogs {
			fillName(issues[i].Worklogs[j].Author, names)
		}
		for j := range issues[i].Attachments {
			fillName(issues[i].Attachments[j].Author, names)
		}
	}
	return issues, warnings
}

func fillName(u *UserRef, names map[string]string) {
	if u != nil && (u.Name == "" || u.Name == u.Ref) {
		if n, ok := names[u.Ref]; ok {
			u.Name = n
		} else {
			u.Name = u.Ref
		}
	}
}

func userOf(id, name string) *UserRef {
	name = strings.TrimSpace(name)
	id = strings.TrimSpace(id)
	u := &UserRef{Ref: id, Name: name}
	if u.Ref == "" {
		u.Ref = name
	}
	if u.Name == "" {
		u.Name = u.Ref
	}
	if strings.Contains(u.Ref, "@") && !strings.Contains(u.Ref, " ") {
		u.Email = strings.ToLower(u.Ref)
	}
	return u
}

func firstOf(v string, multi bool) string {
	if !multi {
		return strings.TrimSpace(v)
	}
	l := splitList(v)
	if len(l) == 0 {
		return ""
	}
	return l[0]
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '|' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitCell divide i valori multipli di una cella: YouTrack li separa con la virgola; nel
// CSV generico si usa «|», perché la virgola è anche il separatore decimale italiano.
func splitCell(v, system string) []string {
	if system == "youtrack" {
		return splitList(v)
	}
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == '|' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitLabels(v, system string) []string {
	if system == "jira" {
		return strings.Fields(v) // le etichette di Jira non hanno spazi
	}
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == '|' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func appendUnique(list []string, vals ...string) []string {
	for _, v := range vals {
		dup := false
		for _, x := range list {
			if strings.EqualFold(x, v) {
				dup = true
				break
			}
		}
		if !dup && v != "" {
			list = append(list, v)
		}
	}
	return list
}

// parseCommentCell: Jira scrive «data;autore;testo» (il testo può contenere «;»). Altrove
// la cella è solo il testo.
func parseCommentCell(v, system string) (Comment, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return Comment{}, false
	}
	if system == "jira" {
		parts := strings.SplitN(v, ";", 3)
		if len(parts) == 3 {
			if _, err := parseDate(parts[0], "auto", time.UTC); err == nil {
				return Comment{Created: strings.TrimSpace(parts[0]), Author: userOf(parts[1], ""), Body: strings.TrimSpace(parts[2])}, true
			}
		}
	}
	return Comment{Body: v}, true
}

// parseWorklogCell: Jira scrive «commento;data;autore;secondi»; il commento può contenere
// «;», quindi si legge da destra.
func parseWorklogCell(v string) (Worklog, error) {
	parts := strings.Split(v, ";")
	if len(parts) < 4 {
		return Worklog{}, errors.New("formato atteso: commento;data;autore;secondi")
	}
	n := len(parts)
	secs, err := strconv.ParseFloat(strings.TrimSpace(parts[n-1]), 64)
	if err != nil || secs <= 0 {
		return Worklog{}, errors.New("durata in secondi non valida")
	}
	return Worklog{
		Comment: strings.TrimSpace(strings.Join(parts[:n-3], ";")),
		Started: strings.TrimSpace(parts[n-3]),
		Author:  userOf(parts[n-2], ""),
		Minutes: secs / 60,
	}, nil
}

// parseAttachmentCell: Jira scrive «data;autore;nome;url».
func parseAttachmentCell(v string) (Attachment, bool) {
	parts := strings.Split(v, ";")
	if len(parts) < 4 {
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			return Attachment{Name: v[strings.LastIndex(v, "/")+1:], URL: v}, true
		}
		return Attachment{}, false
	}
	n := len(parts)
	return Attachment{
		Created: strings.TrimSpace(parts[0]),
		Author:  userOf(parts[1], ""),
		Name:    strings.TrimSpace(strings.Join(parts[2:n-1], ";")),
		URL:     strings.TrimSpace(parts[n-1]),
	}, true
}

var durTokenRe = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(w|sett\w*|weeks?|d|g|gg|giorn\w*|days?|h|ore|ora|hours?|m|min\w*)`)

// parseDurationText legge «1w 2d 3h 30m», «2g 4h» o un numero di minuti. Settimana = 5
// giorni, giorno = 8 ore (come nel campo Stima).
func parseDurationText(v string) (float64, error) {
	v = strings.TrimSpace(v)
	if n, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64); err == nil {
		return n, nil
	}
	ms := durTokenRe.FindAllStringSubmatch(v, -1)
	if len(ms) == 0 {
		return 0, errors.New("durata non leggibile")
	}
	total := 0.0
	for _, m := range ms {
		n, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		u := strings.ToLower(m[2])
		switch {
		case u == "w" || strings.HasPrefix(u, "sett") || strings.HasPrefix(u, "week"):
			total += n * 5 * 8 * 60
		case u == "d" || u == "g" || u == "gg" || strings.HasPrefix(u, "giorn") || strings.HasPrefix(u, "day"):
			total += n * 8 * 60
		case u == "h" || strings.HasPrefix(u, "or") || strings.HasPrefix(u, "hour"):
			total += n * 60
		default:
			total += n
		}
	}
	return total, nil
}

func short(s string) string {
	if utf8.RuneCountInString(s) > 60 {
		return string([]rune(s)[:60]) + "…"
	}
	return s
}
