package application

import (
	"context"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
	websiterepo "github.com/Zubrium/cdn/engines/website/repository"
)

// RetirePage advances only the Website-owned page lifecycle. The trusted outer
// composition owns route withdrawal, supplies the durable job identity and
// calls this operation before and after that fan-out. Website never selects a
// route and never calls Router.
func (s *Service) RetirePage(ctx context.Context, request websiteapi.RetirePageRequest) (websiteapi.Page, error) {
	if err := s.requireLevel(ctx, 2); err != nil {
		return websiteapi.Page{}, err
	}
	if !snapshotIDPattern.MatchString(request.PageID) || request.ExpectedRevision < 1 ||
		!snapshotIDPattern.MatchString(request.RetirementJobID) ||
		!snapshotIdempotencyKeyPattern.MatchString(request.IdempotencyKey) ||
		(request.LifecycleState != "retiring" && request.LifecycleState != "retired") {
		return websiteapi.Page{}, websiteapi.ErrInvalid
	}
	page, record, err := s.loadPage(ctx, request.PageID)
	if err != nil {
		return websiteapi.Page{}, err
	}
	if page.LifecycleState == "retired" {
		if request.LifecycleState == "retired" && page.RetirementJobID == request.RetirementJobID {
			return page, nil
		}
		return websiteapi.Page{}, websiteapi.ErrConflict
	}
	if page.LifecycleState == "retiring" && request.LifecycleState == "retiring" &&
		page.RetirementJobID == request.RetirementJobID {
		return page, nil
	}
	if page.RetirementJobID != "" && page.RetirementJobID != request.RetirementJobID {
		return websiteapi.Page{}, websiteapi.ErrConflict
	}
	if request.ExpectedRevision != record.Revision {
		return websiteapi.Page{}, websiteapi.ErrConflict
	}
	switch request.LifecycleState {
	case "retiring":
		if page.LifecycleState != "active" && page.RetirementJobID == request.RetirementJobID {
			return page, nil
		}
		if page.LifecycleState != "active" {
			return websiteapi.Page{}, websiteapi.ErrConflict
		}
	case "retired":
		if page.LifecycleState != "retiring" {
			return websiteapi.Page{}, websiteapi.ErrConflict
		}
	}
	page.LifecycleState = request.LifecycleState
	page.RetirementJobID = request.RetirementJobID
	value, err := pageModuleDataValue(page, record.Revision+1)
	if err != nil {
		return websiteapi.Page{}, err
	}
	updatedRevision := record.Revision + 1
	if err := s.store.Batch(ctx, []core.ModuleDataMutation{{
		Key: websiterepo.PageKey(page.ID), ExpectedRevision: record.Revision, Value: value,
	}}); err != nil {
		return websiteapi.Page{}, mapCoreError(err)
	}
	page.Revision = updatedRevision
	return page, nil
}
