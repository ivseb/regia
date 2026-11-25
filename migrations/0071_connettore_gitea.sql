-- Connettore Gitea (DevSecOps): le tabelle dei connettori sono già generiche per tipo
-- (integrations, connector_repositories, connector_sync_state, connector_sync_runs,
-- inbound_deliveries, con RLS dalle migrazioni 0001 e 0025). Serve solo ammettere il nuovo
-- valore «gitea» nel tipo dell'integrazione e nel provider di un servizio.

alter table integrations drop constraint integrations_kind_check;
alter table integrations add constraint integrations_kind_check
  check (kind in ('github','gitlab','gitea','bitbucket','azure_devops','jenkins','generic'));

alter table services drop constraint services_provider_check;
alter table services add constraint services_provider_check
  check (provider in ('github','gitlab','gitea','azure_devops','bitbucket','other'));
