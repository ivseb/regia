package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Webhook in ingresso da GitHub (facoltativi): POST /api/v1/devsecops/github/webhook/{id}.
// La firma HMAC-SHA256 (X-Hub-Signature-256) con il segreto dell'integrazione è
// obbligatoria; la consegna si registra (X-GitHub-Delivery) per non elaborarla due volte.
// Gli eventi release, deployment_status, check_suite (e push sul repository GitOps)
// avviano una sincronizzazione mirata in background; il job periodico resta il meccanismo
// principale.

// WebhookResult è la risposta da dare a GitHub.
type WebhookResult struct {
	Status  int    `json:"-"`
	Message string `json:"message"`
	Scope   string `json:"scope,omitempty"`
}

// MaxWebhookBody è il limite del corpo di un webhook (GitHub ne invia fino a 25 MB, ma a
// Regia servono pochi campi: i corpi più grandi si rifiutano).
const MaxWebhookBody = 5 << 20

// VerifySignature confronta in tempo costante la firma "sha256=<hex>".
func VerifySignature(secret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok || secret == "" {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// Sign calcola la firma di un corpo (test e strumenti di sviluppo).
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// HandleWebhook verifica e smista un webhook. background: la sincronizzazione mirata parte
// in un goroutine (in produzione); falso nei test, per un esito deterministico.
func (s *Service) HandleWebhook(ctx context.Context, integrationID string, h http.Header, body []byte, background bool) WebhookResult {
	notFound := WebhookResult{Status: http.StatusNotFound, Message: "integrazione inesistente o senza webhook"}
	if !validID(integrationID) {
		return notFound
	}
	var tenantID *string
	if err := s.o.DB.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select connector_integration_tenant($1, 'github')::text", integrationID).Scan(&tenantID)
	}); err != nil {
		s.o.Log.Warn("webhook GitHub", "errore", err)
		return WebhookResult{Status: http.StatusServiceUnavailable, Message: "servizio temporaneamente non disponibile"}
	}
	if tenantID == nil {
		return notFound
	}
	event := h.Get("X-GitHub-Event")
	delivery := strings.TrimSpace(h.Get("X-GitHub-Delivery"))
	var res WebhookResult
	var scope *Scope
	err := s.o.DB.InTenant(ctx, *tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		it, sec, err := s.load(ctx, tx, integrationID)
		if err != nil {
			return err
		}
		if sec.WebhookSecret == "" {
			res = notFound
			return nil
		}
		if !VerifySignature(sec.WebhookSecret, body, h.Get("X-Hub-Signature-256")) {
			res = WebhookResult{Status: http.StatusUnauthorized, Message: "firma mancante o non valida (X-Hub-Signature-256)"}
			return nil
		}
		if delivery == "" || len(delivery) > 100 {
			res = WebhookResult{Status: http.StatusBadRequest, Message: "manca X-GitHub-Delivery"}
			return nil
		}
		tag, err := tx.Exec(ctx, `insert into inbound_deliveries (tenant_id, integration_id, delivery_id) values (app_tenant(), $1, $2)
			on conflict do nothing`, integrationID, delivery)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			res = WebhookResult{Status: http.StatusOK, Message: "consegna già ricevuta"}
			return nil
		}
		// Le consegne registrate servono solo a riconoscere i doppioni recenti.
		if _, err := tx.Exec(ctx, "delete from inbound_deliveries where integration_id = $1 and received_at < now() - interval '30 days'", integrationID); err != nil {
			return err
		}
		sc, msg, err := s.webhookScope(ctx, tx, it, event, body)
		if err != nil {
			return err
		}
		res = WebhookResult{Status: http.StatusAccepted, Message: msg}
		if sc != nil {
			scope = sc
			res.Scope = sc.label()
		} else {
			res.Status = http.StatusOK
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return notFound
	}
	if err != nil {
		s.o.Log.Warn("webhook GitHub", "integrazione", integrationID, "errore", err)
		return WebhookResult{Status: http.StatusInternalServerError, Message: "errore interno"}
	}
	if scope != nil {
		if !s.isActive(ctx, *tenantID, integrationID) {
			return WebhookResult{Status: http.StatusOK, Message: "integrazione disattivata"}
		}
		if err := s.SyncScoped(ctx, *tenantID, integrationID, *scope, background); err != nil {
			s.o.Log.Warn("webhook GitHub: sincronizzazione mirata", "integrazione", integrationID, "errore", err)
		}
	}
	return res
}

// isActive dice se l'integrazione è attiva (letto fuori dalla transazione del webhook).
func (s *Service) isActive(ctx context.Context, tenantID, id string) bool {
	var active bool
	_ = s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select active from integrations where id = $1", id).Scan(&active)
	})
	return active
}

type hookPayload struct {
	Action     string `json:"action"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Ref string `json:"ref"`
}

// webhookScope decide cosa sincronizzare per un evento (nil = niente).
func (s *Service) webhookScope(ctx context.Context, tx pgx.Tx, itg *Integration, event string, body []byte) (*Scope, string, error) {
	switch event {
	case "ping":
		return nil, "pong: webhook configurato", nil
	case "pull_request":
		msg, err := s.adoptionEvent(ctx, tx, itg, body)
		return nil, msg, err
	case "release", "deployment_status", "check_suite", "push":
	default:
		return nil, "evento " + event + " ignorato", nil
	}
	var p hookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, "corpo non leggibile: ignorato", nil
	}
	if event == "push" {
		repos := itg.Config.GitOpsRepositories
		if len(repos) == 0 && itg.Config.ProfileID != "" {
			d, _, err := definitionFor(ctx, tx, itg.Config.ProfileID)
			if err != nil {
				return nil, "", err
			}
			if d != nil && d.GitOps != nil {
				repos = d.GitOps.Repositories
			}
		}
		if slices.ContainsFunc(repos, func(r string) bool { return strings.EqualFold(r, p.Repository.FullName) }) {
			return &Scope{GitOps: true}, "sincronizzazione del repository GitOps avviata", nil
		}
		return nil, "push su un repository che non è GitOps: ignorato", nil
	}
	if event == "check_suite" && p.Action != "completed" {
		return nil, "check_suite non ancora completata: ignorata", nil
	}
	var repoID string
	err := tx.QueryRow(ctx, `select id from connector_repositories where integration_id = $1 and external_id = $2 and mapping_status = 'confirmed'`,
		itg.ID, strconv.FormatInt(p.Repository.ID, 10)).Scan(&repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "repository non associato: ignorato", nil
	}
	if err != nil {
		return nil, "", err
	}
	sc := &Scope{RepoIDs: []string{repoID}}
	switch event {
	case "release":
		sc.Releases = true
	case "deployment_status":
		sc.Deployments = true
	case "check_suite":
		sc.Checks = true
	}
	return sc, "sincronizzazione mirata avviata", nil
}
