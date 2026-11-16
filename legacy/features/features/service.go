package features

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"websiteengine/pkg/response"
)

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

func (s *Service) List(ctx context.Context, status, featureType, search, rawCursor string, limit int) ([]Feature, string, error) {
	if status != "" && status != "active" && status != "archived" {
		return nil, "", &ValidationError{Message: "status must be active or archived"}
	}
	switch featureType {
	case "", "form":
	default:
		return nil, "", &ValidationError{Message: "type must be omitted or form"}
	}
	if limit < 1 {
		return nil, "", &ValidationError{Message: "limit must be positive"}
	}
	params := ListParams{Status: status, Type: featureType, Search: strings.TrimSpace(search), Limit: limit + 1}
	if rawCursor != "" {
		cursor, ok := response.DecodeCursor(rawCursor)
		if !ok {
			return nil, "", &ValidationError{Message: "invalid cursor"}
		}
		id, err := uuid.Parse(cursor.ID)
		if err != nil {
			return nil, "", &ValidationError{Message: "invalid cursor"}
		}
		params.BeforeTime = &cursor.CreatedAt
		params.BeforeID = &id
	}
	items, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = response.EncodeCursor(last.CreatedAt, last.ID)
	}
	return items, next, nil
}

func (s *Service) Versions(ctx context.Context, id uuid.UUID, limit int) ([]Version, error) {
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, errors.New("limit must be positive")
	}
	return s.repo.Versions(ctx, id, limit)
}
