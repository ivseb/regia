import type { Translation } from "../types";
import type it from "../it/errors-alm";

export default {
  "errors.alm.unit_not_found": "ALM unit not found.",
  "errors.alm.error": "ALM returned an error.",
} satisfies Translation<typeof it>;
