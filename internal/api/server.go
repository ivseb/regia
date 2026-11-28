// Package api espone l'API REST /api/v1 e serve il frontend incluso nel binario.
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"regia/internal/ai"
	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/engine/webhooks"
	"regia/internal/modules/architecture"
	"regia/internal/modules/collab"
	"regia/internal/modules/demand"
	"regia/internal/modules/devsecops"
	gtconn "regia/internal/modules/devsecops/connectors/gitea"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	glconn "regia/internal/modules/devsecops/connectors/gitlab"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/finance"
	"regia/internal/modules/governance"
	"regia/internal/modules/grc"
	"regia/internal/modules/importer"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/planning"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/privacy"
	"regia/internal/modules/problems"
	"regia/internal/modules/projects"
	"regia/internal/modules/report"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/risk"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
	"regia/internal/platform/secretbox"
)

const sessionCookie = "regia_session"

type Server struct {
	db        *db.DB
	auth      *auth.Service
	eng       *engine.Service
	approvals *approval.Service
	demand    *demand.Service
	arch      *architecture.Service
	plan      *planning.Service
	// Valutazione e priorità del portafoglio, governance (piano, passi 8 e 9).
	portfolio *portfolio.Service
	gov       *governance.Service
	events    *hub
	log       *slog.Logger
	web       fs.FS // contenuto compilato del frontend (può essere vuoto)
	version   string
	commit    string
	source    string
	dev       bool
	ttl       time.Duration
	collab    *collab.Service // commenti, allegati, osservatori (collaboration.go)
	// Accesso unico (nil = disattivato) e chiave che firma il cookie del flusso OIDC.
	oidc     *auth.OIDC
	stateKey []byte
	// Budget e costi (piano, passi 11 e 12; finance.go).
	fin *finance.Service
	// Valutazioni con questionari (piano, passo 4).
	reviews *reviews.Service
	// Work management di base e assegnazione ai team (piano, passi 10 e 18; work.go).
	work *work.Service
	// Progetti: piano del PM, modi di consegna, Panoramica (projects.go).
	proj *projects.Service
	// Avanzamento: status report, cruscotto e portale (piano, passi 13 e 14; report.go).
	report *report.Service
	swr    swrCache
	// Requisiti, verifica, deroghe e gate di rilascio (piano, passi 5 e 6; requirements.go).
	req *requirements.Service
	// Persone e accessi, MFA e webhook (admin.go, mfa.go, webhooks.go).
	adm adminState
	// DevSecOps: connettore GitHub e profili di pipeline (devsecops_github.go).
	gh *ghconn.Service
	// DevSecOps: connettore GitLab (devsecops_gitlab.go).
	gl *glconn.Service
	// DevSecOps: connettore Gitea (devsecops_gitea.go).
	gt *gtconn.Service
	// DevSecOps: API di push, pannello dell'asset, configurazione (piano, passo 21; devsecops.go).
	dso *devsecops.Service
	// Knowledge: spazi di documentazione e pagine (piano, passo 19; knowledge.go).
	kb *knowledge.Service
	// Rischi, fornitori ICT e incidenti (piano, passo 20; risk.go).
	risk *risk.Service
	// Conformità: cataloghi, controlli, applicabilità, evidenze (piano, passo 20; grc.go).
	grc      *grc.Service
	grcFiles *collab.FileStore
	// Import di progetti da Jira, YouTrack e CSV (imports.go).
	imp *importer.Service
	// Moduli spenti per tenant, con cache breve (modules.go).
	mods moduleCache
	// Dal problema all'azione (problems.go).
	prob *problems.Service
	// Vista DPO (privacy.go).
	privacy *privacy.Service
	// AI: impostazioni, assistente e strumenti (ai.go; replica/ai.md).
	ai *ai.Service
}

