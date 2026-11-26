# Timesheet nel motore delle approvazioni

Le regole di prodotto sono in `replica/work.md` (sezione timesheet) e non cambiano: nessuno approva il proprio
timesheet, per respingere serve il motivo, la settimana respinta torna modificabile, quella inviata si può
richiamare, quella approvata la riapre solo un amministratore del tempo (`tenant.admin` o `finance.write`).
Cambia il modo in cui sono realizzate.

## Richiesta di approvazione

- Migrazione `0074_timesheet_approvazioni.sql`: `approval_requests.element_id` diventa facoltativo. Il vincolo
  `approval_requests_subject_element` lo impone obbligatorio per tutti i soggetti tranne `timesheet`, dove sono
  obbligatori `timesheet_member_id` e `timesheet_week` (lunedì). Una sola richiesta aperta per persona e settimana
  (indice univoco parziale). RLS invariata.
- Un passo solo, con `approver_pool = true`: gli approvatori sono righe di `approval_step_members` e **ne basta
  uno** (negli altri passi più righe con lo stesso ordinale vogliono tutti). `candidatesSQL` e
  `approval_refresh_assignees` leggono l'insieme, quindi inbox, deleghe a tempo e `mayDecide` funzionano senza
  casi speciali.
- `SubmitWeek` calcola l'insieme (`approverPool`: responsabile del team, persona scelta o amministratori del
  tempo, mai la persona stessa) e chiama `timesheet_open_request`. `WithdrawWeek` e `ReopenWeek` chiudono la
  richiesta (`cancelled`).
- Esito: `approval.Decide` chiama la reazione `closeTimesheet` (registrata con `OnClose("timesheet")`), che scrive
  l'esito su `timesheets`, nell'audit e avvisa la persona. `DecideWeek` (timesheet) passa dallo stesso `Decide`:
  decidere dall'inbox o dal timesheet è identico. Il motivo è obbligatorio per respingere in entrambi i punti; il
  «rinvio» non esiste (`validation.work.timesheet_no_defer`). `SeparateRequester("timesheet")` impedisce di
  decidere la propria settimana anche dal passo del motore.
- Inbox: `approval.InboxItem` ha `approval_subject`, `timesheet_member`, `timesheet_week`; per il timesheet
  `element_key` è vuoto e `title` è il nome della persona. «Da fare» le toglie dalle decisioni (hanno la sezione
  Tempo) per non contarle due volte. Il numero accanto a «Timesheet» (`GET …/pending`) legge gli stessi
  assegnatari del motore, deleghe comprese.

## Regola di transizione per le versioni della configurazione

Gli approvatori si scelgono **all'invio** con le regole del tempo di quel momento e restano quelli: se
`TimeSettings` cambia (responsabile del team → persona scelta, altra persona…), le settimane già inviate restano
sugli approvatori di quando sono partite e valgono le nuove regole solo per i prossimi invii. Eccezione voluta:
gli amministratori del tempo decidono sempre; se lo sono diventati dopo l'invio, al primo `decide` dal timesheet
entrano nell'insieme.

Le settimane già inviate prima della migrazione ricevono la richiesta nella migrazione stessa (ruoli assegnati
direttamente o tramite gruppo, senza l'ereditarietà tra gruppi). Se una settimana inviata risultasse senza
richiesta, `DecideWeek` la apre al momento.

## Notifiche

Un solo kind, `timesheet` (`internal/engine/notify/timesheet.go`), senza elemento (`element_id` nullo), con
`event` nel payload: `submitted` (a chi approva), `approved`, `rejected` (con `comment`) e `reopened` (alla persona).
Più settimane inviate dalla stessa persona restano **una** notifica non letta per approvatore (`weeks`, `count`).
Preferenze, silenziamento per tipo ed email (outbox, con il link `/tempo?persona=&settimana=`) come per gli altri
tipi. Testi: lato server in `notify.timesheet.*`, lato client `lavoro.notifications.timesheet.*`
(`web/src/i18n/{it,en}/lavoro-timesheet.ts`).

## Test

- `go test ./internal/itest -run TestTimesheetApprovazioni` (invio, inbox, decisione da inbox e da timesheet,
  motivo, auto-approvazione, delega, richiamo, cambio delle regole, notifiche); `go test ./internal/engine/notify/...`.
- `web/e2e/lacune-timesheet.spec.ts` (anche a 375 px).

## Decisione in linea da «Da fare»

La sezione «Tempo» di «Da fare» permette di approvare o respingere (motivo obbligatorio, campo in linea) la
settimana di un'altra persona senza aprire il timesheet: passa dalla stessa `POST /work/timesheet/decide`, quindi
`DecideWeek` e `approval.Decide` restano l'unica logica. L'elemento `time` di `GET /todo` porta anche le ore per
giorno (`days`) per il dettaglio richiudibile. La propria settimana non compare mai tra quelle da decidere.
