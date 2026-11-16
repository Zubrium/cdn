package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolSettings struct {
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
}

// Open creates a validated PostgreSQL pool.
func Open(ctx context.Context, databaseURL string, settings PoolSettings) (*pgxpool.Pool, error) {
	if settings.MaxConns < 1 || settings.MinConns < 0 || settings.MinConns > settings.MaxConns || settings.MaxConnLifetime <= 0 || settings.MaxConnIdleTime <= 0 || settings.HealthCheckPeriod <= 0 || settings.ConnectTimeout <= 0 {
		return nil, errors.New("database pool settings must be explicitly configured and valid")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = settings.MaxConns
	cfg.MinConns = settings.MinConns
	cfg.MaxConnLifetime = settings.MaxConnLifetime
	cfg.MaxConnIdleTime = settings.MaxConnIdleTime
	cfg.HealthCheckPeriod = settings.HealthCheckPeriod

	connectCtx, cancel := context.WithTimeout(ctx, settings.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
