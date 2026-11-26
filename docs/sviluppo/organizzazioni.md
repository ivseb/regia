# Organizzazioni (tenant) dall'interfaccia

Prima un tenant si creava solo con `regia tenant create`. Ora il **superamministratore dell'istanza** lo crea dall'interfaccia e capisce subito come entrarci e come metterci persone.

## Chi è il superamministratore

È un account con `accounts.is_platform_admin = true` (`Principal.IsPlatformAdmin`). Non si assegna dall'interfaccia: `regia platform-admin grant|revoke --email ...`, oppure `--platform-admin` in `tenant create`. `GET /api/v1/me` espone `is_platform_admin`. Il seed di sviluppo (`scripts/dev-seed.sh`) lo dà ad `admin@demo.test`.

## API (internal/api/platform.go)

Solo sessione con cookie e superamministratore; altrimenti 403 `platform.admin_required` (il rifiuto va nel log del server).

- `GET /api/v1/platform/tenants`: elenco con nome, identificativo, lingua, valuta, numero di membri, data di creazione e `status` (`active`, `suspended`, `offboarding`).
- `POST /api/v1/platform/tenants` `{name, slug, language?, base_currency?, admin_email?}`: chiama `tenant.Create` (le stesse funzioni e gli stessi provisioner del comando, in un punto solo: `api.Provisioners()`), nella stessa transazione e con la RLS del nuovo tenant. Chi crea diventa amministratore con il **proprio account**, senza password nuova; l'evento `tenant.create` finisce nell'audit del nuovo tenant con lui come attore. Con `admin_email` un'altra persona è invitata come amministratore (link monouso, e email se l'istanza ha SMTP).

- `PATCH /api/v1/platform/tenants/{id}` `{name}`: rinomina, con la stessa validazione della creazione. L'identificativo non cambia. Evento `tenant.update` nell'audit di quel tenant con il superamministratore come attore (`tenant.Rename`).
- `POST /api/v1/platform/tenants/{id}/suspend` e `/reactivate`: cambiano `tenants.status` (`suspended` / `active`) e scrivono `tenant.suspend` / `tenant.reactivate` nell'audit del tenant (`tenant.SetStatus`). Non si sospende l'organizzazione attiva nella propria sessione: 409 `platform.cannot_suspend_current`. Id inesistente: 404 `platform.tenant_not_found`.

Errori (code): `validation.organizzazioni.name_invalid`, `slug_invalid`, `language_invalid`, `currency_invalid`, `admin_email_invalid` (422), `slug_taken` (409).

L'elenco usa il ruolo proprietario (nessun tenant attivo), come `tenant create`: mai query senza tenant dal ruolo applicativo sulle tabelle con RLS.

## Organizzazione sospesa

La sospensione non cancella niente: i dati restano, ma nessuno entra finché non è riattivata.

- La funzione SQL `account_memberships` (migrazione 0081) restituisce anche `status` del tenant; è il codice Go a decidere. `auth.Membership.Status` / `Active()`, e `GET /api/v1/me` espone `status` per ogni organizzazione dell'account.
- **Sessioni esistenti**: `Authenticate` non carica il tenant sospeso; la sessione passa alla prima organizzazione attiva dell'account (per nome) e lo registra, oppure resta senza organizzazione attiva (`active_tenant` null). Vale dalla richiesta successiva.
- **Cambio di organizzazione** (`POST /api/v1/me/tenant`) verso una sospesa: 403 `tenant.suspended`.
- **Login** (password, OIDC): riesce comunque; il tenant iniziale è l'ultimo usato o il primo **attivo**. Chi ha solo organizzazioni sospese entra senza organizzazione attiva.
- **Token API**: `api_token_use` già richiedeva `tenants.status = 'active'`: un token di un tenant sospeso dà 401.
- **Interfaccia**: il selettore dell'organizzazione mostra la sospesa «(sospesa)» e non selezionabile (si preferisce mostrarla, così la persona capisce perché non c'è); senza organizzazioni attive la pagina dice «L'organizzazione X è sospesa. Rivolgiti all'amministratore dell'istanza.» (`OrganizationSuspended`). Le pagine `/piattaforma/...` restano raggiungibili al superamministratore.

## Interfaccia

- Link «Gestisci organizzazioni» sotto il selettore dell'organizzazione nella shell, solo per il superamministratore. Il selettore resta com'è (compare con più di un tenant).
- Pagina «Organizzazioni» (`web/src/pages/Organizations.tsx`, `web/src/organizationsApi.ts`): modulo (nome, identificativo proposto dal nome, lingua con il selettore comune, valuta base, altro amministratore facoltativo) ed elenco.
- Ogni riga mostra lo stato (Attiva/Sospesa) e le azioni «Rinomina» (modifica in linea), «Sospendi» (con conferma che spiega l'effetto) e «Riattiva». «Entra» non compare per una sospesa; per l'organizzazione in cui si sta lavorando «Sospendi» è sostituito da una nota.
- Dopo la creazione: «Entra» cambia organizzazione e porta a «Inizia da qui» (nella lingua scelta); un riquadro spiega i tre passi per mettere dentro le persone (Configurazione → Persone e accessi → «Aggiungi persona» → «Invita») con il pulsante «Entra e apri Persone e accessi».

## Cosa non c'è (ancora)

- Il **modello di partenza** non si sceglie alla creazione: i modelli si applicano da dentro il tenant (`Inizia da qui` → Modelli, `avvio_modelli.go`), e la pagina lo dice.
- Niente eliminazione (né stato «in chiusura» dall'interfaccia) di un'organizzazione.
- Un superamministratore che non è membro di un'organizzazione la vede nell'elenco ma non può entrarci.

## Test

`internal/itest/organizzazioni_test.go` (403 per chi non è superamministratore, validazioni, creazione con membership attiva, invito, isolamento RLS; rinomina, sospensione con audit, sessione che perde l'accesso, cambio rifiutato, login con la sola organizzazione sospesa, riattivazione, 409 sulla propria) e `web/e2e/lacune-organizzazioni.spec.ts` (anche a 375 px).
