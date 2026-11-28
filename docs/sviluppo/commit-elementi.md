# Commit → elemento e lead time dai commit

Nato dal pilota «Home lab»: i connettori leggevano release, deploy e controlli ma non i commit, quindi
«HL-12» in un messaggio non legava nulla e il lead time DORA restava vuoto.

## Lettura dei commit (GitHub e Gitea)
- Pacchetto `internal/modules/devsecops/connectors/commitsync`: la logica dei cursori è una sola, i
  connettori forniscono solo la lettura (`Source`). GitLab resta fuori.
- Per ogni repository associato: commit del branch predefinito dal cursore (`since`), più le pull request
  chiuse e unite (per data di modifica, più recenti del cursore). Il commit di merge della PR
  (`merge_commit_sha`) si lega agli elementi citati nel titolo o nel nome del branch.
- Cursore per repository (`connector_repositories.cursors.commits`: `at`, `sha`, `prs_at`), salvato nella
  stessa transazione dei commit: un errore non lo fa avanzare. Prima lettura: 90 giorni fa; `Full` ignora il
  cursore. Tetti per giro: 1000 commit e 250 PR (oltre, avviso: i più vecchi non si leggono).
- Un errore nella lettura dei commit (repository vuoto, Gitea vecchio) è un avviso: release e controlli
  proseguono, il cursore dei commit resta dov'era.
- Nuovo ambito `Commits` nello `Scope` (etichetta «commit») e contatore `commits` in `Stats` (collegamenti
  nuovi creati).

## Riconoscimento delle chiavi
`contract.CommitKeys`: `\b([A-Z][A-Z0-9]{1,19})-([0-9]+)\b` sul messaggio intero (commit) o su titolo e branch
(PR); il trattino basso conta come separatore (`HL-12_fix`). `IngestCommits` tiene solo le coppie con uno
spazio di quella chiave e un elemento con quel numero non cancellato. Più chiavi, più righe. La chiusura
con `fixes HL-12` è descritta più sotto.

## Dati
Tabella `element_commits` (migrazione 0076): unica su (tenant, elemento, sha), inserimento idempotente.
L'autore è il login o il nome, mai l'email.

## API e scheda
`GET /api/v1/elements/{key}/commits`: i commit legati e il rilascio che li contiene, con lo stesso calcolo
di «In produzione dal…» (sotto) e `release_id` per il link alla pagina del rilascio; lead time dal primo
commit. Vale la visibilità dell'elemento (404 per chi non lo vede). Nella scheda il blocco «Codice» sta
nella barra laterale e compare solo se ci sono commit.

## Lead time DORA
Nuovo candidato `commit`: per un deploy di produzione del servizio S al tempo T, il primo `committed_at`
di `element_commits` dello stesso servizio tra il deploy di produzione precedente (escluso, ignorando i
gemelli con la stessa versione) e T (incluso). Approssima «primo commit → rilascio che lo contiene». In
`auto` viene per primo, poi controlli e change. Valore `commit` anche tra le scelte di `lead_time_start`.

## Primo ricalcolo
Dopo ogni sincronizzazione riuscita (o parziale) il connettore chiede il ricalcolo DORA se il tenant non
ha ancora nessuna metrica (`RecomputeDoraIfEmpty`); un errore è solo un avviso nelle statistiche.

