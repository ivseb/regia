# Catalogo del portale (ondata 3)

Il portale dei richiedenti (`/portale`) mostra un **catalogo di voci**: carte con nome, descrizione, icona generica e
categoria, con ricerca. Ogni voce crea un elemento di un tipo, con il suo modulo a passi e le sue regole di
instradamento. Si configura in Configurazione → Portale (`/configurazione/portale`, `tenant.admin` o `portfolio.admin`).

## Modello

- Tabella `portal_entries` (migrazione `0067`, RLS come le vicine): `key`, `label`, `description`, `category`, `icon`,
  `type_key`, `position`, `active`, `group_id` (visibilità per gruppo, sotto-gruppi compresi), `form` (pubblicato),
  `draft` (modifiche non pubblicate).
- Il modulo (`EntryForm`, `internal/modules/report/portal_catalog.go`): testo introduttivo, domande del titolo e della
  descrizione, **passi** (`title`, `intro`, `fields`) e **regole** (`routes`). Un campo (`FormField`) è un campo del tipo
  o, solo per le richieste, un dato della domanda (`std`); ha etichetta per il richiedente, testo di aiuto, obbligo
  e condizione di visibilità (`visible_when`, il linguaggio di condizioni di sempre, costruito con `ConditionBuilder`).
- **Senza voci salvate** il catalogo è quello di prima: una voce per ogni tipo esposto, con chiave uguale a quella del
  tipo e il modulo di sempre («Tempi e contesto», «Altri dettagli»). Alla prima modifica queste voci si salvano, così
  non spariscono. I passi con `preset` `context` o `details` e titolo vuoto prendono il titolo tradotto di prodotto.
- Una **bozza per persona e voce** (`portal_drafts.entry_key`, indice univoco). Nel catalogo, la carta con bozza offre
  «Riprendi la bozza» e «Ricomincia».

## Modifica → anteprima → pubblica

Nome, descrizione, categoria, icona, gruppo e attivazione valgono subito (`PUT /portal/catalog/{key}`). Il modulo e
l'instradamento vanno in bozza (stessa chiamata, campo `form`) e si pubblicano con `POST …/publish`; `DELETE …/draft`
scarta la bozza. L'anteprima (`POST …/preview`) risolve il modulo in modifica nello stesso formato che vede il
richiedente (`EntryView`): l'editor e il wizard condividono `web/src/pages/PortalForm.tsx`. Chi chiede vede sempre e
solo la versione pubblicata. Sul telefono l'editor ha due schede, «Modulo» e «Anteprima».

L'invio (`SubmitDraft`) controlla la bozza contro il modulo pubblicato: scarta i campi nascosti dalla condizione, esige
quelli obbligatori visibili (`validation.portal.field_required`) e crea l'elemento del tipo della voce.

## Instradamento

Dopo l'invio, ogni regola la cui condizione è vera sull'elemento (fatti del motore: campi, fase, tipo…) assegna un
**ruolo** (quelli esistenti) al **referente di un team** (`teams.lead_id`) o a un **gruppo**, e può aggiungere una
**priorità** (bassa, media, alta) e un'**etichetta** iniziale, che sono etichette (`tags`) dell'elemento. Non c'è un
motore nuovo: la condizione è `engine.ParseCondition`, l'assegnazione è `engine.SetStakeholder`. Ogni regola applicata
lascia una voce nella storia dell'elemento. Le regole valgono per tutte quelle vere, nell'ordine.

## Richieste simili

Mentre si scrive il titolo, il riquadro propone le richieste aperte simili. «È questa» (`POST /portal/support/{ref}`)
aggiunge la persona come richiedente e un commento alla richiesta esistente, invece di un duplicato; «No, è diversa» la
nasconde. L'unità precompilata dal profilo (radice esclusa) c'era già dell'ondata 2; qui la fase mostrata al richiedente non ha più il «3/9».

## Errori

I codici `validation.portal.*` stanno in `web/src/i18n/{it,en}/errors-portale.ts`.
