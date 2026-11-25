import type { Translation } from "../types";
import type it from "../it/help";

export default {
  "help.hint": "Explanation of the term “{term}”",
  "help.close": "Close the explanation",

  "help.term.richiesta": "A request is something someone asks the organisation for: a project, a change, a spend. It follows a process until it is decided.",
  "help.term.fase": "A stage is one stretch of the process. A request is always in exactly one stage, and moving to the next one takes a few conditions.",
  "help.term.valutazione": "A review (or opinion) is a questionnaire a competent team fills in or examines to say whether the request is fine from its point of view: security, architecture, privacy.",
  "help.term.impatto": "An impact is the effect of the request on one application and on the team that runs it. Every team involved gets one.",
  "help.term.epic": "An epic is the work a team opens in its own space when it accepts an impact. It gathers the stories that deliver it.",
  "help.term.story": "A story is a small, concrete piece of work inside an epic. When all of them are closed, the epic is done.",
  "help.term.approvazione": "An approval is a decision by a person or a body (yes, no, with conditions) that is needed to enter a stage.",
  "help.term.regola": "A rule makes something happen on its own when the request enters a stage, for example opening a review or assigning a role. It is set up in the process.",
  "help.term.processo": "The process is the path a request follows: the stages, the allowed moves, who can make them and the rules that fire.",
  "help.term.versione": "Every change to the process creates a new version. Requests already under way stay on the version they started with.",
} satisfies Translation<typeof it>;
