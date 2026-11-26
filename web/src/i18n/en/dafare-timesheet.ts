import type { Translation } from "../types";
import type it from "../it/dafare-timesheet";

export default {
  "dafare.ts.approve": "Approve",
  "dafare.ts.reject": "Reject",
  "dafare.ts.confirm": "Confirm rejection",
  "dafare.ts.cancel": "Cancel",
  "dafare.ts.reason": "Reason for rejecting",
  "dafare.ts.reasonHint": "Required: the person reads it and fixes the week.",
  "dafare.ts.reasonRequired": "Write the reason for rejecting.",
  "dafare.ts.open": "Open the timesheet",
  "dafare.ts.detail": "Hours per day",
  "dafare.ts.total": "Total: {time}",
  "dafare.ts.approved": "{member}'s week approved.",
  "dafare.ts.rejected": "{member}'s week rejected: they can fix it.",
} satisfies Translation<typeof it>;
