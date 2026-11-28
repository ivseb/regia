// Errori dell'identità aziendale e della chiusura della scalata (internal/platform/admin).
import type { Source } from "../types";

export default {
  "errors.admin.escalation": "Operazione non consentita: servono permessi che non hai. Si possono dare (e togliere) solo ruoli che si hanno.",
  "errors.group.idp_managed": "Il gruppo è governato dall'IdP aziendale: l'appartenenza si cambia nell'IdP, non in Regia.",
  "errors.idp.group_scim": "Il gruppo arriva da SCIM: lo toglie l'IdP.",
  "errors.idp.mapping_exists": "La mappatura c'è già.",
  "errors.scim.too_many_tokens": "Al massimo 5 token SCIM attivi: revocane uno.",
} satisfies Source;
