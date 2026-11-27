# AI: fondamenta (ondata 1), server MCP e domande sul grafo (ondata 2), assistente nella scheda (ondata 3), wiki personale (ondata 4)

Disegno e decisioni in [replica/ai.md](../../replica/ai.md) (stato nella sezione «Stato»); guida per chi amministra in [ai.md](../ai.md) e, per collegare i client MCP, [ai-mcp.md](../ai-mcp.md). Migrazioni `0083_ai_fondamenta.sql`, `0084_ai_mcp_domande.sql`, `0086_ai_scheda.sql` e `0087_ai_wiki.sql`; test in `internal/itest/ai_test.go`, `ai_ondata2_test.go`, `ai_scheda_test.go`, `ai_wiki_test.go` e `query_relazioni_test.go`, nei package `internal/ai/...` e in `web/e2e/ai.spec.ts`, `ai-ondata2.spec.ts`, `ai-scheda.spec.ts` e `ai-wiki.spec.ts`.

## Pezzi

| Dove | Cosa |
|---|---|
| `internal/ai/model.go` | interfaccia unica del modello: `Provider` (`Chat`, `Models`), messaggi neutri, `ToolSpec`, `Kind` (tipo di fornitore registrato), `ProviderError` |
| `internal/ai/anthropic` | adattatore Anthropic con l'SDK ufficiale `github.com/anthropics/anthropic-sdk-go/v2`; modelli proposti `claude-haiku-5-5` (rapido) e `claude-opus-5-5` (approfondito) |
| `internal/ai/openaicompat` | adattatore generico Chat Completions (OpenAI, Mistral, vLLM, LM Studio, Ollama), solo libreria standard; `GET /models` elenca i modelli installati |
| `internal/devtools/fakeai` | fornitore finto, senza rete né chiavi: a regole o programmabile con `Program` |
| `internal/ai/settings.go` | impostazioni del tenant, fornitori, livelli, funzioni, risoluzione funzione → modello |
| `internal/ai/service.go` | `Resolve` e `Call`: budget prima, registro dopo, sempre |
| `internal/ai/budget.go` | listino, costo stimato, budget del mese, riepilogo del consumo |
| `internal/ai/mask.go` | mascheramento reversibile dei dati personali |
| `internal/ai/tools` | strumenti con i permessi della persona (gli stessi del server MCP) |
| `internal/ai/assistant.go` | assistente: conversazioni, giro con gli strumenti, proposte, conservazione; `AskStream` con gli eventi |
| `internal/ai/graph.go` | domande sul grafo: traduzione in query, prova con il motore, una correzione |
| `internal/ai/card.go` | assistente nella scheda: riassunto, mancanze, bozze (`Card`), inserimento (`Insert`), analisi alla creazione (`Triage`), asset accettati (`Links`) |
| `internal/engine/query_rel.go` | clausola di relazione e sottoquery del linguaggio di query; relazioni dagli impatti (`impatta`, `impattata_da`, `impatti` su `ext_impact`, dall'ondata 4) |
| `internal/ai/wiki` | wiki personale: pagine, revisioni (istantanee), annullamento e ripristino, condivisione, fonti a mano, stato dell'aggiornamento, collegamenti (chiavi e codici), pagine pertinenti, esportazione, conservazione; non chiama il modello |
| `internal/ai/wikiai.go` | aggiornamento della wiki con il modello (`UpdateWiki`), lavoro in background (`RunWiki`), conservazione (`PurgeWiki`), contesto per l'assistente (`wikiContext`), attore di un membro (`MemberActor`) |
| `internal/api/wiki.go` | rotte `/api/v1/wiki/...` |
| `internal/ai/mcp.go` | pezzi del livello AI per il server MCP: interruttore, registro, proposte senza conversazione |
| `internal/ai/mcpserver` | server MCP con l'SDK ufficiale `github.com/modelcontextprotocol/go-sdk` |
| `internal/engine/notify/ai.go` | notifica `ai` (avviso del budget a chi amministra) |
| `internal/api/ai.go` | rotte `/api/v1/ai/...` e `/api/v1/mcp`; `aiKinds(dev)` registra i fornitori |
| `web/src/aiApi.ts`, `pages/AiConfig.tsx`, `pages/AiUsage.tsx`, `pages/Assistente.tsx` | interfaccia (`askStream` legge lo streaming) |
| `web/src/wikiApi.ts`, `pages/Wiki.tsx`, `pages/WikiAdd.tsx` | wiki nel profilo (`/profilo/wiki`, `/profilo/wiki/$id`) e «Aggiungi alla wiki» (scheda e risposte del pannello) |
| `web/src/pages/CardAssistant.tsx`, `pages/CreateAssistant.tsx` | riquadro nella scheda (caricato solo con le funzioni pronte) e analisi nel form della nuova richiesta |

## Dati (migrazione 0083)

- `ai_settings` (già nella 0001, una riga per tenant): `enabled`, `mask_personal_data`, `monthly_budget_usd`, `monthly_token_budget`, `retention_days` (90), `fast_provider_id`/`fast_model`, `deep_provider_id`/`deep_model`, `routes` (funzione → `{"tier"}` o `{"provider_id","model"}`), `budget_warned_month`. La colonna `provider` della 0001 non si usa più.
- `ai_providers`: tipo (`anthropic`, `openai_compat`, `fake`), nome, `base_url`, `secret_enc`, `no_tools_models`, prezzi per milione di token.
- `ai_calls` (già nella 0001) con in più `tier`, `cost_usd`, `duration_ms`, `error_code`, `conversation_id`.
- `ai_conversations`, `ai_messages` (solo domanda e risposta, con `refs`, proposte e strumenti usati), `ai_proposals`.

Tutte con la RLS per tenant. Le conversazioni e le proposte si cancellano con il membro.

Migrazione 0086: `ai_proposals.feature` (funzione della proposta: `bozze` o `classificazione`; nulla = assistente o client MCP). Migrazione 0085 (registro di audit, non solo AI): l'unicità di `audit_log` è per catena, `(tenant_id, seq, at) nulls not distinct`, al posto della chiave primaria `(at, seq)` che faceva scontrare due tenant con lo stesso numero nello stesso microsecondo (due organizzazioni create insieme davano 500; `internal/itest/audit_chiave_test.go`).

Migrazione 0084: `ai_settings.mcp_enabled` (interruttore del server MCP, spento), `ai_calls.cache_read_tokens` e `cache_write_tokens` (cache del prompt), indice sulle proposte senza conversazione (quelle dei client MCP).

Migrazione 0087 (wiki personale): `wiki_pages` (titolo unico per persona fra le pagine non tolte, `version`, `updated_by` persona/assistente, `shared`, `promoted_key`, `deleted_at`), `wiki_revisions` (una riga per modifica con l'istantanea dopo la modifica: titolo, testo, `deleted`; `op` crea/modifica/elimina/ripristina/annulla, `author`, `note`, righe `added`/`removed`, `restored_version`), `wiki_sources` (aggiunte a mano: elemento o risposta, nota, `used_at`), `wiki_state` (per persona: `auto`, `requested_at`, `running_until`, esito dell'ultimo aggiornamento). Oltre a `enable_tenant_rls` ogni tabella ha **politiche restrittive sul membro** (`app_member()`, cioè `app.member_id`): si legge e si scrive solo il proprio; `wiki_pages` lascia leggere anche le pagine `shared` non tolte a qualunque membro. Le transazioni della wiki vanno aperte con `db.TenantOpts.Member` (`Service.WikiTx`): senza membro non si vede niente. Trigger `members_wiki_forget` (security definer): il membro anonimizzato perde pagine, fonti e stato; le chiavi esterne cancellano tutto con il membro.

## Regole

- **Spenta di default.** Senza riga in `ai_settings` valgono i valori di partenza: spenta, 90 giorni, mascheramento acceso solo se il tenant è **regolamentato** (una normativa di tipo `regulation` o `directive` attiva in `tenant_frameworks`).
- **Chiavi.** `secretbox` con la chiave applicativa e il contesto `ai-provider:<id>`, come i token dei connettori (`integration:<id>`). L'API restituisce solo `has_key`; l'audit (`ai.provider.*`) registra solo `"key": true`. Gli errori del fornitore si traducono in `ProviderError` con un codice breve, senza la risposta grezza.
- **L'adattatore Anthropic non legge l'ambiente** (`option.WithoutEnvironmentDefaults()`): chiave e indirizzo arrivano solo dal tenant. Ragionamento adattivo dei modelli Claude 5 regolato con `output_config.effort` (`high` per l'approfondito, `low` per il rapido); i blocchi nativi della risposta tornano invariati nel giro successivo (`Message.Native`).
- **Rete.** Il client HTTP è quello dei connettori (`ghapi.NewHTTPClient`): con `--dev` qualsiasi indirizzo; con `REGIA_CONNECTOR_ALLOW_PRIVATE` anche la rete interna; altrimenti solo indirizzi pubblici in `https`. Un fornitore con indirizzo locale (`localhost`, `.lan`, `.local`, `.home.arpa`, IP privati) è `local`: i dati non escono e il mascheramento non serve.
- **Livelli e funzioni.** `Features` elenca le funzioni con il livello predefinito (attive `assistente`, dall'ondata 2 `domande_grafo`, dall'ondata 3 `riassunti` rapido, `mancanze` approfondito, `bozze` approfondito e `classificazione` rapido, che non chiamano strumenti; dall'ondata 4 `wiki` rapido, con strumenti). `Status.features` dice quali funzioni della scheda sono pronte, per mostrare solo i pulsanti che funzionano, e `wiki` se il suo modello sa usare gli strumenti. `Resolve(feature)`: modello preciso della funzione, altrimenti il suo livello; un livello vuoto usa l'altro.
- **Strumenti.** `Target.Tools` è falso se il modello è in `no_tools_models`: l'assistente non riceve strumenti e lo dice nel prompt e nell'interfaccia (`status.tools`).
- **Conteggio dei token prima della chiamata.** Con un budget impostato `CallStream` conta i token dell'input: con un adattatore che implementa `ai.TokenCounter` (Anthropic: `Messages.CountTokens` dell'SDK ufficiale, `/v1/messages/count_tokens`, stessi modello, istruzioni, strumenti e messaggi; il finto: la stessa stima di `Chat`, o i token programmati dello scenario), altrimenti, o se il conteggio non riesce, con `EstimateTokens` (quattro caratteri per token, per eccesso). Se il costo dell'input da solo o i token porterebbero oltre il limite, la chiamata non parte: `refused`/`budget` con i token contati nel registro e l'errore `ai.budget_insufficient` (429). Senza budget non si conta niente.
- **Budget e registro.** `Call` legge il consumo del mese (fuso del tenant) da `ai_calls`; dal 100% (dollari o token) non chiama e registra `refused`/`budget`. Ogni chiamata, riuscita o no, scrive una riga con persona, funzione, fornitore, modello, livello, token, costo stimato, durata, esito, codice d'errore, conversazione, elementi citati e SHA-256 del prompt. Al primo superamento dell'80% nel mese resta una voce `ai.budget.warning` nell'audit e parte una notifica di tipo `ai` (evento `budget_warning`, con `percent` e `month`) a chi ha `tenant.admin` a livello di tenant (`notify.AdminsSQL`, `notify.EmitDirect`: rispetta le preferenze e le email). Le decisioni sulle proposte entrano come `accepted_by_user`/`rejected_by_user` (fornitore `regia`, zero token).
- **Mascheramento.** `Masker` sostituisce nomi delle persone del tenant, email, telefoni, IBAN e codici fiscali con `[persona-1]`, `[email-2]`… prima di ogni chiamata a un fornitore esterno; gli input degli strumenti e la risposta tornano smascherati. La tabella vive solo in memoria per una domanda.
- **Contenuto come dato.** I risultati degli strumenti stanno fra `<dati origine="…">` e `</dati>` (`tools.Data`, che neutralizza i tag simili nel contenuto); il prompt di sistema dice che quel contenuto non si segue mai come istruzione.

## Strumenti (`internal/ai/tools`)

`Services{DB, Engine, Collab, Work, Arch}` e `Env{TenantID, Actor}`. Ogni strumento apre una transazione del tenant con l'attore della persona e passa dai servizi: `Engine.Search` (RLS e `CanRead`), `Engine.Get`, `ElementLinks`, `Collab.Comments`; per le scritture `Work.CreateItem`, `Engine.Update` (con la versione letta alla proposta), `Arch.CreateRelation`, `Collab.AddComment`, `Work.LogTime` (con il costo in una transazione finanza separata, come l'API).

| Nome | Tipo | Input |
|---|---|---|
| `cerca` | lettura | `testo`, `limite` |
| `query` | lettura | `query` nel linguaggio di Regia, `limite` |
| `leggi` | lettura | `elemento` (chiave o codice) |
| `crea` | proposta | `spazio`, `tipo`, `titolo`, `testo`, `genitore` |
| `aggiorna` | proposta | `elemento`, `titolo`, `testo` |
| `collega` | proposta | `tipo`, `origine`, `destinazione` |
| `commenta` | proposta | `elemento`, `testo` |
| `registra_ore` | proposta | `elemento`, `durata`, `data`, `nota` |

`Prepare` esegue le letture e, per le scritture, controlla e descrive la proposta senza scrivere; `Apply` esegue la scrittura confermata (di nuovo con i permessi di chi conferma). Un input sbagliato è un `InputError`, che il modello legge per correggersi. Il server MCP dell'ondata 2 userà `List`, `Prepare` e `Apply` così come sono.

## Cache del prompt e streaming

- **Cache.** L'adattatore mette `cache_control` (effimero, 5 minuti) in cima a ogni richiesta (`MessageNewParams.CacheControl`): l'API marca da sola l'ultimo blocco, così strumenti, istruzioni e storia restano in cache e il giro successivo con gli strumenti rilegge il prefisso. Le istruzioni non hanno parti variabili (niente data, niente id): non cambiarle per domanda, o la cache si invalida. `Response.CacheReadTokens` e `CacheWriteTokens` arrivano da `usage.cache_read_input_tokens` e `cache_creation_input_tokens`; `InputTokens` resta il totale. `CostCached` stima lettura al 10% e scrittura al 125% del prezzo dell'input; il registro ha le due colonne.
- **Streaming.** Un adattatore che implementa `ai.Streamer` (`ChatStream(ctx, req, onText)`) passa i pezzi del testo mentre arrivano: Anthropic usa `Messages.NewStreaming` e ricompone il messaggio con `Message.Accumulate` (blocchi nativi e input degli strumenti compresi); il finto manda una parola alla volta (`fakeai.StreamPause`). Il compatibile OpenAI usa `chat/completions` con `stream: true` e `stream_options.include_usage`: legge gli eventi `data: {chunk}` fino a `data: [DONE]`, ricompone le chiamate di strumenti per indice e prende l'uso dei token dall'ultimo pezzo; un servizio che ignora lo streaming e risponde in JSON si legge come `Chat`, uno che si interrompe prima della fine è un errore di rete. Chi non implementa `Streamer` risponde tutto insieme e il servizio passa il testo intero alla fine. `Service.CallStream` registra come `Call`.
- **Eventi dell'assistente.** `AskStream(…, emit)` manda `text` (pezzi già smascherati: un segnaposto spezzato aspetta di essere completo), `tool` (strumento usato) e `step` (il testo finora era un passaggio prima degli strumenti, si ricomincia). `POST /api/v1/ai/ask` con `Accept: text/event-stream` risponde in SSE: `start`, gli eventi, poi `done` con lo stesso corpo della risposta JSON oppure `error` con il problema (stesso formato degli errori). La scadenza di scrittura del server non vale per lo streaming.

## Domande sul grafo (`internal/ai/graph.go`)

`GraphQuestion` risolve la funzione `domande_grafo`, prepara le istruzioni (sintassi del linguaggio di query con le relazioni e le sottoquery, esempi, formato `QUERY:`/`SPIEGAZIONE:`) e il **catalogo del tenant** fra `<dati origine="catalogo">` (tipi di elemento, campi di `Engine.QueryFields` con i valori degli elenchi, tipi di relazione di `Engine.QueryRelations` con i nomi nei due versi e, dall'ondata 4, le relazioni dagli impatti di `engine.ImpactRelations`), chiama il modello senza strumenti e prova la query con `Engine.Search` (limite 1) e i permessi della persona. Se il motore risponde con un `QueryError`, il modello riceve la query, il messaggio e il frammento sbagliato e corregge **una volta**; poi la risposta dice se la query è valida (`valid`, `corrected`, `first_query`, `first_error`, `error`). Le query non si salvano: la persona le salva come vista (pagina `domande`, elenco `/elenco/domande`). Mascheramento come per l'assistente.

## Assistente nella scheda (`internal/ai/card.go`)

- **Contesto.** `loadCard` legge l'elemento con i permessi della persona (`Engine.Get`, `FieldViews`, `ElementLinks`, `Collab.Comments`, gli ultimi 30 con alias `c1`, `c2`…) e lo scrive come testo fra `<dati origine="CHIAVE">`; i valori dei campi a elenco e i riferimenti diventano nomi. Il modello non riceve strumenti; le istruzioni (`cardSystem` più il compito) non hanno parti variabili, per la cache del prompt. Mascheramento come per l'assistente.
- **Riassunto** (`riassunti`): testo; le chiavi del contesto citate diventano `refs` per i link.
- **Mancanze** (`mancanze`): `regiaGaps` trova da sé campi obbligatori adesso e vuoti, descrizione vuota o sotto i 40 caratteri, nessun collegamento, una richiesta senza impatti; il modello riceve queste fra `<dati origine="già trovate">` e risponde in JSON (`{"mancanze":[{"tipo","testo","campo","commento","elemento"}]}`, al massimo 8). Campo, alias del commento ed elemento si verificano sul contesto: se non esistono la voce resta senza link e `unverified` lo conta.
- **Bozze** (`bozze`): `descrizione`, `commento`, `risposta` (al richiedente: Regia aggiunge `@Nome` di chi ha aperto l'elemento, così la menzione notifica), con `hint` facoltativo fra `<dati origine="indicazioni della persona">`. `Insert` riceve il testo riletto e lo applica con `proposeAndApply`: `Prepare` dello strumento (`commenta` o `aggiorna`) con i permessi della persona, proposta salvata con `feature = 'bozze'`, `Decide` con la conferma. Nel registro la decisione è `accepted_by_user` con la funzione della proposta; `PendingMCP` esclude le proposte con una funzione.
- **Alla creazione** (`classificazione`): `similar` trova i candidati con `similarity`/`word_similarity` di `pg_trgm` sul titolo, le parole significative in alternativa (`websearch_to_tsquery` con `or`) sul `search` e, per gli asset, il nome citato nel testo; ogni candidato passa da `Engine.Get` con i permessi della persona. Il modello riceve nuovo elemento, simili, campi a elenco del tipo (più il driver per le richieste) e asset logici, e risponde in JSON; Regia tiene solo chiavi fra i candidati e valori fra le opzioni (restituiti come id dell'opzione, come li vuole il form). `Links` cita gli asset accettati dopo il salvataggio con `Arch.CiteAsset` (regole di «Cita un asset»: chi analizza, il richiedente, il business owner), con l'audit `ai.triage.link` e la decisione nel registro (`proposta:cita_asset`).
- **API:** `POST /api/v1/ai/card` `{element, action: riassunto|mancanze|bozza, draft, hint}`, `POST /api/v1/ai/card/insert` `{element, draft, text}` → proposta, `POST /api/v1/ai/card/triage` `{type, title, description}` → `{duplicates, fields, links, candidates, unverified}`, `POST /api/v1/ai/card/links` `{element, links}` → `{items: [{key, ok, error}]}`. Card e triage non pubblicano l'evento «changed» (letture in POST).
- **Altri form (seguito, ondata 4):** `CreateAssistant` è anche nel dialogo `NewCard` della board e del backlog (duplicati e campi a elenco del tipo; i valori accettati entrano in `fields` della creazione) e in `NewApplication` dell'architettura (solo duplicati: senza `onAccept` e `onLinks` il riquadro non mostra campi né asset). Il servizio è lo stesso (`POST /api/v1/ai/card/triage` con il tipo); test `TestAITriageAltriTipi`.
- **Interfaccia:** `ElementPage` carica `CardAssistant` (lazy) solo se `status.features` ha una funzione della scheda; i link portano a `#campo-vista-<chiave>`, `#descrizione-scheda` e `#c-<id del commento>` (scheda Attività). `NewRequest` mostra `CreateAssistant`; i valori accettati entrano nello stato del form, gli asset spuntati si citano dopo la creazione.

## Wiki personale (`internal/ai/wiki`, `internal/ai/wikiai.go`)

- **Revisioni come istantanee.** `wiki.Save` crea o modifica (con `BaseVersion` per il conflitto: 409 `wiki.conflict`; nessuna revisione se niente cambia), `Delete` toglie (`deleted_at`, la pagina resta nella storia), `Restore(version)` torna allo stato di una versione con una revisione nuova (`version` 0 = prima della creazione: la pagina si toglie), `Undo(revisione)` è `Restore(v-1)` e vale solo per l'ultima modifica della pagina (409 `wiki.undo_not_latest`). Le righe aggiunte e tolte si calcolano con `knowledge.Diff` alla scrittura; `wiki.Diff` confronta una versione con la precedente per l'interfaccia.
- **Collegamenti.** `wiki.Tokens` trova chiavi (`DEM-12`) e codici del catalogo (`APP-MOB`, `CRM.CORE`) fuori dai blocchi di codice; `wiki.Refs` li risolve con `engine.Get` e i permessi di chi legge. Nel client `linkCodes` trasforma i codici risolti in link Markdown verso la chiave, poi `PageMarkdown` (lo stesso di Knowledge) rende la pagina.
- **Condivisione e promozione.** `SetShared` (solo il proprietario: `ErrNotOwner`, 403, per gli altri); l'API mostra le pagine condivise solo a chi ha `element.read` (non ai richiedenti del portale). `POST /wiki/pages/{id}/promote` chiama `knowledge.CreatePage` con l'attore della persona nello spazio scelto, ricorda `promoted_key` e scrive l'audit `wiki.promote`.
- **«Aggiungi alla wiki»** (`wiki.Add`): un elemento (letto con `engine.Get`: 404 se la persona non lo vede) o un testo (risposta dell'assistente, citata con `>`), in fondo alla pagina scelta o a quella con il titolo dato (creata se manca); è una modifica della persona e lascia una riga in `wiki_sources` per il prossimo aggiornamento.
- **Aggiornamento** (`Service.UpdateWiki(tenant, membro, force)`, funzione `wiki`, livello rapido). `wiki.Claim` prende lo stato (`running_until`, 10 minuti: due aggiornamenti della stessa persona non si sovrappongono, `ai.wiki_running`), `MemberActor` ricostruisce l'attore come alla sessione (ruoli diretti e dei gruppi a livello di tenant; membro non attivo = niente). Fonti dopo `last_run_at` (tutte le più recenti con `force`): elementi non bozza creati dalla persona o seguiti senza silenzio (`watchers`), ognuno passato da `engine.Get` con i suoi permessi (al massimo 20), le domande e risposte delle sue conversazioni (12), le aggiunte a mano non usate (20). Senza fonti l'aggiornamento finisce con `nulla` **senza chiamare il modello**. Il modello riceve istruzioni fisse (`wikiSystem`, con `WikiMarker`) e un messaggio con data, elenco delle pagine e fonti, ognuna fra `<dati origine="…">`; strumenti: quelli di lettura di `tools.Services` (cerca, query, leggi, wiki) più `wiki_scrivi` (solo qui, non nel server MCP). `wiki_scrivi` crea o riscrive tutta una pagina per titolo come «assistente», al massimo 8 per aggiornamento, e rifiuta una pagina modificata dalla persona dopo l'inizio. Dieci giri al massimo, l'ultimo senza strumenti. Mascheramento come per l'assistente (input degli strumenti smascherati prima di salvare). Esito in `wiki_state` (`ok`, `nulla`, `errore` con il codice: `ai.budget_exhausted`, `ai.not_configured`, `ai.wiki_no_tools`…); il registro delle chiamate ha una riga per giro con funzione `wiki` e gli elementi delle fonti.
- **Lavoro in background.** `RunWiki` (avviato da `serve.go`, ogni 5 minuti) chiede al pool proprietario le persone da aggiornare: chi ha `requested_at` («Aggiorna adesso»), e chi ha `auto` acceso senza aggiornamenti da `WikiMinGap` (un'ora), nei tenant attivi con l'AI accesa e con il membro attivo; poi lavora per ciascuna con le transazioni del membro. `POST /api/v1/wiki/refresh` controlla che la funzione sia pronta, segna `requested_at` e avvia subito `UpdateWiki` in una goroutine (risposta 202); l'interfaccia rilegge lo stato ogni 1,5 secondi finché l'aggiornamento è in corso. La coda `jobs` della 0001 è del ruolo di piattaforma e non è usata dal ruolo applicativo: come gli altri lavori periodici (`RunSLA`, `RunDeadlines`…) la wiki usa un ciclo in-process con lo stato nella sua tabella.
- **Contesto per l'assistente.** `wikiContext(tx, membro, testo, chiavi)` legge le pagine della persona e sceglie con `wiki.Pertinent` (10 punti per chiave in comune, 3 per parola del testo nel titolo, 1 nel corpo; parole di almeno quattro lettere, senza le più comuni) fino a `WikiContextChars` (6000 caratteri, circa 1500 token) e cinque pagine, tagliando l'ultima; il risultato è un blocco `<dati origine="wiki personale">`. Entra nel messaggio della persona in `AskStream` e `GraphQuestion` (prima della domanda, solo nella richiesta al modello: la conversazione salvata ha la domanda com'è) e in fondo al contesto della scheda (`Card`, con la chiave dell'elemento e dei collegamenti). Le istruzioni non cambiano, quindi la cache del prompt resta valida. Le transazioni di lettura di queste funzioni ora hanno il membro.
- **Strumento `wiki`** in `tools.Services` (lettura): senza input elenca le pagine della persona, con `pagina` ne legge il testo (fra `<dati origine="wiki: Titolo">`), con `testo` cerca. Il server MCP lo espone così com'è, anche ai token di sola lettura; legge solo le pagine di chi chiama, mai quelle condivise da altri.
- **Conservazione.** `PurgeWiki` (nel ciclo orario di `Run`): per ogni persona con storia o fonti, con `retention_days` del tenant, cancella le pagine tolte da più giorni, le revisioni più vecchie tranne l'ultima di ogni pagina e le fonti usate. `GET /api/v1/wiki/export` restituisce tutte le pagine in un file Markdown (non esiste un'esportazione generale dei dati della persona: questa è la sua).
- **Eventi.** Le scritture su `/api/v1/wiki/...` non pubblicano l'evento «changed» (riguardano solo la persona), tranne la promozione a Knowledge.
- **Fornitore finto.** Con `ai.WikiMarker` nelle istruzioni il finto a regole scrive «Le mie priorità» con gli elementi delle fonti, una pagina per ogni progetto e il «Registro delle decisioni» con le frasi delle conversazioni che parlano di una decisione; dopo i risultati conclude con il numero di pagine. Per le altre funzioni toglie dai messaggi il blocco «wiki personale» prima di applicare le regole.

### API della wiki (chiunque abbia un membro nel tenant, con i propri permessi)

`GET /api/v1/wiki` (pagine, condivise con me, stato, `ai.ready`, `context_tokens`), `GET /wiki/log`, `GET /wiki/export`, `PUT /wiki/settings` `{auto}`, `POST /wiki/refresh` (202), `POST /wiki/add` `{element | text, page | title, note}`, `POST /wiki/pages` `{title, body}`, `GET|PUT|DELETE /wiki/pages/{id}` (`PUT` con `base_version`), `GET /wiki/pages/{id}/history`, `GET /wiki/pages/{id}/versions/{n}` (confronto con la precedente), `POST /wiki/pages/{id}/restore` `{version}`, `POST /wiki/pages/{id}/share` `{shared}`, `POST /wiki/pages/{id}/promote` `{space}`, `POST /wiki/revisions/{id}/undo`. Errori `wiki.*` (`not_found`, `conflict`, `not_owner`, `title_taken`, `title_invalid`, `body_invalid`, `too_many`, `version_invalid`, `undo_not_latest`, `add_invalid`, `text_invalid`, `space_required`, `no_member`) e `ai.wiki_running`, `ai.wiki_no_tools`.

## Server MCP (`internal/ai/mcpserver`)

- **SDK.** `github.com/modelcontextprotocol/go-sdk` (v1.8.0): `mcp.NewStreamableHTTPHandler` **senza sessioni** (`Stateless`, risposte JSON), un `mcp.Server` per richiesta con gli strumenti di chi chiama. `DisableLocalhostProtection`: l'accesso è il token nell'intestazione, non un cookie, e la protezione rifiuterebbe le richieste di un proxy inverso sulla stessa macchina.
- **Accesso.** `/api/v1/mcp` (`api.MCPPath`), solo con un token API (`Authorization: Bearer myt_…`; la sessione del browser non basta: 401 con `WWW-Authenticate`). Il token vale nel suo tenant con i permessi attuali del membro ridotti a quelli del token. Il middleware lascia passare i POST dei token di sola lettura solo su questo percorso: il server espone loro solo le letture (e `proposte`). Spento (`ai_settings.mcp_enabled`): 403 `ai.mcp_disabled`.
- **Strumenti.** Quelli di `tools.Services.List()` così come sono, con le annotazioni MCP (`readOnlyHint` per le letture), più `proposte` (lettura), `conferma` (`destructiveHint`) e `rifiuta`. Le letture restituiscono il testo dello strumento (dati fra `<dati>`) con i link `…/e/<chiave>` e `structuredContent.elementi`. Le scritture fanno `Prepare` e salvano una proposta **senza conversazione** (`ProposeMCP`): la risposta dice l'identificativo e che non è applicata. `conferma` chiama `Service.Decide`, che rifà `Apply` con i permessi di adesso.
- **Registro.** Ogni chiamata è una riga di `ai_calls` con `feature = 'mcp'`, `provider = 'client'`, `model = 'strumento:<nome>'`, zero token e costo, l'impronta dell'input, gli elementi citati, la durata e l'esito (`error_code`: `input`, `not_found`, `forbidden`, `validation`…). Le decisioni entrano come `accepted_by_user`/`rejected_by_user` con `model = 'proposta:<strumento>'`.
- **Conservazione.** Le proposte MCP ancora lì dopo `retention_days` giorni dalla creazione si cancellano con le conversazioni scadute.
- **OAuth** per i client MCP non c'è ancora (rimandato dopo l'ondata 3): i client che lo richiedono passano da `mcp-remote` con l'intestazione (vedi [ai-mcp.md](../ai-mcp.md)).

## API

Lettura e modifica delle impostazioni: `tenant.admin`; le modifiche solo da una sessione (non con un token API), come le integrazioni.

- `GET /api/v1/ai/status` (tutti): `enabled`, `ready`, `tools`, `model`, `masked`, `budget`, `retention_days`.
- `GET|PUT /api/v1/ai/settings`: impostazioni, fornitori (senza chiavi), tipi disponibili con i modelli proposti, funzioni, budget.
- `POST /api/v1/ai/providers`, `PATCH|DELETE /api/v1/ai/providers/{id}`, `GET /api/v1/ai/providers/{id}/models` (verifica la chiave o l'indirizzo e elenca i modelli).
- `GET /api/v1/ai/usage?month=AAAA-MM`: totali, per funzione, per persona, per modello, ultime 100 chiamate.
- Assistente (tutti, con i propri permessi): `POST /api/v1/ai/ask` con `{"question", "conversation_id"}` (in SSE con `Accept: text/event-stream`), `GET /api/v1/ai/conversations[/{id}]`, `DELETE /api/v1/ai/conversations/{id}`, `POST /api/v1/ai/proposals/{id}/confirm|reject`.
- Assistente nella scheda (tutti, con i propri permessi): `POST /api/v1/ai/card`, `/card/insert`, `/card/triage`, `/card/links` (vedi sopra).
- Domande sul grafo (tutti): `POST /api/v1/ai/graph` con `{"question"}` → `{query, explanation, valid, corrected, first_query, first_error, error, status}`.
- Server MCP: `/api/v1/mcp` (protocollo MCP, trasporto HTTP «streamable»), solo con un token API.
- Impostazioni: `mcp_enabled` in `GET|PUT /api/v1/ai/settings`.

Errori: `ai.disabled` (409), `ai.not_configured` (409), `ai.budget_exhausted` (429), `ai.budget_insufficient` (429, la chiamata da sola supererebbe il budget), `ai.provider_error` (502, `params.reason` con il codice del fornitore), `ai.mcp_disabled` (403), `auth.token_required` (401, server MCP senza token), più i codici di validazione `ai.*`.

## Fornitore finto

Registrato solo con `regia serve --dev` e nei test (`aiKinds(dev)`), come i finti connettori. Con un modello qualunque segue regole semplici, utili agli e2e: «commenta DEM-1: testo» propone un commento, una chiave nella domanda si legge, altrimenti cerca le parole; dopo i risultati elenca gli elementi trovati. Con il modello `scenario:<nome>` segue le risposte programmate:

```go
fakeai.Program("maschera",
	fakeai.Step{Calls: []fakeai.Call{{Name: "leggi", Input: map[string]any{"elemento": "DEM-3"}}}},
	fakeai.Step{Text: "Il referente è [persona-1].", InputTokens: 80, OutputTokens: 5})
// ... impostazioni con deep.model = "scenario:maschera"
reqs := fakeai.Requests("maschera") // ciò che è «uscito» verso il fornitore
```

`Step.Err` simula un errore del fornitore, `Step.Refuse` un rifiuto. Con le istruzioni della scheda («Lavori sulla scheda di un elemento») il finto a regole risponde ai compiti (`CardSummaryMarker`, `CardGapsMarker`, `CardTriageMarker`, bozze): riassunto con chiave, titolo, fase e il primo collegamento; una domanda aperta per ogni commento con «?» (e un elemento inventato se il contesto contiene «inventa»); duplicati con almeno due parole del titolo in comune, il valore di un campo se il testo nomina l'opzione, gli asset nominati nel testo. Implementa anche `ai.TokenCounter`. Con le istruzioni delle domande sul grafo (`fakeai.GraphMarker`) il finto a regole traduce: la query fra apici inversi nella domanda, se c'è; altrimenti parole note («richieste», «aperte», «applicazioni»…) diventano clausole. «critiche» produce apposta un campo inesistente, che il giro di correzione toglie. Il finto implementa anche `ai.Streamer`.

## Conservazione

`Service.Run` (avviato da `serve.go`, ogni ora) chiama `PurgeExpired`: cancella in ogni tenant le conversazioni ferme da più di `retention_days` giorni (con messaggi e proposte). Poi `PurgeWiki` applica lo stesso numero di giorni alla storia e alle pagine tolte delle wiki (sezione «Wiki personale»). Il registro delle chiamate resta: non ha il contenuto.

## Provare

```bash
go test ./internal/ai/... && go test ./internal/itest -run 'TestAIFondamenta|TestAIMCP|TestAIOndata2|TestAIScheda|TestAIWiki|TestAITriageAltriTipi|TestQueryRelazioni|TestQueryImpatti|TestAuditChiavePerTenant'
cd web && pnpm exec playwright test e2e/ai.spec.ts e2e/ai-ondata2.spec.ts e2e/ai-scheda.spec.ts e2e/ai-wiki.spec.ts
```

In sviluppo: Configurazione → Intelligenza artificiale → Aggiungi fornitore → «Fornitore finto», scegli il livello approfondito, accendi l'AI e apri «Chiedi all'assistente» dal menu (anche in modo «Domanda sul grafo»).

Il server MCP in sviluppo, con un token creato nel profilo e l'interruttore acceso:

```bash
curl -s http://127.0.0.1:8080/api/v1/mcp -H "Authorization: Bearer $REGIA_TOKEN" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Oppure con l'ispettore ufficiale: `npx @modelcontextprotocol/inspector`, trasporto «Streamable HTTP», indirizzo `/api/v1/mcp` e l'intestazione `Authorization`.
