package gitlab

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Webhook in ingresso da GitLab (facoltativi): POST /api/v1/devsecops/gitlab/webhook/{id}.
// GitLab non firma il corpo: manda il segreto così com'è in X-Gitlab-Token, e Regia lo
// confronta in tempo costante; senza token o con un token errato risponde 401. La consegna
// si registra (X-Gitlab-Event-UUID, altrimenti Idempotency-Key, altrimenti l'impronta del
// corpo) per non elaborarla due volte. Gli eventi pipeline conclusa, deployment, release e
// tag push avviano una sincronizzazione mirata in background; il job periodico resta il
// meccanismo principale.

// WebhookResult è la risposta da dare a GitLab.
type WebhookResult struct {
	Status  int    `json:"-"`
	Message string `json:"message"`
	Scope   string `json:"scope,omitempty"`
}

// MaxWebhookBody è il limite del corpo di un webhook.
const MaxWebhookBody = 5 << 20

// VerifyToken confronta in tempo costante il token ricevuto con il segreto.
func VerifyToken(secret, header string) bool {
	header = strings.TrimSpace(header)
	if secret == "" || header == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(secret)), sha256.Sum256([]byte(header))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
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
		return tx.QueryRow(ctx, "select connector_integration_tenant($1, 'gitlab')::text", integrationID).Scan(&tenantID)
	}); err != nil {
		s.o.Log.Warn("webhook GitLab", "errore", err)
		return WebhookResult{Status: http.StatusServiceUnavailable, Message: "servizio temporaneamente non disponibile"}
	}
	if tenantID == nil {
		return notFound
	}
	event := h.Get("X-Gitlab-Event")
	delivery := strings.TrimSpace(h.Get("X-Gitlab-Event-UUID"))
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
		if !VerifyToken(sec.WebhookSecret, h.Get("X-Gitlab-Token")) {
			res = WebhookResult{Status: http.StatusUnauthorized, Message: "token mancante o non valido (X-Gitlab-Token)"}
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
		s.o.Log.Warn("webhook GitLab", "integrazione", integrationID, "errore", err)
		return WebhookResult{Status: http.StatusInternalServerError, Message: "errore interno"}
	}
	if scope != nil {
		if !s.isActive(ctx, *tenantID, integrationID) {
			return WebhookResult{Status: http.StatusOK, Message: "integrazione disattivata"}
		}
		if err := s.SyncScoped(ctx, *tenantID, integrationID, *scope, background); err != nil {
			s.o.Log.Warn("webhook GitLab: sincronizzazione mirata", "integrazione", integrationID, "errore", err)
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
	ObjectKind string `json:"object_kind"`
	Status     string `json:"status"` // deployment
	Ref        string `json:"ref"`    // tag push: refs/tags/…
	ProjectID  int64  `json:"project_id"`
	Project    struct {
		ID int64 `json:"id"`
	} `json:"project"`
	Attributes struct {
		Status string `json:"status"`
		Tag    bool   `json:"tag"`
	} `json:"object_attributes"`
}

func (p hookPayload) project() int64 {
	if p.Project.ID != 0 {
		return p.Project.ID
	}
	return p.ProjectID
}

// webhookScope decide cosa sincronizzare per un evento (nil = niente).
func (s *Service) webhookScope(ctx context.Context, tx pgx.Tx, integrationID, event string, body []byte) (*Scope, string, error) {
	var p hookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, "corpo non leggibile: ignorato", nil
	}
	kind := p.ObjectKind
	if kind == "" {
		switch event {
		case "Pipeline Hook":
			kind = "pipeline"
		case "Deployment Hook":
			kind = "deployment"
		case "Release Hook":
			kind = "release"
		case "Tag Push Hook":
			kind = "tag_push"
		}
	}
	switch kind {
	case "pipeline", "deployment", "release", "tag_push":
	default:
		return nil, "evento " + event + " ignorato", nil
	}
	if kind == "pipeline" && !isFinishedStatus(p.Attributes.Status) {
		return nil, "pipeline non ancora conclusa: ignorata", nil
	}
	if kind == "tag_push" && !strings.HasPrefix(p.Ref, "refs/tags/") {
		return nil, "push senza tag: ignorato", nil
	}
	var id string
	err := tx.QueryRow(ctx, `select id from connector_repositories where integration_id = $1 and external_id = $2 and mapping_status = 'confirmed'`,
		integrationID, strconv.FormatInt(p.project(), 10)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "progetto non associato: ignorato", nil
	}
	if err != nil {
		return nil, "", err
	}
	sc := &Scope{ProjectIDs: []string{id}}
	switch kind {
	case "pipeline":
		sc.Checks = true
		if p.Attributes.Tag {
			sc.Releases = true // gli artefatti della pipeline del tag completano la Release
		}
	case "deployment":
		sc.Deployments = true
	default:
		sc.Releases = true
	}
	return sc, "sincronizzazione mirata avviata", nil
}

func isFinishedStatus(s string) bool {
	switch s {
	case "success", "failed", "canceled", "skipped":
		return true
	}
	return false
}
