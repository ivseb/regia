# Esercizio e dati

## Come gira
- `regia serve` al primo avvio esegue `initdb` in `data/pg`, avvia Postgres come processo figlio **solo su socket unix** (`data/run`, permessi 0700, nessuna porta TCP), applica le migrazioni e lo ferma alla chiusura.
- Se un altro processo regia ha già avviato Postgres sulla stessa cartella dati, i comandi (es. `tenant create`) si collegano a quello e non lo fermano.
- Con `DATABASE_URL` + `REGIA_APP_DATABASE_URL` il binario usa un Postgres esterno e non lo gestisce. Il proprietario dello schema deve essere superuser o avere BYPASSRLS; il ruolo `mytrack_app` può essere creato prima dall'amministratore del DB.

## Variabili d'ambiente
| variabile | default | uso |
| --- | --- | --- |
| `REGIA_HTTP_ADDR` | `:8080` | indirizzo di ascolto |
| `REGIA_DATA_DIR` | `./data` | Postgres incluso, file, chiavi |
| `REGIA_PG_BIN` | ricerca automatica | cartella di `initdb` e `postgres` |
| `DATABASE_URL` | vuota | Postgres esterno (ruolo proprietario) |
| `REGIA_APP_DATABASE_URL` | vuota | Postgres esterno (ruolo applicativo) |
| `REGIA_LOG_FORMAT` | `text` | `text` o `json` |
| `REGIA_DEV` | `false` | cookie non Secure, log di debug |
| `REGIA_SESSION_TTL` | `12h` | durata della sessione |
| `REGIA_MAX_UPLOAD_MB` | `25` | limite di un allegato, in MB |
| `REGIA_SMTP_HOST` | vuota | server SMTP: con host e mittente le notifiche partono anche per email |
| `REGIA_SMTP_PORT` | `587` | porta SMTP (465 = TLS implicito, altrimenti STARTTLS se offerto) |
| `REGIA_SMTP_USER` / `REGIA_SMTP_PASSWORD` | vuote | credenziali SMTP, usate solo su connessione cifrata |
| `REGIA_SMTP_FROM` | vuota | mittente, es. `Regia <noreply@azienda.it>` |
| `REGIA_MAIL_INTERVAL` | `3m` | raggruppamento delle email: un riepilogo per persona a ogni intervallo |
| `REGIA_BASE_URL` | vuota | indirizzo pubblico dell'app, per i link nelle email |
| `REGIA_OIDC_ISSUER` | vuota | issuer OIDC; vuota = accesso unico disattivato |
| `REGIA_OIDC_CLIENT_ID` | vuota | client_id registrato sull'IdP |
| `REGIA_OIDC_CLIENT_SECRET` | vuota | segreto del client; vuota = client pubblico (solo PKCE) |
| `REGIA_OIDC_REDIRECT_URL` | vuota | `https://<host>/api/v1/auth/oidc/callback` |
| `REGIA_OIDC_NAME` | `SSO` | etichetta del pulsante "Accedi con …" |
| `REGIA_OIDC_SCOPES` | `openid email profile` | scope richiesti, separati da spazi |
| `REGIA_OIDC_ONLY` | `false` | solo SSO: password solo per gli amministratori di piattaforma |
| `REGIA_OIDC_TRUST_EMAIL` | `false` | considera verificata l'email se l'IdP non manda `email_verified` |
| `REGIA_BACKUP_DIR` | `<data>/backups` | backup automatici e di `regia backup` senza `--out` ([operativo.md](../operativo.md)) |
| `REGIA_BACKUP_BEFORE_MIGRATE` | `true` | backup automatico prima di applicare migrazioni nuove |
| `REGIA_BACKUP_KEEP` | `5` | backup automatici da tenere (0 = tutti) |
| `REGIA_BACKUP_PASSPHRASE_FILE` / `REGIA_BACKUP_PASSPHRASE` | vuote | cifra i backup con age (almeno 12 caratteri) |
| `REGIA_WEBHOOK_ALLOW_PRIVATE` | `false` | webhook anche verso indirizzi interni e via http: toglie la protezione SSRF (implicita con `--dev`) |
| `REGIA_CONNECTOR_ALLOW_PRIVATE` | `false` | i connettori DevSecOps (GitHub Enterprise Server) possono attivare «Rete interna»: reti private sì, link-local (metadati del cloud) mai. Con `--dev` la protezione è spenta |

