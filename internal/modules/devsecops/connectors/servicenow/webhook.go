package servicenow

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Webhook in ingresso da ServiceNow (facoltativo): POST /api/v1/devsecops/servicenow/webhook/{id}.
// Lo manda una business rule (o un flow con un passo REST) quando un incidente o una change
// cambia. Il corpo è JSON e dice solo quale tabella è cambiata:
//
//	{"table": "incident", "sys_id": "…", "number": "INC0010001"}
//
// Regia non si fida del contenuto: avvia una sincronizzazione mirata di quella tabella, che
// rilegge da ServiceNow con le sue credenziali. La firma è HMAC-SHA256 del corpo con il
// segreto, in X-Regia-Signature: esadecimale (anche con il prefisso «sha256=») oppure
// base64, che è ciò che restituisce GlideCertificateEncryption.generateMac in ServiceNow.
// Senza firma, o con una firma errata, 401; senza segreto configurato, 404. Ogni consegna
// vale una volta: X-Regia-Delivery, altrimenti Idempotency-Key, altrimenti l'impronta del
// corpo.

// WebhookResult è la risposta da dare a ServiceNow.
type WebhookResult struct {
	Status  int    `json:"-"`
	Message string `json:"message"`
	Scope   string `json:"scope,omitempty"`
}

// MaxWebhookBody è il limite del corpo di un webhook.
const MaxWebhookBody = 64 << 10

// Sign calcola la firma di un corpo (esadecimale minuscolo).
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifySignature confronta in tempo costante la firma ricevuta (esadecimale o base64) con
// quella del corpo.
func VerifySignature(secret string, body []byte, header string) bool {
	header = strings.TrimSpace(header)
	if l := strings.ToLower(header); strings.HasPrefix(l, "sha256=") {
		header = header[len("sha256="):]
	}
	if secret == "" || header == "" {
		return false
	}
	want, _ := hex.DecodeString(Sign(secret, body))
	if got, err := hex.DecodeString(header); err == nil && len(got) == len(want) {
		return hmac.Equal(got, want)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if got, err := enc.DecodeString(header); err == nil && len(got) == len(want) {
			return hmac.Equal(got, want)
		}
	}
	return false
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
		return tx.QueryRow(ctx, "select connector_integration_tenant($1, 'servicenow')::text", integrationID).Scan(&tenantID)
	}); err != nil {
		s.o.Log.Warn("webhook ServiceNow", "errore", err)
		return WebhookResult{Status: http.StatusServiceUnavailable, Message: "servizio temporaneamente non disponibile"}
	}
	if tenantID == nil {
		return notFound
	}
	delivery := strings.TrimSpace(h.Get("X-Regia-Delivery"))
	if delivery == "" {
		delivery = strings.TrimSpace(h.Get("Idempotency-Key"))
	}
	if delivery == "" {
		sum := sha256.Sum256(body)
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
		if !VerifySignature(sec.WebhookSecret, body, h.Get("X-Regia-Signature")) {
			res = WebhookResult{Status: http.StatusUnauthorized, Message: "firma mancante o non valida (X-Regia-Signature)"}
			return nil
		}
		if len(delivery) > 100 {
			res = WebhookResult{Status: http.StatusBadRequest, Message: "identificativo della consegna troppo lungo"}
			return nil
		}
		var ev struct {
			Table string `json:"table"`
		}
		if err := json.Unmarshal(body, &ev); err != nil {
			res = WebhookResult{Status: http.StatusBadRequest, Message: "corpo non valido: serve JSON con il campo table"}
			return nil
		}
		var sc Scope
		switch strings.TrimSpace(ev.Table) {
		case "incident":
			sc.Incidents = true
		case "change_request":
			sc.Changes = true
		default:
			res = WebhookResult{Status: http.StatusOK, Message: "tabella ignorata: Regia legge solo incident e change_request"}
			return nil
		}
		tag, err := tx.Exec(ctx, `insert into inbound_deliveries (tenant_id, integration_id, delivery_id) values (app_tenant(), $1, $2)
			on conflict do nothing`, integrationID, delivery)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			res = WebhookResult{Status: http.StatusOK, Message: "consegna già ricevuta: ignorata"}
			return nil
		}
		scope = &sc
		res = WebhookResult{Status: http.StatusAccepted, Message: "sincronizzazione mirata avviata", Scope: sc.label()}
		return nil
	})
	if err == ErrNotFound {
		return notFound
	}
	if err != nil {
		s.o.Log.Warn("webhook ServiceNow", "errore", err)
		return WebhookResult{Status: http.StatusServiceUnavailable, Message: "servizio temporaneamente non disponibile"}
	}
	if scope != nil {
		if err := s.SyncScoped(ctx, *tenantID, integrationID, *scope, background); err != nil {
			s.o.Log.Warn("webhook ServiceNow: sincronizzazione", "errore", err)
		}
	}
	return res
}
