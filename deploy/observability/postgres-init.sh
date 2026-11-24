#!/bin/sh
set -eu

# Disposable observability stack only. Runtime DML and Atlas schema changes use
# separate PostgreSQL identities as required by Core.
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=ON_ERROR_STOP=1 \
  --set=postgres_user="$POSTGRES_USER" \
  --set=runtime_password="$OBS_POSTGRES_RUNTIME_PASSWORD" \
  --set=migration_password="$OBS_POSTGRES_MIGRATION_PASSWORD" \
  --set=db_name="$POSTGRES_DB" <<'SQL'
CREATE ROLE zubrium_core_runtime NOLOGIN;
SELECT format('GRANT zubrium_core_runtime TO %I', :'postgres_user') \gexec
CREATE ROLE obs_runtime LOGIN PASSWORD :'runtime_password';
GRANT zubrium_core_runtime TO obs_runtime;
CREATE ROLE obs_migrator LOGIN PASSWORD :'migration_password';
SELECT format('GRANT CONNECT, CREATE, TEMPORARY ON DATABASE %I TO obs_migrator', :'db_name') \gexec
GRANT CREATE ON SCHEMA public TO obs_migrator;
SQL