type Options struct {
	DB        *db.DB
	Auth      *auth.Service
	Engine    *engine.Service
	Approvals *approval.Service
	Demand    *demand.Service
	Arch      *architecture.Service
	Log       *slog.Logger
	Web       fs.FS
	Version   string
	// Commit e SourceURL: il link «Codice sorgente» punta al codice in esecuzione (AGPL-3.0 §13).
	Commit     string
	SourceURL  string
	Dev        bool
	SessionTTL time.Duration
	// TrustedProxies: da questi indirizzi si accetta X-Forwarded-For per l'IP del client.
	TrustedProxies []netip.Prefix
	// FilesDir è la cartella degli allegati (<data>/files); vuota = allegati non disponibili.
	FilesDir string
	// MaxUploadBytes è il limite di un allegato (0 = 25 MB).
	MaxUploadBytes int64
	// OIDC è l'accesso unico; nil se non configurato.
	OIDC *auth.OIDC
	// StateKey firma il cookie temporaneo del flusso OIDC; vuota = chiave casuale in memoria.
	StateKey []byte
	// Reviews sono le valutazioni (piano, passo 4); nil = il server le registra sul motore.
	Reviews *reviews.Service
	// Work è il work management (piano, passi 10 e 18); nil = il server lo registra sul motore.
	Work *work.Service
	// Report è l'avanzamento (piano, passi 13 e 14); nil = il server lo registra sul motore.
	Report *report.Service
	// Requirements sono i requisiti e le deroghe (piano, passi 5 e 6); nil = il server li
	// registra sul motore.
	Requirements *requirements.Service
	// Persone e accessi, MFA e webhook (admin.go). SecretBox è la chiave applicativa
	// (segreti dei webhook); Webhooks nil = servizio creato qui, senza worker;
	// WebhookAllowPrivate toglie la protezione SSRF; InviteMail invia i link per email
	// (nil = solo link da copiare); BaseURL serve ai link assoluti.
	SecretBox           *secretbox.Box
	Webhooks            *webhooks.Service
	WebhookAllowPrivate bool
	InviteMail          notify.Sender
	BaseURL             string
	// DevSecOps, connettore GitHub (devsecops_github.go). GitHub nil = servizio creato qui,
	// senza job periodico; DevSecOpsIngestor salva gli oggetti del contratto (nil = non si
	// salvano, l'interfaccia lo dice); ConnectorAllowPrivate è il consenso dell'operatore
	// alle reti interne (REGIA_CONNECTOR_ALLOW_PRIVATE).
	GitHub                *ghconn.Service
	DevSecOpsIngestor     contract.Ingestor
	ConnectorAllowPrivate bool
	// DevSecOps, connettore GitLab (devsecops_gitlab.go). GitLab nil = servizio creato qui,
	// senza job periodico.
	GitLab *glconn.Service
	// DevSecOps, connettore Gitea (devsecops_gitea.go). Gitea nil = servizio creato qui,
	// senza job periodico.
	Gitea *gtconn.Service
	// Devsecops è il modulo DevSecOps (piano, passo 21); nil = il server lo registra sul
	// motore, con la verifica automatica dei requisiti.
	Devsecops *devsecops.Service
	// Knowledge è il modulo dei documenti (piano, passo 19); nil = il server lo registra sul motore.
	Knowledge *knowledge.Service
	// Risk sono rischi, fornitori ICT e incidenti (piano, passo 20); nil = il server li
	// registra sul motore.
	Risk *risk.Service
	// GRC è la conformità (piano, passo 20); nil = il server la registra sul motore.
	GRC *grc.Service
	// Import di progetti (imports.go): nil = servizio creato qui; ImportAllowPrivate permette
	// ai connettori API di raggiungere indirizzi interni (Jira o YouTrack on-premise).
	Importer           *importer.Service
	ImportAllowPrivate bool
	// AI (ai.go): nil = servizio creato qui, con il fornitore finto solo se Dev.
	AI *ai.Service
}

