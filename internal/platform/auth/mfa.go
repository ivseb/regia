package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/qr"
	"regia/internal/platform/secretbox"
)

// Secondo fattore locale con TOTP (RFC 6238), solo per gli account con password: chi entra
// con l'SSO fa l'MFA presso l'IdP.
//
//   - Attivazione dal profilo: segreto nuovo in accounts.mfa_pending_enc, QR generato qui,
//     conferma con un codice valido; solo allora diventa il segreto attivo
//     (mfa_secret_enc) e nascono 10 codici di recupero monouso, mostrati una volta.
//   - Segreti cifrati con la chiave applicativa (secretbox), legati all'id dell'account.
//   - Login in due tempi: dopo la password non c'è sessione ma una "sfida" (cookie
//     HttpOnly di 5 minuti, al massimo 5 tentativi); il codice TOTP o un codice di
//     recupero la trasforma in sessione. Un codice già usato (stesso intervallo) non vale.
//   - Limitatore: 5 codici sbagliati per account in 15 minuti, oltre si risponde 429.
//   - Politica del tenant (tenant_security.mfa_policy): se l'account deve avere l'MFA e
//     non l'ha, la sfida è di tipo "setup" e il login passa dall'attivazione.

const (
	mfaChallengeTTL      = 5 * time.Minute
	mfaChallengeAttempts = 5
	mfaMaxAttempts       = 5
	recoveryCodes        = 10
	recoveryAlphabet     = "abcdefghjkmnpqrstuvwxyz23456789" // niente 0/o, 1/l/i
)

var (
	ErrMFAChallenge   = errors.New("la verifica è scaduta o non è valida: accedi di nuovo")
	ErrMFACode        = errors.New("codice non valido")
	ErrMFAUnavailable = errors.New("l'autenticazione a due fattori locale vale solo per gli account con password: con l'accesso unico il secondo fattore lo chiede il provider di identità")
	ErrMFAPolicy      = errors.New("l'autenticazione a due fattori è obbligatoria per la politica della tua organizzazione")
	ErrMFANotPending  = errors.New("nessuna attivazione in corso: ricomincia")
	ErrMFAAlready     = errors.New("l'autenticazione a due fattori è già attiva")
	ErrMFANotEnabled  = errors.New("l'autenticazione a due fattori non è attiva")
)

// MFARequiredError: password corretta, manca il secondo fattore. Challenge va nel cookie
// della sfida; Setup dice che prima bisogna attivare l'MFA (politica del tenant).
type MFARequiredError struct {
	Challenge string
	Setup     bool
}

func (e *MFARequiredError) Error() string { return "serve il secondo fattore" }

// SetSecretBox imposta la chiave applicativa che cifra i segreti TOTP.
func (s *Service) SetSecretBox(b *secretbox.Box) {
	if b != nil {
		s.box = b
	}
}

// MFASetup è ciò che serve all'app di autenticazione: segreto (anche da digitare), URI e QR.
type MFASetup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
	QRSVG  string `json:"qr_svg"`
}

// MFAStatus è lo stato dell'MFA dell'account, per il profilo.
type MFAStatus struct {
	Available    bool       `json:"available"` // l'account ha una password
	Enabled      bool       `json:"enabled"`
	EnabledAt    *time.Time `json:"enabled_at"`
	RecoveryLeft int        `json:"recovery_codes_left"`
	Required     bool       `json:"required"` // la politica di un tenant la impone
	SessionMFA   bool       `json:"session_mfa"`
}

func mfaContext(accountID string) string { return "mfa:" + accountID }

