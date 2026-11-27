import type { Translation } from "../types";
import type it from "../it/errors-ai-grc";

export default {
  "errors.ai.grc_map_subject": "Choose an evidence item or a control.",
  "errors.ai.grc_kind_invalid": "Unknown document type: policy or runbook.",
  "errors.ai.grc_source_invalid": "A draft starts from a control, a risk or a regulatory requirement.",
  "errors.validation.grc.evidence_not_linkable": "Rejected or revoked evidence cannot be linked.",
  "errors.validation.risk.notification_not_dora": "The DORA template applies only to notifications of the DORA scheme.",
} satisfies Translation<typeof it>;
