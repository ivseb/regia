# AI, ondata 1: le fondamenta

Disegno e decisioni in [replica/ai.md](../../replica/ai.md) (stato nella sezione «Stato»); guida per chi amministra in [ai.md](../ai.md). Migrazione `0083_ai_fondamenta.sql`; test in `internal/itest/ai_test.go`, nei package `internal/ai/...` e in `web/e2e/ai.spec.ts`.

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
| `internal/ai/tools` | strumenti con i permessi della persona (gli stessi del server MCP dell'ondata 2) |
| `internal/ai/assistant.go` | assistente: conversazioni, giro con gli strumenti, proposte, conservazione |
| `internal/api/ai.go` | rotte `/api/v1/ai/...`; `aiKinds(dev)` registra i fornitori |
| `web/src/aiApi.ts`, `pages/AiConfig.tsx`, `pages/AiUsage.tsx`, `pages/Assistente.tsx` | interfaccia |

## Dati (migrazione 0083)

- `ai_settings` (già nella 0001, una riga per tenant): `enabled`, `mask_personal_data`, `monthly_budget_usd`, `monthly_token_budget`, `retention_days` (90), `fast_provider_id`/`fast_model`, `deep_provider_id`/`deep_model`, `routes` (funzione → `{"tier"}` o `{"provider_id","model"}`), `budget_warned_month`. La colonna `provider` della 0001 non si usa più.
- `ai_providers`: tipo (`anthropic`, `openai_compat`, `fake`), nome, `base_url`, `secret_enc`, `no_tools_models`, prezzi per milione di token.
- `ai_calls` (già nella 0001) con in più `tier`, `cost_usd`, `duration_ms`, `error_code`, `conversation_id`.
- `ai_conversations`, `ai_messages` (solo domanda e risposta, con `refs`, proposte e strumenti usati), `ai_proposals`.

Tutte con la RLS per tenant. Le conversazioni e le proposte si cancellano con il membro.

## Regole

- **Spenta di default.** Senza riga in `ai_settings` valgono i valori di partenza: spenta, 90 giorni, mascheramento acceso solo se il tenant è **regolamentato** (una normativa di tipo `regulation` o `directive` attiva in `tenant_frameworks`).
- **Chiavi.** `secretbox` con la chiave applicativa e il contesto `ai-provider:<id>`, come i token dei connettori (`integration:<id>`). L'API restituisce solo `has_key`; l'audit (`ai.provider.*`) registra solo `"key": true`. Gli errori del fornitore si traducono in `ProviderError` con un codice breve, senza la risposta grezza.
- **L'adattatore Anthropic non legge l'ambiente** (`option.WithoutEnvironmentDefaults()`): chiave e indirizzo arrivano solo dal tenant. Ragionamento adattivo dei modelli Claude 5 regolato con `output_config.effort` (`high` per l'approfondito, `low` per il rapido); i blocchi nativi della risposta tornano invariati nel giro successivo (`Message.Native`).
- **Rete.** Il client HTTP è quello dei connettori (`ghapi.NewHTTPClient`): con `--dev` qualsiasi indirizzo; con `REGIA_CONNECTOR_ALLOW_PRIVATE` anche la rete interna; altrimenti solo indirizzi pubblici in `https`. Un fornitore con indirizzo locale (`localhost`, `.lan`, `.local`, `.home.arpa`, IP privati) è `local`: i dati non escono e il mascheramento non serve.
- **Livelli e funzioni.** `Features` elenca le funzioni con il livello predefinito (`assistente` è l'unica attiva nell'ondata 1). `Resolve(feature)`: modello preciso della funzione, altrimenti il suo livello; un livello vuoto usa l'altro.
- **Strumenti.** `Target.Tools` è falso se il modello è in `no_tools_models`: l'assistente non riceve strumenti e lo dice nel prompt e nell'interfaccia (`status.tools`).
- **Budget e registro.** `Call` legge il consumo del mese (fuso del tenant) da `ai_calls`; dal 100% (dollari o token) non chiama e registra `refused`/`budget`. Ogni chiamata, riuscita o no, scrive una riga con persona, funzione, fornitore, modello, livello, token, costo stimato, durata, esito, codice d'errore, conversazione, elementi citati e SHA-256 del prompt. Al primo superamento dell'80% nel mese resta una voce `ai.budget.warning` nell'audit. Le decisioni sulle proposte entrano come `accepted_by_user`/`rejected_by_user` (fornitore `regia`, zero token).
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

## API

Lettura e modifica delle impostazioni: `tenant.admin`; le modifiche solo da una sessione (non con un token API), come le integrazioni.

- `GET /api/v1/ai/status` (tutti): `enabled`, `ready`, `tools`, `model`, `masked`, `budget`, `retention_days`.
- `GET|PUT /api/v1/ai/settings`: impostazioni, fornitori (senza chiavi), tipi disponibili con i modelli proposti, funzioni, budget.
- `POST /api/v1/ai/providers`, `PATCH|DELETE /api/v1/ai/providers/{id}`, `GET /api/v1/ai/providers/{id}/models` (verifica la chiave o l'indirizzo e elenca i modelli).
- `GET /api/v1/ai/usage?month=AAAA-MM`: totali, per funzione, per persona, per modello, ultime 100 chiamate.
- Assistente (tutti, con i propri permessi): `POST /api/v1/ai/ask` con `{"question", "conversation_id"}`, `GET /api/v1/ai/conversations[/{id}]`, `DELETE /api/v1/ai/conversations/{id}`, `POST /api/v1/ai/proposals/{id}/confirm|reject`.

Errori: `ai.disabled` (409), `ai.not_configured` (409), `ai.budget_exhausted` (429), `ai.provider_error` (502, `params.reason` con il codice del fornitore), più i codici di validazione `ai.*`.

## Fornitore finto

Registrato solo con `regia serve --dev` e nei test (`aiKinds(dev)`), come i finti connettori. Con un modello qualunque segue regole semplici, utili agli e2e: «commenta DEM-1: testo» propone un commento, una chiave nella domanda si legge, altrimenti cerca le parole; dopo i risultati elenca gli elementi trovati. Con il modello `scenario:<nome>` segue le risposte programmate:

```go
fakeai.Program("maschera",
	fakeai.Step{Calls: []fakeai.Call{{Name: "leggi", Input: map[string]any{"elemento": "DEM-3"}}}},
	fakeai.Step{Text: "Il referente è [persona-1].", InputTokens: 80, OutputTokens: 5})
// ... impostazioni con deep.model = "scenario:maschera"
reqs := fakeai.Requests("maschera") // ciò che è «uscito» verso il fornitore
```

`Step.Err` simula un errore del fornitore, `Step.Refuse` un rifiuto.

## Conservazione

`Service.Run` (avviato da `serve.go`, ogni ora) chiama `PurgeExpired`: cancella in ogni tenant le conversazioni ferme da più di `retention_days` giorni (con messaggi e proposte). Il registro delle chiamate resta: non ha il contenuto.

## Provare

```bash
go test ./internal/ai/... && go test ./internal/itest -run TestAIFondamenta
cd web && pnpm exec playwright test e2e/ai.spec.ts
```

In sviluppo: Configurazione → Intelligenza artificiale → Aggiungi fornitore → «Fornitore finto», scegli il livello approfondito, accendi l'AI e apri «Chiedi all'assistente» dal menu.