func New(o Options) *Server {
	trustedProxies = o.TrustedProxies
	cs := newCollab(o)
	s := &Server{collab: cs, db: o.DB, auth: o.Auth, eng: o.Engine, approvals: o.Approvals, demand: o.Demand, arch: o.Arch, plan: planning.New(o.Engine), events: newHub(), log: o.Log, web: o.Web, version: o.Version, commit: o.Commit, source: o.SourceURL, dev: o.Dev, ttl: o.SessionTTL}
	s.oidc, s.stateKey = o.OIDC, o.StateKey
	// Budget e costi: l'esito delle approvazioni di budget e trasferimenti lo applica il modulo.
	if o.Approvals != nil {
		s.fin = finance.Register(o.Engine, o.Approvals)
	} else {
		s.fin = finance.New(o.Engine, nil)
	}
	// Valutazioni con questionari (piano, passo 4): fatti e azioni delle regole sul motore.
	s.reviews = o.Reviews
	if s.reviews == nil && o.Engine != nil {
		s.reviews = reviews.Register(o.Engine)
	}
	// Work management (piano, passi 10 e 18): fatti, azioni create_epic e degli sprint.
	s.work = o.Work
	if s.work == nil && o.Engine != nil {
		s.work = work.Register(o.Engine)
	}
	// Avanzamento (piano, passi 13 e 14): campi di query del cruscotto sul motore.
	s.report = o.Report
	if s.report == nil && o.Engine != nil {
		s.report = report.Register(o.Engine)
	}
	// Requisiti e deroghe (piano, passi 5 e 6): fatti, gate di rilascio, azioni delle regole,
	// requisiti dalle prescrizioni e approvazione delle deroghe.
	s.req = o.Requirements
	if s.req == nil && o.Engine != nil {
		s.req = requirements.Register(o.Engine, o.Approvals)
	}
	// DevSecOps (piano, passo 21): fatti sull'applicazione e sulla richiesta, evidenze per i requisiti.
	s.dso = o.Devsecops
	if s.dso == nil && o.Engine != nil {
		s.dso = devsecops.Register(o.Engine, s.req)
	}
	// Progetti (replica/progetti.md, passi 1 e 2): dopo il lavoro (create_epic) e la finanza (budget).
	s.projectsInit()
	// Timesheet: la settimana inviata passa dal motore delle approvazioni.
	if s.work != nil {
		s.work.UseApprovals(o.Approvals)
	}
	// Vulnerabilità governate: le eccezioni passano dal motore delle approvazioni.
	if s.dso != nil {
		s.dso.UseApprovals(o.Approvals)
	}
	if len(s.stateKey) == 0 {
		s.stateKey = make([]byte, 32)
		_, _ = rand.Read(s.stateKey)
	}
	// Persone e accessi, MFA e webhook (admin.go).
	s.adminInit(o)
	// Knowledge (piano, passo 19): documento controllato, campo di query "revisione".
	s.kb = o.Knowledge
	if s.kb == nil && o.Engine != nil {
		s.kb = knowledge.Register(o.Engine, o.Approvals)
	}
	// DevSecOps: connettore GitHub e profili di pipeline (devsecops_github.go).
	s.githubInit(o)
	// DevSecOps: connettore GitLab (devsecops_gitlab.go).
	s.gitlabInit(o)
	// DevSecOps: connettore Gitea (devsecops_gitea.go).
	s.giteaInit(o)
	// Rischi, fornitori ICT e incidenti (piano, passo 20): fatti, azioni e accettazione.
	s.risk = o.Risk
	if s.risk == nil && o.Engine != nil {
		s.risk = risk.Register(o.Engine, o.Approvals)
	}
	// Conformità (piano, passo 20; grc.go).
	s.grcInit(o)
	// DevSecOps, gate di rilascio (devsecops_gate.go): conformità per le evidenze, approvazioni per gli override.
	s.gateInit(o)
	// Import di progetti da altri strumenti (imports.go).
	s.imp = newImporter(o, s)
	// Dal problema all'azione (problems.go).
	s.prob = problems.New(o.Engine)
	// Vista DPO (ondata 3): trattamenti, DPIA, violazioni e flussi di dati.
	s.privacy = privacy.New()
	// AI (replica/ai.md, ondata 1): dopo i moduli, perché gli strumenti li usano.
	s.aiInit(o)
	return s
}

