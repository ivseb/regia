# AI: fondamenta (ondata 1), server MCP e domande sul grafo (ondata 2)

Disegno e decisioni in [replica/ai.md](../../replica/ai.md) (stato nella sezione «Stato»); guida per chi amministra in [ai.md](../ai.md) e, per collegare i client MCP, [ai-mcp.md](../ai-mcp.md). Migrazioni `0083_ai_fondamenta.sql` e `0084_ai_mcp_domande.sql`; test in `internal/itest/ai_test.go` e `internal/itest/ai_ondata2_test.go`, nei package `internal/ai/...` e in `web/e2e/ai.spec.ts` e `web/e2e/ai-ondata2.spec.ts`.

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
| `internal/ai/mcp.go` | pezzi del livello AI per il server MCP: interruttore, registro, proposte senza conversazione |
| `internal/ai/mcpserver` | server MCP con l'SDK ufficiale `github.com/modelcontextprotocol/go-sdk` |
| `internal/engine/notify/ai.go` | notifica `ai` (avviso del budget a chi amministra) |
| `internal/api/ai.go` | rotte `/api/v1/ai/...` e `/api/v1/mcp`; `aiKinds(dev)` registra i fornitori |
| `web/src/aiApi.ts`, `pages/AiConfig.tsx`, `pages/AiUsage.tsx`, `pages/Assistente.tsx` | interfaccia (`askStream` legge lo streaming) |

## Dati (migrazione 0083)

- `ai_settings` (già nella 0001, una riga per tenant): `enabled`, `mask_personal_data`, `monthly_budget_usd`, `monthly_token_budget`, `retention_days` (90), `fast_provider_id`/`fast_model`, `deep_provider_id`/`deep_model`, `routes` (funzione → `{"tier"}` o `{"provider_id","model"}`), `budget_warned_month`. La colonna `provider` della 0001 non si usa più.
- `ai_providers`: tipo (`anthropic`, `openai_compat`, `fake`), nome, `base_url`, `secret_enc`, `no_tools_models`, prezzi per milione di token.
- `ai_calls` (già nella 0001) con in più `tier`, `cost_usd`, `duration_ms`, `error_code`, `conversation_id`.
- `ai_conversations`, `ai_messages` (solo domanda e risposta, con `refs`, proposte e strumenti usati), `ai_proposals`.

Tutte con la RLS per tenant. Le conversazioni e le proposte si cancellano con il membro.

Migrazione 0084: `ai_settings.mcp_enabled` (interruttore del server MCP, spento), `ai_calls.cache_read_tokens` e `cache_write_tokens` (cache del prompt), indice sulle proposte senza conversazione (quelle dei client MCP).

## Regole

