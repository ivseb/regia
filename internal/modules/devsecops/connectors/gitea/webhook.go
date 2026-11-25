package gitea

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Webhook in ingresso da Gitea (facoltativi): POST /api/v1/devsecops/gitea/webhook/{id}.
// Gitea firma il corpo con HMAC-SHA256 del segreto e manda l'esadecimale in
// X-Gitea-Signature (anche X-Hub-Signature-256, con il prefisso «sha256=»): Regia lo
// ricalcola e lo confronta in tempo costante; senza firma o con una firma errata risponde
// 401, senza segreto configurato 404. La consegna si registra (X-Gitea-Delivery, altrimenti
// X-GitHub-Delivery o Idempotency-Key, altrimenti l'impronta del corpo) per non elaborarla
// due volte. Gli eventi push (tag o branch predefinito), release e pull request unita
// avviano una sincronizzazione mirata in background; il job periodico resta il meccanismo
// principale.

// WebhookResult è la risposta da dare a Gitea.
type WebhookResult struct {
	Status  int    `json:"-"`
	Message string `json:"message"`
	Scope   string `json:"scope,omitempty"`
}

// MaxWebhookBody è il limite del corpo di un webhook.
const MaxWebhookBody = 5 << 20

// Sign calcola la firma di un corpo (esadecimale minuscolo, come X-Gitea-Signature).
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifySignature confronta in tempo costante la firma ricevuta con quella del corpo.
func VerifySignature(secret string, body []byte, header string) bool {
	header = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(header)), "sha256=")
	if secret == "" || header == "" {
		return false
	}
	got, err := hex.DecodeString(header)
	if err != nil {
		return false
	}
	want, _ := hex.DecodeString(Sign(secret, body))
	return hmac.Equal(got, want)
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
		return tx.QueryRow(ctx, "select connector_integration_tenant($1, 'gitea')::text", integrationID).Scan(&tenantID)
	}); err != nil {
		s.o.Log.Warn("webhook Gitea", "errore", err)
		return WebhookResult{Status: http.StatusServiceUnavailable, Message: "servizio temporaneamente non disponibile"}
	}
	if tenantID == nil {
		return notFound
	}
	event := h.Get("X-Gitea-Event")
	if event == "" {
		event = h.Get("X-GitHub-Event")
	}
	delivery := strings.TrimSpace(h.Get("X-Gitea-Delivery"))
	if delivery == "" {
		delivery = strings.TrimSpace(h.Get("X-GitHub-Delivery"))
	}
	if delivery == "" {
		delivery = strings.TrimSpace(h.Get("Idempotency-Key"))
	}
	if delivery == "" {
		sum := sha256.Sum256(append([]byte(event+"\x00"), body...))
		delivery = "body:" + hex.EncodeToString(sum[:16])
	}
	var res WebhookResult
	var scope *Scope
	err := s.o.DB.InTenant(ctx, *tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, sec, err := s.load(ctx, tx, integrationID)
		if err != nil {
			return err
		}
		if sec.WebhookSecret == "" {
			res = notFound
			return nil
		}
		sig := h.Get("X-Gitea-Signature")
		if sig == "" {
			sig = h.Get("X-Hub-Signature-256")
		}
		if !VerifySignature(sec.WebhookSecret, body, sig) {
			res = WebhookResult{Status: http.StatusUnauthorized, Message: "firma mancante o non valida (X-Gitea-Signature)"}
			return nil
		}
		if len(delivery) > 100 {
			res = WebhookResult{Status: http.StatusBadRequest, Message: "identificativo della consegna troppo lungo"}
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
		if _, err := tx.Exec(ctx, "delete from inbound_deliveries where integration_id = $1 and received_at < now() - interval '30 days'", integrationID); err != nil {
			return err
		}
		sc, msg, err := s.webhookScope(ctx, tx, integrationID, event, body)
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
		s.o.Log.Warn("webhook Gitea", "integrazione", integrationID, "errore", err)
		return WebhookResult{Status: http.StatusInternalServerError, Message: "errore interno"}
	}
	if scope != nil {
		if !s.isActive(ctx, *tenantID, integrationID) {
			return WebhookResult{Status: http.StatusOK, Message: "integrazione disattivata"}
		}
		if err := s.SyncScoped(ctx, *tenantID, integrationID, *scope, background); err != nil {
			s.o.Log.Warn("webhook Gitea: sincronizzazione mirata", "integrazione", integrationID, "errore", err)
		}
	}
	return res
}

func (s *Service) isActive(ctx context.Context, tenantID, id string) bool {
	var active bool
	_ = s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select active from integrations where id = $1", id).Scan(&active)
	})
	return active
}

type hookPayload struct {
	Action     string `json:"action"`
	Ref        string `json:"ref"`      // push: refs/tags/… o refs/heads/…
	RefType    string `json:"ref_type"` // create/delete: tag o branch
	Repository struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	PullRequest struct {
		Merged bool `json:"merged"`
		Base   struct {
			Ref string `json:"ref"`
		} `json:"base"`
	} `json:"pull_request"`
}

// webhookScope decide cosa sincronizzare per un evento (nil = niente).
func (s *Service) webhookScope(ctx context.Context, tx pgx.Tx, integrationID, event string, body []byte) (*Scope, string, error) {
	var p hookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, "corpo non leggibile: ignorato", nil
	}
	sc := &Scope{}
	switch event {
	case "push":
		switch {
		case strings.HasPrefix(p.Ref, "refs/tags/"):
			sc.Releases = true
		case p.Repository.DefaultBranch != "" && p.Ref == "refs/heads/"+p.Repository.DefaultBranch:
			sc.Checks = true
		default:
			return nil, "push su un branch non predefinito: ignorato", nil
		}
	case "create":
		if p.RefType != "tag" {
			return nil, "creazione di un branch: ignorata", nil
		}
		sc.Releases = true
	case "release":
		if p.Action == "deleted" {
			return nil, "release eliminata: ignorata", nil
		}
		sc.Releases = true
	case "pull_request":
		if p.Action != "closed" || !p.PullRequest.Merged {
			return nil, "pull request non unita: ignorata", nil
		}
		if p.Repository.DefaultBranch != "" && p.PullRequest.Base.Ref != p.Repository.DefaultBranch {
			return nil, "pull request verso un branch non predefinito: ignorata", nil
		}
		sc.Checks = true
	default:
		return nil, "evento " + event + " ignorato", nil
	}
	var id string
	err := tx.QueryRow(ctx, `select id from connector_repositories where integration_id = $1 and external_id = $2 and mapping_status = 'confirmed'`,
		integrationID, strconv.FormatInt(p.Repository.ID, 10)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "repository non associato: ignorato", nil
	}
	if err != nil {
		return nil, "", err
	}
	sc.RepoIDs = []string{id}
	return sc, "sincronizzazione mirata avviata", nil
}
