-- Questionari precompilati dall'AI (docs/ai.md §11, docs/sviluppo/ai.md «Questionari»).
-- L'AI propone una risposta per domanda con la sua fonte; la persona la accetta, la cambia o la
-- scarta. Le proposte restano finché non si decide (riaprendo la pagina non si rifà la chiamata)
-- e le vede solo chi le ha chieste: vengono da fonti lette con i suoi permessi.

-- Risposte accettate da una proposta: sorgente «ai» e l'origine, per l'audit («proposta AI
-- accettata da …», «modificata e accettata»). Niente chiave esterna: la storia è immutabile.
alter table questionnaire_answers drop constraint questionnaire_answers_source_check;
alter table questionnaire_answers add constraint questionnaire_answers_source_check check (source in ('manual','prefill','ai'));
alter table questionnaire_answers add column ai_proposal_id uuid;
alter table questionnaire_answers add column ai_edited boolean not null default false;

create table questionnaire_ai_proposals (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  review_id uuid not null,
  question_key text not null,
  -- null = nessuna fonte: da compilare a mano.
  value jsonb,
  -- [{kind, key, title, question, detail}]: risposta confermata, applicazione simile, pagina
  -- Knowledge, fatto del grafo, allegato.
  sources jsonb not null default '[]',
  reason text not null default '',
  confidence text not null check (confidence in ('alta','media','bassa','nessuna')),
  status text not null default 'proposed' check (status in ('proposed','accepted','edited','discarded','superseded')),
  created_by uuid not null,
  created_at timestamptz not null default now(),
  decided_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete cascade
);
create unique index questionnaire_ai_proposals_open on questionnaire_ai_proposals (tenant_id, review_id, created_by, question_key)
  where status = 'proposed';
create index on questionnaire_ai_proposals (tenant_id, review_id, question_key);
create index on questionnaire_ai_proposals (tenant_id, created_by);
select enable_tenant_rls('questionnaire_ai_proposals');