// mfaGate decide, dopo la password, se serve il secondo fattore.
func (s *Service) mfaGate(ctx context.Context, accountID string, ip netip.Addr, userAgent string) error {
	var enabled, required bool
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select mfa_enabled_at is not null and mfa_secret_enc is not null, account_mfa_required($1) from accounts where id = $1",
			accountID).Scan(&enabled, &required)
	})
	if err != nil {
		return err
	}
	if !enabled && !required {
		return nil
	}
	purpose := "verify"
	if !enabled {
		purpose = "setup"
	}
	token, err := s.newChallenge(ctx, accountID, purpose, ip, userAgent)
	if err != nil {
		return err
	}
	return &MFARequiredError{Challenge: token, Setup: !enabled}
}

func (s *Service) newChallenge(ctx context.Context, accountID, purpose string, ip netip.Addr, userAgent string) (string, error) {
	token, h, err := newToken()
	if err != nil {
		return "", err
	}
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		// Una sola sfida aperta per account: un nuovo login annulla la precedente.
		if _, err := tx.Exec(ctx, "delete from mfa_challenges where account_id = $1 or expires_at < now()", accountID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into mfa_challenges (token_hash, account_id, purpose, ip, user_agent, expires_at)
			values ($1, $2, $3, $4, $5, $6)`, h, accountID, purpose, ipOrNil(ip), truncate(userAgent, 400), time.Now().Add(mfaChallengeTTL))
		return err
	})
	return token, err
}

// useChallenge conta un tentativo sulla sfida e restituisce l'account. Il conteggio si
// salva subito (transazione a parte), anche se poi il codice è sbagliato.
func (s *Service) useChallenge(ctx context.Context, token, purpose string) (string, error) {
	if token == "" {
		return "", ErrMFAChallenge
	}
	h := sha256.Sum256([]byte(token))
	var accountID string
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `update mfa_challenges set attempts = attempts + 1
			where token_hash = $1 and purpose = $2 and expires_at > now() and attempts < $3
			returning account_id`, h[:], purpose, mfaChallengeAttempts).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = tx.Exec(ctx, "delete from mfa_challenges where token_hash = $1", h[:])
			return ErrMFAChallenge
		}
		return err
	})
	if errors.Is(err, ErrMFAChallenge) {
		return "", err
	}
	if err != nil {
		return "", err
	}
	if !s.mfaLimiter.allow(accountID) {
		return "", ErrRateLimited
	}
	return accountID, nil
}

func (s *Service) dropChallenge(ctx context.Context, tx pgx.Tx, token string) error {
	h := sha256.Sum256([]byte(token))
	tag, err := tx.Exec(ctx, "delete from mfa_challenges where token_hash = $1", h[:])
	if err == nil && tag.RowsAffected() == 0 {
		return ErrMFAChallenge // già consumata da una richiesta parallela
	}
	return err
}

// checkCode verifica un codice TOTP del segreto attivo (o, con recovery, un codice di
// recupero) e lo consuma. Restituisce il metodo usato: "totp" o "recovery".
func (s *Service) checkCode(ctx context.Context, tx pgx.Tx, accountID, code string, recovery bool) (string, error) {
	var sealed []byte
	var last *int64
	err := tx.QueryRow(ctx, "select mfa_secret_enc, mfa_last_step from accounts where id = $1 and mfa_enabled_at is not null for update",
		accountID).Scan(&sealed, &last)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && sealed == nil) {
		return "", ErrMFANotEnabled
	}
	if err != nil {
		return "", err
	}
	secret, err := s.box.Open(sealed, mfaContext(accountID))
	if err != nil {
		return "", err
	}
	lastStep := int64(-1)
	if last != nil {
		lastStep = *last
	}
	if step, ok := verifyTOTP(secret, code, time.Now(), lastStep); ok {
		_, err := tx.Exec(ctx, "update accounts set mfa_last_step = $2 where id = $1", accountID, step)
		return "totp", err
	}
	if !recovery {
		return "", ErrMFACode
	}
	rc := normalizeRecovery(code)
	if len(rc) != 10 {
		return "", ErrMFACode
	}
	tag, err := tx.Exec(ctx, "update mfa_recovery_codes set used_at = now() where account_id = $1 and code_hash = $2 and used_at is null",
		accountID, s.recoveryHash(accountID, rc))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrMFACode
	}
	return "recovery", nil
}

func (s *Service) auditMFA(ctx context.Context, accountID, action string, ip netip.Addr, payload map[string]any) {
	_ = s.db.Global(ctx, func(tx pgx.Tx) error {
		return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "account", EntityID: accountID, ActorAccountID: accountID, IP: ip, Payload: payload})
	})
}

// VerifyMFA è il secondo passo del login: codice TOTP o di recupero, poi la sessione.
func (s *Service) VerifyMFA(ctx context.Context, challenge, code string, ip netip.Addr, userAgent string) (string, error) {
	accountID, err := s.useChallenge(ctx, challenge, "verify")
	if err != nil {
		return "", err
	}
	var method string
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		if method, err = s.checkCode(ctx, tx, accountID, code, true); err != nil {
			return err
		}
		if err := s.dropChallenge(ctx, tx, challenge); err != nil {
			return err
		}
		if method == "recovery" {
			return audit.Append(ctx, tx, audit.Event{Action: "auth.mfa_recovery_used", EntityType: "account", EntityID: accountID, ActorAccountID: accountID, IP: ip})
		}
		return nil
	})
	if errors.Is(err, ErrMFACode) {
		s.auditMFA(ctx, accountID, "auth.mfa_failed", ip, nil)
		return "", err
	}
	if err != nil {
		return "", err
	}
	s.mfaLimiter.reset(accountID)
	return s.newSession(ctx, accountID, "password", true, ip, userAgent)
}

// BeginMFAChallengeSetup prepara l'attivazione obbligatoria durante il login.
func (s *Service) BeginMFAChallengeSetup(ctx context.Context, challenge string) (*MFASetup, error) {
	if challenge == "" {
		return nil, ErrMFAChallenge
	}
	h := sha256.Sum256([]byte(challenge))
	var accountID string
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "select account_id from mfa_challenges where token_hash = $1 and purpose = 'setup' and expires_at > now()", h[:]).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMFAChallenge
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.beginSetup(ctx, accountID)
}

// ConfirmMFAChallengeSetup attiva l'MFA con il primo codice e apre la sessione.
func (s *Service) ConfirmMFAChallengeSetup(ctx context.Context, challenge, code string, ip netip.Addr, userAgent string) (string, []string, error) {
	accountID, err := s.useChallenge(ctx, challenge, "setup")
	if err != nil {
		return "", nil, err
	}
	var codes []string
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		if codes, err = s.enable(ctx, tx, accountID, code, ip); err != nil {
			return err
		}
		return s.dropChallenge(ctx, tx, challenge)
	})
	if errors.Is(err, ErrMFACode) {
		s.auditMFA(ctx, accountID, "auth.mfa_failed", ip, map[string]any{"setup": true})
		return "", nil, err
	}
	if err != nil {
		return "", nil, err
	}
	s.mfaLimiter.reset(accountID)
	token, err := s.newSession(ctx, accountID, "password", true, ip, userAgent)
	return token, codes, err
}

// MFAStatus è lo stato dell'MFA per il profilo.
func (s *Service) MFAStatus(ctx context.Context, p *Principal) (*MFAStatus, error) {
	out := &MFAStatus{SessionMFA: p.MFA}
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select password_hash is not null, mfa_enabled_at is not null and mfa_secret_enc is not null, mfa_enabled_at,
			(select count(*) from mfa_recovery_codes where account_id = a.id and used_at is null), account_mfa_required(a.id)
			from accounts a where a.id = $1`, p.AccountID).Scan(&out.Available, &out.Enabled, &out.EnabledAt, &out.RecoveryLeft, &out.Required)
	})
	if !out.Enabled {
		out.EnabledAt = nil
	}
	return out, err
}

