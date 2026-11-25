# Organizzazioni (tenant) dall'interfaccia

Prima un tenant si creava solo con `regia tenant create`. Ora il **superamministratore dell'istanza** lo crea dall'interfaccia e capisce subito come entrarci e come metterci persone.

## Chi è il superamministratore

È un account con `accounts.is_platform_admin = true` (`Principal.IsPlatformAdmin`). Non si assegna dall'interfaccia: `regia platform-admin grant|revoke --email ...`, oppure `--platform-admin` in `tenant create`. `GET /api/v1/me` espone `is_platform_admin`. Il seed di sviluppo (`scripts/dev-seed.sh`) lo dà ad `admin@demo.test`.

## API (internal/api/platform.go)

Solo sessione con cookie e superamministratore; altrimenti 403 `platform.admin_required` (il rifiuto va nel log del server).

- `GET /api/v1/platform/tenants`: elenco con nome, identificativo, lingua, valuta, numero di membri, data di creazione.
- `POST /api/v1/platform/tenants` `{name, slug, language?, base_currency?, admin_email?}`: chiama `tenant.Create` (le stesse funzioni e gli stessi provisioner del comando, in un punto solo: `api.Provisioners()`), nella stessa transazione e con la RLS del nuovo tenant. Chi crea diventa amministratore con il **proprio account**, senza password nuova; l'evento `tenant.create` finisce nell'audit del nuovo tenant con lui come attore. Con `admin_email` un'altra persona è invitata come amministratore (link monouso, e email se l'istanza ha SMTP).

Errori (code): `validation.organizzazioni.name_invalid`, `slug_invalid`, `language_invalid`, `currency_invalid`, `admin_email_invalid` (422), `slug_taken` (409).

L'elenco usa il ruolo proprietario (nessun tenant attivo), come `tenant create`: mai query senza tenant dal ruolo applicativo sulle tabelle con RLS.

## Interfaccia

- Link «Gestisci organizzazioni» sotto il selettore dell'organizzazione nella shell, solo per il superamministratore. Il selettore resta com'è (compare con più di un tenant).
- Pagina «Organizzazioni» (`web/src/pages/Organizations.tsx`, `web/src/organizationsApi.ts`): modulo (nome, identificativo proposto dal nome, lingua con il selettore comune, valuta base, altro amministratore facoltativo) ed elenco.
- Dopo la creazione: «Entra» cambia organizzazione e porta a «Inizia da qui» (nella lingua scelta); un riquadro spiega i tre passi per mettere dentro le persone (Configurazione → Persone e accessi → «Aggiungi persona» → «Invita») con il pulsante «Entra e apri Persone e accessi».

## Cosa non c'è (ancora)

- Il **modello di partenza** non si sceglie alla creazione: i modelli si applicano da dentro il tenant (`Inizia da qui` → Modelli, `avvio_modelli.go`), e la pagina lo dice.
- Niente modifica, sospensione o eliminazione di un'organizzazione dall'interfaccia.
- Un superamministratore che non è membro di un'organizzazione la vede nell'elenco ma non può entrarci.

## Test

`internal/itest/organizzazioni_test.go` (403 per chi non è superamministratore, validazioni, creazione con membership attiva, invito, isolamento RLS) e `web/e2e/lacune-organizzazioni.spec.ts` (anche a 375 px).
