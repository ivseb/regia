// Notifiche dei progetti (passo 4): conflitti sulle applicazioni, SAL e consegne da accettare.
import type { Source } from "../types";

export default {
  "lavoro.notifications.kind.project.label": "Progetti",
  "lavoro.notifications.kind.project.hint": "Conflitti tra progetti sulle applicazioni, SAL e consegne dei fornitori da accettare",
  "lavoro.notifications.project.conflict": "<f>{project}</f> e <f>{other}</f> modificano {application} nello stesso periodo, fino al {until}",
  "lavoro.notifications.project.claim_submitted": "{actor} ha dichiarato il SAL {number} di <b>{work_package}</b>: <f>è da accettare</f>",
  "lavoro.notifications.project.deliverable_delivered": "{actor} ha consegnato {key}: <f>è da accettare</f>",
  "lavoro.notifications.project.dependency_late": "<f>{milestone}</f> di {other} è prevista il {date}, <d>oltre il {need}</d> che serve a {project}",
  "lavoro.notifications.project.plain": "Novità sul progetto: {key}",
} satisfies Source;
