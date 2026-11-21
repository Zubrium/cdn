#!/bin/sh
set -eu

psql --username "$POSTGRES_USER" --dbname postgres --set=ON_ERROR_STOP=1 \
  --set=runtime_password="$CORE_QA_RUNTIME_PASSWORD" \
  --set=migration_password="$CORE_QA_MIGRATION_PASSWORD" \
  --set=elevated_password="$CORE_QA_ELEVATED_PASSWORD" <<'SQL'
CREATE ROLE zubrium_core_runtime NOLOGIN;
CREATE ROLE core_qa_runtime LOGIN PASSWORD :'runtime_password';
GRANT zubrium_core_runtime TO core_qa_runtime;
CREATE ROLE core_qa_migrator LOGIN PASSWORD :'migration_password';
CREATE ROLE core_qa_elevated LOGIN PASSWORD :'elevated_password';
GRANT zubrium_core_runtime TO core_qa_elevated;
CREATE ROLE core_qa_extra NOLOGIN;
GRANT core_qa_extra TO core_qa_elevated;
SQL

for database in \
  zubrium_core_qa_platform \
  zubrium_core_qa_core \
  zubrium_core_qa_bootstrap \
  zubrium_core_qa_manager_race \
  zubrium_core_qa_credential_race \
  zubrium_core_qa_groups \
  zubrium_core_qa_schema \
  zubrium_core_qa_dirty \
  zubrium_core_qa_engine_data \
  zubrium_core_qa_runtime_role \
  zubrium_core_qa_coordinator \
  zubrium_core_qa_coordinator_workflow \
  zubrium_core_qa_workflow_race \
  zubrium_core_qa_children \
  zubrium_core_qa_cancel \
  zubrium_core_qa_stale_capability \
  zubrium_core_qa_retry \
  zubrium_core_qa_join \
  zubrium_core_qa_delayed \
  zubrium_core_qa_restart \
  zubrium_core_qa_restart_pending \
  zubrium_core_qa_takeover \
  zubrium_core_qa_retention \
  zubrium_core_qa_dependency \
  zubrium_core_qa_dependency_wait \
  zubrium_core_qa_exhausted \
  zubrium_core_qa_coordinator_lifecycle
do
  psql --username "$POSTGRES_USER" --dbname postgres --set=ON_ERROR_STOP=1 --set=database="$database" <<'SQL'
SELECT format('CREATE DATABASE %I', :'database') \gexec
SQL
  psql --username "$POSTGRES_USER" --dbname postgres --set=ON_ERROR_STOP=1 --set=database="$database" <<'SQL'
SELECT format('GRANT CONNECT, CREATE, TEMPORARY ON DATABASE %I TO core_qa_migrator', :'database') \gexec
SQL
  psql --username "$POSTGRES_USER" --dbname "$database" --set=ON_ERROR_STOP=1 <<'SQL'
GRANT CREATE ON SCHEMA public TO core_qa_migrator;
SQL
done
