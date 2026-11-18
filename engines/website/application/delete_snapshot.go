package application

import (
	"context"
	"encoding/json"
	"strconv"

	core "github.com/Zubrium/cdn/core/presentation"
	websiteapi "github.com/Zubrium/cdn/engines/website/presentation"
)

// DeleteSnapshot removes an unprotected snapshot and reparents its children in
// one Core module-data CAS batch. It never deletes referenced Core files.
func (s *Service) DeleteSnapshot(ctx context.Context, request websiteapi.DeleteSnapshotRequest) error {
	if err := s.requireLevel(ctx, core.AccessLevelWrite); err != nil {
		return err
	}
	if !snapshotIDPattern.MatchString(request.SnapshotID) || request.ExpectedRevision < 1 {
		return websiteapi.ErrInvalid
	}
	target, targetKey, targetRecord, err := s.findSnapshot(ctx, request.SnapshotID)
	if err != nil {
		return err
	}
	if targetRecord.Revision != request.ExpectedRevision {
		return websiteapi.ErrConflict
	}
	registryRecord, registryState, err := s.loadSiteRegistry(ctx, target.SiteID)
	if err != nil {
		return err
	}
	registryState.LastSequence, err = s.ensureSiteSequence(ctx, target.SiteID, registryState)
	if err != nil {
		return err
	}
	// Re-read after the registry revision. Every concurrent create/assignment
	// commits against that same revision, so this snapshot read and the final
	// batch form a safe optimistic transaction across separate Core records.
	target, targetRecord, err = s.snapshotInSite(ctx, target.SiteID, request.SnapshotID)
	if err != nil {
		return err
	}
	targetKey = snapshotKey(target.SiteID, target.ID)
	if targetRecord.Revision != request.ExpectedRevision {
		return websiteapi.ErrConflict
	}
	if err := s.checkSnapshotDeletionProtection(ctx, target); err != nil {
		return err
	}

	var newParent *websiteapi.Snapshot
	if target.ParentID != "" {
		parent, _, err := s.snapshotInSite(ctx, target.SiteID, target.ParentID)
		if err != nil {
			// A missing parent indicates corrupt tree state; deleting would make
			// the remaining children impossible to place consistently.
			return websiteapi.ErrUnavailable
		}
		newParent = &parent
	}

	selectors := []core.ModuleDataSelector{
		{Field: "record_type", Operator: core.SelectorEqual, Values: []any{"snapshot"}},
		{Field: "parent_id", Operator: core.SelectorEqual, Values: []any{target.ID}},
	}
	childrenKeys, childrenRecords, err := s.listSnapshotChildren(ctx, target.SiteID, selectors)
	if err != nil {
		return err
	}
	if len(childrenKeys)+2 > s.store.MaxBatchSize() {
		return websiteapi.ErrTooLarge
	}

	mutations := make([]core.ModuleDataMutation, 0, len(childrenKeys)+2)
	for i, childRecord := range childrenRecords {
		childKey := childrenKeys[i]
		child, err := decodeSnapshotRecord(target.SiteID, childKey, childRecord)
		if err != nil {
			return err
		}
		if child.ParentID != target.ID {
			return websiteapi.ErrUnavailable
		}
		child.ParentID = ""
		child.ParentName = ""
		child.ParentSequence = 0
		if newParent != nil {
			child.ParentID = newParent.ID
			child.ParentName = newParent.Name
			child.ParentSequence = newParent.Sequence
		}
		child.Revision = childRecord.Revision + 1
		payload, err := json.Marshal(child)
		if err != nil {
			return websiteapi.ErrUnavailable
		}
		var index map[string]json.RawMessage
		if err := json.Unmarshal(childRecord.IndexData, &index); err != nil || index == nil {
			return websiteapi.ErrUnavailable
		}
		if newParent == nil {
			delete(index, "parent_id")
		} else {
			index["parent_id"], err = json.Marshal(newParent.ID)
			if err != nil {
				return websiteapi.ErrUnavailable
			}
		}
		index["revision"] = json.RawMessage(strconv.FormatInt(child.Revision, 10))
		indexPayload, err := json.Marshal(index)
		if err != nil {
			return websiteapi.ErrUnavailable
		}
		mutations = append(mutations, core.ModuleDataMutation{
			Key: childKey, ExpectedRevision: childRecord.Revision,
			Value: &core.ModuleDataValue{Payload: payload, PayloadVersion: childRecord.PayloadVersion, IndexData: indexPayload},
		})
	}
	mutations = append(mutations, core.ModuleDataMutation{Key: targetKey, ExpectedRevision: targetRecord.Revision})
	registryMutation, err := siteRegistryMutation(target.SiteID, registryRecord, registryState.LastSequence)
	if err != nil {
		return err
	}
	mutations = append(mutations, registryMutation)
	if err := s.store.Batch(ctx, mutations); err != nil {
		return mapCoreError(err)
	}
	return nil
}

func (s *Service) listSnapshotChildren(ctx context.Context, siteID string, selectors []core.ModuleDataSelector) ([]core.ModuleDataKey, []core.ModuleDataRecord, error) {
	pageLimit := s.store.MaxPageSize()
	if batchLimit := s.store.MaxBatchSize(); pageLimit > batchLimit {
		pageLimit = batchLimit
	}
	keys := make([]core.ModuleDataKey, 0)
	records := make([]core.ModuleDataRecord, 0)
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		pageKeys, pageRecords, next, err := s.store.List(ctx, snapshotsNamespace, "site", siteID, after, selectors, pageLimit)
		if err != nil {
			return nil, nil, mapCoreError(err)
		}
		if len(pageKeys) != len(pageRecords) {
			return nil, nil, websiteapi.ErrUnavailable
		}
		keys = append(keys, pageKeys...)
		records = append(records, pageRecords...)
		if len(keys)+1 > s.store.MaxBatchSize() {
			return nil, nil, websiteapi.ErrTooLarge
		}
		if next == "" {
			return keys, records, nil
		}
		if len(keys)+1 >= s.store.MaxBatchSize() {
			return nil, nil, websiteapi.ErrTooLarge
		}
		if next == after {
			return nil, nil, websiteapi.ErrUnavailable
		}
		after = next
	}
}

func (s *Service) checkSnapshotDeletionProtection(ctx context.Context, target websiteapi.Snapshot) error {
	for _, branchID := range s.branchOrder {
		branch := s.branches[branchID]
		if branch.SiteID != target.SiteID {
			continue
		}
		state, _, err := s.loadBranchState(ctx, branch)
		if err != nil {
			return err
		}
		if state.SnapshotID == "" {
			continue
		}
		if state.SnapshotID == target.ID {
			return websiteapi.ErrConflict
		}
		assigned, _, err := s.snapshotInSite(ctx, target.SiteID, state.SnapshotID)
		if err != nil {
			return websiteapi.ErrUnavailable
		}
		if assigned.ParentID == target.ID {
			return websiteapi.ErrConflict
		}
	}
	return nil
}
