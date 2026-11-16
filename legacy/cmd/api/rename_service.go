package main

import (
	"context"
	"fmt"
	"websiteengine/features/files"
	"websiteengine/pkg/config"
	"websiteengine/pkg/postgres"
)

func renameService(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: engine rename-service OLD_NAME NEW_NAME (then update FILE_SERVICES and restart)")
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
	_, err = files.RenameConfiguredService(context.Background(), pool, args[0], args[1])
	return err
}
