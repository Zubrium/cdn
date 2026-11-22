#!/bin/sh
# Provision the dedicated fresh database consumed by
# tests/qa/run-core-red.sh integration (CORE-001).
set -eu

: "${CORE_QA_OWNER_RESULT_DATABASE:?set CORE_QA_OWNER_RESULT_DATABASE}"
database=$CORE_QA_OWNER_RESULT_DATABASE

case "$database" in
	zubrium_core_qa_*) ;;
	*)
		printf '%s\n' "owner-result-init: refusing a non-disposable database name" >&2
		exit 1
		;;
esac
if [ "${#database}" -gt 63 ]; then
	printf '%s\n' "owner-result-init: database name exceeds the PostgreSQL 63-byte identifier limit" >&2
	exit 1
fi

psql --username "$POSTGRES_USER" --dbname postgres --set=ON_ERROR_STOP=1 \
	--set=database="$database" <<'SQL'
SELECT format('CREATE DATABASE %I', :'database') \gexec
SQL

psql --username "$POSTGRES_USER" --dbname postgres --set=ON_ERROR_STOP=1 \
	--set=database="$database" <<'SQL'
SELECT format('GRANT CONNECT, CREATE, TEMPORARY ON DATABASE %I TO core_qa_migrator', :'database') \gexec
SQL

psql --username "$POSTGRES_USER" --dbname "$database" --set=ON_ERROR_STOP=1 <<'SQL'
GRANT CREATE ON SCHEMA public TO core_qa_migrator;
SQL
