import type { Translation } from "../types";
import type it from "../it/lavoro-timesheet";

export default {
  "lavoro.inbox.timesheetStage": "Timesheet",
  "lavoro.inbox.timesheetOpen": "{name}'s timesheet",
  "lavoro.inbox.timesheetWeek": "week {week}",
  "lavoro.notifications.kind.timesheet.label": "Timesheet",
  "lavoro.notifications.kind.timesheet.hint": "Weeks sent for approval, the outcome of your week, and reopening",
  "lavoro.notifications.timesheet.submitted": {
    one: "<b>{member}</b> sent the timesheet for {week} for approval",
    other: "<b>{member}</b> sent {count} timesheet weeks for approval",
  },
  "lavoro.notifications.timesheet.approved": "Timesheet for {week} <b>approved</b>",
  "lavoro.notifications.timesheet.rejected": "<d>Timesheet rejected</d> for {week}: {comment}",
  "lavoro.notifications.timesheet.reopened": "Timesheet for {week} <f>reopened</f>: you can edit and resubmit it",
  "lavoro.notifications.timesheet.plain": "Timesheet for {week}",
} satisfies Translation<typeof it>;
