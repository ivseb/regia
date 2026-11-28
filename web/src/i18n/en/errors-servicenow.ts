import type { Translation } from "../types";
import type it from "../it/errors-servicenow";

export default {
  "errors.servicenow.service_not_found": "CMDB service not found.",
  "errors.servicenow.record_not_found": "ServiceNow incident or change not found.",
  "errors.servicenow.error": "ServiceNow returned an error.",
  "errors.servicenow.incident_linked": "This ServiceNow incident is already linked to a Regia incident.",
} satisfies Translation<typeof it>;