// AI è il servizio dell'AI (per il job di conservazione in serve.go).
func (s *Server) AI() *ai.Service { return s.ai }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /api/v1/health", s.health)

	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", session(s.logout))
	mux.HandleFunc("GET /api/v1/me", authed(s.me))
	mux.HandleFunc("POST /api/v1/me/tenant", session(s.switchTenant))
	mux.HandleFunc("GET /api/v1/audit", can(rbac.AuditRead, s.auditPage))

	// Accesso unico OIDC e token API personali (sso.go).
	mux.HandleFunc("GET /api/v1/auth/oidc/config", s.oidcConfig)
	mux.HandleFunc("GET /api/v1/auth/oidc/start", s.oidcStart)
	mux.HandleFunc("GET /api/v1/auth/oidc/callback", s.oidcCallback)
	mux.HandleFunc("GET /api/v1/me/account", authed(s.account))
	mux.HandleFunc("GET /api/v1/me/tokens", sessionTenant(s.listTokens))
	mux.HandleFunc("POST /api/v1/me/tokens", sessionTenant(s.createToken))
	mux.HandleFunc("DELETE /api/v1/me/tokens/{id}", sessionTenant(s.revokeToken))
	// Aspetto: tema e font per utente e default del tenant (appearance.go).
	s.appearanceRoutes(mux)
	s.languageRoutes(mux)
	s.problemRoutes(mux)
	s.moduleRoutes(mux)
	s.avvioRoutes(mux)
	s.mappaRoutes(mux)
	s.percorsoRoutes(mux)
	s.coinvolgiRoutes(mux)
	s.provaRoutes(mux)
	s.schedaRoutes(mux)
	s.percorsoRichiestaRoutes(mux)
	s.guideRoutes(mux)
	s.projectRoutes(mux)
	s.aiRoutes(mux)
	s.wikiRoutes(mux)

	mux.HandleFunc("GET /api/v1/types/{key}", inTenant(s.getType))
	mux.HandleFunc("GET /api/v1/elements", inTenant(s.search))
	mux.HandleFunc("GET /api/v1/elements/{ref}", inTenant(s.getElement))
	mux.HandleFunc("PATCH /api/v1/elements/{id}", inTenant(s.updateElement))
	mux.HandleFunc("POST /api/v1/elements/{id}/transitions", inTenant(s.transition))
	mux.HandleFunc("GET /api/v1/elements/{id}/activity", inTenant(s.activity))

	mux.HandleFunc("POST /api/v1/demand/requests", inTenant(s.createRequest))
	mux.HandleFunc("PATCH /api/v1/demand/requests/{id}", inTenant(s.updateRequest))
	mux.HandleFunc("GET /api/v1/approvals/inbox", inTenant(s.inbox))
	mux.HandleFunc("POST /api/v1/approvals/steps/{id}/decision", inTenant(s.decide))

	pa := func(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
		return can(rbac.ProcessAdmin, h)
	}
	mux.HandleFunc("GET /api/v1/processes", pa(s.listProcesses))
	mux.HandleFunc("POST /api/v1/processes", pa(s.createProcess))
	mux.HandleFunc("POST /api/v1/processes/import", pa(s.importProcess))
	mux.HandleFunc("POST /api/v1/conditions/check", inTenant(s.checkCondition))
	mux.HandleFunc("POST /api/v1/conditions/count", inTenant(s.countCondition))
	mux.HandleFunc("GET /api/v1/conditions/facts", inTenant(s.conditionFacts))
	mux.HandleFunc("GET /api/v1/processes/{ref}", pa(s.getProcess))
	mux.HandleFunc("PATCH /api/v1/processes/{ref}", pa(s.updateProcess))
	mux.HandleFunc("GET /api/v1/processes/{ref}/versions/{number}", pa(s.getProcessVersion))
	mux.HandleFunc("GET /api/v1/processes/{ref}/export", pa(s.exportProcess))
	mux.HandleFunc("PUT /api/v1/processes/{ref}/draft", pa(s.saveDraft))
	mux.HandleFunc("DELETE /api/v1/processes/{ref}/draft", pa(s.discardDraft))
	mux.HandleFunc("POST /api/v1/processes/{ref}/publish", pa(s.publishProcess))
	mux.HandleFunc("POST /api/v1/processes/{ref}/migration/preview", pa(s.previewMigration))
	mux.HandleFunc("POST /api/v1/processes/{ref}/migration", pa(s.migrateProcess))
	// Editor visuale dei processi (process_editor.go).
	mux.HandleFunc("GET /api/v1/process-catalog", pa(s.processCatalog))
	mux.HandleFunc("GET /api/v1/processes/{ref}/meta", pa(s.processMeta))
	// Automazioni: anteprima, esecuzioni delle regole a tempo, impostazioni (automations.go).
	mux.HandleFunc("POST /api/v1/rules/preview", pa(s.previewRule))
	mux.HandleFunc("GET /api/v1/processes/{ref}/rules/{rule}/runs", pa(s.ruleRuns))
	mux.HandleFunc("GET /api/v1/automation/settings", pa(s.automationSettings))
	mux.HandleFunc("PUT /api/v1/automation/settings", pa(s.setAutomationSettings))
	mux.HandleFunc("POST /api/v1/processes/{ref}/check", pa(s.checkDefinition))

	mux.HandleFunc("GET /api/v1/teams", inTenant(s.listTeams))
	mux.HandleFunc("POST /api/v1/teams", inTenant(s.createTeam))
	mux.HandleFunc("PATCH /api/v1/teams/{id}", inTenant(s.updateTeam))
	mux.HandleFunc("GET /api/v1/architecture/assets", inTenant(s.listAssets))
	mux.HandleFunc("POST /api/v1/architecture/assets", inTenant(s.createAsset))
	mux.HandleFunc("POST /api/v1/architecture/assets/{id}/components", inTenant(s.addComponent))
	mux.HandleFunc("DELETE /api/v1/architecture/assets/{id}/components/{app}", inTenant(s.removeComponent))
	mux.HandleFunc("GET /api/v1/architecture/applications", inTenant(s.listApplications))
	mux.HandleFunc("POST /api/v1/architecture/applications", inTenant(s.createApplication))
	mux.HandleFunc("POST /api/v1/architecture/applications/import", inTenant(s.importApplications))
	mux.HandleFunc("PATCH /api/v1/architecture/applications/{id}", inTenant(s.updateApplication))
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/analysis", inTenant(s.analysis))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/assets", inTenant(s.citeAsset))
	mux.HandleFunc("DELETE /api/v1/demand/requests/{ref}/assets/{asset}", inTenant(s.unciteAsset))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/impacts", inTenant(s.createImpact))
	mux.HandleFunc("PATCH /api/v1/impacts/{id}", inTenant(s.updateImpact))

	mux.HandleFunc("GET /api/v1/events", inTenant(s.streamEvents))
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/plan", inTenant(s.getPlan))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/plan/auto", inTenant(s.autoPlan))
	mux.HandleFunc("PUT /api/v1/demand/requests/{ref}/schedule", inTenant(s.shiftRequest))
	mux.HandleFunc("PUT /api/v1/impacts/{id}/schedule", inTenant(s.schedule))
	mux.HandleFunc("GET /api/v1/demand/roadmap", inTenant(s.roadmap))
	mux.HandleFunc("GET /api/v1/teams/load", inTenant(s.teamLoad))

	// Collaborazione (piano, passo 3): commenti, allegati, osservatori, notifiche.
	mux.HandleFunc("GET /api/v1/elements/{id}/comments", inTenant(s.listComments))
	mux.HandleFunc("POST /api/v1/elements/{id}/comments", inTenant(s.addComment))
	mux.HandleFunc("PATCH /api/v1/comments/{id}", inTenant(s.editComment))
	mux.HandleFunc("DELETE /api/v1/comments/{id}", inTenant(s.deleteComment))
	mux.HandleFunc("GET /api/v1/elements/{id}/attachments", inTenant(s.listAttachments))
	mux.HandleFunc("POST /api/v1/elements/{id}/attachments", inTenant(s.uploadAttachment))
	mux.HandleFunc("GET /api/v1/attachments/{id}/content", inTenant(s.downloadAttachment))
	mux.HandleFunc("DELETE /api/v1/attachments/{id}", inTenant(s.removeAttachment))
	mux.HandleFunc("GET /api/v1/elements/{id}/watch", inTenant(s.watchStatus))
	mux.HandleFunc("PUT /api/v1/elements/{id}/watch", inTenant(s.watch))
	mux.HandleFunc("DELETE /api/v1/elements/{id}/watch", inTenant(s.unwatch))
	mux.HandleFunc("GET /api/v1/notifications", inTenant(s.listNotifications))
	mux.HandleFunc("GET /api/v1/notifications/count", inTenant(s.countNotifications))
	mux.HandleFunc("POST /api/v1/notifications/{id}/read", inTenant(s.readNotification))
	mux.HandleFunc("POST /api/v1/notifications/read-all", inTenant(s.readAllNotifications))
	mux.HandleFunc("GET /api/v1/me/notification-prefs", inTenant(s.getNotificationPrefs))
	mux.HandleFunc("PUT /api/v1/me/notification-prefs", inTenant(s.setNotificationPrefs))
	// Valutazione e priorità del portafoglio (piano, passo 8).
	// Vista DPO (ondata 3): trattamenti, DPIA, violazioni e flussi di dati.
	mux.HandleFunc("GET /api/v1/privacy/home", inTenant(s.privacyHome))
	mux.HandleFunc("GET /api/v1/privacy/treatments", inTenant(s.privacyTreatments))
	mux.HandleFunc("POST /api/v1/privacy/treatments", inTenant(s.privacyCreateTreatment))
	mux.HandleFunc("GET /api/v1/privacy/treatments.csv", inTenant(s.privacyCSV))
	mux.HandleFunc("GET /api/v1/privacy/treatments/{id}", inTenant(s.privacyTreatment))
	mux.HandleFunc("PUT /api/v1/privacy/treatments/{id}", inTenant(s.privacyUpdateTreatment))
	mux.HandleFunc("DELETE /api/v1/privacy/treatments/{id}", inTenant(s.privacyDeleteTreatment))
	mux.HandleFunc("GET /api/v1/privacy/treatments/{id}/dpia", inTenant(s.privacyDPIA))
	mux.HandleFunc("PUT /api/v1/privacy/treatments/{id}/dpia", inTenant(s.privacySaveDPIA))
	mux.HandleFunc("POST /api/v1/privacy/treatments/{id}/dpia/transition", inTenant(s.privacyDPIATransition))
	mux.HandleFunc("GET /api/v1/privacy/dpias", inTenant(s.privacyDPIAs))
	mux.HandleFunc("GET /api/v1/privacy/breaches", inTenant(s.privacyBreaches))
	mux.HandleFunc("GET /api/v1/privacy/breaches/{id}", inTenant(s.privacyBreach))
	mux.HandleFunc("PUT /api/v1/privacy/breaches/{id}", inTenant(s.privacySetBreach))
	mux.HandleFunc("POST /api/v1/privacy/breaches/{id}/notification", inTenant(s.privacyNotifyBreach))
	mux.HandleFunc("GET /api/v1/privacy/flows", inTenant(s.privacyFlows))
	s.portfolio = portfolio.New(s.eng)
	mux.HandleFunc("GET /api/v1/scorecards", inTenant(s.listScorecards))
	mux.HandleFunc("GET /api/v1/scorecards/{key}", inTenant(s.getScorecard))
	mux.HandleFunc("PUT /api/v1/scorecards/{key}/draft", inTenant(s.saveScorecardDraft))
	mux.HandleFunc("DELETE /api/v1/scorecards/{key}/draft", inTenant(s.discardScorecardDraft))
	mux.HandleFunc("POST /api/v1/scorecards/{key}/publish", inTenant(s.publishScorecard))
	mux.HandleFunc("GET /api/v1/demand/requests/{ref}/evaluation", inTenant(s.getEvaluation))
	mux.HandleFunc("POST /api/v1/demand/requests/{ref}/evaluation", inTenant(s.scoreRequest))
	mux.HandleFunc("GET /api/v1/portfolio/ranking", inTenant(s.getRanking))
	mux.HandleFunc("GET /api/v1/portfolio/ranking.csv", inTenant(s.rankingCSV))
	mux.HandleFunc("PUT /api/v1/portfolio/ranking/{ref}", inTenant(s.moveInRanking))
	mux.HandleFunc("PUT /api/v1/portfolio/settings", inTenant(s.updatePortfolioSettings))
	mux.HandleFunc("GET /api/v1/portfolio/cabina", inTenant(s.getCabina))
	mux.HandleFunc("POST /api/v1/portfolio/scenarios", inTenant(s.saveScenario))
	mux.HandleFunc("DELETE /api/v1/portfolio/scenarios/{id}", inTenant(s.deleteScenario))
	mux.HandleFunc("GET /api/v1/portfolio/sedute/{id}/pacchetto", inTenant(s.getSedutaPackage))
	mux.HandleFunc("POST /api/v1/portfolio/sedute/{id}/verbale", inTenant(s.generateVerbale))

	// Governance: organi e sedute (piano, passo 9).
	s.gov = governance.New(s.eng, s.approvals)
	mux.HandleFunc("GET /api/v1/governance/bodies", inTenant(s.listBodies))
	mux.HandleFunc("POST /api/v1/governance/bodies", inTenant(s.createBody))
	mux.HandleFunc("PATCH /api/v1/governance/bodies/{id}", inTenant(s.updateBody))
	mux.HandleFunc("GET /api/v1/governance/meetings", inTenant(s.listMeetings))
	mux.HandleFunc("POST /api/v1/governance/meetings", inTenant(s.createMeeting))
	mux.HandleFunc("GET /api/v1/governance/meetings/{id}", inTenant(s.getMeeting))
	mux.HandleFunc("PATCH /api/v1/governance/meetings/{id}", inTenant(s.updateMeeting))
	mux.HandleFunc("POST /api/v1/governance/meetings/{id}/status", inTenant(s.meetingStatus))
	mux.HandleFunc("POST /api/v1/governance/meetings/{id}/items", inTenant(s.addMeetingItem))
	mux.HandleFunc("DELETE /api/v1/governance/meetings/{id}/items/{element}", inTenant(s.removeMeetingItem))
	mux.HandleFunc("POST /api/v1/governance/meetings/{id}/items/{element}/decision", inTenant(s.meetingDecision))
	mux.HandleFunc("PUT /api/v1/governance/meetings/{id}/attendance", inTenant(s.meetingAttendance))
	mux.HandleFunc("PUT /api/v1/governance/meetings/{id}/minutes", inTenant(s.saveMinutes))
	mux.HandleFunc("POST /api/v1/governance/meetings/{id}/minutes/approve", inTenant(s.approveMinutes))

	// Budget e costi (piano, passi 11 e 12): rotte in finance.go.
	s.financeRoutes(mux)
	s.budgetRoutes(mux)
	// Valutazioni con questionari (piano, passo 4): rotte in reviews.go.
	s.reviewsHandlers(mux)
	// Campi personalizzati, collegamenti e gruppi per la pagina di dettaglio (fields.go).
	s.fieldsRoutes(mux)
	s.typesRoutes(mux)
	// Status report, cruscotto di portafoglio e portale del richiedente (report.go).
	s.reportRoutes(mux)
	// Requisiti, matrice, verifica, deroghe e gate di rilascio (piano, passi 5 e 6; requirements.go).
	s.requirementsRoutes(mux)
	// Linguaggio di query: autocompletamento e campi (piano, passo 16; query.go).
	mux.HandleFunc("GET /api/v1/query/suggest", inTenant(s.querySuggest))
	mux.HandleFunc("GET /api/v1/query/fields", inTenant(s.queryFields))

	// Work management: spazi dei team, board, backlog e sprint, ore (work.go).
	s.workRoutes(mux)
	// Tempo e team: timesheet, report del tempo, comandi in blocco, membri dei team (work_tempo.go).
	s.tempoRoutes(mux)
	// Architettura enterprise: capability, dipendenze, grafo, diagrammi (architecture_ea.go).
	s.eaRoutes(mux)
	// «Da fare»: decisioni, valutazioni, verifiche, impatti, tempo e menzioni in una sola pagina (dafare.go).
	s.todoRoutes(mux)

	mux.HandleFunc("GET /api/v1/org-units", inTenant(s.listOrgUnits))
	mux.HandleFunc("POST /api/v1/org-units", inTenant(s.createOrgUnit))
	mux.HandleFunc("GET /api/v1/members", inTenant(s.listMembers))
	// Persone e accessi, verifica dell'audit, MFA, link monouso e webhook (admin.go).
	s.adminRoutes(mux)
	// Organizzazioni dall'interfaccia, solo per il superamministratore (platform.go).
	s.platformRoutes(mux)
	// Viste salvate delle liste, preferiti e vista predefinita (views.go).
	s.viewsRoutes(mux)
	// DevSecOps: integrazioni GitHub, repository, sincronizzazioni, webhook in ingresso e
	// profili di pipeline (devsecops_github.go).
	s.githubRoutes(mux)
	// DevSecOps: integrazioni GitLab, progetti, sincronizzazioni e webhook in ingresso (devsecops_gitlab.go).
	s.gitlabRoutes(mux)
	// DevSecOps: integrazioni Gitea, repository, sincronizzazioni e webhook in ingresso (devsecops_gitea.go).
	s.giteaRoutes(mux)
	// DevSecOps: push delle pipeline, pannello, panoramica e configurazione (devsecops.go).
	s.devsecopsRoutes(mux)
	s.gateRoutes(mux)
	// DevSecOps: metriche DORA delle quattro chiavi (devsecops_dora.go).
	s.doraRoutes(mux)
	// DevSecOps, vulnerabilità governate: SLA, ciclo di vita, eccezioni, cruscotto (devsecops_vuln.go).
	s.vulnRoutes(mux)
	// DevSecOps fase 3: politica centrale della pipeline, aderenza e adozione guidata (devsecops_policy.go).
	s.policyRoutes(mux)
	// Knowledge: spazi di documentazione, pagine, versioni, documenti controllati (knowledge.go).
	s.knowledgeRoutes(mux)
	// Rischi, fornitori ICT, registro delle informazioni e incidenti (risk.go).
	s.riskRoutes(mux)
	// Conformità: cataloghi, controlli, applicabilità, evidenze e cruscotto (grc.go).
	s.grcRoutes(mux)
	// Import di progetti da Jira, YouTrack e CSV (imports.go).
	s.importRoutes(mux)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, http.StatusNotFound, "route.not_found", "percorso API inesistente")
	})
	mux.Handle("/", s.spa())

	app := s.base(s.withPrincipal(s.withModules(s.notify(mux))))
	// SCIM (scim.go): token dell'organizzazione, niente sessione né withPrincipal.
	scim := s.base(s.scimHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == scimPrefix || strings.HasPrefix(r.URL.Path, scimPrefix+"/") {
			scim.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "db.unavailable", "database non raggiungibile")
		return
	}
	_, _ = w.Write([]byte("ready\n"))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.version, "commit": s.commit, "source": sourceLink(s.source, s.commit)})
}

