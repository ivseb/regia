# Esercizio: backup, ripristino, aggiornamenti e chiavi

Come si tiene in produzione un'istanza di Regia (piano, passo 17).
Il binario fa tutto da solo: non servono servizi esterni.

> **Avvertenza su `secrets.key`.**
> La chiave `<data>/keys/secrets.key` cifra i segreti MFA degli utenti, i segreti dei webhook, delle integrazioni e dei provider AI.
> - **Se la perdi**, quei segreti non si decifrano più. Gli utenti con MFA non entrano finché un amministratore non azzera il loro MFA, e i webhook vanno riconfigurati.
> - **Se esce insieme a un dump del database**, chi li ha entrambi legge tutti i segreti.
>
> Per questo il backup la contiene, e un backup **non cifrato** va trattato come la chiave stessa.
> Cifra i backup con una passphrase (vedi sotto), oppure tienili dove solo l'amministratore arriva.
> `regia doctor` controlla che la chiave ci sia e che decifri i segreti del database.

## Cosa c'è nella cartella dati

| percorso | contenuto | nel backup |
| --- | --- | --- |
| `pg/` | cluster del Postgres incluso | sì, come dump coerente (`pg_dump -Fc`) |
| `keys/secrets.key` | chiave applicativa (32 byte, 0600) | sì |
| `keys/oidc-state.key` | firma del cookie del login OIDC | sì |
| `files/` | allegati, indirizzati per sha256, mai cancellati | sì (tranne `files/tmp/`, i caricamenti in corso) |
| `run/` | socket di Postgres | no |
| `backups/` | backup, se `REGIA_BACKUP_DIR` non indica altro | no |

Con un Postgres esterno (`DATABASE_URL`) il database non sta in `pg/`, ma chiavi e allegati restano nella cartella dati: il backup li prende da lì.

## Backup

```bash
regia backup --data /var/lib/regia --out /var/backups/regia/manuale.tar
regia backup --data /var/lib/regia --dir /var/backups/regia --keep 14   # nome con data e ora
regia backup --data /var/lib/regia --encrypt                              # chiede la passphrase
regia backup --data /var/lib/regia --passphrase-file /etc/regia/backup-passphrase
```

- **Mentre il servizio è avviato:** funziona. `pg_dump` legge un'istantanea coerente senza bloccare le scritture, e il comando si aggancia al Postgres incluso già avviato.
- **Con il servizio fermo:** avvia Postgres per il tempo del dump e poi lo ferma. Non applica migrazioni: il backup fotografa il database com'è.
- **Allegati:** si copiano dopo il dump. Non si cancellano mai e nascono prima della riga che li cita, quindi ogni allegato nominato nel dump è nell'archivio.
- **Scrittura:** il file nasce con permessi 0600, in modo atomico (file temporaneo e rename). Un backup interrotto non lascia un file che sembra buono.
- **Utente:** il comando va eseguito con l'utente del servizio (`runuser -u regia -- …`), che è il proprietario della cartella dati.

### Formato

È un archivio tar, leggibile anche senza Regia:

```
manifest.json      versione e commit di Regia, versioni di Postgres e di pg_dump,
                   migrazioni applicate, estensioni (prima voce)
db/mytrack.dump    pg_dump in formato custom
keys/…             chiavi applicative
files/…            allegati
SHA256SUMS         checksum di tutte le voci (ultima voce)
```

`tar xf backup.tar && sha256sum -c SHA256SUMS` verifica l'archivio con gli strumenti standard.

### Cifratura

Con una passphrase l'archivio è cifrato con [age](https://age-encryption.org): scrypt per derivare la chiave, ChaCha20-Poly1305 a blocchi. L'estensione diventa `.tar.age`.

| come | dove |
| --- | --- |
| `--passphrase-file FILE` | per il singolo comando |
| `--encrypt` | chiede la passphrase a terminale, due volte |
| `REGIA_BACKUP_PASSPHRASE_FILE` | per i backup automatici e periodici (consigliato: file 0400 fuori dalla cartella dati) |
| `REGIA_BACKUP_PASSPHRASE` | alternativa in variabile d'ambiente |

