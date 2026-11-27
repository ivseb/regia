import type { Translation } from "../types";
import type it from "../it/errors-ai-ondata6";

export default {
  "errors.ai.region_invalid": "Invalid region: for AWS for example eu-central-1, for Google Cloud europe-west8, eu or global.",
  "errors.ai.project_invalid": "Invalid Google Cloud project ID.",
  "errors.ai.fields_invalid": "Invalid form list.",
} satisfies Translation<typeof it>;
