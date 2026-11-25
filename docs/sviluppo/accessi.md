# Accessi e persone

## Accesso unico OIDC e token API (piano, passo 15)

- **OIDC:** authorization code con PKCE S256 e nonce, configurato con le variabili `REGIA_OIDC_*` qui sopra.
  - `GET /api/v1/auth/oidc/config` dice se è attivo, con che nome e se il login con password è ancora ammesso;
  - `GET /api/v1/auth/oidc/start?next=/percorso` manda all'IdP; `GET /api/v1/auth/oidc/callback` crea la sessione (stesso cookie `regia_session` del login con password) e torna a `next`, solo se è un percorso interno;
  - in caso di errore si torna a `/?sso_error=codice` (`unknown_account`, `email_unverified`, `already_linked`, `state`, `invalid`, `unavailable`, `denied`, `failed`) e la pagina di accesso mostra il messaggio.
- **Collegamento all'account:** al primo accesso si cerca l'account con la stessa email, purché `email_verified` sia vero; poi vale la coppia issuer e sub (`account_identities`), anche se l'email cambia. Nessun account viene creato in automatico.
- **Email non verificata:** se l'IdP non manda `email_verified` l'email non è considerata verificata e il primo collegamento è rifiutato. `REGIA_OIDC_TRUST_EMAIL=true` cambia questo default solo per IdP che verificano le email; un `email_verified: false` esplicito è sempre rifiutato.
- **Cookie del flusso:** `regia_oidc`, valido 10 minuti e firmato con HMAC; la chiave sta in `<data>/keys/oidc-state.key`, creata al primo avvio.
- **Solo SSO:** con `REGIA_OIDC_ONLY=true` il login con password risponde 403 `auth.password_disabled`, tranne per gli amministratori di piattaforma (emergenza). Il logout resta locale: non chiude la sessione sull'IdP.
- **Token API:** `Authorization: Bearer myt_…`.
  - Gestione dalla pagina `/profilo` o via API, solo con una sessione: `GET/POST /api/v1/me/tokens`, `DELETE /api/v1/me/tokens/{id}`. Corpo della creazione: `{"name", "expires_in_days" (default 90, massimo 365), "scopes" (vuoto = tutti i permessi del membro)}`.
  - Il token vale nel tenant in cui è stato creato. I permessi sono quelli attuali del membro intersecati con quelli del token; con soli permessi `*.read` il token è in sola lettura.
  - Con un token non si creano altri token, non si cambia tenant e non si fa logout. Non serve il controllo dell'origine, perché il cookie non si usa.
  - Nel database c'è solo lo sha256; ogni evento di audit scritto con un token ha `api_token` nel payload.
- **Profilo:** `GET /api/v1/me/account` restituisce email, tenant attivo, metodo della sessione e identità collegate.

## Persone e accessi, MFA e webhook (piano, passo 15)

Disegno in `replica/architecture.md` §4; migrazione `0022_admin.sql`; test in `internal/itest/admin_users_test.go`.

**Chiave applicativa: `<data>/keys/secrets.key`.**
- 32 byte casuali creati al primo avvio, permessi 0600. Cifra (AES-256-GCM) i segreti TOTP e dei webhook e firma i codici di recupero.
- **Va nel backup insieme al database.** Se si perde, gli account con MFA vanno reimpostati e i webhook hanno bisogno di un segreto nuovo; non si recupera altrimenti.
- Con più istanze tutte devono avere lo stesso file. Le altre chiavi della cartella (`oidc-state.key`) restano separate.

