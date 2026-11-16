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
	// Keep application queries inside their own schema. PostgreSQL still
	// implicitly searches pg_catalog; exposing public here would permit a
	// public object to shadow a missing Core relation or function.
	parsed.ConnConfig.RuntimeParams["search_path"] = "core"
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

// ValidateRuntimeAccess rejects a Core application connection that can manage
// schemas, alter Atlas history, or reach another module's PostgreSQL schema.
// The runtime login must inherit the DML grants on Core's application tables.
func ValidateRuntimeAccess(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil database pool")
	}
	var safe bool
	err := pool.QueryRow(ctx, `SELECT current_schema()='core'
		AND NOT has_schema_privilege(current_user,'core','CREATE')
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
			WHERE n.nspname NOT IN ('core','public','pg_catalog','information_schema')
			AND n.nspname !~ '^pg_(toast|temp)'
			AND (has_schema_privilege(current_user,n.oid,'USAGE')
				OR has_schema_privilege(current_user,n.oid,'CREATE')))
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_class c
			JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname IN ('core','public')
			AND (has_table_privilege(current_user,c.oid,'SELECT')
				OR has_table_privilege(current_user,c.oid,'INSERT')
				OR has_table_privilege(current_user,c.oid,'UPDATE')
				OR has_table_privilege(current_user,c.oid,'DELETE'))
			AND NOT (n.nspname='core' AND c.relname IN (
				'core_principals','core_bootstrap','core_credentials','core_access_groups',
				'core_group_access','core_objects','core_file_groups','core_upload_requests',
				'core_staged_objects','core_file_sets','core_file_set_members')))
		AND has_schema_privilege(current_user,'core','USAGE')
		AND NOT EXISTS (
			SELECT 1 FROM unnest(ARRAY[
				'core.core_principals','core.core_bootstrap','core.core_credentials',
				'core.core_access_groups','core.core_group_access','core.core_objects',
				'core.core_file_groups','core.core_upload_requests','core.core_staged_objects','core.core_file_sets',
				'core.core_file_set_members']::text[]) AS t(relation_name)
			WHERE NOT (has_table_privilege(current_user,t.relation_name,'SELECT')
				AND has_table_privilege(current_user,t.relation_name,'INSERT')
				AND has_table_privilege(current_user,t.relation_name,'UPDATE')
				AND has_table_privilege(current_user,t.relation_name,'DELETE'))
			OR has_table_privilege(current_user,t.relation_name,'TRUNCATE')
			OR has_table_privilege(current_user,t.relation_name,'REFERENCES')
			OR has_table_privilege(current_user,t.relation_name,'TRIGGER'))`).Scan(&safe)
	if err != nil {
		return fmt.Errorf("verify Core runtime database grants: %w", err)
	}
	if !safe {
		return errors.New("core runtime database user must have DML access only to the core schema")
	}
	return nil
}
