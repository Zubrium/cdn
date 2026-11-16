package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"websiteengine/features/sitebuilder"
)

// modelAcceptanceBindManifestInputs builds the minimum explicit snapshot
// model required by current feature builds. These fixtures intentionally
// derive their input contract from the ZIP manifest rather than carrying the
// retired per-feature editor schema.
func modelAcceptanceBindManifestInputs(t *testing.T, rawConfig map[string]any, manifests []sitebuilder.Manifest) map[string]any {
	t.Helper()
	raw, err := json.Marshal(rawConfig)
	if err != nil {
		t.Fatal(err)
	}
	var config sitebuilder.SiteConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode snapshot fixture before adding manifest-backed model: %v", err)
	}
	byBuild := make(map[string]sitebuilder.Manifest, len(manifests))
	for _, manifest := range manifests {
		byBuild[manifest.Key+"@"+manifest.Version] = manifest
	}
	model := sitebuilder.DataModel{FormatVersion: 1}
	if config.DataModel != nil {
		model = *config.DataModel
		if model.FormatVersion == 0 {
			model.FormatVersion = 1
		}
	}
	for pageIndex := range config.Pages {
		for placementIndex := range config.Pages[pageIndex].Placements {
			placement := &config.Pages[pageIndex].Placements[placementIndex]
			manifest, exists := byBuild[placement.Build.FeatureKey+"@"+placement.Build.Version]
			if !exists || len(manifest.DataInputs) == 0 {
				continue
			}
			for _, port := range manifest.DataInputs {
				entityIndex := len(model.Entities)
				entity := sitebuilder.DataModelEntity{
					ID: uuid.NewString(), Key: fmt.Sprintf("qa_input_%d", entityIndex),
					Name: fmt.Sprintf("%s %s", manifest.Name, port.Key), Fields: []sitebuilder.DataModelField{},
				}
				binding := sitebuilder.DataInputBinding{InputKey: port.Key, EntityID: entity.ID}
				fields := port.Fields
				if port.Kind == "scalar" {
					fields = []sitebuilder.DataInputField{{Path: "/value", Type: port.Type, Values: port.Values}}
				}
				for fieldIndex, inputField := range fields {
					field := sitebuilder.DataModelField{
						ID: uuid.NewString(), Key: fmt.Sprintf("field_%d", fieldIndex),
						Name: fmt.Sprintf("%s %d", port.Key, fieldIndex+1), Type: inputField.Type,
						Options: append([]string(nil), inputField.Values...),
					}
					entity.Fields = append(entity.Fields, field)
					if port.Kind == "scalar" {
						binding.FieldID = field.ID
					} else {
						binding.Fields = append(binding.Fields, sitebuilder.DataInputFieldBinding{InputPath: inputField.Path, FieldID: field.ID})
					}
				}
				model.Entities = append(model.Entities, entity)
				placement.DataBindings = append(placement.DataBindings, binding)
			}
		}
	}
	if len(model.Entities) > 0 || config.DataModel != nil {
		config.DataModel = &model
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("encode manifest-backed model fixture: %v", err)
	}
	return result
}

func modelAcceptanceManifestFromArchive(t *testing.T, archive []byte) sitebuilder.Manifest {
	t.Helper()
	verified, err := sitebuilder.VerifyArchive(archive, int64(len(archive)))
	if err != nil {
		t.Fatalf("verify manifest for model-backed acceptance fixture: %v", err)
	}
	return verified.Manifest
}

func modelAcceptanceUploadFeatureBuild(t *testing.T, env *testEnv, manifest map[string]any, entry string) map[string]any {
	t.Helper()
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, item := range []struct {
		name    string
		content []byte
	}{
		{name: "manifest.json", content: manifestRaw},
		{name: "entry.js", content: []byte(entry)},
	} {
		file, err := writer.Create(item.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(item.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	status, body, _ := env.rawRequest(http.MethodPost, "/api/v2/admin/feature-builds", env.adminToken, &archive, "application/zip")
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("upload model acceptance feature build: status=%d body=%v", status, body)
	}
	return body
}

func modelAcceptanceSaveAndDeploySnapshot(t *testing.T, env *testEnv, config map[string]any, name string) map[string]any {
	t.Helper()
	request := map[string]any{"name": name, "config": config}
	if os.Getenv("LIGHTHOUSE_BIN") != "" {
		metadata := sitebuilder.DefaultSiteMetadata()
		metadata.CanonicalDomain = "https://vitasil.example.test"
		metadata.SiteName = "Vitasil Lighthouse QA"
		metadata.Description = "Disposable Vitasil fixture for local Lighthouse analysis."
		request["metadata"] = metadata
	}
	status, snapshot, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, request)
	if status != http.StatusCreated {
		t.Fatalf("create model acceptance snapshot: status=%d body=%v", status, snapshot)
	}
	snapshotID, err := uuid.Parse(snapshot["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	branchID, err := uuid.Parse(string(env.devBranch))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := env.snapshots.Branch(context.Background(), branchID)
	if err != nil {
		t.Fatalf("load model acceptance branch: %v", err)
	}
	actor := modelAcceptanceActor(t, env)
	contentRevision := int64(snapshot["contentRevision"].(float64))
	if _, err := env.snapshots.AssignBranch(context.Background(), actor, branchID, snapshotID, branch.Revision, &contentRevision); err != nil {
		t.Fatalf("assign model acceptance snapshot: %v", err)
	}
	return snapshot
}

func modelAcceptanceCreateChildSnapshot(t *testing.T, env *testEnv, parentID, name string) map[string]any {
	t.Helper()
	status, snapshot, _ := env.request(http.MethodPost, "/api/v2/admin/site/snapshots", env.adminToken, map[string]any{
		"name": name, "parentSnapshotId": parentID,
	})
	if status != http.StatusCreated {
		t.Fatalf("create model acceptance child: status=%d body=%v", status, snapshot)
	}
	return snapshot
}

func modelAcceptanceActor(t *testing.T, env *testEnv) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.pool.QueryRow(context.Background(), `SELECT id FROM access_tokens WHERE role='admin' LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func modelAcceptanceRuntimePlacement(t *testing.T, env *testEnv, serverURL, path string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/api/v2/runtime?path="+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get model acceptance runtime: status=%d body=%s", response.StatusCode, raw)
	}
	var composition map[string]any
	if err := json.Unmarshal(raw, &composition); err != nil {
		t.Fatal(err)
	}
	placements, ok := composition["placements"].([]any)
	if !ok || len(placements) != 1 {
		t.Fatalf("expected exactly one model acceptance placement, got %v", composition["placements"])
	}
	placement, ok := placements[0].(map[string]any)
	if !ok {
		t.Fatalf("runtime placement has unexpected shape: %T", placements[0])
	}
	return placement
}
