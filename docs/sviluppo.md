# Sviluppo

## Requisiti
- Go (la versione è in `go.mod`; il toolchain si scarica da solo)
- Node 24 e pnpm (versione in `web/package.json`)
- Binari di Postgres ≥ 16 nel PATH, oppure `REGIA_PG_BIN`. Il pacchetto usa la 18, con pgvector.

## Primo avvio
```bash
make web     # compila il frontend dentro internal/webui/dist/app
make build   # binario in bin/regia
make seed    # tenant "demo" e "altro" + persone del demand (credenziali in scripts/dev-seed.sh)
make run     # http://127.0.0.1:8080
```
Persone di prova nel tenant demo: `admin@`, `richiedente@` (solo portale), `dm@` (demand manager), `sponsor@`, `pmo@`, `lead@`, `arch@`, gli sviluppatori `dev@` e `dev2@` e i valutatori `sicurezza@`, `architetture@`, `dpo@`, `compliance@demo.test`, e per rischi e incidenti `rischi@`, `rischi2@`, `incidenti@demo.test`.
Richieste di esempio in varie fasi dello stage-gate: `scripts/dev-demo-data.sh http://127.0.0.1:8080` (con il server avviato).

## Argomenti
- [Interfaccia](sviluppo/interfaccia.md): design, temi, schermate, barra della query.
- [Testi e lingue](sviluppo/testi-e-lingue.md): catalogo dei testi, aree registrate in automatico, nuove lingue, formati, testi del server.
- [Esercizio e dati](sviluppo/esercizio.md): come gira il binario, variabili d'ambiente, regole sui dati, migrazioni, Docker, comandi di amministrazione.
- [Test e benchmark](sviluppo/test.md): suite Go e frontend, e2e Playwright, benchmark.
- [Demand e richieste](sviluppo/demand.md): API del demand, processi, impatti, questionari, campi personalizzati, status report.
- [Valutazione, budget e governo](sviluppo/governance.md): graduatoria, governance, budget e costi, ondata 1 del governo, sintesi di scheda.
- [Budget collegato all'albero](sviluppo/budget-collegamento.md): «Attinge da» nel budget di un elemento, disponibile del nodo, budget di progetto nella scheda del nodo.
- [Architettura enterprise](sviluppo/architettura-ea.md): capacità, applicazioni, dipendenze, diagrammi.
- [Accessi e persone](sviluppo/accessi.md): OIDC, token API, MFA, webhook, permessi lungo i gruppi, moduli per tenant.
- [Nomi per lingua di fasi e opzioni](sviluppo/etichette-lingua.md): `labels` di fasi e opzioni, traduzioni in Campi e nell'editor dei processi.
- [Scorecard e catalogo dei requisiti](sviluppo/scorecard-catalogo.md): editor visuali, pesi, regole con il costruttore, import CSV delle voci.
- [Processi guidati](sviluppo/processi-guidati.md): diagramma adattivo, «Aggiungi un passo», priorità, mappa dei processi, rischi e incidenti a righe.
- [Cabina di portafoglio](sviluppo/cabina-portafoglio.md): graduatoria, budget dal plafond, capacità e roadmap insieme, scenari, pacchetto della seduta e verbale.
- [Ambiente di prova](sviluppo/ambiente-prova.md): tenant di prova collegato, differenze e promozione selettiva.
- [Primo avvio guidato](sviluppo/primo-avvio.md): «Inizia da qui», modelli per tipo di azienda (banca/assicurazione), cosa è applicato e ripristino, stati vuoti.
- [Modello «Persona sola / home lab»](sviluppo/modello-persona-sola.md): in un passo moduli, tipi Attività/Bug/Idea, flusso a quattro fasi, solo la priorità e una board kanban senza sprint con la chiave scelta.
- [Regole a frase](sviluppo/regole-frase.md): «Quando… solo se… allora…» nell'editor dei processi e «Quando si apre» nelle valutazioni.
- [Catalogo del portale](sviluppo/catalogo-portale.md): voci di catalogo, editor visivo del modulo, instradamento, richieste simili e una bozza per voce.
- [Percorso della richiesta](sviluppo/percorso-richiesta.md): dove siamo, chi è coinvolto, catena richiesta → impatti → epic → story, cosa succede dopo, «Cosa faccio adesso» e termini spiegati.
- [Coinvolgi un team](sviluppo/coinvolgi-team.md): procedura guidata che scrive tipo di valutazione, regola (parere e/o impatto) e nuova versione del processo, con anteprima.
- [Percorso guidato e mappa dei processi](sviluppo/percorso-guidato-mappa.md): tappe di configurazione calcolate dai dati del tenant e mappa d'insieme di tipi, fasi, team, valutazioni e approvazioni.
- [Versioni in vigore](sviluppo/versioni-vigore.md): data di entrata in vigore e di fine, deprecazione, regola di transizione per fase con anteprima, guide allegate; componente riusabile per gli altri moduli.
- [Organizzazioni](sviluppo/organizzazioni.md): il superamministratore dell'istanza crea organizzazioni dall'interfaccia e ci entra; comando `regia platform-admin`.
- [Progetti](sviluppo/progetti.md): progetto sopra le epiche, dalla richiesta approvata o da zero; piano con fasi, work package agili, a cascata o del fornitore, attività, milestone, dipendenze fs/ss/ff; cancelli di fase; avanzamento pesato e Panoramica del PM.
- [Board per team](sviluppo/board-team.md): colonne proprie con più fasi, WIP, corsie, modelli Scrum/Kanban/Supporto.
- [Tipi del lavoro](sviluppo/tipi-lavoro.md): board, backlog e «Il mio lavoro» per ogni tipo del modulo work, «Nuovo elemento» con i tipi e i campi della board, tipi di sistema archiviabili senza elementi.
- [Lavoro](sviluppo/lavoro.md): pianificazione e tempo, commenti e notifiche, work management, import, viste salvate, «Da fare», etichette nelle liste (`etichetta:`) e board paginata con tutti gli sprint.
- [Timesheet nel motore delle approvazioni](sviluppo/timesheet-approvazioni.md): settimana inviata come richiesta `timesheet` in «Le mie approvazioni», deleghe, notifiche di invio ed esito.
- [Requisiti e gate di rilascio](sviluppo/requisiti.md): requisiti, verifica, deroghe, gate; requisiti veri dalle regole dei questionari (origine `questionnaire`).
- [AI](sviluppo/ai.md): impostazioni del tenant, adattatori Anthropic e compatibile OpenAI, registro e budget, strumenti con i permessi della persona, assistente, fornitore finto ([guida per chi amministra](ai.md)).
- [DevSecOps](sviluppo/devsecops.md): vulnerabilità, connettori GitHub, GitLab e Gitea ([guida Gitea](devsecops-gitea.md)), gate, politica della pipeline, DORA.
- [Commit collegati agli elementi](sviluppo/commit-elementi.md): commit e PR unite di GitHub e Gitea con le chiavi degli spazi (`HL-12`), blocco «Codice» nella scheda, lead time DORA dal primo commit.
- [Knowledge](sviluppo/knowledge.md): documenti della knowledge base.
- [Editor Knowledge a blocchi](sviluppo/knowledge-blocchi.md): blocchi sopra il Markdown, «/», maniglia, pannello «evidenza di…», nota clean-room.
- [Rischi, conformità e crisi](sviluppo/grc.md): rischi, fornitori ICT, incidenti, conformità, vista di crisi.
- [Vista DPO](sviluppo/vista-dpo.md): registro dei trattamenti, DPIA, violazioni con le 72 ore, flussi di dati.
- [Spazio dell'auditor](sviluppo/spazio-auditor.md): incarico, campionamento, richieste di evidenza, rilievi.
- [Controlli vivi](sviluppo/controlli-vivi.md): efficacia proposta dal sistema e confermata, evidenze automatiche, rischio ↔ controlli, mappatura guidata.

Una funzione nuova si documenta in un file nuovo sotto `docs/sviluppo/`; l'indice qui sopra lo aggiorna chi unisce.