## Rilascio → elementi
`GET /api/v1/devsecops/releases/{id}` e la pagina `/rilasci/$id` (link da «Ultimi rilasci» in `/devsecops`
e dal blocco «Codice»). File: `release_items.go`, `web/src/pages/Rilascio.tsx`.
- **Finestra** `(from, to]` sul servizio e sull'ambiente del rilascio. `to` è il primo deploy riuscito di
  questa versione nell'ambiente (o la data del rilascio, se viene prima): un gemello ridistribuito dopo
  non allarga la finestra. `from` è l'ultimo deploy riuscito (o rilascio) di un'**altra** versione prima
  di `to`; senza, la finestra è aperta (primo rilascio del servizio in quell'ambiente).
- **Elementi**: quelli con commit di quel servizio nella finestra, con tipo, stato, numero di commit e
  `continues` (ha commit successivi: il lavoro prosegue). Tetto 500. Chi non legge un elemento
  (`CanRead`) non lo vede: resta solo il numero (`hidden`).
- **Gruppi delle note** (`noteGroup`, dal tipo): novità (story, epic, feature, change_request,
  business_request, requirement), correzioni (bug, incident, problem, defect, vulnerability), altro.
- **Note di rilascio**: Markdown generato nel client (`web/src/releaseNotes.ts`, nella lingua di chi le
  guarda), da copiare o scaricare in `.md`.
- **Riscrivi per il business** (`POST /api/v1/ai/release-notes`, `internal/ai/release_notes.go`): con la
  funzione Bozze accesa. Il client manda solo l'id del rilascio e le indicazioni; gli elementi li legge il
  server con la visibilità di chi chiede. È una proposta: si copia, si scarica o si scarta, non si salva.

## «In produzione dal…»
`GET /api/v1/elements/{ref}/production` (`ProductionOf`). Un elemento con commit è in produzione quando,
per **ogni** servizio su cui ha commit, c'è un deploy di produzione riuscito (o un rilascio di produzione)
con data ≥ dell'ultimo commit su quel servizio, di una versione che non era già in produzione prima di
quel commit (un gemello non conta). La data è la più recente fra i servizi; `pending` elenca i servizi
che mancano.
- `self`: l'elemento stesso, se ha commit.
- In salita: i discendenti per `parent_id` (richiesta → impatti → epic → story, fino a 5 livelli), non
  annullati, con almeno un commit e visibili. `total`, `in_production`, `last_at`. Il denominatore sono
  gli elementi **con codice**: una story di sola analisi non tiene la richiesta sotto il 100%.
- Dove si vede: nella riga «Il lavoro» del pannello Percorso (richiesta e impatto), nella sezione del
  lavoro dell'epic (`web/src/pages/InProduzione.tsx`) e, per la story, nel blocco «Codice».

## Chiusura con le parole chiave
`fixes`, `fixed`, `fix`, `closes`, `closed`, `close`, `resolves`, `resolved`, `resolve` seguite dalla chiave
(`fixes HL-12`, `Closes: HL-12`), nel messaggio del commit o nel titolo della PR unita
(`contract.CloseKeys`). `fix(HL-12):` non chiude. **Spenta per default.**
- Impostazioni: `devsecops_release_settings.close_on_keywords` per il tenant; `devsecops_space_close`
  forza uno spazio a sì o a no (senza riga eredita). API `GET/PUT /api/v1/devsecops/release-settings`,
  in Configurazione → DevSecOps, sezione «Rilasci».
- Scatta solo su un collegamento **nuovo** (una rilettura non richiude un elemento riaperto a mano) e se
  l'elemento non è già chiuso o annullato.
- Il passaggio è del processo (`engine.Transition`, mai i passaggi di sistema): fra quelli disponibili e
  non bloccati verso una fase di categoria «done», se ce n'è uno solo (o uno solo in avanti) si esegue con
  il commento «Chiuso dal commit abc1234 (fixes HL-12)», in un savepoint. Valgono guardie e campi
  obbligatori.
- Storia: voce `commit/closed` con sha, link e parola chiave. Se il passaggio non è possibile (nessuno, più
  d'uno, o rifiutato dal motore) resta la voce `commit/close_skipped` (motivo `not_allowed` o `ambiguous`)
  e non succede altro: la sincronizzazione prosegue.

## Certificazione del rilascio
Documento di una pagina per numero di change: `/change/$numero/certificazione` (link stabile da mettere
nella change; vale anche un alias del numero), `GET/POST /api/v1/devsecops/changes/{number}/certification`.
File: `certification.go`, `web/src/pages/Certificazione.tsx`. Il PDF è la stampa del browser.
- Sezioni (configurabili e ordinabili, `cert_sections`): change e richiesta; rilasci della change;
  elementi (dai rilasci di produzione, o da tutti se non ce ne sono, senza doppioni); test; Sonar; CVE al
  rilascio; decisione del gate (ultima per servizio, ambiente e versione, in sola lettura) e approvazioni
  dichiarate.
- Test: `TestRun` con `origin`.
  - «pipeline»: i controlli del rilascio con nome da test (`test`, `unit`, `e2e`, `*tests`…); i numeri si
    leggono dalla sintesi («120 passed, 2 skipped»).
  - «alm» (e domani «native», «import»): una riga per test set del numero di change, dalle tabelle generiche
    dei test (`certification_tests.go`, migrazione 0120), con pianificati, bloccati e non eseguiti; sotto, i
    difetti aperti per gravità (`defects`). Vedi [devsecops-alm.md](../devsecops-alm.md).
  - Sonar: controlli con «sonar» o «quality gate» nel nome.
- «Genera» (`canCertify`: configurazione DevSecOps o `devsecops.write`, quindi anche la pipeline) congela i
  dati dal vivo in `release_certifications` (versione crescente, JSON, SHA-256, autore); le versioni
  precedenti restano e si aprono con `?versione=N`. Un change senza rilasci non si certifica.
- Evidenza GRC: alla generazione, se in Configurazione → Gate di rilascio c'è un controllo mappato sulla
  sorgente `certification`, nasce un'evidenza automatica (collettore `devsecops.certification`,
  idempotente per change e versione). Senza mappatura non nasce nulla e la pagina lo dice.
  Con un controllo mappato sulla sorgente `tests` nasce anche l'evidenza dell'esito dei test del change
  (collettore `devsecops.tests`).
- Stato e approvazione della change da ServiceNow: si aggiungono quando la sessione ServiceNow avrà
  fissato i campi di `changes`.

## Dati (migrazione 0105)
`devsecops_release_settings` (chiusura e sezioni della certificazione), `devsecops_space_close`
(eccezioni per spazio), `release_certifications` (istantanee). Tutte con RLS.