**Amministrazione** (`tenant.admin`; le modifiche solo con una sessione, non con un token API):
- Membri: `GET /api/v1/admin/members?q=`, `GET /api/v1/admin/members/{id}`, `POST /api/v1/admin/members` `{"email", "display_name", "member_type": "standard"|"requester", "mode": "invite"|"create", "role_ids", "group_ids"}`, `PATCH /api/v1/admin/members/{id}` `{"display_name"?, "member_type"?}`.
- Azioni sul membro: `POST …/members/{id}/suspend`, `…/reactivate`, `…/invitation` (nuovo link d'invito), `…/password-link`, `…/mfa-reset`.
- I link monouso tornano come `{"kind", "path", "url"?, "expires_at"}`: `path` è `/invito/<token>` o `/password/<token>`, `url` c'è con `REGIA_BASE_URL`; con SMTP configurato partono anche per email (`emailed`). Nel database c'è solo lo sha256.
- Ruoli: `GET/POST /api/v1/admin/roles` (la lettura porta anche il catalogo `permissions`), `PATCH/DELETE /api/v1/admin/roles/{id}` (solo personalizzati), `POST /api/v1/admin/roles/{id}/assignments` `{"member_id"|"group_id"}`, `DELETE /api/v1/admin/role-assignments/{id}`.
- Gruppi: `GET/POST /api/v1/admin/groups`, `PATCH/DELETE /api/v1/admin/groups/{id}` `{"name", "parent_id"}`, `POST /api/v1/admin/groups/{id}/members` `{"member_id"}`, `DELETE /api/v1/admin/groups/{id}/members/{member}`.
- Ruoli sugli oggetti: `GET/POST /api/v1/admin/stakeholder-roles` `{"key", "name"}`, `PATCH /api/v1/admin/stakeholder-roles/{id}` `{"name"}`.
- Sicurezza: `GET/PUT /api/v1/admin/security` `{"mfa_policy": "off"|"admins"|"all"}`.
- Errori con codici stabili: `admin.last_admin` (409, l'organizzazione resterebbe senza amministratori), `admin.shared_account` (409, account valido anche altrove), `admin.self`, `group.in_use`, `group.system`, `role.system`.

**Audit** (`audit.read`): `GET /api/v1/audit` accetta `action` (anche prefisso, es. `member`), `entity_type`, `entity_id`, `actor` (id del membro), `from`, `to` (AAAA-MM-GG, `to` compreso) oltre a `before` e `limit`; la risposta porta `actors` (nomi dei membri). `GET /api/v1/audit/verify` ricalcola la catena: `{"entries", "intact", "broken_seq", "last_seq", "last_hash"}`.

**MFA (TOTP)**
- Login: `POST /api/v1/auth/login` con MFA risponde `{"mfa_required": true, "mfa_setup": bool}` e il cookie `regia_mfa` (HttpOnly, 5 minuti). Poi `POST /api/v1/auth/mfa/verify` `{"code"}` (codice a 6 cifre o di recupero) crea la sessione; con `mfa_setup` prima `POST /api/v1/auth/mfa/setup` (QR) e `POST /api/v1/auth/mfa/setup/confirm` `{"code"}` (sessione e codici di recupero).
- Profilo (solo sessione): `GET /api/v1/me/mfa`, `POST /api/v1/me/mfa/setup`, `…/enable`, `…/disable`, `…/recovery-codes` (gli ultimi tre con `{"code"}`).
- Errori: 422 `auth.mfa_invalid_code`, 401 `auth.mfa_expired`, 429 `auth.rate_limited` (5 codici sbagliati in 15 minuti), 409 `auth.mfa_required_by_policy`.
- Per provare a mano: `auth.TOTPCode(segreto, time.Now())` in Go calcola il codice di un segreto base32.

**Link pubblici** (senza sessione): `GET/POST /api/v1/auth/invitations/{token}` (`…/accept` con `{"display_name", "password"}`), `GET/POST /api/v1/auth/password-reset/{token}` (`{"password"}`; chiude tutte le sessioni dell'account).

**Webhook** (`tenant.admin`)
- `GET/POST /api/v1/admin/webhooks` `{"name", "url", "events", "filter", "active"}`: la creazione restituisce il segreto `whsec_…` una volta sola. `PATCH/DELETE /api/v1/admin/webhooks/{id}`, `POST …/{id}/secret` (nuovo segreto), `POST …/{id}/ping`, `GET …/{id}/deliveries`, `POST /api/v1/admin/webhook-deliveries/{id}/redeliver`.
- Eventi: `element.created`, `element.phase_changed`, `comment.created`, `attachment.added`, `approval.decided`, `review.completed`, `governance.decision`, più `ping`.
- Payload, versione 1 (i campi nuovi si aggiungono senza cambiare versione):
  ```json
  {"version": 1, "id": "<id evento>", "type": "element.phase_changed", "occurred_at": "2026-11-24T10:00:00Z",
   "tenant": {"id": "…", "slug": "demo", "name": "Demo S.p.A."},
   "actor": {"member_id": "…", "name": "Sara Villa"},
   "element": {"id": "…", "key": "DEM-12", "type": "business_request", "type_name": "…", "title": "…",
               "phase": "Triage", "phase_category": "in_progress", "url": "https://…/e/DEM-12"},
   "data": {"from": "Inviata", "to": "Triage", "comment": null}}
  ```
  `data` per evento: creazione `{phase}`; cambio di fase `{from, to, comment}`; commento `{comment_id, text}` (o `restricted: true` per i commenti a visibilità ristretta); approvazione `{decision, comment, conditions}`; valutazione `{review, review_type, team, outcome}`.
- Intestazioni: `X-Regia-Event`, `X-Regia-Event-Id` (uguale nei reinvii: serve a non elaborare due volte), `X-Regia-Delivery`, `X-Regia-Signature: t=<unix>,v1=<hex>` con `v1 = HMAC-SHA256(segreto, "<t>.<corpo>")`. Il destinatario confronta in tempo costante e scarta `t` più vecchi di 5 minuti.
- Consegna: risposta 2xx = consegnata; altrimenti retry dopo 30 s, 1, 2, 4… minuti fino a 6 ore, 10 tentativi. I redirect non si seguono. Il worker gira ogni 5 secondi dentro `regia serve`.
- SSRF: solo `https` verso indirizzi pubblici; bloccati loopback, reti private, link-local (metadati del cloud), CGNAT, ULA, NAT64 e nomi locali, controllati sull'indirizzo a cui ci si connette dopo il DNS. `--dev` o `REGIA_WEBHOOK_ALLOW_PRIVATE=true` li consentono.

## Permessi lungo i gruppi, organi con gruppi e diagrammi C4 (migrazione 0042)
- **Ereditarietà dei gruppi.** Opzione del tenant `tenant_security.group_inheritance` (Configurazione → Persone e accessi → Sicurezza). Attiva per i nuovi tenant; i tenant esistenti alla migrazione restano com'erano (spenta). Un ruolo dato a un gruppo vale per i membri dei suoi sottogruppi: permessi del tenant, ruoli sugli spazi (Knowledge), stakeholder sugli oggetti, notifiche, approvazioni. Il calcolo sta in SQL: `member_groups(membro)` (gruppi diretti più antenati), `group_effective_members(gruppo)` (membri diretti e dei sottogruppi), `member_in_group`, `body_effective_members(organo)`. **Nuove query**: per "i gruppi del membro" usare `select member_groups($1)`, non più `group_members`.
- **Spiegazione dei permessi:** `GET /api/v1/admin/members/{id}/access` (catena gruppo → ruolo → permesso); in Persone e accessi, per ogni membro, «Da dove arrivano i permessi».
- **Organi di governance con gruppi:** `governance_body_groups`; `BodyInput.groups` (assente = invariati). I membri effettivi si calcolano a ogni lettura, alla creazione della seduta e alla convocazione (le presenze si riallineano; chi è già segnato presente resta). Il quorum conta i votanti effettivi.
- **Diagrammi C4** (`internal/modules/architecture/c4.go`): `POST /api/v1/architecture/diagrams/c4 {root_id, level}` con level `context` | `container` (applicazione) | `component` (asset logico: i moduli sono le applicazioni che lo compongono). Si salva solo la specifica (`layout.c4`: attori, nodi nascosti) e le posizioni (`layout.pos`); riquadri e frecce si calcolano dal grafo a ogni apertura. Container: servizi DevSecOps (`services`), tecnologie di categoria database, asset di cui fa parte. Frontend: `web/src/pages/ea/c4.tsx` (disposizione a strati, legenda, export SVG e PNG); pulsanti nella scheda Architettura dell'elemento.

## Moduli accendibili per tenant e menu del revisore (migrazione 0047)
- **Dati.** `tenant_modules (tenant_id, module, enabled, updated_by, updated_at)` con RLS. Nessuna riga = modulo acceso: i tenant esistenti e quelli nuovi partono con tutto acceso, senza riempimento.
- **Moduli** (`internal/api/modules.go`, `web/src/modules.ts`, stesse chiavi): `demand` (richieste, valutazioni, graduatoria, roadmap, cruscotto, governance, finanza, portale), `work` (board, backlog, timesheet), `knowledge`, `devsecops`, `grc` (conformità, rischi, fornitori ICT, incidenti), `architecture`. Ogni modulo elenca i prefissi di `/api/v1/` che gli appartengono; per aggiungerne uno basta una voce in `moduleDefs` e la corrispondente in `MODULE_PATHS` (frontend).
- **Spegnere un modulo** nasconde menu, ⌘K e pagine (pagina «Modulo spento» sull'indirizzo diretto) e fa rispondere **404** `module.disabled` alle sue API (anche ai token delle pipeline); i dati non si toccano. Il middleware `withModules` sta dopo `withPrincipal`; la lettura ha una cache di 10 s per tenant, azzerata alla scrittura. Eccezione: `GET /api/v1/architecture/applications` e `/assets` (catalogo condiviso con gli impatti) restano leggibili. Le richieste e i requisiti non hanno un modulo proprio.
- **API.** `GET /api/v1/tenant/modules` (tutti i membri) e `PUT /api/v1/tenant/modules {"modules": {"knowledge": false}}` (`tenant.admin`, audit `tenant.modules.update`; chiave sconosciuta = 422). `GET /api/v1/me` porta `active_tenant.modules` (chiave → acceso) e `active_tenant.menu_profile`.
- **Profilo del revisore.** `menu_profile = "auditor"` per chi ha il ruolo `auditor` (diretto o da gruppo, o `member_type` auditor) e nessun permesso di scrittura: il menu e ⌘K si riducono a governance, conformità, rischi e incidenti, DevSecOps, architettura, documenti, elenchi e registro di audit (`AUDITOR_PATHS` in `web/src/modules.ts`). È solo una vista del menu: i permessi restano quelli del ruolo.
- **Interfaccia.** Configurazione → Moduli (`/configurazione/moduli`). Il menu si costruisce in `web/src/nav.ts` (permessi, moduli, profilo); ⌘K usa le stesse regole e non cerca nei tipi dei moduli spenti.
- **Test.** `internal/itest/moduli_test.go`, `internal/api/modules_test.go`, `web/e2e/ondata1-trasversale.spec.ts`.
