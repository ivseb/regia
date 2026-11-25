# API del budget gerarchico

Disegno: [replica/budget.md](../replica/budget.md). Questo file descrive ciò che il backend espone (passi 1-3: organizzazione, stanziamenti, fine periodo e trasferimenti). I tipi TypeScript e le chiamate sono in `web/src/api.ts` (`orgApi`, `budgetApi`).

Convenzioni:
- Tutti gli importi sono **centesimi** nella valuta base del tenant.
- Gli errori di validazione sono `422` con `field` e `detail` in italiano (da mostrare così come sono); `403` se manca il permesso, `404` se il nodo non esiste **o non si vede**.
- Le rotte `/org/*` e `/budget/*` le può chiamare ogni membro del tenant: ciò che vede lo decide il database, ciò che può fare il servizio (tabelle sotto).
- Ogni scrittura finisce nell'audit.

## Chi vede e chi può

Visibilità, applicata dalla RLS (tabelle `budget_allocations`, `budget_movements`, `allocation_transfers`) e non solo dall'API:

| profilo | vede |
| --- | --- |
| finanza (`finance.read`) | tutto, radice compresa |
| responsabile o referente finanziario di un'unità | i nodi della sua unità e di quelle sotto, e i nodi dei progetti che ne attingono |
| PM, assegnatario o sponsor di un elemento | il nodo del suo elemento |
| tutti gli altri | nessun importo (l'albero è vuoto) |

Il «responsabile» è la persona indicata come tale sull'unità (`manager_id`) o con il ruolo `manager` nell'unità; il responsabile di un'unità madre vede anche le figlie. Tariffe e costi da ore restano solo della finanza.

Scrittura:

| azione | chi |
| --- | --- |
| struttura (unità, persone, import) | `tenant.admin` o `finance.write` |
| plafond (radice), politica di fine periodo di un nodo, impostazioni, periodi, chiusura | `finance.write` |
| creare nodi, cambiare importi, ripartire sotto un nodo | `finance.write`, oppure il **responsabile** di quell'unità se `free_allocation` è attiva |
| chiedere un trasferimento | `finance.write`, oppure il responsabile di un'unità sopra o uguale al nodo di partenza (vedendo entrambi i nodi) |
| collegare un budget di progetto a un nodo | `finance.write` |

I campi `can_allocate` e `can_edit` di ogni nodo dicono già cosa può fare chi chiama.

## Organizzazione

| metodo e percorso | note |
| --- | --- |
| `GET /api/v1/org/units?q=&limit=&archived=1` | albero in ordine di visita (padre prima dei figli) con `depth`, `path`, `members`, `children`. `q` cerca in nome e codice (elenco piatto col percorso); `limit` predefinito 500 |
| `POST /api/v1/org/units` | `{name, parent_id?, kind?, code?, manager_id?}` → 201. `kind`: `legal_entity`, `business_unit`, `department` (predefinito), `cost_center`. Il codice è unico nel tenant |
| `PATCH /api/v1/org/units/{id}` | `{name?, kind?, code?, manager_id?, archived?}`; `""` toglie codice o responsabile. Non si archivia un'unità con unità attive sotto |
| `POST /api/v1/org/units/{id}/move` | `{parent_id}` (`""` = in cima). Rifiuta cicli (anche un trigger del database) |
| `GET /api/v1/org/units/{id}/members` | persone con ruolo `manager`, `finance_ref` (referente finanziario) o `member` |
| `PUT /api/v1/org/units/{id}/members/{member}` | `{role}`: aggiunge o cambia il ruolo. Risponde con l'elenco aggiornato |
| `DELETE /api/v1/org/units/{id}/members/{member}` | 204 |
| `GET /api/v1/org/people?q=&limit=` | cerca una persona (nome o email) e restituisce le sue unità con il ruolo (il `manager_id` conta come `manager`) |
| `POST /api/v1/org/import/preview` | `{csv, delimiter?}` → anteprima senza scrivere: `rows[]` con `action` (`create`, `update`, `unchanged`, `error`), `message`, i conteggi e `sha256` del file |
| `POST /api/v1/org/import/confirm` | `{csv, sha256}`: stesso file dell'anteprima; applica solo se non ci sono errori |

CSV: intestazione obbligatoria con `nome`; colonne facoltative `codice`, `padre` (codice o nome di un'unità presente o del file), `tipo` (`società`, `area`, `reparto`, `ufficio`, `centro di costo` o le chiavi inglesi), `responsabile` e `referente` (email di un membro). Le unità si riconoscono per codice (o per nome, se manca il codice); reimportare lo stesso file non duplica. Separatore `,`, `;` o tab riconosciuto da solo.

Funzioni SQL (security invoker, usabili nelle policy): `org_subtree(unit)`, `managed_org_units(member)`, `visible_org_units(member)`, `managed_elements(member)`, `app_member()`.

## Impostazioni del tenant

`GET /api/v1/budget/settings` (ogni membro), `PUT` con `finance.write` (campi assenti invariati). Sono anche in `GET /api/v1/finance/overview` (`budget_settings`, più `periods`).

| campo | predefinito |
| --- | --- |
| `period_granularity` (`year`, `semester`, `quarter`, `month`) | `year` |
| `end_policy` (`return`, `carry`, `lapse`) | `return` |
| `transfer_threshold_cents` (`clear_threshold: true` per toglierla) | nessuna |
| `transfer_body_kind` (tipo di organo di governance) | `investment_committee` |
| `free_allocation` | `true` |
| `warn_days` | 30 |

## Periodi

Uno stanziamento appartiene sempre a un periodo. Stati: `planning` → `open` → `closing` → `closed`. I periodi di un esercizio si creano da soli quando si salva un plafond (con la granularità del tenant); gli esercizi futuri nascono in `planning`.

| metodo e percorso | note |
| --- | --- |
| `GET /api/v1/budget/periods?fiscal_year_id=` | `items[]` con date, `status`, `days_to_close`, `root_id` (solo finanza) |
| `POST /api/v1/budget/fiscal-years/{id}/periods` | `{granularity?}` genera (o rigenera) i periodi; si cambia suddivisione solo se non c'è ancora nessuno stanziamento |
| `POST /api/v1/budget/periods/{id}/open` | da `planning` a `open` |
| `POST /api/v1/budget/periods/{id}/root` | `{amount_cents, reason?}` crea la radice e ne imposta il plafond |

Il plafond dell'esercizio (`fiscal_years.envelope_cents`, usato dalla graduatoria) resta uguale alla somma delle radici dei suoi periodi: con più periodi, salvare un plafond sull'esercizio lo divide in parti uguali la prima volta. Per i tenant esistenti la migrazione ha creato un periodo `open` per esercizio con la radice pari al vecchio plafond.

## Albero e nodi

`GET /api/v1/budget/tree?period_id=` → `{periods, period_id, items}`. `items` è già in ordine di visita (`depth`, `parent_id`, `children`); senza `period_id` restituisce solo i periodi. Un nodo ha:

- `kind`: `root` (plafond), `unit` o `element` (portafoglio, programma, progetto);
- `label`, `unit_code`, `manager_name`, `nature` (`capex`/`opex`), `funding_source_id`, `cost_category_id`, `end_policy` (eccezione per nodo, null = come il tenant), `note`, `archived`;
- i **numeri**, calcolati dalla stessa funzione ovunque: `allocated_cents` (stanziato = somma dei movimenti), `distributed_cents` (ripartito ai figli), `reserve_cents` (riserva = stanziato − ripartito), `committed_cents` (impegnato), `orders_cents`, `spent_cents` (speso, da `cost_rollups` nei mesi del periodo), `residual_cents` (residuo = stanziato − impegnato; negativo = sforamento), `forecast_cents`;
- `can_allocate`, `can_edit`, e per gli elementi `budgets[]` (budget di progetto che attingono dal nodo).

Definizioni: per un elemento l'impegnato è il maggiore tra il budget approvato collegato (le linee del periodo), gli ordini aperti e lo speso; per un'unità è la somma dei figli. Il forecast è la previsione pubblicata, altrimenti lo speso proiettato linearmente sul periodo, mai sotto l'impegnato; per un'unità è la somma dei figli.

| metodo e percorso | note |
| --- | --- |
| `POST /api/v1/budget/allocations` | `{period_id, parent_id, org_unit_id \| element_id, nature?, funding_source_id?, cost_category_id?, amount_cents, note?, reason?}` → 201. Un'unità sta sotto la radice o sotto una sua unità superiore; un elemento sotto un'unità o la radice; un destinatario (con natura e fonte) compare una volta per periodo |
| `GET /api/v1/budget/allocations/{id}` | il nodo con i numeri |
| `PATCH /api/v1/budget/allocations/{id}` | `{end_policy?, note?, archived?, cost_category_id?}`; `end_policy` solo la finanza (`""` = come il tenant). Si archivia solo un nodo a zero, senza figli attivi né budget collegati |
| `PUT /api/v1/budget/allocations/{id}/amount` | modifica in linea: `{amount_cents, reason?}` → `{node, parent}` (il padre con la nuova riserva; null se non è visibile a chi modifica) |
| `GET /api/v1/budget/allocations/{id}/movements` | storia del nodo: `kind` (`initial`, `allocate`, `transfer`, `return`, `carry`, `lapse`, `adjust`), `amount_cents` con segno, `balance_cents` progressivo, `reason`, chi e quando, `approval_request_id` |

Regole (le stesse del database, che le ripete a fine transazione come rete di sicurezza):
- la **somma dei figli non supera il padre**; l'importo di un nodo non è negativo;
- i movimenti sono **immutabili**: nessun UPDATE né DELETE, una correzione è un movimento di segno opposto;
- non si porta un elemento sotto il suo impegnato;
- le scritture sullo stesso albero sono serializzate (blocco del periodo): richieste concorrenti non superano mai la riserva;
- in `planning` e `open` scrivono tutti gli autorizzati, in `closing` solo la finanza, in `closed` nessuno.

## «Ripartisci»

Su un nodo di unità o sulla radice.

- `POST /api/v1/budget/allocations/{id}/distribute/preview` e `POST .../distribute` con lo stesso corpo `{mode, total_cents?, targets?, reason?}`.
- `mode`: `equal` (parti uguali, il resto in centesimi ai primi), `percent` (`targets[].percent`, somma ≤ 100, il resto resta di riserva), `previous` (importi del periodo precedente, senza i movimenti di chiusura), `requests` (in proporzione alle richieste in graduatoria dell'unità: stima alta o bassa delle richieste con `rank` e unità richiedente nel sottoalbero).
- `total_cents`: da ripartire; predefinito lo stanziato del nodo.
- `targets`: figli esistenti (`allocation_id`) o nuovi (`org_unit_id`/`element_id`, con `nature`, `funding_source_id`); se manca, i figli attuali, oppure le unità sotto quella del nodo.
- L'anteprima non scrive: `lines[]` (attuale, proposto, differenza, impegnato, avvisi, `new`), `distributed_after`, `reserve_after`, `exceeds_cents` e `valid`. La conferma ricalcola con il periodo bloccato e rifiuta (422) ciò che l'anteprima segnala come non valido (superamento del nodo, importo sotto l'impegnato o sotto il ripartito ai figli).

## Budget di progetto

- `POST /api/v1/finance/budgets` accetta `allocation_id` (nodo dell'elemento del budget, in un periodo dello stesso esercizio); revisioni e previsioni ereditano il nodo. Tutte le risposte dei budget hanno `allocation_id`.
- `PUT /api/v1/finance/budgets/{id}/allocation` `{allocation_id}` (`null` scollega), con `finance.write`.
- `GET /api/v1/budget/element-allocations?element_id=&fiscal_year_id=&budget_id=` (ogni membro) elenca le opzioni di collegamento: i nodi di un elemento (`element_id` è l'id o la chiave) nei periodi non chiusi dell'esercizio, non archiviati, **solo quelli che chi chiama vede** (la RLS). Con `budget_id` l'elemento e l'esercizio sono quelli del budget. Risposta `{items, linked_id, linked_hidden}`; `linked_hidden` dice che il budget attinge da un nodo che chi chiama non vede. Ogni voce ha `id`, `period`, `period_status`, `start_date`, `end_date`, `fiscal_year`, `path` (nomi dei nodi sopra, dalla radice, fino al primo che la RLS nasconde), `allocated_cents`, `others_cents` (impegnato dagli altri budget approvati sul nodo), `available_cents` (= stanziato − altri, può essere negativo), `mine_cents` (totale del budget nel periodo del nodo, 0 senza `budget_id`) e `linked`.
- I nodi del tree con un elemento hanno `budgets[]`: `id`, `kind`, `version`, `status`, `total_cents`, `element_key`, `element_title`, `committed_cents` (il totale se approvato e non previsione, altrimenti 0).
- L'**invio** e l'**approvazione** controllano che il totale del budget (le linee dei mesi del periodo, più quelle senza mese) stia nello stanziamento insieme agli altri budget approvati sul nodo; l'approvazione lo **impegna** (`committed_cents`). Chi decide l'approvazione non ha bisogno di vedere il nodo. Un budget senza `allocation_id` funziona come prima.

## Fine periodo

| metodo e percorso | note |
| --- | --- |
| `GET /api/v1/budget/periods/{id}/close-preview` | nodo per nodo: `policy`, `policy_source` (`tenant`/`node`), `remainder_cents`, `return_cents`, `carry_cents`, `lapse_cents`, avvisi; totali, `open_commitments`, `needs_approval`, `can_confirm`, `next_period` |
| `POST /api/v1/budget/periods/{id}/start-closing` | `open` → `closing`: da qui solo la finanza modifica gli stanziamenti |
| `POST /api/v1/budget/periods/{id}/close` | `{approve_carry?}`; con un riporto serve `approve_carry: true` (altrimenti 422) |
| `POST /api/v1/budget/periods/{id}/undo-close` | annulla l'ultima chiusura con movimenti opposti, finché il periodo successivo non ha ripartizioni (`allocate`, `transfer`, `adjust`) |
| `GET /api/v1/budget/periods/{id}/closures` | chiusure del periodo (`confirmed`/`undone`) |

Politiche, nodo per nodo dal basso: il «residuo» di un nodo è stanziato − ripartito ai figli (dopo le loro restituzioni) − impegnato proprio.
- **return** (predefinita): il residuo torna al padre (movimento `return` sul nodo, la riserva del padre cresce) e, se anche il padre chiude, sale fino alla radice, dove resta nel plafond del periodo;
- **carry**: il residuo esce dal periodo e passa allo stesso nodo del successivo (creato se manca, con i suoi antenati); escono e entrano anche gli antenati, così la regola dei figli vale in entrambi i periodi. È l'approvazione esplicita di chi chiude (`approve_carry`);
- **lapse**: come il riporto ma senza destinazione: il residuo si perde e resta nel registro (`lapse`).

Dopo la chiusura il vecchio `envelope_cents` dell'esercizio segue la somma delle radici.

## Trasferimenti

- `GET /api/v1/budget/transfers?period_id=`: quelli che si vedono (`status`: `pending`, `approved`, `rejected`; `approval_kind`: `none`, `manager`, `body`).
- `POST /api/v1/budget/transfers` `{from_allocation_id, to_allocation_id, amount_cents, reason}` → 201. Stesso periodo; i nodi non stanno uno sotto l'altro; chi chiede deve vederli entrambi (per spostare denaro tra aree che il responsabile non vede lo chiede la finanza). Disponibile = min(riserva, residuo) del nodo di partenza meno i trasferimenti in attesa.
- Approvazione, con il motore esistente (`GET /approvals/inbox`, stage «Trasferimento di stanziamento»):
  - importo sopra `transfer_threshold_cents`: l'organo `transfer_body_kind`;
  - altrimenti il responsabile del nodo comune più vicino (risalendo fino al primo nodo con un responsabile): se è chi chiede, e `free_allocation` è attiva, si applica subito; se nessuno ha un responsabile e chi chiede è la finanza, si applica subito;
  - la richiesta di approvazione si aggancia a un elemento: quello del nodo di partenza o di arrivo se è un elemento, altrimenti un portafoglio o una richiesta del tenant (serve che ne esista uno).
- All'approvazione si scrivono i movimenti `transfer` sul percorso dai due nodi fino al nodo comune (−X da una parte, +X dall'altra): nessun nodo di mezzo viola la regola dei figli.

## Avvisi

Il job giornaliero (`finance.RunDaily`, un minuto dopo l'avvio e poi ogni 24 ore) manda a ogni responsabile di un'unità con residuo, quando mancano `warn_days` giorni o meno alla fine di un periodo `open`/`closing`, una notifica di tipo `budget` (una sola per periodo e nodo): `payload` con `event: "period_closing"`, `period`, `node`, `node_id`, `residual_cents`, `days`. Testo: «Ti restano 40.000 € non impegnati in «S2 2027» (Direzione IT): il periodo si chiude tra 20 giorni». La notifica non ha elemento (`element_id` nullo).

## Passo 4: graduatoria per area, richiesta

- `GET /api/v1/portfolio/ranking?unit=&period=` (e `.csv`): restringe alle richieste dell'unità e di quelle sotto, e a quelle del periodo o senza periodo. Con unità e periodo il budget è il residuo dello stanziamento dell'unità (se lo si vede); la risposta ha `scope` e `budget_source.kind = "allocation"`.
- `PATCH /api/v1/demand/requests/{id}` accetta `requesting_unit_id` e `budget_period_id` (`""` li toglie); la vista della richiesta li restituisce con `budget_period`.
