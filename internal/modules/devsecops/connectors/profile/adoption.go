package profile

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Adozione guidata (replica/devsecops.md §3.4, fase 3): la parte del profilo che dice a
// Regia quali template di pipeline proporre a un repository e come richiamarli. Regia
// genera due file, entrambi generici:
//
//   - un workflow GitHub Actions che richiama i workflow riusabili configurati qui (le righe
//     «uses:» arrivano dalla configurazione del tenant, mai dal codice di Regia);
//   - un file di configurazione dei controlli (off, warn, block) derivato dalla politica per
//     la classe dell'applicazione.
//
// Il contenuto dei template aziendali non è mai conosciuto né copiato da Regia: qui stanno
// solo i riferimenti e i parametri.

// Percorsi predefiniti dei file generati.
const (
	DefaultWorkflowPath = ".github/workflows/regia-pipeline.yml"
	DefaultConfigPath   = ".github/pipeline.yml"
	DefaultConfigKey    = "checks"
)

// AdoptionSpec è la configurazione dell'adozione.
type AdoptionSpec struct {
	// WorkflowPath e ConfigPath sono i file generati nel repository.
	WorkflowPath string `json:"workflow_path,omitempty"`
	ConfigPath   string `json:"config_path,omitempty"`
	// ConfigKey è la chiave del file di configurazione sotto cui stanno i controlli.
	ConfigKey string `json:"config_key,omitempty"`
	// WorkflowName è il nome del workflow (default «Pipeline»).
	WorkflowName string `json:"workflow_name,omitempty"`
	// Branches sono i branch che avviano il workflow (default main).
	Branches  []string           `json:"branches,omitempty"`
	Params    []AdoptionParam    `json:"params,omitempty"`
	Templates []AdoptionTemplate `json:"templates"`
}

// AdoptionParam è un parametro scelto nella procedura guidata e usabile nei valori dei
// template come {chiave}.
type AdoptionParam struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Default string   `json:"default,omitempty"`
	Options []string `json:"options,omitempty"`
}

// AdoptionTemplate è un workflow riusabile richiamabile dal workflow generato.
type AdoptionTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Uses è il riferimento del workflow riusabile: org/repo/.github/workflows/file.yml@ref,
	// oppure ./.github/workflows/file.yml per uno dello stesso repository.
	Uses string `json:"uses"`
	// With sono i parametri passati al template; i valori accettano {application}, {service},
	// {repository}, {default_branch} e i parametri dell'adozione.
	With           map[string]string `json:"with,omitempty"`
	InheritSecrets bool              `json:"inherit_secrets,omitempty"`
	Needs          []string          `json:"needs,omitempty"`
	// Default: il template è preselezionato nella procedura.
	Default bool `json:"default,omitempty"`
	// Controls sono i controlli della politica che il template esegue (informativo: la
	// procedura avvisa se la classe ne richiede uno che nessun template scelto copre).
	Controls []string `json:"controls,omitempty"`
	// Stack: gli stack a cui il template è adatto (informativo, per il filtro).
	Stack []string `json:"stack,omitempty"`
}

