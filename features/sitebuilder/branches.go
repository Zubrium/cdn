package sitebuilder

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net"
	"strconv"
	"time"
	"websiteengine/pkg/config"
)

type BranchRecord struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	HTTPAddr    string     `json:"httpAddr"`
	CORSOrigins []string   `json:"corsOrigins"`
	Indexable   bool       `json:"indexable"`
	SnapshotID  *uuid.UUID `json:"snapshotId"`
	Revision    int64      `json:"revision"`
	Enabled     bool       `json:"enabled"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// The same lock serializes registration, assignment, rename and deletion.
func lockSnapshotTree(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_snapshot_tree'))`)
	return err
}

func (s *SnapshotService) RegisterBranches(ctx context.Context, definitions []config.BranchConfig) ([]BranchRecord, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("%w: at least one configured branch is required", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockSnapshotTree(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE site_environments SET enabled=false`); err != nil {
		return nil, err
	}
	for _, b := range definitions {
		_, port, _ := net.SplitHostPort(b.HTTPAddr)
		n, _ := strconv.Atoi(port)
		if b.Indexable == nil || n < 1 {
			return nil, fmt.Errorf("%w: branch configuration is invalid", ErrInvalid)
		}
		_, err = tx.Exec(ctx, `INSERT INTO site_environments(key,name,http_port,http_addr,cors_origins,indexable,enabled) VALUES($1,$2,$3,$4,$5,$6,true)
 ON CONFLICT(name) DO UPDATE SET http_port=EXCLUDED.http_port,http_addr=EXCLUDED.http_addr,cors_origins=EXCLUDED.cors_origins,indexable=EXCLUDED.indexable,enabled=true`, uuid.NewString(), b.Name, n, b.HTTPAddr, b.CORSOrigins, *b.Indexable)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Branches(ctx)
}

func (s *SnapshotService) Branches(ctx context.Context) ([]BranchRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT key,name,http_addr,cors_origins,indexable,active_snapshot_id,revision,enabled,updated_at FROM site_environments WHERE enabled ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BranchRecord{}
	for rows.Next() {
		var b BranchRecord
		if err := rows.Scan(&b.ID, &b.Name, &b.HTTPAddr, &b.CORSOrigins, &b.Indexable, &b.SnapshotID, &b.Revision, &b.Enabled, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *SnapshotService) Branch(ctx context.Context, id uuid.UUID) (BranchRecord, error) {
	var b BranchRecord
	err := s.pool.QueryRow(ctx, `SELECT key,name,http_addr,cors_origins,indexable,active_snapshot_id,revision,enabled,updated_at FROM site_environments WHERE key=$1 AND enabled`, id.String()).Scan(&b.ID, &b.Name, &b.HTTPAddr, &b.CORSOrigins, &b.Indexable, &b.SnapshotID, &b.Revision, &b.Enabled, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

func (s *SnapshotService) AssignBranch(ctx context.Context, actor, branchID, snapshotID uuid.UUID, revision int64, expectedContentRevision ...*int64) (BranchRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BranchRecord{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockSnapshotTree(ctx, tx); err != nil {
		return BranchRecord{}, err
	}
	var name string
	var sequence int64
	var contentRevision int64
	err = tx.QueryRow(ctx, `SELECT name,sequence,content_revision FROM site_snapshots WHERE id=$1 FOR SHARE`, snapshotID).Scan(&name, &sequence, &contentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return BranchRecord{}, ErrNotFound
	}
	if err != nil {
		return BranchRecord{}, err
	}
	if len(expectedContentRevision) > 0 && expectedContentRevision[0] != nil && *expectedContentRevision[0] != contentRevision {
		return BranchRecord{}, fmt.Errorf("%w: snapshot content changed after compatibility review", ErrConflict)
	}
	tag, err := tx.Exec(ctx, `UPDATE site_environments SET active_snapshot_id=$1,revision=revision+1,updated_at=now() WHERE key=$2 AND revision=$3 AND enabled`, snapshotID, branchID.String(), revision)
	if err != nil {
		return BranchRecord{}, err
	}
	if tag.RowsAffected() != 1 {
		return BranchRecord{}, fmt.Errorf("%w: branch assignment changed or branch is disabled", ErrConflict)
	}
	_, err = tx.Exec(ctx, `INSERT INTO site_snapshot_events(id,snapshot_id,environment,action,actor_token_id,snapshot_name,snapshot_sequence) VALUES($1,$2,$3,'deploy',$4,$5,$6)`, uuid.New(), snapshotID, branchID.String(), actor, name, sequence)
	if err != nil {
		return BranchRecord{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BranchRecord{}, err
	}
	return s.Branch(ctx, branchID)
}

func (s *SnapshotService) RenameBranch(ctx context.Context, from, to string) error {
	if from == "" || to == "" || len([]rune(to)) > 100 {
		return fmt.Errorf("%w: branch names must contain 1–100 characters", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockSnapshotTree(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE site_environments SET name=$2 WHERE name=$1`, from, to)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
