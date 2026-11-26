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
automatica con `fixes HL-12` non esiste.

## Dati
Tabella `element_commits` (migrazione 0076): unica su (tenant, elemento, sha), inserimento idempotente.
L'autore è il login o il nome, mai l'email.

## API e scheda
`GET /api/v1/elements/{key}/commits`: i commit legati e il rilascio che li contiene (primo deploy di
produzione riuscito, o prima release, dello stesso servizio con data ≥ all'ultimo commit; lead time dal
primo commit). Vale la visibilità dell'elemento (404 per chi non lo vede). Nella scheda il blocco «Codice»
sta nella barra laterale e compare solo se ci sono commit.

## Lead time DORA
Nuovo candidato `commit`: per un deploy di produzione del servizio S al tempo T, il primo `committed_at`
di `element_commits` dello stesso servizio tra il deploy di produzione precedente (escluso, ignorando i
gemelli con la stessa versione) e T (incluso). Approssima «primo commit → rilascio che lo contiene». In
`auto` viene per primo, poi controlli e change. Valore `commit` anche tra le scelte di `lead_time_start`.

## Primo ricalcolo
Dopo ogni sincronizzazione riuscita (o parziale) il connettore chiede il ricalcolo DORA se il tenant non
ha ancora nessuna metrica (`RecomputeDoraIfEmpty`); un errore è solo un avviso nelle statistiche.
