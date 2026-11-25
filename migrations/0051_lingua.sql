-- Lingua dell'interfaccia (replica/lingua.md, passo 1).
-- Il membro sceglie la propria (members.locale, nulla = segue il tenant); il tenant ne fissa
-- la predefinita in tenant_language. Senza scelta né default vale l'intestazione
-- Accept-Language del browser, e in mancanza l'italiano. I codici ammessi li decide
-- l'applicazione (internal/api/language.go); qui si limita il formato alle lingue di oggi.

-- La colonna esisteva già dalla 0001, mai usata: si ripuliscono eventuali valori fuori elenco.
update members set locale = null where locale is not null and locale not in ('it', 'en');
alter table members add constraint members_locale_ck check (locale is null or locale in ('it', 'en'));

-- Lingua predefinita del tenant, scelta da chi ha tenant.admin. Nessuna riga = nessuna scelta.
create table tenant_language (
  tenant_id uuid not null references tenants(id) on delete cascade,
  locale text not null check (locale in ('it', 'en')),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on tenant_language (tenant_id, updated_by);
select enable_tenant_rls('tenant_language');
