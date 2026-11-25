import type { Translation } from "../types";
import type it from "../it/errors-timesheet";

export default {
  "errors.validation.work.timesheet_no_defer": "A timesheet is approved or rejected: it can't be deferred.",
} satisfies Translation<typeof it>;