- **Lunghezza:** la passphrase ha almeno 12 caratteri.
- **Senza Regia:** si apre anche con il comando `age -d backup.tar.age > backup.tar`.
- **Senza passphrase non si recupera nulla:** custodiscila separata dai backup (per esempio in un gestore di password aziendale).
- **Cambio di passphrase:** vale per i backup nuovi; quelli vecchi restano con la loro, quindi tieni traccia di quale vale per quali date.

### Backup automatico prima delle migrazioni

Quando `serve` (o `migrate`, `tenant`, `member`) trova migrazioni nuove da applicare a un database già in uso, prima fa un backup in `REGIA_BACKUP_DIR` con nome `auto-<data e ora>.tar[.age]`, poi applica le migrazioni.

- **Retention:** si tengono gli ultimi `REGIA_BACKUP_KEEP` backup automatici.
- **Primo avvio:** su un database vuoto non c'è niente da salvare, e il backup non si fa.
- **Se il backup non riesce,** l'avvio si ferma: aggiornare senza un punto di ritorno è peggio che non aggiornare.

| variabile | default | uso |
| --- | --- | --- |
| `REGIA_BACKUP_DIR` | `<data>/backups` | cartella dei backup automatici e di `regia backup` senza `--out`; meglio su un altro disco |
| `REGIA_BACKUP_BEFORE_MIGRATE` | `true` | `false` salta il backup prima delle migrazioni (per esempio con un Postgres gestito che ha già il ripristino point-in-time) |
| `REGIA_BACKUP_KEEP` | `5` | backup automatici da tenere; `0` = tutti |
| `REGIA_BACKUP_PASSPHRASE_FILE` | vuota | cifra i backup con la passphrase del file |
| `REGIA_BACKUP_PASSPHRASE` | vuota | come sopra, in chiaro nella variabile |

Con un Postgres esterno serve `pg_dump` della stessa versione principale del server, o successiva: nel PATH oppure in `REGIA_PG_BIN`.

### Backup periodico con systemd

In `deploy/systemd/` ci sono gli esempi: `regia.service`, `regia-backup.service` e `regia-backup.timer`. Il timer fa un backup ogni notte alle 2:30 e tiene gli ultimi 14.

```bash
install -d -o regia -g regia -m 0700 /var/backups/regia
cp deploy/systemd/regia-backup.service deploy/systemd/regia-backup.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now regia-backup.timer
systemctl list-timers regia-backup.timer       # prossima esecuzione
systemctl start regia-backup.service           # un backup adesso
journalctl -u regia-backup.service -n 20       # esito
```

- **Backup periodici e automatici:** i periodici si chiamano `regia-<data e ora>.tar`, gli automatici `auto-…`. Le due retention sono separate.
- **Copia fuori dalla macchina:** un backup sullo stesso disco non protegge da un guasto del disco. Copia la cartella altrove, per esempio con l'`ExecStartPost` commentato nella unit (rsync o rclone) o con il backup della macchina virtuale.
- **Controllo:** `regia doctor` avverte se l'ultimo backup ha più di 48 ore.

## Ripristino

```bash
regia restore --check --in backup.tar.age                       # verifica senza ripristinare
regia restore --in backup.tar.age --data /var/lib/regia-nuova  # cartella dati nuova
regia doctor --data /var/lib/regia-nuova
regia serve --data /var/lib/regia-nuova
```

1. **Verifica dell'archivio.** `--check` legge tutto l'archivio, verifica i checksum e la compatibilità, e mostra il manifest.
2. **Compatibilità.** Si controlla prima di scrivere qualcosa:
   - il formato dell'archivio deve essere noto;
   - il backup non deve contenere migrazioni che questo binario non conosce, cioè non deve venire da una versione più recente;
   - `pg_restore` deve essere almeno della versione di `pg_dump` che ha creato il dump.

   Un backup **più vecchio** del binario va bene: al primo `serve` le migrazioni mancanti si applicano, con il loro backup automatico.