// ---------------------------------------------------------------------
// Accesso
// ---------------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	token, err := s.auth.Login(r.Context(), in.Email, in.Password, clientIP(r), r.UserAgent())
	// Password giusta ma serve il secondo fattore (mfa.go): niente sessione, una sfida.
	if s.mfaChallengeResponse(w, r, err) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, http.StatusUnauthorized, "auth.invalid_credentials", err.Error())
		return
	case errors.Is(err, auth.ErrPasswordDisabled):
		writeProblem(w, r, http.StatusForbidden, "auth.password_disabled", err.Error())
		return
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "900")
		writeProblem(w, r, http.StatusTooManyRequests, "auth.rate_limited", err.Error())
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, Secure: !s.dev, SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.ttl.Seconds()),
	})
	p, err := s.auth.Authenticate(r.Context(), token)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := meView(p)
	s.decorateLanguage(r, p, &out)
	s.decorateMe(r.Context(), p, &out)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.auth.Logout(r.Context(), p); err != nil {
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !s.dev, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

type me struct {
	AccountID   string            `json:"account_id"`
	Email       string            `json:"email"`
	DisplayName string            `json:"display_name"`
	Tenants     []auth.Membership `json:"tenants"`
	// IsPlatformAdmin: superamministratore dell'istanza (può creare organizzazioni).
	IsPlatformAdmin bool          `json:"is_platform_admin"`
	Active          *activeTenant `json:"active_tenant"`
	// Appearance è l'aspetto effettivo (tema e font); solo in GET /me.
	Appearance *effectiveAppearance `json:"appearance,omitempty"`
	// Language è la lingua effettiva dell'interfaccia (membro, tenant, Accept-Language).
	Language *languageView `json:"language,omitempty"`
}

type activeTenant struct {
	TenantID    string   `json:"tenant_id"`
	MemberID    string   `json:"member_id"`
	MemberType  string   `json:"member_type"`
	Permissions []string `json:"permissions"`
	// Modules: chiave del modulo → acceso (tutti accesi se il tenant non ha scelto).
	Modules map[string]bool `json:"modules"`
	// MenuProfile: «auditor» per il revisore (menu ridotto ai moduli di controllo), vuoto altrimenti.
	MenuProfile string `json:"menu_profile"`
}

func meView(p *auth.Principal) me {
	out := me{AccountID: p.AccountID, Email: p.Email, DisplayName: p.DisplayName, Tenants: p.Tenants, IsPlatformAdmin: p.IsPlatformAdmin}
	if out.Tenants == nil {
		out.Tenants = []auth.Membership{}
	}
	if p.TenantID != "" {
		out.Active = &activeTenant{TenantID: p.TenantID, MemberID: p.MemberID, MemberType: p.MemberType, Permissions: p.Permissions}
	}
	return out
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	out := meView(p)
	if a, err := s.appearanceFor(r.Context(), p); err != nil {
		s.log.Warn("aspetto non disponibile", "errore", err, "request_id", requestID(r.Context()))
	} else {
		out.Appearance = a
	}
	s.decorateLanguage(r, p, &out)
	s.decorateMe(r.Context(), p, &out)
	writeJSON(w, http.StatusOK, out)
}

// decorateLanguage aggiunge a /me la lingua effettiva; se manca, il client usa quella del browser.
func (s *Server) decorateLanguage(r *http.Request, p *auth.Principal, out *me) {
	if l, err := s.languageFor(r, p); err != nil {
		s.log.Warn("lingua non disponibile", "errore", err, "request_id", requestID(r.Context()))
	} else {
		out.Language = &l
	}
}

func (s *Server) switchTenant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		TenantID string `json:"tenant_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	err := s.auth.SwitchTenant(r.Context(), p, in.TenantID)
	if errors.Is(err, auth.ErrNotMember) {
		writeProblem(w, r, http.StatusForbidden, "tenant.not_member", err.Error())
		return
	}
	if errors.Is(err, auth.ErrTenantSuspended) {
		writeProblem(w, r, http.StatusForbidden, "tenant.suspended", err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := meView(p)
	s.decorateLanguage(r, p, &out)
	s.decorateMe(r.Context(), p, &out)
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------

type auditEntry struct {
	Seq            int64          `json:"seq"`
	At             time.Time      `json:"at"`
	ActorAccountID *string        `json:"actor_account_id"`
	ActorMemberID  *string        `json:"actor_member_id"`
	Action         string         `json:"action"`
	EntityType     string         `json:"entity_type"`
	EntityID       *string        `json:"entity_id"`
	Payload        map[string]any `json:"payload"`
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var before *int64
	if v := r.URL.Query().Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "before deve essere un numero")
			return
		}
		before = &n
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "limit tra 1 e 500")
			return
		}
		limit = n
	}
	// Filtri del Registro di audit (admin.go): azione (anche per prefisso, es. "member"),
	// tipo ed id dell'entità, membro che ha agito, intervallo di date.
	f, ok := auditFilters(w, r)
	if !ok {
		return
	}
	var items []auditEntry
	var actors map[string]string
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), "select * from audit_search($1, $2, $3, $4, $5, $6, $7, $8)",
			before, limit, f.action, f.entityType, f.entity, f.actor, f.from, f.to)
		if err != nil {
			return err
		}
		if items, err = pgx.CollectRows(rows, pgx.RowToStructByPos[auditEntry]); err != nil {
			return err
		}
		actors, err = auditActors(r.Context(), tx, items)
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if items == nil {
		items = []auditEntry{}
	}
	resp := map[string]any{"items": items, "actors": actors}
	if len(items) == limit {
		resp["next_before"] = items[len(items)-1].Seq
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	// Un identificativo malformato nel percorso o nel corpo (es. una chiave DEM-4 dove
	// serve l'id) arriva fin qui come errore di sintassi di Postgres: è un errore del
	// client, non del server.
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "22P02" {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_id", "identificativo non valido")
		return
	}
	// Il client ha chiuso la connessione (pagina cambiata, richiesta annullata): niente
	// da rispondere e niente da segnalare come errore.
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		s.log.Debug("richiesta annullata dal client", "request_id", requestID(r.Context()), "percorso", r.URL.Path)
		return
	}
	s.log.Error("errore interno", "errore", err, "request_id", requestID(r.Context()), "percorso", r.URL.Path)
	writeProblem(w, r, http.StatusInternalServerError, "internal", "errore interno")
}

// sourceLink è l'indirizzo del codice in esecuzione: il commit sul repository quando è noto
// (un hash esadecimale), altrimenti il repository. Vuoto se manca il repository.
func sourceLink(repo, commit string) string {
	if repo == "" {
		return ""
	}
	if len(commit) >= 7 && len(commit) <= 40 && strings.Trim(strings.ToLower(commit), "0123456789abcdef") == "" {
		return repo + "/tree/" + commit
	}
	return repo
}