// BeginMFASetup genera un nuovo segreto (in attesa di conferma) per il profilo.
func (s *Service) BeginMFASetup(ctx context.Context, p *Principal) (*MFASetup, error) {
	return s.beginSetup(ctx, p.AccountID)
}

func (s *Service) beginSetup(ctx context.Context, accountID string) (*MFASetup, error) {
	secret := newTOTPSecret()
	sealed, err := s.box.Seal(secret, mfaContext(accountID))
	if err != nil {
		return nil, err
	}
	var email string
	err = s.db.Global(ctx, func(tx pgx.Tx) error {
		var hasPassword, enabled bool
		if err := tx.QueryRow(ctx, "select email, password_hash is not null, mfa_enabled_at is not null from accounts where id = $1", accountID).
			Scan(&email, &hasPassword, &enabled); err != nil {
			return err
		}
		if !hasPassword {
			return ErrMFAUnavailable
		}
		if enabled {
			return ErrMFAAlready
		}
		_, err := tx.Exec(ctx, "update accounts set mfa_pending_enc = $2 where id = $1", accountID, sealed)
		return err
	})
	if err != nil {
		return nil, err
	}
	uri := totpURI(secret, email)
	code, err := qr.Encode([]byte(uri))
	if err != nil {
		return nil, err
	}
	// Il segreto da digitare, a gruppi di quattro per leggerlo senza errori.
	raw := b32.EncodeToString(secret)
	var groups []string
	for i := 0; i < len(raw); i += 4 {
		groups = append(groups, raw[i:min(i+4, len(raw))])
	}
	return &MFASetup{Secret: strings.Join(groups, " "), URI: uri, QRSVG: code.SVG(4)}, nil
}

