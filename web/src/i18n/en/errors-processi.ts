import type { Translation } from "../types";
import type it from "../it/errors-processi";

export default {
  "errors.validation.process.priority_required": "The priority is required when the process has no condition: it applies to every item of the type.",
  "errors.validation.process.priority_negative": "The priority is a whole number, zero or positive.",
} satisfies Translation<typeof it>;