3. **Destinazione.**
   - Con il Postgres incluso: cartella dati nuova; `initdb` lo fa il ripristino.
   - Con `DATABASE_URL`: il database deve essere vuoto. Il ruolo `mytrack_app` si crea se manca, oppure lo crea prima l'amministratore del database.
4. **Database.** Si ripristina con `pg_restore --single-transaction`: o tutto o niente. Seguono `ANALYZE` (le statistiche non sono nel dump), poi chiavi e allegati, verificati con i checksum.
5. **pgvector.** Se il backup ha pgvector e la destinazione no (per esempio l'immagine `PGVECTOR=false`), il ripristino prosegue senza la tabella `embeddings`. Sono dati derivati della ricerca semantica, che senza pgvector resta spenta.

### Ripristino sopra l'istanza esistente

Per tornare a un backup sulla stessa macchina:

```bash
systemctl stop regia
runuser -u regia -- env REGIA_PG_BIN=/usr/lib/postgresql/18/bin \
  /opt/regia/bin/regia restore --in /var/backups/regia/auto-20261124T183000Z.tar --data /var/lib/regia --force
systemctl start regia
```

- **Cosa fa `--force`:** non cancella nulla. Sposta `pg/`, `keys/` e `files/` esistenti in `<data>/pre-restore-<data e ora>/`; quando tutto funziona si eliminano a mano.
- **Servizio avviato:** il ripristino si rifiuta se Postgres incluso è ancora in esecuzione sulla cartella.
- **Se il ripristino fallisce,** il cluster appena creato si toglie. Per tornare allo stato di prima, rimetti a posto le cartelle spostate.

## Organizzazioni e superamministratore

Le organizzazioni (tenant) si creano dall'interfaccia (voce «Gestisci organizzazioni», pagina `/piattaforma/organizzazioni`), ma solo dal **superamministratore dell'istanza**. Quel ruolo non si dà dall'interfaccia: lo si assegna da riga di comando a un account già esistente.

```
regia platform-admin grant  --data ./data --email nome@dominio   # dà il ruolo
regia platform-admin revoke --data ./data --email nome@dominio   # lo toglie
regia tenant create --data ./data --slug acme --name "ACME S.p.A." --admin-email nome@dominio --platform-admin
```

`tenant create` resta il modo per creare la prima organizzazione (e il primo account); `--platform-admin` rende superamministratore anche quell'account. Dettagli in [sviluppo/organizzazioni.md](sviluppo/organizzazioni.md).

## Aggiornamento

1. **Controllo con il binario nuovo**, a servizio ancora avviato:

   ```bash
   runuser -u regia -- /opt/regia/bin/regia.new doctor --data /var/lib/regia
   ```

   Mostra quante migrazioni si applicheranno. Si ferma con un errore se qualcosa non va: disco quasi pieno, chiave mancante o diversa, `pg_dump` assente, cluster di un'altra versione di Postgres.
2. **Sostituzione.** Il binario attuale si tiene come `regia.prev`, poi si mette il nuovo al suo posto e si riavvia il servizio.
3. **Avvio.** Se ci sono migrazioni nuove, `serve` fa il backup automatico (`auto-…`) e poi le applica. Nei log compaiono «backup automatico scritto» e «migrazioni applicate».
4. **Verifica.** `/healthz` risponde, poi `regia doctor` non segnala errori.

**Tornare indietro.**
- **Di un rilascio:** basta `regia.prev`, perché le migrazioni sono additive e retrocompatibili per un rilascio (`architecture.md` §10). `doctor` del binario vecchio lo segnala come avvertenza («migrazioni che questo binario non conosce»).
- **Di più rilasci, o dopo un problema nei dati:** ripristina il backup `auto-…` fatto prima dell'aggiornamento con `restore --force`, poi usa il binario di allora.

**Migrazioni lunghe.**
- Gli indici su tabelle grandi (`elements`, `activities`, `element_values`) si creano senza bloccare le scritture, con `create index concurrently` nelle migrazioni marcate `-- regia:no-transaction`. Il servizio resta usabile mentre l'indice si costruisce. Dettagli in `docs/sviluppo/esercizio.md`, sezione «Migrazioni».
- Le migrazioni non hanno limite di tempo, mentre le richieste hanno 15 secondi: un aggiornamento con molti dati può tenere l'avvio fermo più a lungo del solito, e il controllo di salute va atteso di conseguenza.

**Postgres di un'altra versione principale** (per esempio dalla 17 alla 18). Il cluster su disco non parte con binari di un'altra versione, e `doctor` lo segnala. Due strade:
- `regia backup` con i binari vecchi, poi `regia restore` in una cartella nuova con quelli nuovi;
- `pg_upgrade`.

## Controllo dell'istanza: `regia doctor`

```
$ regia doctor --data /var/lib/regia
[ok]        cartella     /var/lib/regia, permessi 0700
[ok]        disco        41.2 GiB liberi su 60.0 GiB (69%) in /var/lib/regia
[ok]        postgres     binari di Postgres 18
[ok]        postgres     cluster su disco di Postgres 18
[ok]        postgres     server Postgres 18.7
[ok]        orologio     allineato con il database (differenza 0s)
[ok]        migrazioni   23 applicate, nessuna in attesa (ultima 0023_report)
[ok]        chiavi       secrets.key presente (0600)
[ok]        chiavi       secrets.key decifra i segreti MFA del database
[ok]        backup       pg_dump 18.7
[ok]        backup       pg_restore 18.7
[ok]        backup       ultimo backup regia-20261124T023112Z.tar (16h5m0s fa)
```

| area | cosa controlla |
| --- | --- |
| cartella | esistenza, proprietario uguale all'utente che esegue, permessi (0700 consigliati), scrittura |
| disco | spazio libero della cartella dati e di quella dei backup: avvertenza sotto 5 GiB o il 10%, errore sotto 1 GiB o il 2% |
| postgres | versione dei binari (almeno 16, consigliata 18), versione del cluster su disco uguale a quella dei binari, versione del server |
| orologio | differenza con l'orologio del database; con systemd, sincronizzazione NTP (i codici MFA valgono 30 secondi) |
| migrazioni | applicate, in attesa, modificate (errore: l'avvio si bloccherebbe), sconosciute (avvertenza: database di una versione più recente) |
| chiavi | `secrets.key` presente, di 32 byte, 0600. Se manca e nel database ci sono segreti cifrati è un errore. Verifica anche che decifri un segreto MFA |
| backup | `pg_dump` e `pg_restore` presenti e non più vecchi del server, età dell'ultimo backup |

- **Codice di uscita:** diverso da zero se un controllo fallisce. Si può usare negli script di deploy, prima di sostituire il binario in uso.
- **Con il Postgres incluso fermo:** lo avvia per il tempo dei controlli, senza `initdb` e senza migrazioni.

## Rotazione delle chiavi

- **`oidc-state.key`** firma solo il cookie che dura il tempo di un login OIDC. Per cambiarla:
  1. ferma il servizio;
  2. cancella il file;
  3. riavvia: se ne crea una nuova.

  Si perdono soltanto i login OIDC in corso in quel momento.
- **`secrets.key`**: la rotazione automatica non c'è ancora (decisione da prendere: un comando `regia keys rotate` che ricifra MFA, webhook, integrazioni e provider AI in un'unica transazione). Oggi:
  - **per sostituirla in modo ordinato:** azzera l'MFA delle persone, che lo riattivano al primo accesso, e reimposta i segreti dei webhook e delle integrazioni. Fallo a servizio fermo, con una chiave nuova di 32 byte casuali (`head -c 32 /dev/urandom > secrets.key`, permessi 0600);
  - **se è stata esposta insieme a un dump:** stessa procedura, considerando compromessi tutti i segreti MFA e dei webhook. Avvisa i destinatari dei webhook di cambiare il segreto di verifica;
  - **dopo la rotazione:** fai subito un backup nuovo. Quelli vecchi contengono la chiave vecchia e vanno conservati o distrutti di conseguenza.
- **Passphrase dei backup:** vale per i backup nuovi (vedi «Cifratura»).
- **Credenziali del database:**
  - con il Postgres incluso non ce ne sono: si accede solo dal socket unix con permessi 0700;
  - con `DATABASE_URL` si cambiano sul server e nelle due variabili d'ambiente, poi si riavvia.

## Docker

```bash
docker build -f deploy/Dockerfile -t regia .                              # con pgvector
docker build -f deploy/Dockerfile --build-arg PGVECTOR=false -t regia .   # senza
docker run -d --name regia -p 8080:8080 -v regia-data:/data --stop-timeout 60 regia
docker exec regia regia doctor
docker exec regia regia backup                       # in /data/backups
docker cp regia:/data/backups ./backups-regia        # copia fuori dal volume
```

- **Base:** Alpine 3.24. Di Postgres 18 ci sono solo `postgres`, `initdb`, `pg_dump`, `pg_restore`, i moduli e i file di share, più le librerie a cui sono collegati (verificate con `ldd`). `pg_ctl` e `psql` non servono e non ci sono.
- **Utente:** `regia`, uid 10001, non root. Volume `/data` con permessi 0700.
- **Controllo di salute:** su `/healthz` (`wget` di busybox), con 120 secondi di avvio per `initdb`, migrazioni e backup automatico.
- **Processo 1:** `tini`. Inoltra SIGTERM a regia, che chiude le richieste (fino a 20 s) e poi ferma Postgres (fino a 20 s). Il default di Docker è 10 secondi: usa `--stop-timeout 60` o `stop_grace_period: 60s` in compose. Un arresto troppo breve fa ripartire Postgres in modalità di recupero.
- **Ripristino in un volume nuovo:**

  ```bash
  docker run --rm -v ./backups-regia:/backups:ro -v regia-nuovo:/data regia restore --in /backups/<file>
  ```

**Dimensione dell'immagine** (misurata il 2026-11-24 con Docker 29 su arm64):

| immagine | su disco | compressa (scaricata) |
| --- | --- | --- |
| prima: base `pgvector/pgvector:pg18` (Debian) | 695 MB | 167 MB |
| dopo: Alpine, con pgvector | 92 MB | 24,6 MB |
| dopo: Alpine, `PGVECTOR=false` | 91 MB | 24,5 MB |

Il binario di Regia pesa 19 MB. pgvector aggiunge appena 255 KB: per questo è incluso di default, e la variante senza serve solo a chi non vuole l'estensione.

## Codice sorgente (AGPL-3.0 §13)

Ogni pagina mostra «Regia <versione> · Codice sorgente». Il link porta al commit in esecuzione, letto da `GET /api/v1/health` (`version`, `commit`, `source`).
- **Repository:** `REGIA_SOURCE_URL`, di default `https://github.com/ivseb/regia`. Chi offre via rete una versione modificata lo punta al proprio repository, con il codice di quella versione.
- **Commit:** quello passato in compilazione (`-X main.commit=…`, `make build` usa `git rev-parse --short HEAD`). Se non è un hash, il link porta alla pagina del repository.

## Nomi storici nel database

Il database del Postgres incluso si chiama `mytrack` e i ruoli `mytrack_owner` e `mytrack_app`: vengono dal nome di lavoro del progetto. Stanno nelle migrazioni già applicate e nei dati delle istanze esistenti, quindi non cambiano col nome del prodotto. Con un Postgres esterno (`DATABASE_URL`) il ruolo applicativo resta `mytrack_app`.