// enable verifica il codice sul segreto in attesa e lo rende attivo, con i codici di recupero.
func (s *Service) enable(ctx context.Context, tx pgx.Tx, accountID, code string, ip netip.Addr) ([]string, error) {
	var pending []byte
	var enabled bool
	if err := tx.QueryRow(ctx, "select mfa_pending_enc, mfa_enabled_at is not null from accounts where id = $1 for update", accountID).Scan(&pending, &enabled); err != nil {
		return nil, err
	}
	if enabled {
		return nil, ErrMFAAlready
	}
	if pending == nil {
		return nil, ErrMFANotPending
	}
	secret, err := s.box.Open(pending, mfaContext(accountID))
	if err != nil {
		return nil, err
	}
	step, ok := verifyTOTP(secret, code, time.Now(), -1)
	if !ok {
		return nil, ErrMFACode
	}
	if _, err := tx.Exec(ctx, `update accounts set mfa_secret_enc = mfa_pending_enc, mfa_pending_enc = null,
		mfa_enabled_at = now(), mfa_last_step = $2 where id = $1`, accountID, step); err != nil {
		return nil, err
	}
	codes, err := s.newRecoveryCodes(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	return codes, audit.Append(ctx, tx, audit.Event{Action: "auth.mfa_enable", EntityType: "account", EntityID: accountID, ActorAccountID: accountID, IP: ip})
}

// EnableMFA conferma l'attivazione dal profilo; la sessione corrente diventa "con MFA".
func (s *Service) EnableMFA(ctx context.Context, p *Principal, code string, ip netip.Addr) ([]string, error) {
	if !s.mfaLimiter.allow(p.AccountID) {
		return nil, ErrRateLimited
	}
	var codes []string
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		var err error
		if codes, err = s.enable(ctx, tx, p.AccountID, code, ip); err != nil {
			return err
		}
		if p.SessionID != "" {
			_, err = tx.Exec(ctx, "update sessions set mfa = true where id = $1", p.SessionID)
		}
		return err
	})
	if err == nil {
		s.mfaLimiter.reset(p.AccountID)
	}
	return codes, err
}

