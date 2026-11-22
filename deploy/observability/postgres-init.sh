#!/bin/sh
set -eu

# Disposable observability stack only. Runtime DML and Atlas schema changes use
# separate PostgreSQL identities as required by Core.
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=ON_ERROR_STOP=1 \
  --set=migration_password="$OBS_POSTGRES_MIGRATION_PASSWORD" \
  --set=db_name="$POSTGRES_DB" <<'SQL'
CREATE ROLE obs_migrator LOGIN PASSWORD :'migration_password';
SELECT format('GRANT CONNECT, CREATE, TEMPORARY ON DATABASE %I TO obs_migrator', :'db_name') \gexec
GRANT CREATE ON SCHEMA public TO obs_migrator;
SQL
