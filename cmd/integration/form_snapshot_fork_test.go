package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

func TestSnapshotForkCopiesModelBackedFormContentWithoutCopyingSubmissions(t *testing.T) {
	requireDisposableModelContentDatabase(t)
	env := newTestEnv(t)
	ctx := context.Background()
	actor := modelAcceptanceActor(t, env)
	key := "snapshot-form-model-fork"

	manifest := map[string]any{
		"formatVersion": 2,
		"key":           key,
		"name":          "Snapshot form",
		"version":       "2.0.0",
		"engineApi":     "2",
		"runtime":       map[string]any{"abi": "static-module-v1", "entry": "entry.js"},
		"slots":         []string{"main"},
		"arguments":     map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		"dataInputs": []any{map[string]any{
			"key": "settings", "kind": "record", "fields": []any{map[string]any{"path": "/submitLabel", "type": "text"}},
		}},
		"form": map[string]any{"submissionSchema": map[string]any{
			"type": "object", "properties": map[string]any{"email": map[string]any{"type": "string", "format": "email"}},
			"required": []string{"email"}, "additionalProperties": false,
		}},
	}
	modelAcceptanceUploadFeatureBuild(t, env, manifest, "export default function Feature() { return null; }")

	entityID, fieldID := uuid.NewString(), uuid.NewString()
	model := sitebuilder.DataModel{FormatVersion: 1, Entities: []sitebuilder.DataModelEntity{{
		ID: entityID, Key: "form_settings", Name: "Form settings", Fields: []sitebuilder.DataModelField{{
			ID: fieldID, Key: "submit_label", Name: "Submit label", Type: "text",
		}},
	}}}
	config := sitebuilder.SiteConfig{
		FormatVersion: 3,
		DataModel:     &model,
		Pages: []sitebuilder.Page{{ID: "home", Name: "Home", Kind: "page", Placements: []sitebuilder.Placement{{
			ID: "form-placement", Build: sitebuilder.BuildRef{FeatureKey: key, Version: "2.0.0"}, Slot: "main",
			DataBindings: []sitebuilder.DataInputBinding{{InputKey: "settings", EntityID: entityID, Fields: []sitebuilder.DataInputFieldBinding{{InputPath: "/submitLabel", FieldID: fieldID}}}},
		}}}},
		RouteTree: sitebuilder.RouteTreeNode{ID: "root", Type: "site", Children: []sitebuilder.RouteTreeNode{{ID: "home-route", Type: "path", Path: "/", PageID: "home"}}},
	}
	configRaw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var configMap map[string]any
	if err := json.Unmarshal(configRaw, &configMap); err != nil {
		t.Fatal(err)
	}
	parentJSON := modelAcceptanceSaveAndDeploySnapshot(t, env, configMap, "form source")
	parentID := uuid.MustParse(parentJSON["id"].(string))

	firstDraft, err := env.snapshots.SaveModelContentWorkspace(ctx, actor, parentID, sitebuilder.ModelContentDraftInput{
		Revision: 0,
		Records:  []sitebuilder.ModelContentRecord{{EntityID: entityID, Values: map[string]any{"submit_label": "Отправить"}}},
	})
	if err != nil {
		t.Fatalf("save initial form copy: %v", err)
	}
	if len(firstDraft.Records) != 1 || firstDraft.Records[0].ID == "" {
		t.Fatalf("initial form draft did not return a stable record ID: %+v", firstDraft.Records)
	}
	selectedDraft, err := env.snapshots.SaveModelContentWorkspace(ctx, actor, parentID, sitebuilder.ModelContentDraftInput{
		Revision: firstDraft.Revision,
		Records:  firstDraft.Records,
		Selections: []sitebuilder.ModelContentSelection{{
			PlacementID: "form-placement", InputKey: "settings", Mode: "ids", RecordIDs: []string{firstDraft.Records[0].ID},
		}},
	})
	if err != nil {
		t.Fatalf("select form model record for its feature placement: %v", err)
	}
	published, err := env.snapshots.PublishModelContentWorkspace(ctx, actor, parentID, selectedDraft.Revision)
	if err != nil {
		t.Fatalf("publish initial form copy: %v", err)
	}
	if published.PublishedRevision != selectedDraft.Revision || len(published.Records) != 1 || len(published.Selections) != 1 {
		t.Fatalf("initial form publication has unexpected state: %+v", published)
	}
	formRecord := published.Records[0]
	draft, err := env.snapshots.SaveModelContentWorkspace(ctx, actor, parentID, sitebuilder.ModelContentDraftInput{
		Revision:   published.Revision,
		Records:    []sitebuilder.ModelContentRecord{{ID: formRecord.ID, EntityID: entityID, Values: map[string]any{"submit_label": "Черновик"}}},
		Selections: published.Selections,
	})
	if err != nil {
		t.Fatalf("save unpublished form copy: %v", err)
	}

	env.forms.SetSnapshotSchemaProvider(env.snapshots)
	submission, err := env.forms.Submit(ctx, env.devBranch, key, json.RawMessage(`{"email":"reader@example.com"}`))
	if err != nil {
		t.Fatalf("create source-branch submission: %v", err)
	}
	var submissionsBefore int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM form_submissions WHERE environment=$1`, string(env.devBranch)).Scan(&submissionsBefore); err != nil {
		t.Fatal(err)
	}
	if submissionsBefore != 1 {
		t.Fatalf("source submission was not stored once before fork: count=%d", submissionsBefore)
	}

	child, err := env.snapshots.CreateSnapshot(ctx, actor, sitebuilder.CreateSnapshotInput{
		Name: "form child", ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("fork form snapshot: %v", err)
	}
	childWorkspace, err := env.snapshots.ModelContentWorkspace(ctx, child.ID)
	if err != nil {
		t.Fatalf("read copied model content: %v", err)
	}
	if childWorkspace.Revision != draft.Revision || childWorkspace.PublishedRevision != published.PublishedRevision || len(childWorkspace.Records) != 1 {
		t.Fatalf("form draft and published revisions were not inherited independently: %+v", childWorkspace)
	}
	if childWorkspace.Records[0].ID != formRecord.ID || childWorkspace.Records[0].Values["submit_label"] != "Черновик" {
		t.Fatalf("fork did not preserve stable record identity and draft values: %+v", childWorkspace.Records)
	}
	if child.CreationSummary == nil || len(child.CreationSummary.InheritedModelContent) != 1 || child.CreationSummary.InheritedModelContent[0].EntityKey != "form_settings" {
		t.Fatalf("snapshot provenance should summarize copied model records without values: %+v", child.CreationSummary)
	}

	assignSnapshotToBranch(t, env, string(env.prodBranch), child.ID.String())
	childRuntime := modelAcceptanceRuntimePlacement(t, env, env.prodServer.URL, "/")
	childContent := childRuntime["content"].(map[string]any)["settings"].(map[string]any)
	if childContent["submitLabel"] != "Отправить" {
		t.Fatalf("child runtime must expose published form copy only: %v", childRuntime["content"])
	}
	parentRuntime := modelAcceptanceRuntimePlacement(t, env, env.devServer.URL, "/")
	parentContent := parentRuntime["content"].(map[string]any)["settings"].(map[string]any)
	if parentContent["submitLabel"] != "Отправить" {
		t.Fatalf("child assignment must not alter source-branch form copy: %v", parentRuntime["content"])
	}

	var submissionsAfter int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM form_submissions WHERE environment=$1`, string(env.devBranch)).Scan(&submissionsAfter); err != nil {
		t.Fatal(err)
	}
	if submissionsAfter != submissionsBefore {
		t.Fatalf("snapshot fork copied a form submission: before=%d after=%d", submissionsBefore, submissionsAfter)
	}
	stored, err := env.forms.Get(ctx, submission.ID)
	if err != nil || stored.ID != submission.ID || stored.Environment != string(env.devBranch) {
		t.Fatalf("source submission should remain unchanged on its source branch: submission=%+v err=%v", stored, err)
	}
	branch, err := env.snapshots.Branch(ctx, uuid.MustParse(string(env.devBranch)))
	if err != nil || branch.SnapshotID == nil || *branch.SnapshotID != parentID {
		t.Fatalf("forking must not change the source branch assignment: branch=%+v err=%v", branch, err)
	}
}
