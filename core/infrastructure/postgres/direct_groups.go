package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	appaccess "github.com/Zubrium/cdn/core/application/access"
	"github.com/Zubrium/cdn/core/domain/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateConfiguredGroups(groups map[string]access.Level) error {
	for group, level := range groups {
		if !appaccess.ValidGroupKey(group) || level > access.LevelWrite {
			return errors.New("invalid configured access group")
		}
	}
	return nil
}

// ReconcileAccessGroups activates configured groups, disables removed groups,
// and preserves their saved assignments and file membership. Only a group
// never seen before receives its configured initial bootstrap level.
func ReconcileAccessGroups(ctx context.Context, pool *pgxpool.Pool, groups map[string]access.Level) error {
	if pool == nil {
		return errors.New("nil database pool")
	}
	if err := validateConfiguredGroups(groups); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('zubrium_core_v2_group_config'))`); err != nil {
		return err
	}
	var bootstrapID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT user_id FROM access.bootstrap WHERE singleton=true`).Scan(&bootstrapID); err != nil {
		return fmt.Errorf("read bootstrap principal: %w", err)
	}
	if err := reconcileAccessGroupsTx(ctx, tx, groups, bootstrapID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reconcileAccessGroupsTx(ctx context.Context, tx pgx.Tx, groups map[string]access.Level, bootstrapID uuid.UUID) error {
	keys := make([]string, 0, len(groups))
	for group := range groups {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	for _, group := range keys {
		level := groups[group]
		tag, err := tx.Exec(ctx, `INSERT INTO access.groups(group_key,active) VALUES($1,true) ON CONFLICT(group_key) DO NOTHING`, group)
		if err != nil {
			return fmt.Errorf("register access group %q: %w", group, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE access.groups SET active=true WHERE group_key=$1`, group); err != nil {
			return fmt.Errorf("activate access group %q: %w", group, err)
		}
		if tag.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `INSERT INTO access.grants(user_id,group_key,access_level) VALUES($1,$2,$3) ON CONFLICT(user_id,group_key) DO NOTHING`, bootstrapID, group, int16(level)); err != nil {
				return fmt.Errorf("assign initial bootstrap access to group %q: %w", group, err)
			}
		}
	}
	if len(keys) == 0 {
		if _, err := tx.Exec(ctx, `UPDATE access.groups SET active=false WHERE active`); err != nil {
			return fmt.Errorf("disable removed access groups: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE access.groups SET active=false WHERE group_key <> ALL($1::text[]) AND active`, keys); err != nil {
			return fmt.Errorf("disable removed access groups: %w", err)
		}
	}
	if err := bumpAccessEpoch(ctx, tx); err != nil {
		return fmt.Errorf("bump access epoch for configured groups: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return fmt.Errorf("notify configured access groups: %w", err)
	}
	return nil
}

func SetGroupAccess(ctx context.Context, pool *pgxpool.Pool, actorID, targetID uuid.UUID, group string, level access.Level) error {
	if pool == nil || actorID == uuid.Nil || targetID == uuid.Nil || !appaccess.ValidGroupKey(group) || level > access.LevelWrite {
		return errors.New("invalid group access assignment")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireGroupAdministrator(ctx, tx, actorID, group); err != nil {
		return err
	}
	var targetActive bool
	if err := tx.QueryRow(ctx, `SELECT active FROM access.users WHERE id=$1 FOR SHARE`, targetID).Scan(&targetActive); errors.Is(err, pgx.ErrNoRows) || (err == nil && !targetActive) {
		return ErrForbidden
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access.grants(user_id,group_key,access_level) VALUES($1,$2,$3)
		ON CONFLICT(user_id,group_key) DO UPDATE SET access_level=EXCLUDED.access_level`, targetID, group, int16(level)); err != nil {
		return err
	}
	if err := bumpAccessEpoch(ctx, tx, targetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('core_cache_invalidation','*')`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func GroupAccess(ctx context.Context, pool *pgxpool.Pool, actorID, targetID uuid.UUID, group string) (access.Level, error) {
	if pool == nil || actorID == uuid.Nil || targetID == uuid.Nil || !appaccess.ValidGroupKey(group) {
		return access.LevelNone, errors.New("invalid group access query")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return access.LevelNone, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireGroupAdministrator(ctx, tx, actorID, group); err != nil {
		return access.LevelNone, err
	}
	var level int16
	err = tx.QueryRow(ctx, `SELECT ga.access_level FROM access.grants ga JOIN access.users p ON p.id=ga.user_id
		WHERE ga.user_id=$1 AND ga.group_key=$2 AND p.active`, targetID, group).Scan(&level)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.LevelNone, nil
	}
	if err != nil {
		return access.LevelNone, err
	}
	return access.Level(level), nil
}

func requireGroupAdministrator(ctx context.Context, tx pgx.Tx, actorID uuid.UUID, group string) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT p.active AND p.can_manage_administrators AND g.active AND ga.access_level=2
		FROM access.users p JOIN access.groups g ON g.group_key=$2
		JOIN access.grants ga ON ga.user_id=p.id AND ga.group_key=g.group_key
		WHERE p.id=$1 FOR SHARE OF p,g,ga`, actorID, group).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return ErrForbidden
	}
	return err
}

func Assignments(ctx context.Context, pool *pgxpool.Pool, principalID string) ([]access.GroupAssignment, error) {
	if pool == nil {
		return nil, errors.New("nil database pool")
	}
	id, err := uuid.Parse(principalID)
	if err != nil || id == uuid.Nil {
		return nil, ErrUnauthorized
	}
	rows, err := pool.Query(ctx, `SELECT ga.group_key,ga.access_level,g.active
		FROM access.grants ga JOIN access.groups g ON g.group_key=ga.group_key
		WHERE ga.user_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := make([]access.GroupAssignment, 0)
	for rows.Next() {
		var group string
		var active bool
		var level int16
		if err := rows.Scan(&group, &level, &active); err != nil {
			return nil, err
		}
		assignment, err := access.NewGroupAssignment(group, access.Level(level), active)
		if err != nil {
			return nil, fmt.Errorf("invalid persisted group assignment: %w", err)
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

type AccessGroup struct {
	Key    string
	Active bool
	Level  access.Level
}

func ListAccessGroups(ctx context.Context, pool *pgxpool.Pool, principalID string) ([]AccessGroup, error) {
	if pool == nil {
		return nil, errors.New("nil database pool")
	}
	id, err := uuid.Parse(principalID)
	if err != nil || id == uuid.Nil {
		return nil, ErrUnauthorized
	}
	rows, err := pool.Query(ctx, `SELECT g.group_key,g.active,COALESCE(ga.access_level,0)
		FROM access.groups g LEFT JOIN access.grants ga ON ga.group_key=g.group_key AND ga.user_id=$1
		WHERE g.active AND EXISTS(SELECT 1 FROM access.users p WHERE p.id=$1 AND p.active)
		ORDER BY g.group_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]AccessGroup, 0)
	for rows.Next() {
		var group AccessGroup
		var level int16
		if err := rows.Scan(&group.Key, &group.Active, &level); err != nil {
			return nil, err
		}
		group.Level = access.Level(level)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}
