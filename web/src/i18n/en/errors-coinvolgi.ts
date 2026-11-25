import type { Translation } from "../types";
import type it from "../it/errors-coinvolgi";

export default {
  "errors.validation.coinvolgi.nothing": "Choose what the team does: an opinion, some work, or both.",
  "errors.validation.coinvolgi.process_type": "Choose the process of a request type.",
  "errors.validation.coinvolgi.draft_open": "The process has an unpublished draft: publish or discard it, then try again.",
  "errors.validation.coinvolgi.no_version": "The process has no published version yet.",
  "errors.validation.coinvolgi.phase_unknown": "The phase \"{phase}\" does not exist in the process.",
} satisfies Translation<typeof it>;
