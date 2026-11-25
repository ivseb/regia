import type { Translation } from "../types";
import type it from "../it/errors-privacy";

export default {
  "errors.validation.privacy.name_required": "Give the processing activity a name.",
  "errors.validation.privacy.dpia_approved": "The DPIA is approved: reopen it to edit it.",
  "errors.validation.privacy.dpia_incomplete": "To send it to review, fill in the description and the risk assessment.",
  "errors.validation.privacy.dpia_opinion_required": "To approve it, the DPO's opinion is required.",
  "errors.validation.privacy.dpia_transition": "You can't move from this status to the requested one.",
  "errors.validation.privacy.reason_required": "Say why the notification is not required.",
  "errors.validation.privacy.notified_future": "The notification can't be in the future.",
} satisfies Translation<typeof it>;
