# Licenza

Decisione del 2026-11-25: **AGPL-3.0-only**, copyright di **Ivan Sebben**.
- Il testo integrale è in [`LICENSE`](../LICENSE), copiato senza modifiche da <https://www.gnu.org/licenses/agpl-3.0.txt> (661 righe, SHA-256 `0d96a4ff68ad6d4b6f1f30f713b18d5184912ba8dd389f86aa7710db079abcb0`).
- Gli avvisi di terze parti sono in [`NOTICE`](../NOTICE).
- Niente licenza commerciale, CLA o intestazioni in ogni file per ora: si è scelta la strada semplice.

Niente di questo vale come parere legale.

## Regola da rispettare: per ora nessun contributo esterno

Finché tutto il codice è di un solo titolare, il titolare può in futuro distribuire le versioni nuove anche con una licenza commerciale (doppia licenza) oppure chiuse.
- **Un contributo esterno accettato solo con l'AGPL toglierebbe questa libertà** per la parte contribuita. Per questo il repository non accetta codice di altri.
- Se in futuro servono contributi, prima si prepara un accordo con i contributori (CLA con diritto di rilicenza, sul modello dell'Apache Individual CLA). Il DCO (`Signed-off-by`) non basta.
- Il codice generato da strumenti o copiato da dipendenze resta con la licenza della sua fonte (vedi sotto).

## Se un giorno diventa commerciale

- **Cosa resta open:** le versioni già pubblicate con l'AGPL restano AGPL per chi le ha ricevute; la licenza non si può revocare.
- **Cosa si può fare:** le versioni successive si possono distribuire anche con una licenza commerciale (doppia licenza) o solo commerciale.
- **Cosa serve allora:**
  - una nota sulla doppia licenza in testa a `LICENSE`;
  - il testo della licenza commerciale;
  - eventualmente l'intestazione SPDX nei file, `AGPL-3.0-only OR LicenseRef-Regia-Commercial`. Le migrazioni già applicate non si toccano, perché il checksum cambierebbe.

## Uso via rete (§13 dell'AGPL)

Chi modifica Regia e la offre via rete deve dare agli utenti il sorgente della versione in esecuzione. Il titolare non è vincolato dalla propria licenza.
- **Il link c'è:** in fondo al menu, nel portale e nella pagina di accesso si legge «Regia <versione> · Codice sorgente». Porta al commit in esecuzione sul repository pubblico <https://github.com/ivseb/regia>, letto da `GET /api/v1/health`.
- **Chi offre una versione modificata** imposta `REGIA_SOURCE_URL` sul proprio repository, con il codice di quella versione (`docs/operativo.md`, «Codice sorgente»).

## Dipendenze e licenze

L'elenco generato è in [licenze-dipendenze.md](licenze-dipendenze.md) e si rigenera con `make licenses-report`. Usa gli stessi strumenti del controllo licenze.

**Controllo in CI** (`make licenses`):
- **Go:** `go-licenses check`. Blocca le licenze classificate come *forbidden*, *restricted* o *unknown*, quindi GPL, LGPL, AGPL di terzi e licenze non riconosciute.
- **npm:** `scripts/check-npm-licenses.mjs`. Ammette solo MIT, ISC, BSD, Apache-2.0, 0BSD, BlueOak, CC0, Unlicense, MPL-2.0, Python-2.0, CC-BY-4.0 e OFL-1.1.

**Stato al 2026-11-24:**
- **Go**, 15 moduli inclusi nel binario: BSD-3-Clause, MIT, Apache-2.0;
- **npm**, 23 pacchetti di produzione inclusi nel frontend: MIT, OFL-1.1 (caratteri), Unlicense;
- **compatibilità:** tutte compatibili con l'AGPL-3.0 (Apache-2.0 lo è con la famiglia GPLv3) e con una licenza commerciale.

**Nuova dipendenza di questo passo:** `filippo.io/age` (BSD-3-Clause), con `filippo.io/hpke` (BSD-3-Clause), per la cifratura facoltativa dei backup.

### Componenti dell'immagine Docker

L'immagine contiene programmi separati, non collegati al binario di Regia, che è statico e senza cgo.

| componente | licenza |
| --- | --- |
| PostgreSQL 18 (postgres, initdb, pg_dump, pg_restore, moduli) e libpq | PostgreSQL |
| pgvector | PostgreSQL |
| musl, tini, libxml2, alpine-keys | MIT |
| OpenSSL (libcrypto3, libssl3) | Apache-2.0 |
| ICU | ICU (Unicode) |
| OpenLDAP (libldap) | OLDAP-2.8 |
| cyrus-sasl (libsasl) | BSD-3-Clause-Attribution e BSD-4-Clause |
| zlib | Zlib |
| zstd | BSD-3-Clause oppure GPL-2.0-or-later |
| lz4, xz, liburing | permissive o a scelta (vedi `apk info --license`) |
| tzdata | pubblico dominio |
| **busybox**, apk-tools, alpine-baselayout, scanelf, ssl_client | **GPL-2.0-only** |
| **gdbm** (dipendenza di cyrus-sasl, a sua volta di libldap, collegata a postgres) | **GPL-3.0-or-later** |
| libgcc, libstdc++ | GPL con eccezione per le librerie di runtime (GCC Runtime Library Exception) |

- **Natura dei componenti:** quelli copyleft sono pacchetti non modificati del sistema operativo, distribuiti come programmi e librerie di sistema. Regia non li incorpora nel proprio codice.
- **Obblighi di chi distribuisce l'immagine:** rendere disponibile il sorgente dei componenti GPL, per esempio con un'offerta scritta che rimanda ai sorgenti di Alpine (aports) delle versioni incluse.
- **Scelta del 2026-11-25:** si accettano come componenti separati del sistema operativo; l'avviso è in `NOTICE`. Se servisse ridurli: un sottocomando `regia healthcheck` al posto di `wget` di busybox e Postgres compilato senza LDAP (sparisce gdbm).
- **Fonte dell'elenco:** `apk info --license` dentro l'immagine del 2026-11-24.
