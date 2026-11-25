import type { Translation } from "../types";
import type it from "../it/errors-auditor";

export default {
  "errors.validation.audit.conclusion_invalid": "Conclusion not allowed: reviewed or to clarify.",
  "errors.validation.audit.control_not_in_scope": "The control is not in the scope of the engagement.",
  "errors.validation.audit.engagement_closed": "The engagement is closed: nothing more can be added.",
  "errors.validation.audit.evidence_not_found": "No such evidence: check the key.",
  "errors.validation.audit.finding_closed": "The finding is already closed.",
  "errors.validation.audit.framework_unknown": "The framework does not exist.",
  "errors.validation.audit.no_assignee": "The control has no owner: say who to ask for the evidence.",
  "errors.validation.audit.note_required": "Write a note: it is needed for a non-compliant or to-clarify outcome and to close a finding.",
  "errors.validation.audit.outcome_invalid": "Outcome not allowed: compliant, non-compliant or to clarify.",
  "errors.validation.audit.period_invalid": "The end of the period is before the start.",
  "errors.validation.audit.plan_required": "Write the action plan.",
  "errors.validation.audit.request_not_open": "The request is no longer open.",
  "errors.validation.audit.response_required": "Write a reply or give a link or an evidence item.",
  "errors.validation.audit.sample_empty": "The control has no evidence in the period to sample.",
  "errors.validation.audit.sample_size_invalid": "The sample size is from 1 to 200.",
  "errors.validation.audit.severity_invalid": "Severity not allowed: low, medium, high or critical.",
} satisfies Translation<typeof it>;
