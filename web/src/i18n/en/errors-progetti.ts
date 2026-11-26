import type { Translation } from "../types";
import type it from "../it/errors-progetti";

export default {
  "errors.validation.projects.dep_kind": "Dependency type not allowed: finish-to-start, start-to-start, or finish-to-finish.",
  "errors.validation.projects.dep_lag": "The lag goes from 0 to 365 working days.",
  "errors.validation.projects.dep_self": "An item cannot depend on itself.",
  "errors.validation.projects.dep_unknown": "“{ref}” is not an item of this project.",
  "errors.validation.projects.gate_closed": "The phase “{phase}” has a gate to approve before moving on.",
  "errors.validation.projects.gate_no_policy": "No approval policy for phase gates: set one up in Approvals.",
  "errors.validation.projects.gate_off": "The phase has no approval gate.",
  "errors.validation.projects.gate_status": "The gate is already awaiting approval or approved.",
  "errors.validation.projects.mode": "Delivery mode not allowed: agile, waterfall, or supplier.",
  "errors.validation.projects.no_approvals": "The approval engine is not available.",
  "errors.validation.projects.percent": "The percentage goes from 0 to 100.",
  "errors.validation.projects.progress_milestone": "A milestone does not progress: mark it as reached.",
  "errors.validation.projects.request_not_approved": "The request is not approved yet: it becomes a project after approval.",
  "errors.validation.projects.template": "Project template not allowed: agile, waterfall, mixed, or contract.",
  "errors.validation.projects.weight": "The weight must be greater than zero.",
  "errors.validation.work.epic_agile_only": "Only agile work packages get an epic.",
  "errors.validation.work.wp_no_team": "The work package has no responsible team.",
} satisfies Translation<typeof it>;
