import type { Translation } from "../types";
import type it from "../it/errors-continuita";

export default {
  "errors.validation.continuity.not_ready": "Continuity is not set up in this organization yet.",
  "errors.validation.continuity.bia_exists": "This capability already has BIA {key}.",
  "errors.validation.continuity.bia_locked": "A BIA can be edited only in Draft: use «Update» to bring it back to Draft.",
  "errors.validation.continuity.bia_incomplete": "To proceed, the impact grid must be full and the RTO and RPO set.",
  "errors.validation.continuity.not_capability": "A BIA is made on a capability.",
  "errors.validation.continuity.subject_invalid": "A test concerns an application or a capability.",
  "errors.validation.continuity.test_locked": "The test is closed or cancelled: it can no longer be edited.",
  "errors.validation.continuity.test_type": "This test type is not in the configuration.",
  "errors.validation.continuity.outcome": "Invalid outcome or findings: an outcome and an execution date are needed and, if the test has findings or failed, at least one finding.",
  "errors.validation.continuity.config": "The configuration is not valid: check names, keys, levels and horizons.",
  "errors.validation.continuity.plan_page": "The plan must be a document page.",
  "errors.validation.continuity.plan_target": "A plan is linked to a capability or an application.",
  "errors.validation.continuity.version_conflict": "Someone saved the configuration in the meantime: reload and try again.",
} satisfies Translation<typeof it>;
