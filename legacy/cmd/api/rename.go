package main

import (
	"context"
	"fmt"
	"websiteengine/features/sitebuilder"
	"websiteengine/pkg/config"
	"websiteengine/pkg/postgres"
)

// Operator command, run against a stopped engine before updating config.
func renameBranch(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: engine rename-branch OLD_NAME NEW_NAME (then update BRANCHES and restart)")
	}
	database, err := config.LoadDatabase()
	if err != nil {
		return err
	}
	pool, err := postgres.Open(context.Background(), database.URL, database.Pool)
	if err != nil {
		return err
	}
	defer pool.Close()
	return sitebuilder.NewSnapshotService(pool, nil).RenameBranch(context.Background(), args[0], args[1])
}
