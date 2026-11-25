import type { Translation } from "../types";
import type it from "../it/errors-avvio";

export default {
  "errors.validation.avvio.model_unavailable": "This template is not available yet.",
  "errors.validation.avvio.nothing_to_restore": "The process is already the starting one: there is nothing to restore.",
  "errors.validation.avvio.restore_invalid": "The starting process is not compatible with the current data: fix the missing references first.",
} satisfies Translation<typeof it>;
