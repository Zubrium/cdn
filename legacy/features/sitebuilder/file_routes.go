package sitebuilder

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/pkg/environment"
)

type PublicFileRoute struct {
	FileID      uuid.UUID
	Disposition string
}

// ResolveFileRoute matches the active snapshot without reading page content.
// File routes are fixed public-file deliveries and never create a runtime page.
func (s *SnapshotService) ResolveFileRoute(ctx context.Context, env environment.Environment, requestPath string) (PublicFileRoute, bool, error) {
	_, payload, err := s.Active(ctx, env)
	if err != nil {
		return PublicFileRoute{}, false, err
	}
	routes, err := CompileRouteTree(payload.Config.RouteTree)
	if err != nil {
		return PublicFileRoute{}, false, err
	}
	route, _, matched, err := MatchRoute(routes, requestPath)
	if err != nil || !matched || route.FileID == "" {
		return PublicFileRoute{}, matched && route.FileID != "", err
	}
	id, err := uuid.Parse(route.FileID)
	if err != nil {
		return PublicFileRoute{}, true, fmt.Errorf("invalid file route %q: %w", route.ID, err)
	}
	disposition := route.FileDisposition
	if disposition == "" {
		disposition = "inline"
	}
	return PublicFileRoute{FileID: id, Disposition: disposition}, true, nil
}

func (s *SnapshotService) validateRouteFileTargets(ctx context.Context, tx pgx.Tx, root RouteTreeNode) error {
	var walk func(RouteTreeNode) error
	walk = func(node RouteTreeNode) error {
		if node.FileID != "" {
			id, err := uuid.Parse(node.FileID)
			if err != nil {
				return fmt.Errorf("route %q file id: %w", node.ID, err)
			}
			var status, visibility string
			err = tx.QueryRow(ctx, `SELECT status,visibility FROM media_objects WHERE id=$1`, id).Scan(&status, &visibility)
			if err == pgx.ErrNoRows {
				return fmt.Errorf("route %q references missing file %s", node.ID, id)
			}
			if err != nil {
				return err
			}
			if status != "ready" || visibility != "public" {
				return fmt.Errorf("route %q requires a ready public file", node.ID)
			}
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}