// DisableMFA spegne l'MFA con un codice valido (TOTP o di recupero), se la politica lo consente.
func (s *Service) DisableMFA(ctx context.Context, p *Principal, code string, ip netip.Addr) error {
	if !s.mfaLimiter.allow(p.AccountID) {
		return ErrRateLimited
	}
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		var required bool
		if err := tx.QueryRow(ctx, "select account_mfa_required($1)", p.AccountID).Scan(&required); err != nil {
			return err
		}
		if required {
			return ErrMFAPolicy
		}
		if _, err := s.checkCode(ctx, tx, p.AccountID, code, true); err != nil {
			return err
		}
		if err := clearMFA(ctx, tx, p.AccountID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "auth.mfa_disable", EntityType: "account", EntityID: p.AccountID, ActorAccountID: p.AccountID, IP: ip})
	})
	if errors.Is(err, ErrMFACode) {
		s.auditMFA(ctx, p.AccountID, "auth.mfa_failed", ip, map[string]any{"disable": true})
	}
	if err == nil {
		s.mfaLimiter.reset(p.AccountID)
	}
	return err
}

// RegenerateRecoveryCodes sostituisce i codici di recupero (serve un codice TOTP).
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, p *Principal, code string, ip netip.Addr) ([]string, error) {
	if !s.mfaLimiter.allow(p.AccountID) {
		return nil, ErrRateLimited
	}
	var codes []string
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		if _, err := s.checkCode(ctx, tx, p.AccountID, code, false); err != nil {
			return err
		}
		var err error
		if codes, err = s.newRecoveryCodes(ctx, tx, p.AccountID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "auth.mfa_recovery_regenerate", EntityType: "account", EntityID: p.AccountID, ActorAccountID: p.AccountID, IP: ip})
	})
	if errors.Is(err, ErrMFACode) {
		s.auditMFA(ctx, p.AccountID, "auth.mfa_failed", ip, map[string]any{"recovery_codes": true})
	}
	if err == nil {
		s.mfaLimiter.reset(p.AccountID)
	}
	return codes, err
}

// clearMFA toglie segreto, attivazione in corso e codici di recupero (anche per il reset
// fatto da un amministratore, in internal/platform/admin).
func clearMFA(ctx context.Context, tx pgx.Tx, accountID string) error {
	if _, err := tx.Exec(ctx, `update accounts set mfa_secret_enc = null, mfa_pending_enc = null, mfa_enabled_at = null,
		mfa_last_step = null where id = $1`, accountID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from mfa_challenges where account_id = $1", accountID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "delete from mfa_recovery_codes where account_id = $1", accountID)
	return err
}

// ClearMFA è clearMFA per il pacchetto di amministrazione.
func ClearMFA(ctx context.Context, tx pgx.Tx, accountID string) error {
	return clearMFA(ctx, tx, accountID)
}

func (s *Service) newRecoveryCodes(ctx context.Context, tx pgx.Tx, accountID string) ([]string, error) {
	if _, err := tx.Exec(ctx, "delete from mfa_recovery_codes where account_id = $1", accountID); err != nil {
		return nil, err
	}
	out := make([]string, 0, recoveryCodes)
	for len(out) < recoveryCodes {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for i, x := range b {
			// 248 = 8 * 31: si scartano i byte oltre, niente sbilanciamento tra i caratteri.
			for x >= 248 {
				var r [1]byte
				_, _ = rand.Read(r[:])
				x = r[0]
			}
			if i == 5 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryAlphabet[int(x)%len(recoveryAlphabet)])
		}
		code := sb.String()
		tag, err := tx.Exec(ctx, "insert into mfa_recovery_codes (account_id, code_hash) values ($1, $2) on conflict do nothing",
			accountID, s.recoveryHash(accountID, normalizeRecovery(code)))
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			out = append(out, code)
		}
	}
	return out, nil
}

func (s *Service) recoveryHash(accountID, code string) []byte {
	return s.box.MAC("mfa-recovery", []byte(accountID+":"+code))
}

func normalizeRecovery(c string) string {
	return strings.ToLower(normalizeCode(c))
}