- **Spenta di default.** Senza riga in `ai_settings` valgono i valori di partenza: spenta, 90 giorni, mascheramento acceso solo se il tenant è **regolamentato** (una normativa di tipo `regulation` o `directive` attiva in `tenant_frameworks`).
- **Chiavi.** `secretbox` con la chiave applicativa e il contesto `ai-provider:<id>`, come i token dei connettori (`integration:<id>`). L'API restituisce solo `has_key`; l'audit (`ai.provider.*`) registra solo `"key": true`. Gli errori del fornitore si traducono in `ProviderError` con un codice breve, senza la risposta grezza.
- **L'adattatore Anthropic non legge l'ambiente** (`option.WithoutEnvironmentDefaults()`): chiave e indirizzo arrivano solo dal tenant. Ragionamento adattivo dei modelli Claude 5 regolato con `output_config.effort` (`high` per l'approfondito, `low` per il rapido); i blocchi nativi della risposta tornano invariati nel giro successivo (`Message.Native`).
- **Rete.** Il client HTTP è quello dei connettori (`ghapi.NewHTTPClient`): con `--dev` qualsiasi indirizzo; con `REGIA_CONNECTOR_ALLOW_PRIVATE` anche la rete interna; altrimenti solo indirizzi pubblici in `https`. Un fornitore con indirizzo locale (`localhost`, `.lan`, `.local`, `.home.arpa`, IP privati) è `local`: i dati non escono e il mascheramento non serve.
- **Livelli e funzioni.** `Features` elenca le funzioni con il livello predefinito (attive `assistente` e, dall'ondata 2, `domande_grafo`, che non chiama strumenti). `Resolve(feature)`: modello preciso della funzione, altrimenti il suo livello; un livello vuoto usa l'altro.
- **Strumenti.** `Target.Tools` è falso se il modello è in `no_tools_models`: l'assistente non riceve strumenti e lo dice nel prompt e nell'interfaccia (`status.tools`).
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

## Cache del prompt e streaming (Anthropic)

- **Cache.** L'adattatore mette `cache_control` (effimero, 5 minuti) in cima a ogni richiesta (`MessageNewParams.CacheControl`): l'API marca da sola l'ultimo blocco, così strumenti, istruzioni e storia restano in cache e il giro successivo con gli strumenti rilegge il prefisso. Le istruzioni non hanno parti variabili (niente data, niente id): non cambiarle per domanda, o la cache si invalida. `Response.CacheReadTokens` e `CacheWriteTokens` arrivano da `usage.cache_read_input_tokens` e `cache_creation_input_tokens`; `InputTokens` resta il totale. `CostCached` stima lettura al 10% e scrittura al 125% del prezzo dell'input; il registro ha le due colonne.
- **Streaming.** Un adattatore che implementa `ai.Streamer` (`ChatStream(ctx, req, onText)`) passa i pezzi del testo mentre arrivano: Anthropic usa `Messages.NewStreaming` e ricompone il messaggio con `Message.Accumulate` (blocchi nativi e input degli strumenti compresi); il finto manda una parola alla volta (`fakeai.StreamPause`). Chi non lo implementa (compatibile OpenAI, per ora) risponde tutto insieme e il servizio passa il testo intero alla fine. `Service.CallStream` registra come `Call`.
- **Eventi dell'assistente.** `AskStream(…, emit)` manda `text` (pezzi già smascherati: un segnaposto spezzato aspetta di essere completo), `tool` (strumento usato) e `step` (il testo finora era un passaggio prima degli strumenti, si ricomincia). `POST /api/v1/ai/ask` con `Accept: text/event-stream` risponde in SSE: `start`, gli eventi, poi `done` con lo stesso corpo della risposta JSON oppure `error` con il problema (stesso formato degli errori). La scadenza di scrittura del server non vale per lo streaming.

## Domande sul grafo (`internal/ai/graph.go`)

`GraphQuestion` risolve la funzione `domande_grafo`, prepara le istruzioni (sintassi del linguaggio di query, esempi, formato `QUERY:`/`SPIEGAZIONE:`) e il **catalogo del tenant** fra `<dati origine="catalogo">` (tipi di elemento, campi di `Engine.QueryFields` con i valori degli elenchi), chiama il modello senza strumenti e prova la query con `Engine.Search` (limite 1) e i permessi della persona. Se il motore risponde con un `QueryError`, il modello riceve la query, il messaggio e il frammento sbagliato e corregge **una volta**; poi la risposta dice se la query è valida (`valid`, `corrected`, `first_query`, `first_error`, `error`). Le query non si salvano: la persona le salva come vista (pagina `domande`, elenco `/elenco/domande`). Mascheramento come per l'assistente.

## Server MCP (`internal/ai/mcpserver`)

- **SDK.** `github.com/modelcontextprotocol/go-sdk` (v1.8.0): `mcp.NewStreamableHTTPHandler` **senza sessioni** (`Stateless`, risposte JSON), un `mcp.Server` per richiesta con gli strumenti di chi chiama. `DisableLocalhostProtection`: l'accesso è il token nell'intestazione, non un cookie, e la protezione rifiuterebbe le richieste di un proxy inverso sulla stessa macchina.
- **Accesso.** `/api/v1/mcp` (`api.MCPPath`), solo con un token API (`Authorization: Bearer myt_…`; la sessione del browser non basta: 401 con `WWW-Authenticate`). Il token vale nel suo tenant con i permessi attuali del membro ridotti a quelli del token. Il middleware lascia passare i POST dei token di sola lettura solo su questo percorso: il server espone loro solo le letture (e `proposte`). Spento (`ai_settings.mcp_enabled`): 403 `ai.mcp_disabled`.
- **Strumenti.** Quelli di `tools.Services.List()` così come sono, con le annotazioni MCP (`readOnlyHint` per le letture), più `proposte` (lettura), `conferma` (`destructiveHint`) e `rifiuta`. Le letture restituiscono il testo dello strumento (dati fra `<dati>`) con i link `…/e/<chiave>` e `structuredContent.elementi`. Le scritture fanno `Prepare` e salvano una proposta **senza conversazione** (`ProposeMCP`): la risposta dice l'identificativo e che non è applicata. `conferma` chiama `Service.Decide`, che rifà `Apply` con i permessi di adesso.
- **Registro.** Ogni chiamata è una riga di `ai_calls` con `feature = 'mcp'`, `provider = 'client'`, `model = 'strumento:<nome>'`, zero token e costo, l'impronta dell'input, gli elementi citati, la durata e l'esito (`error_code`: `input`, `not_found`, `forbidden`, `validation`…). Le decisioni entrano come `accepted_by_user`/`rejected_by_user` con `model = 'proposta:<strumento>'`.
- **Conservazione.** Le proposte MCP ancora lì dopo `retention_days` giorni dalla creazione si cancellano con le conversazioni scadute.
- **OAuth** per i client MCP non c'è ancora (ondata 3): i client che lo richiedono passano da `mcp-remote` con l'intestazione (vedi [ai-mcp.md](../ai-mcp.md)).

## API

Lettura e modifica delle impostazioni: `tenant.admin`; le modifiche solo da una sessione (non con un token API), come le integrazioni.

- `GET /api/v1/ai/status` (tutti): `enabled`, `ready`, `tools`, `model`, `masked`, `budget`, `retention_days`.
- `GET|PUT /api/v1/ai/settings`: impostazioni, fornitori (senza chiavi), tipi disponibili con i modelli proposti, funzioni, budget.
- `POST /api/v1/ai/providers`, `PATCH|DELETE /api/v1/ai/providers/{id}`, `GET /api/v1/ai/providers/{id}/models` (verifica la chiave o l'indirizzo e elenca i modelli).
- `GET /api/v1/ai/usage?month=AAAA-MM`: totali, per funzione, per persona, per modello, ultime 100 chiamate.
- Assistente (tutti, con i propri permessi): `POST /api/v1/ai/ask` con `{"question", "conversation_id"}` (in SSE con `Accept: text/event-stream`), `GET /api/v1/ai/conversations[/{id}]`, `DELETE /api/v1/ai/conversations/{id}`, `POST /api/v1/ai/proposals/{id}/confirm|reject`.
- Domande sul grafo (tutti): `POST /api/v1/ai/graph` con `{"question"}` → `{query, explanation, valid, corrected, first_query, first_error, error, status}`.
- Server MCP: `/api/v1/mcp` (protocollo MCP, trasporto HTTP «streamable»), solo con un token API.
- Impostazioni: `mcp_enabled` in `GET|PUT /api/v1/ai/settings`.

Errori: `ai.disabled` (409), `ai.not_configured` (409), `ai.budget_exhausted` (429), `ai.provider_error` (502, `params.reason` con il codice del fornitore), `ai.mcp_disabled` (403), `auth.token_required` (401, server MCP senza token), più i codici di validazione `ai.*`.

## Fornitore finto

Registrato solo con `regia serve --dev` e nei test (`aiKinds(dev)`), come i finti connettori. Con un modello qualunque segue regole semplici, utili agli e2e: «commenta DEM-1: testo» propone un commento, una chiave nella domanda si legge, altrimenti cerca le parole; dopo i risultati elenca gli elementi trovati. Con il modello `scenario:<nome>` segue le risposte programmate:

```go
fakeai.Program("maschera",
	fakeai.Step{Calls: []fakeai.Call{{Name: "leggi", Input: map[string]any{"elemento": "DEM-3"}}}},
	fakeai.Step{Text: "Il referente è [persona-1].", InputTokens: 80, OutputTokens: 5})
// ... impostazioni con deep.model = "scenario:maschera"
reqs := fakeai.Requests("maschera") // ciò che è «uscito» verso il fornitore
```

`Step.Err` simula un errore del fornitore, `Step.Refuse` un rifiuto. Con le istruzioni delle domande sul grafo (`fakeai.GraphMarker`) il finto a regole traduce: la query fra apici inversi nella domanda, se c'è; altrimenti parole note («richieste», «aperte», «applicazioni»…) diventano clausole. «critiche» produce apposta un campo inesistente, che il giro di correzione toglie. Il finto implementa anche `ai.Streamer`.

## Conservazione

`Service.Run` (avviato da `serve.go`, ogni ora) chiama `PurgeExpired`: cancella in ogni tenant le conversazioni ferme da più di `retention_days` giorni (con messaggi e proposte). Il registro delle chiamate resta: non ha il contenuto.

## Provare

```bash
go test ./internal/ai/... && go test ./internal/itest -run 'TestAIFondamenta|TestAIMCP|TestAIOndata2'
cd web && pnpm exec playwright test e2e/ai.spec.ts e2e/ai-ondata2.spec.ts
```

In sviluppo: Configurazione → Intelligenza artificiale → Aggiungi fornitore → «Fornitore finto», scegli il livello approfondito, accendi l'AI e apri «Chiedi all'assistente» dal menu (anche in modo «Domanda sul grafo»).

Il server MCP in sviluppo, con un token creato nel profilo e l'interruttore acceso:

```bash
curl -s http://127.0.0.1:8080/api/v1/mcp -H "Authorization: Bearer $REGIA_TOKEN" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Oppure con l'ispettore ufficiale: `npx @modelcontextprotocol/inspector`, trasporto «Streamable HTTP», indirizzo `/api/v1/mcp` e l'intestazione `Authorization`.
