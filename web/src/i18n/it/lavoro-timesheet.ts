// Timesheet nel motore delle approvazioni e nelle notifiche (area «lavoro»).
import type { Source } from "../types";

export default {
  "lavoro.inbox.timesheetStage": "Timesheet",
  "lavoro.inbox.timesheetOpen": "Timesheet di {name}",
  "lavoro.inbox.timesheetWeek": "settimana {week}",
  "lavoro.notifications.kind.timesheet.label": "Timesheet",
  "lavoro.notifications.kind.timesheet.hint": "Settimane inviate da approvare, esito della tua settimana e riapertura",
  "lavoro.notifications.timesheet.submitted": {
    one: "<b>{member}</b> ha inviato il timesheet della settimana {week} da approvare",
    other: "<b>{member}</b> ha inviato {count} settimane di timesheet da approvare",
  },
  "lavoro.notifications.timesheet.approved": "Timesheet della settimana {week} <b>approvato</b>",
  "lavoro.notifications.timesheet.rejected": "<d>Timesheet respinto</d> per la settimana {week}: {comment}",
  "lavoro.notifications.timesheet.reopened": "Timesheet della settimana {week} <f>riaperto</f>: puoi modificarlo e reinviarlo",
  "lavoro.notifications.timesheet.plain": "Timesheet della settimana {week}",
} satisfies Source;
