import type { Translation } from "../types";
import type it from "../it/altro-schematag";

export default {
  "altro.schemaTag.title": "Which tag is the release?",
  "altro.schemaTag.intro": "These repositories publish more than one tag per version, for example the chart's and the application's. If they all count as releases, the DORA metrics count them twice.",
  "altro.schemaTag.question": "{repo}: which tag scheme is the real release?",
  "altro.schemaTag.scheme": { one: "{count} recent tag: {examples}", other: "{count} recent tags, for example {examples}" },
  "altro.schemaTag.all": "All of them: every tag is a release",
  "altro.schemaTag.save": "Save",
  "altro.schemaTag.note": "The choice goes into the integration's pipeline profile (the repository's tag_filter) and can be changed there.",
  "altro.schemaTag.saved": "Saved in the profile «{profile}», version {version}.",
  "altro.schemaTag.savedNew": "Created the profile «{profile}» and linked it to the integration.",
  "altro.schemaTag.pruned": { one: "Removed {count} already imported release of the other scheme.", other: "Removed {count} already imported releases of the other schemes." },
} satisfies Translation<typeof it>;
