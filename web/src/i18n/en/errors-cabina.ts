import type { Translation } from "../types";
import type it from "../it/errors-cabina";

export default {
  "errors.validation.portfolio.scenario_name_required": "Give the scenario a name.",
  "errors.validation.portfolio.scenario_limit": "At most {max} scenarios: delete one.",
  "errors.validation.portfolio.minutes_no_decisions": "No item has a decision yet. Generate the minutes after the decisions.",
  "errors.validation.portfolio.minutes_no_space": "There's no Knowledge space you can write in.",
} satisfies Translation<typeof it>;
