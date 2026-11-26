import type { Translation } from "../types";
import type it from "../it/lavoro-progetti";

export default {
  "lavoro.notifications.kind.project.label": "Projects",
  "lavoro.notifications.kind.project.hint": "Conflicts between projects on applications, supplier progress claims and deliveries to accept",
  "lavoro.notifications.project.conflict": "<f>{project}</f> and <f>{other}</f> both change {application} in the same period, until {until}",
  "lavoro.notifications.project.claim_submitted": "{actor} submitted progress claim {number} for <b>{work_package}</b>: <f>it needs your decision</f>",
  "lavoro.notifications.project.deliverable_delivered": "{actor} delivered {key}: <f>it needs your decision</f>",
  "lavoro.notifications.project.plain": "Project update: {key}",
} satisfies Translation<typeof it>;