## Sicurezza dei dati: le regole
1. Il codice applicativo tocca i dati di un tenant **solo** con `db.InTenant(...)`, che imposta `app.tenant_id` per la transazione.
2. Il pool applicativo usa `mytrack_app`: non è proprietario delle tabelle e non ha BYPASSRLS, quindi la RLS vale sempre.
3. Tariffe e voci di costo richiedono anche `TenantOpts{Finance: true}`, da impostare solo dopo il controllo del permesso `finance.read`.
4. Ogni nuova tabella con `tenant_id` chiama `enable_tenant_rls('nome')` nella sua migrazione; il test `TestEveryTenantTableHasRLS` lo verifica.
5. Le scritture rilevanti chiamano `audit.Append` nella stessa transazione. Nell'audit vanno solo identificativi, mai dati personali in chiaro.

## Migrazioni
File SQL in `migrations/`, inclusi nel binario e applicati all'avvio con un advisory lock (più istanze, una sola applicazione).
Un file applicato non si modifica: il checksum è verificato e l'avvio si blocca. Ogni cambiamento è una nuova migrazione, additiva e retrocompatibile per un rilascio.
`migrations/0001_schema.sql` nasce da `replica/schema.sql`, che da ora resta come documento di progetto.

**Indici senza blocco (piano, passo 17).** Ogni migrazione gira nella sua transazione. Un file che inizia con la riga di commento `-- regia:no-transaction` gira invece fuori transazione, un'istruzione alla volta: serve per `create index concurrently` sulle tabelle grandi (`elements`, `activities`, `element_values`), che altrimenti bloccherebbero le scritture per tutta la costruzione dell'indice.

```sql
-- Indice per la ricerca per data di aggiornamento.
-- regia:no-transaction
create index concurrently if not exists elements_updated_idx on elements (tenant_id, updated_at);
create index concurrently if not exists activities_kind_idx on activities (tenant_id, kind, created_at);
```

- **Idempotenza:** ogni istruzione deve poter ripartire (`if not exists`). Se una fallisce, quelle prima restano applicate, il file non risulta applicato e al riavvio riparte da capo.
- **Niente `begin`/`commit`:** ciò che deve andare insieme sta in una migrazione normale.
- **Indice rimasto INVALID:** un `create index concurrently` fallito lo lascia sul database. Al tentativo successivo il migratore lo riconosce, lo elimina (`drop index concurrently`) e lo ricrea.
- **Tabelle partizionate** (`activities`, `audit_log`, `metric_points`): Postgres non accetta `concurrently` sulla tabella madre. Il migratore riconosce il caso e:
  1. crea l'indice `on only` sulla madre;
  2. lo costruisce senza blocco su ogni partizione;
  3. lo aggancia con `alter index … attach partition`.

  L'indice della madre diventa valido quando tutte le partizioni hanno il loro, e le partizioni create dopo lo ereditano.
- **Migrazioni già applicate:** non si toccano, il checksum lo impedisce. La direttiva vale per quelle nuove.
- **Test:** `TestMigrazioniFuoriTransazione` (`internal/itest/operativo_test.go`) e `internal/platform/db/migrate_test.go`.

**Stato.** `db.Status` confronta le migrazioni del binario con quelle del database (applicate, in attesa, modificate, sconosciute) senza scrivere nulla. Lo usano `regia doctor` e il backup automatico prima delle migrazioni (`docs/operativo.md`).

## Docker
`make docker` costruisce l'immagine: Alpine, binario e i soli programmi di Postgres 18 che servono, con pgvector (`--build-arg PGVECTOR=false` per toglierlo). Pesa circa 92 MB, contro i 695 di prima. I dati stanno nel volume `/data`; dettagli, misure e arresto in [operativo.md](../operativo.md#docker).
```bash
docker run -p 8080:8080 -v regia-data:/data --stop-timeout 60 regia:dev
```

## Comandi di amministrazione
```bash
regia tenant create --slug acme --name "ACME S.p.A." --admin-email nome@acme.it
regia tenant provision --slug acme      # configura i moduli in un tenant esistente (idempotente)
regia member add --tenant acme --email p@acme.it --name "Nome" --roles editor --groups PMO
regia backup --dir /var/backups/regia --keep 14   # backup completo (anche a server avviato)
regia restore --in backup.tar --data cartella-nuova # ripristino; --check per la sola verifica
regia doctor                                        # controlli prima di avvio, aggiornamento e dopo un ripristino
```
Backup, ripristino, aggiornamenti e chiavi: [operativo.md](../operativo.md).
Le password non passano mai dalla riga di comando: `REGIA_ADMIN_PASSWORD` / `REGIA_MEMBER_PASSWORD`, oppure richiesta a terminale.
Queste operazioni usano il ruolo proprietario, ma appena individuato il tenant la transazione passa a `mytrack_app` (`bindTenant`). Così anche l'amministrazione è soggetta alla RLS.
Membri, ruoli e gruppi si gestiscono anche dall'interfaccia: Configurazione → Persone e accessi (vedi "Persone e accessi, MFA e webhook" più sotto). La riga di comando resta per il primo amministratore e per gli script.
