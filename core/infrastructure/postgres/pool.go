package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolConfig struct {
	URL                                                      string
	MinConnections, MaxConnections                           int32
	ConnectTimeout, MaxLifetime, MaxIdleTime, HealthInterval time.Duration
}

func Open(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	if cfg.URL == "" || cfg.MinConnections < 0 || cfg.MaxConnections < 1 || cfg.MinConnections > cfg.MaxConnections || cfg.ConnectTimeout <= 0 || cfg.MaxLifetime <= 0 || cfg.MaxIdleTime <= 0 || cfg.HealthInterval <= 0 {
		return nil, errors.New("invalid database pool configuration")
	}
	parsed, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.New("invalid database URL")
	}
	// Every Core query names its bounded-context schema explicitly. Restrict
	// search_path to built-ins so a relation cannot be shadowed by user objects.
	parsed.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
	parsed.MinConns, parsed.MaxConns = cfg.MinConnections, cfg.MaxConnections
	parsed.MaxConnLifetime, parsed.MaxConnIdleTime, parsed.HealthCheckPeriod = cfg.MaxLifetime, cfg.MaxIdleTime, cfg.HealthInterval
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, parsed)
	if err != nil {
		return nil, fmt.Errorf("initialize PostgreSQL connection pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return pool, nil
}

// ValidateRuntimeAccess rejects a Core connection that can manage schemas,
// alter Atlas history, or reach an unowned PostgreSQL schema. Runtime DML is
// limited to the explicit Core tables in the bounded-context schemas.
func ValidateRuntimeAccess(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil database pool")
	}
	var safe bool
	err := pool.QueryRow(ctx, `SELECT current_schema()='pg_catalog'
		AND NOT has_schema_privilege(current_user,'public','CREATE')
		AND NOT has_database_privilege(current_user,current_database(),'CREATE')
		AND NOT COALESCE((SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls
			FROM pg_catalog.pg_roles WHERE rolname=current_user),true)
		AND EXISTS (SELECT 1 FROM pg_catalog.pg_roles runtime_role
			WHERE runtime_role.rolname='zubrium_core_runtime' AND NOT runtime_role.rolcanlogin
			AND NOT runtime_role.rolsuper AND NOT runtime_role.rolcreatedb
			AND NOT runtime_role.rolcreaterole AND NOT runtime_role.rolreplication
			AND NOT runtime_role.rolbypassrls AND pg_has_role(current_user,runtime_role.oid,'MEMBER'))
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_auth_members membership
			JOIN pg_catalog.pg_roles granted ON granted.oid=membership.roleid
			JOIN pg_catalog.pg_roles member ON member.oid=membership.member
			WHERE member.rolname=current_user AND granted.rolname <> 'zubrium_core_runtime')
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_auth_members membership
			JOIN pg_catalog.pg_roles member ON member.oid=membership.member
			WHERE member.rolname='zubrium_core_runtime')
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_namespace n
			WHERE n.nspname NOT IN ('access','files','uploads','file_index','engine_data','coordination','public','pg_catalog','information_schema')
			AND n.nspname !~ '^pg_(toast|temp)'
			AND (has_schema_privilege(current_user,n.oid,'USAGE')
				OR has_schema_privilege(current_user,n.oid,'CREATE')))
		AND NOT EXISTS (
			SELECT 1 FROM unnest(ARRAY['access','files','uploads','file_index','engine_data','coordination']::text[]) AS s(schema_name)
			WHERE NOT has_schema_privilege(current_user,s.schema_name,'USAGE')
				OR has_schema_privilege(current_user,s.schema_name,'CREATE'))
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_class c
			JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname IN ('access','files','uploads','file_index','engine_data','coordination','public')
			AND (has_table_privilege(current_user,c.oid,'SELECT')
				OR has_table_privilege(current_user,c.oid,'INSERT')
				OR has_table_privilege(current_user,c.oid,'UPDATE')
				OR has_table_privilege(current_user,c.oid,'DELETE'))
			AND NOT (n.nspname||'.'||c.relname = ANY(ARRAY[
				'access.users','access.bootstrap','access.credentials','access.sessions','access.groups','access.grants',
				'files.objects','files.memberships','uploads.requests','uploads.staged',
				'file_index.sets','file_index.members','engine_data.records',
				'engine_data.revision_seq',
				'coordination.runtime','coordination.instances','coordination.workflows','coordination.tasks','coordination.dependencies']::text[])))
		AND NOT EXISTS (
			SELECT 1 FROM unnest(ARRAY[
				'access.users','access.bootstrap','access.credentials','access.sessions','access.groups','access.grants',
				'files.objects','files.memberships','uploads.requests','uploads.staged',
				'file_index.sets','file_index.members','engine_data.records',
				'coordination.runtime','coordination.instances','coordination.workflows','coordination.tasks','coordination.dependencies']::text[]) AS t(relation_name)
			WHERE NOT (has_table_privilege(current_user,t.relation_name,'SELECT')
				AND has_table_privilege(current_user,t.relation_name,'INSERT')
				AND has_table_privilege(current_user,t.relation_name,'UPDATE')
				AND has_table_privilege(current_user,t.relation_name,'DELETE'))
			OR has_table_privilege(current_user,t.relation_name,'TRUNCATE')
			OR has_table_privilege(current_user,t.relation_name,'REFERENCES')
			OR has_table_privilege(current_user,t.relation_name,'TRIGGER'))
		AND has_sequence_privilege(current_user,'engine_data.revision_seq','USAGE')
		AND NOT has_sequence_privilege(current_user,'engine_data.revision_seq','UPDATE')`).Scan(&safe)
	if err != nil {
		return fmt.Errorf("verify Core runtime database grants: %w", err)
	}
	if !safe {
		return errors.New("Core runtime database user must have DML access only to Core tables")
	}
	return nil
}