var (
	adoptKey   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	adoptParam = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	usesRe     = regexp.MustCompile(`^(\./\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml|[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/\.github/workflows/[A-Za-z0-9_./-]+\.ya?ml@[A-Za-z0-9_./-]+)$`)
	withKeyRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
	pathRe     = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_./-]{0,199}$`)
	branchRe   = regexp.MustCompile(`^[A-Za-z0-9_./*-]{1,100}$`)
)

func (a *AdoptionSpec) workflowPath() string {
	if a.WorkflowPath != "" {
		return a.WorkflowPath
	}
	return DefaultWorkflowPath
}

func (a *AdoptionSpec) configPath() string {
	if a.ConfigPath != "" {
		return a.ConfigPath
	}
	return DefaultConfigPath
}

func (a *AdoptionSpec) configKey() string {
	if a.ConfigKey != "" {
		return a.ConfigKey
	}
	return DefaultConfigKey
}

// Paths restituisce i percorsi effettivi dei due file.
func (a *AdoptionSpec) Paths() (workflow, config string) { return a.workflowPath(), a.configPath() }

func okPath(p string) bool {
	return pathRe.MatchString(p) && !strings.Contains(p, "..") && !strings.HasSuffix(p, "/") && !strings.Contains(p, "//")
}

func (a *AdoptionSpec) validate(add func(path, msg string)) {
	if a == nil {
		return
	}
	if a.WorkflowPath != "" {
		if !okPath(a.WorkflowPath) || !strings.HasPrefix(a.WorkflowPath, ".github/workflows/") || !(strings.HasSuffix(a.WorkflowPath, ".yml") || strings.HasSuffix(a.WorkflowPath, ".yaml")) {
			add("adoption.workflow_path", "deve stare in .github/workflows/ e finire con .yml o .yaml")
		}
	}
	if a.ConfigPath != "" && !okPath(a.ConfigPath) {
		add("adoption.config_path", "percorso relativo alla radice del repository, senza ..")
	}
	if a.workflowPath() == a.configPath() {
		add("adoption.config_path", "il file di configurazione deve essere diverso dal workflow")
	}
	if a.ConfigKey != "" && !adoptParam.MatchString(a.ConfigKey) {
		add("adoption.config_key", "lettere minuscole, cifre e «_»")
	}
	if len(a.WorkflowName) > 80 || strings.ContainsAny(a.WorkflowName, "\n\r") {
		add("adoption.workflow_name", "al massimo 80 caratteri, su una riga")
	}
	if len(a.Branches) > 10 {
		add("adoption.branches", "al massimo 10 branch")
	}
	for i, b := range a.Branches {
		if !branchRe.MatchString(b) {
			add(fmt.Sprintf("adoption.branches[%d]", i), "nome del branch non valido")
		}
	}
	params := map[string]bool{"application": true, "service": true, "repository": true, "default_branch": true}
	for i, p := range a.Params {
		w := fmt.Sprintf("adoption.params[%d]", i)
		switch {
		case !adoptParam.MatchString(p.Key):
			add(w+".key", "lettere minuscole, cifre e «_», cominciando da una lettera")
		case params[p.Key]:
			add(w+".key", "il nome «"+p.Key+"» è già usato (anche tra quelli riservati: application, service, repository, default_branch)")
		}
		params[p.Key] = true
		if strings.TrimSpace(p.Label) == "" || len(p.Label) > 80 {
			add(w+".label", "serve un'etichetta di al massimo 80 caratteri")
		}
		if len(p.Options) > 30 {
			add(w+".options", "al massimo 30 opzioni")
		}
		if len(p.Options) > 0 && p.Default != "" && !contains(p.Options, p.Default) {
			add(w+".default", "il valore predefinito non è tra le opzioni")
		}
	}
	if len(a.Templates) > 30 {
		add("adoption.templates", "al massimo 30 template")
	}
	keys := map[string]bool{}
	for _, t := range a.Templates {
		keys[t.Key] = true
	}
	seen := map[string]bool{}
	for i, t := range a.Templates {
		w := fmt.Sprintf("adoption.templates[%d]", i)
		switch {
		case !adoptKey.MatchString(t.Key):
			add(w+".key", "lettere minuscole, cifre, «-» e «_», cominciando da una lettera")
		case seen[t.Key]:
			add(w+".key", "la chiave «"+t.Key+"» è ripetuta")
		}
		seen[t.Key] = true
		if strings.TrimSpace(t.Name) == "" || len(t.Name) > 100 {
			add(w+".name", "serve un nome di al massimo 100 caratteri")
		}
		if !usesRe.MatchString(t.Uses) {
			add(w+".uses", "serve un workflow riusabile: organizzazione/repository/.github/workflows/file.yml@versione")
		}
		for k, v := range t.With {
			if !withKeyRe.MatchString(k) {
				add(w+".with."+k, "nome del parametro non valido")
			}
			if len(v) > 500 || strings.ContainsAny(v, "\n\r") {
				add(w+".with."+k, "al massimo 500 caratteri, su una riga")
			}
		}
		for _, n := range t.Needs {
			if !keys[n] || n == t.Key {
				add(w+".needs", "«"+n+"» non è un altro template del profilo")
			}
		}
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// ControlMode è la modalità richiesta di un controllo, per la classe dell'applicazione.
type ControlMode struct {
	Key  string
	Name string
	Mode string // off | warn | block
}

// RenderInput è ciò che serve a generare i file.
type RenderInput struct {
	Application   string // chiave o nome dell'applicazione
	Service       string
	Repository    string // org/nome
	DefaultBranch string
	Class         string // nome della classe di applicazione
	PolicyVersion int
	Controls      []ControlMode
	// Templates sono le chiavi dei template scelti; vuoto = quelli predefiniti.
	Templates []string
	// Params sono i valori dei parametri; assenti valgono i predefiniti.
	Params map[string]string
}

// File è un file da proporre al repository.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SelectTemplates restituisce i template scelti (o quelli predefiniti), con quelli da cui
// dipendono.
func (a *AdoptionSpec) SelectTemplates(keys []string) ([]AdoptionTemplate, error) {
	by := map[string]AdoptionTemplate{}
	for _, t := range a.Templates {
		by[t.Key] = t
	}
	want := map[string]bool{}
	if len(keys) == 0 {
		for _, t := range a.Templates {
			if t.Default {
				want[t.Key] = true
			}
		}
	}
	for _, k := range keys {
		if _, ok := by[k]; !ok {
			return nil, fmt.Errorf("template sconosciuto: %s", k)
		}
		want[k] = true
	}
	for changed := true; changed; {
		changed = false
		for k := range want {
			for _, n := range by[k].Needs {
				if !want[n] {
					want[n], changed = true, true
				}
			}
		}
	}
	var out []AdoptionTemplate
	for _, t := range a.Templates { // l'ordine è quello del profilo
		if want[t.Key] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("scegli almeno un template")
	}
	return out, nil
}

// ParamValues completa i valori dei parametri con i predefiniti e controlla le opzioni.
func (a *AdoptionSpec) ParamValues(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range a.Params {
		v := strings.TrimSpace(in[p.Key])
		if v == "" {
			v = p.Default
		}
		if len(p.Options) > 0 && v != "" && !contains(p.Options, v) {
			return nil, fmt.Errorf("%s: valore non ammesso", p.Label)
		}
		if strings.ContainsAny(v, "\n\r") || len(v) > 200 {
			return nil, fmt.Errorf("%s: valore non valido", p.Label)
		}
		out[p.Key] = v
	}
	return out, nil
}

// quote scrive una stringa YAML tra virgolette (la sintassi JSON è YAML valido).
func quote(s string) string { return strconv.Quote(s) }

var placeholder = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

func substitute(s string, vars map[string]string) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// Render genera il workflow e il file di configurazione.
func (a *AdoptionSpec) Render(in RenderInput) ([]File, error) {
	tpls, err := a.SelectTemplates(in.Templates)
	if err != nil {
		return nil, err
	}
	params, err := a.ParamValues(in.Params)
	if err != nil {
		return nil, err
	}
	vars := map[string]string{"application": in.Application, "service": in.Service, "repository": in.Repository, "default_branch": in.DefaultBranch}
	for k, v := range params {
		vars[k] = v
	}
	branches := a.Branches
	if len(branches) == 0 {
		b := in.DefaultBranch
		if b == "" {
			b = "main"
		}
		branches = []string{b}
	}
	name := a.WorkflowName
	if name == "" {
		name = "Pipeline"
	}
	var w strings.Builder
	w.WriteString("# Generato da Regia. Non contiene la logica della pipeline: richiama i workflow\n")
	w.WriteString("# riusabili dell'organizzazione. Quali controlli sono obbligatori, e in che modalità,\n")
	fmt.Fprintf(&w, "# lo dice %s.\n", a.configPath())
	fmt.Fprintf(&w, "name: %s\n\n", quote(name))
	w.WriteString("on:\n  push:\n    branches: [")
	for i, b := range branches {
		if i > 0 {
			w.WriteString(", ")
		}
		w.WriteString(quote(b))
	}
	w.WriteString("]\n  pull_request:\n  workflow_dispatch:\n\n")
	w.WriteString("permissions:\n  contents: read\n\n")
	w.WriteString("jobs:\n")
	for i, t := range tpls {
		if i > 0 {
			w.WriteString("\n")
		}
		fmt.Fprintf(&w, "  %s:\n", t.Key)
		if len(t.Needs) > 0 {
			fmt.Fprintf(&w, "    needs: [%s]\n", strings.Join(t.Needs, ", "))
		}
		fmt.Fprintf(&w, "    uses: %s\n", t.Uses)
		if len(t.With) > 0 {
			w.WriteString("    with:\n")
			keys := make([]string, 0, len(t.With))
			for k := range t.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&w, "      %s: %s\n", k, quote(substitute(t.With[k], vars)))
			}
		}
		if t.InheritSecrets {
			w.WriteString("    secrets: inherit\n")
		}
	}

	var c strings.Builder
	c.WriteString("# Generato da Regia dalla politica centrale della pipeline.\n")
	if in.Class != "" {
		fmt.Fprintf(&c, "# Classe dell'applicazione: %s (versione %d della politica).\n", in.Class, in.PolicyVersion)
	}
	c.WriteString("# Modalità: off (spento), warn (avviso), block (blocca la pipeline se fallisce).\n")
	c.WriteString("version: 1\n")
	fmt.Fprintf(&c, "%s:\n", a.configKey())
	if len(in.Controls) == 0 {
		c.WriteString("  {}\n")
	}
	for _, m := range in.Controls {
		mode := m.Mode
		if mode == "" {
			mode = "off"
		}
		fmt.Fprintf(&c, "  %s: %s\n", m.Key, mode)
	}
	return []File{{Path: a.workflowPath(), Content: w.String()}, {Path: a.configPath(), Content: c.String()}}, nil
}

// Uncovered elenca i controlli richiesti che nessuno dei template scelti dichiara di eseguire
// (solo per i template che dichiarano i loro controlli).
func (a *AdoptionSpec) Uncovered(tpls []AdoptionTemplate, required []string) []string {
	declared := false
	cover := map[string]bool{}
	for _, t := range tpls {
		if len(t.Controls) > 0 {
			declared = true
		}
		for _, c := range t.Controls {
			cover[c] = true
		}
	}
	if !declared {
		return nil
	}
	var out []string
	for _, r := range required {
		if !cover[r] {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Differenze tra due versioni di un file

// DiffLine è una riga di una differenza: Op vale " ", "+" o "-".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Diff confronta due testi riga per riga (sottosequenza comune più lunga).
func Diff(oldText, newText string) (lines []DiffLine, added, removed int) {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	}
	a, b := split(oldText), split(newText)
	if len(a)*len(b) > 4_000_000 {
		for _, l := range a {
			lines = append(lines, DiffLine{"-", l})
		}
		for _, l := range b {
			lines = append(lines, DiffLine{"+", l})
		}
		return lines, len(b), len(a)
	}
	// lcs[i][j]: lunghezza della sottosequenza comune di a[i:] e b[j:].
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			lines = append(lines, DiffLine{" ", a[i]})
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			lines = append(lines, DiffLine{"+", b[j]})
			j++
			added++
		default:
			lines = append(lines, DiffLine{"-", a[i]})
			i++
			removed++
		}
	}
	return lines, added, removed
}
