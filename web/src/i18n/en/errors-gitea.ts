import type { Translation } from "../types";
import type it from "../it/errors-gitea";

export default {
  "errors.gitea.error": "Gitea returned an error.",
  "errors.gitea.unavailable": "Gitea can't be reached.",
} satisfies Translation<typeof it>;
