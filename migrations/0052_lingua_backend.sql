-- Lingua dell'interfaccia, passo 3 (replica/lingua.md): i tipi di sistema dei tenant esistenti
-- hanno anche il nome inglese in element_types.labels, così con la persona in inglese
-- /type-names?lang=en li restituisce in inglese. I tenant nuovi in inglese nascono con il
-- nome inglese e l'italiano in labels.it (internal/platform/tenant/localize.go).
-- Si riempie solo dove manca la voce inglese: un nome inglese già scelto dal tenant resta.
create or replace function system_type_label_en(k text) returns jsonb language sql immutable as $$
  select case k
    when 'business_request' then '{"name": "Request", "plural": "Requests"}'
    when 'impact' then '{"name": "Impact", "plural": "Impacts"}'
    when 'application' then '{"name": "Application", "plural": "Applications"}'
    when 'logical_asset' then '{"name": "Logical asset", "plural": "Logical assets"}'
    when 'page' then '{"name": "Document", "plural": "Documents"}'
    when 'capability' then '{"name": "Business capability", "plural": "Business capabilities"}'
    when 'risk' then '{"name": "ICT risk", "plural": "Risks"}'
    when 'incident' then '{"name": "Incident", "plural": "Incidents"}'
    when 'ict_provider' then '{"name": "ICT provider", "plural": "ICT providers"}'
    when 'control' then '{"name": "Control", "plural": "Controls"}'
    when 'evidence' then '{"name": "Evidence", "plural": "Evidence"}'
    when 'requirement' then '{"name": "Requirement", "plural": "Requirements"}'
    when 'exception' then '{"name": "Waiver", "plural": "Waivers"}'
    when 'review' then '{"name": "Review", "plural": "Reviews"}'
    when 'epic' then '{"name": "Epic", "plural": "Epics"}'
    when 'story' then '{"name": "Story", "plural": "Stories"}'
    when 'task' then '{"name": "Task", "plural": "Tasks"}'
    when 'bug' then '{"name": "Bug", "plural": "Bugs"}'
    when 'sprint' then '{"name": "Sprint", "plural": "Sprints"}'
    else null end::jsonb
$$;

update element_types
set labels = labels || jsonb_build_object('en', system_type_label_en(key))
where is_system and not labels ? 'en' and system_type_label_en(key) is not null;

-- Anche i tipi creati dopo, dai pacchetti dei moduli (come per i plurali italiani, 0048).
create or replace function element_types_label_en() returns trigger language plpgsql as $$
begin
  if new.is_system and not new.labels ? 'en' and system_type_label_en(new.key) is not null then
    new.labels := new.labels || jsonb_build_object('en', system_type_label_en(new.key));
  end if;
  return new;
end $$;

drop trigger if exists element_types_label_en_tg on element_types;
create trigger element_types_label_en_tg before insert on element_types
  for each row execute function element_types_label_en();
