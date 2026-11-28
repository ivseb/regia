import type { Translation } from "../types";
import type it from "../it/errors-identita";

export default {
  "errors.admin.escalation": "Not allowed: it needs permissions you don't have. You can only grant (and remove) roles you hold.",
  "errors.group.idp_managed": "The group is governed by the company IdP: membership is changed in the IdP, not in Regia.",
  "errors.idp.group_scim": "The group comes from SCIM: the IdP removes it.",
  "errors.idp.mapping_exists": "The mapping already exists.",
  "errors.scim.too_many_tokens": "At most 5 active SCIM tokens: revoke one.",
} satisfies Translation<typeof it>;
