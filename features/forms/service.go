package forms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"websiteengine/features/features"
	"websiteengine/features/files"
	"websiteengine/pkg/environment"
	"websiteengine/pkg/platform/validate"
	"websiteengine/pkg/response"
)

type ValidationError struct {
	Message string
	Details []validate.FieldError
}

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrValidation }

type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }
func (e *ConflictError) Unwrap() error { return ErrConflict }

type ExportLimitError struct{ MaxRows int }

func (e *ExportLimitError) Error() string {
	return fmt.Sprintf("Экспорт превышает настроенный предел (%d строк). Уточните фильтры", e.MaxRows)
}

type Service struct {
	repo          *Repository
	featureRepo   *features.Repository
	schemas       SnapshotSchemaProvider
	maxPageSize   int
	exportMaxRows int
}

type SnapshotSchemaProvider interface {
	FormSchema(context.Context, environment.Environment, string) (uuid.UUID, json.RawMessage, error)
}

type transactionalSnapshotSchemaProvider interface {
	FormSchemaTx(context.Context, pgx.Tx, environment.Environment, string) (uuid.UUID, json.RawMessage, error)
}

type SubmissionListSchemaProvider interface {
	SnapshotSchemaProvider
	FormSchemaForListing(context.Context, environment.Environment, string) (uuid.UUID, json.RawMessage, error)
}

// SubmissionSchemaCatalog includes the current form contracts of configured
// branches; historical schemas are merged independently from stored rows.
type SubmissionSchemaCatalog interface {
	FormSchemasForListing(context.Context, string) ([]json.RawMessage, error)
}

// BranchSubmissionSchemaCatalog supports the all-forms view without mixing
// fields from a different branch when a particular branch is selected.
type BranchSubmissionSchemaCatalog interface {
	FormSchemasForBranchListing(context.Context, environment.Environment, string) ([]json.RawMessage, error)
}

func NewService(repo *Repository, featureRepo *features.Repository) *Service {
	return &Service{repo: repo, featureRepo: featureRepo}
}

func (s *Service) SetSnapshotSchemaProvider(provider SnapshotSchemaProvider) {
	s.schemas = provider
}

// Submit validates and stores a public form submission atomically against the
// currently published schema version.
func (s *Service) Submit(ctx context.Context, env environment.Environment, key string, data json.RawMessage) (Submission, error) {
	if err := validateObject(data); err != nil {
		return Submission{}, err
	}
	if s.schemas == nil {
		return Submission{}, errors.New("snapshot schema provider is not configured")
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return Submission{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_file_references'))`); err != nil {
		return Submission{}, err
	}
	var form FormSchema
	provider, ok := s.schemas.(transactionalSnapshotSchemaProvider)
	if !ok {
		return Submission{}, errors.New("snapshot schema provider does not support transactional submissions")
	}
	schemaID, schema, err := provider.FormSchemaTx(ctx, tx, env, key)
	if err != nil {
		return Submission{}, err
	}
	var featureID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM features WHERE key=$1`, key).Scan(&featureID); err != nil {
		return Submission{}, translateDBError(err)
	}
	form = FormSchema{FeatureID: featureID, FeatureKey: key, VersionID: schemaID, Schema: schema, Environment: env}
	fieldErrors, err := validate.Validate(form.Schema, data)
	if err != nil {
		return Submission{}, fmt.Errorf("validate form data: %w", err)
	}
	if len(fieldErrors) > 0 {
		return Submission{}, &ValidationError{Message: "Данные не соответствуют форме", Details: fieldErrors}
	}
	if err := files.ValidateReferencesTx(ctx, tx, form.Schema, data, false); err != nil {
		return Submission{}, &ValidationError{Message: "Указанный файл недоступен"}
	}
	submission := Submission{
		ID: uuid.New(), FeatureID: form.FeatureID, FeatureKey: form.FeatureKey,
		SchemaVersionID: form.VersionID, Environment: form.Environment.String(), Data: data, Status: "new",
	}
	if err := s.repo.InsertSubmission(ctx, tx, submission); err != nil {
		return Submission{}, translateDBError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Submission{}, translateDBError(err)
	}
	return submission, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Submission, error) {
	return s.repo.Get(ctx, s.repo.pool, id)
}

type ListFilter struct {
	Environment     environment.Environment
	Status          string
	CreatedFrom     *time.Time
	CreatedTo       *time.Time
	Search          string
	SearchField     string
	IncludeArchived bool
	Sort            string
	BeforeTime      *time.Time
	BeforeID        *uuid.UUID
	Limit           int
}

type FieldDefinition struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

func (s *Service) List(ctx context.Context, key string, filter ListFilter) ([]Submission, string, []FieldDefinition, error) {
	if s.maxPageSize < 1 {
		return nil, "", nil, errors.New("submission pagination limits are not configured")
	}
	if filter.Limit < 1 || filter.Limit > s.maxPageSize {
		return nil, "", nil, &ValidationError{Message: fmt.Sprintf("limit должен быть от 1 до %d", s.maxPageSize)}
	}
	feature, form, fields, err := s.formContext(ctx, filter.Environment, key)
	if err != nil {
		return nil, "", nil, err
	}
	_ = feature
	if filter.Status != "" && !validStatus(filter.Status) {
		return nil, "", nil, &ValidationError{Message: "Некорректный status"}
	}
	searchFields, err := chooseSearchFields(fields, filter.SearchField)
	if err != nil {
		return nil, "", nil, err
	}
	if filter.Search != "" && len(searchFields) == 0 {
		return nil, "", nil, &ValidationError{Message: "В схеме формы нет полей, доступных для поиска"}
	}
	params := ListParams{
		FeatureID: form.FeatureID, Environment: filter.Environment, Status: filter.Status, CreatedFrom: filter.CreatedFrom,
		CreatedTo: filter.CreatedTo, SearchFields: searchFields, Search: filter.Search,
		IncludeArchived: filter.IncludeArchived, SortAsc: filter.Sort == "asc",
		BeforeTime: filter.BeforeTime, BeforeID: filter.BeforeID, Limit: filter.Limit + 1,
	}
	items, err := s.repo.List(ctx, s.repo.pool, params)
	if err != nil {
		return nil, "", nil, err
	}
	next := ""
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		last := items[len(items)-1]
		next = response.EncodeCursor(last.CreatedAt, last.ID)
	}
	return items, next, fields, nil
}

// Export rejects a result set above its configured ceiling rather than
// returning a CSV that silently omits matching submissions.
func (s *Service) Export(ctx context.Context, key string, filter ListFilter) ([]Submission, []FieldDefinition, error) {
	if s.maxPageSize < 1 || s.exportMaxRows < 1 {
		return nil, nil, errors.New("submission pagination and export limits are not configured")
	}
	_, form, fields, err := s.formContext(ctx, filter.Environment, key)
	if err != nil {
		return nil, nil, err
	}
	if filter.Status != "" && !validStatus(filter.Status) {
		return nil, nil, &ValidationError{Message: "Некорректный status"}
	}
	searchFields, err := chooseSearchFields(fields, filter.SearchField)
	if err != nil {
		return nil, nil, err
	}
	if filter.Search != "" && len(searchFields) == 0 {
		return nil, nil, &ValidationError{Message: "В схеме формы нет полей, доступных для поиска"}
	}
	items, err := s.repo.List(ctx, s.repo.pool, ListParams{
		FeatureID: form.FeatureID, Environment: filter.Environment, Status: filter.Status, CreatedFrom: filter.CreatedFrom,
		CreatedTo: filter.CreatedTo, SearchFields: searchFields, Search: filter.Search,
		IncludeArchived: filter.IncludeArchived, SortAsc: filter.Sort == "asc", Limit: s.exportMaxRows + 1,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(items) > s.exportMaxRows {
		return nil, nil, &ExportLimitError{MaxRows: s.exportMaxRows}
	}
	return items, fields, nil
}

func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status string, actor uuid.UUID) (Submission, error) {
	if !validStatus(status) {
		return Submission{}, &ValidationError{Message: "status должен быть new, in_progress, processed или rejected"}
	}
	submission, err := s.repo.UpdateStatus(ctx, id, status, actor)
	return submission, translateDBError(err)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) (Submission, error) {
	if err := s.repo.Archive(ctx, id); err != nil {
		return Submission{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return translateDBError(s.repo.Delete(ctx, id))
}

func (s *Service) formContext(ctx context.Context, env environment.Environment, key string) (features.Feature, FormSchema, []FieldDefinition, error) {
	if s.schemas == nil {
		return features.Feature{}, FormSchema{}, nil, errors.New("snapshot schema provider is not configured")
	}
	if key == "" {
		historical, err := s.repo.HistoricalSubmissionSchemas(ctx, uuid.Nil, env)
		if err != nil {
			return features.Feature{}, FormSchema{}, nil, err
		}
		var fields []FieldDefinition
		for _, schema := range historical {
			fields = mergeSchemaFields(fields, schemaFields(schema))
		}
		if catalog, ok := s.schemas.(BranchSubmissionSchemaCatalog); ok {
			schemas, err := catalog.FormSchemasForBranchListing(ctx, env, "")
			if err != nil {
				return features.Feature{}, FormSchema{}, nil, err
			}
			for _, schema := range schemas {
				fields = mergeSchemaFields(fields, schemaFields(schema))
			}
		}
		return features.Feature{}, FormSchema{}, fields, nil
	}
	feature, err := s.featureRepo.GetIdentityByKey(ctx, key)
	if err != nil {
		return features.Feature{}, FormSchema{}, nil, err
	}
	if feature.Type != "form" {
		return features.Feature{}, FormSchema{}, nil, &ValidationError{Message: "Feature не является формой"}
	}
	historical, err := s.repo.HistoricalSubmissionSchemas(ctx, feature.ID, env)
	if err != nil {
		return features.Feature{}, FormSchema{}, nil, err
	}
	historyFields := []FieldDefinition{}
	for _, schema := range historical {
		historyFields = mergeSchemaFields(historyFields, schemaFields(schema))
	}
	if s.schemas != nil {
		listSchema := func(ctx context.Context, env environment.Environment, key string) (uuid.UUID, json.RawMessage, error) {
			if provider, ok := s.schemas.(SubmissionListSchemaProvider); ok {
				return provider.FormSchemaForListing(ctx, env, key)
			}
			return s.schemas.FormSchema(ctx, env, key)
		}
		if env == "" {
			if catalog, ok := s.schemas.(SubmissionSchemaCatalog); ok {
				schemas, err := catalog.FormSchemasForListing(ctx, key)
				if err != nil {
					return features.Feature{}, FormSchema{}, nil, err
				}
				fields := historyFields
				for _, schema := range schemas {
					fields = mergeSchemaFields(fields, schemaFields(schema))
				}
				return feature, FormSchema{FeatureID: feature.ID, FeatureKey: feature.Key}, fields, nil
			}
			return features.Feature{}, FormSchema{}, nil, errors.New("snapshot schema provider does not expose branch form catalogs")
		}
		schemaEnv := env
		schemaID, schema, err := listSchema(ctx, schemaEnv, key)
		if err != nil {
			return features.Feature{}, FormSchema{}, nil, err
		}
		fields := mergeSchemaFields(historyFields, schemaFields(schema))
		return feature, FormSchema{FeatureID: feature.ID, FeatureKey: feature.Key, VersionID: schemaID, Schema: schema, Environment: schemaEnv}, fields, nil
	}
	return features.Feature{}, FormSchema{}, nil, errors.New("snapshot schema provider is not configured")
}

func fieldsForSchema(schema json.RawMessage, err error) []FieldDefinition {
	if err != nil {
		return nil
	}
	return schemaFields(schema)
}

func mergeSchemaFields(groups ...[]FieldDefinition) []FieldDefinition {
	byKey := make(map[string]FieldDefinition)
	for _, fields := range groups {
		for _, field := range fields {
			if existing, ok := byKey[field.Key]; ok {
				if strings.TrimSpace(existing.Label) == "" {
					existing.Label = field.Label
					byKey[field.Key] = existing
				}
				continue
			}
			byKey[field.Key] = field
		}
	}
	fields := make([]FieldDefinition, 0, len(byKey))
	for _, field := range byKey {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return fields
}

func schemaFields(schema json.RawMessage) []FieldDefinition {
	if len(schema) == 0 {
		return nil
	}
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil
	}
	props, _ := root["properties"].(map[string]any)
	fields := make([]FieldDefinition, 0, len(props))
	for name, raw := range props {
		prop, _ := raw.(map[string]any)
		typ, _ := prop["type"].(string)
		switch typ {
		case "string", "number", "integer", "boolean", "":
			label, _ := prop["title"].(string)
			if strings.TrimSpace(label) == "" {
				label = name
			}
			fields = append(fields, FieldDefinition{Key: name, Label: label})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return fields
}

func chooseSearchFields(fields []FieldDefinition, requested string) ([]string, error) {
	if requested == "" {
		keys := make([]string, len(fields))
		for i, field := range fields {
			keys[i] = field.Key
		}
		return keys, nil
	}
	for _, field := range fields {
		if field.Key == requested {
			return []string{requested}, nil
		}
	}
	return nil, &ValidationError{Message: "Поле поиска отсутствует в схеме формы"}
}

func validStatus(status string) bool {
	switch status {
	case "new", "in_progress", "processed", "rejected":
		return true
	}
	return false
}

func validateObject(data json.RawMessage) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return &ValidationError{Message: "Тело заявки обязательно"}
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj == nil {
		return &ValidationError{Message: "Тело заявки должно быть JSON-объектом"}
	}
	return nil
}

func translateDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrValidation) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23000", "23503", "23514":
			return &ConflictError{Message: "Операция нарушает целостность данных"}
		}
	}
	return fmt.Errorf("forms: %w", err)
}

func (s *Service) BranchChoices(ctx context.Context) ([]BranchChoice, error) {
	return s.repo.BranchChoices(ctx)
}

// SetLimits binds production behavior to explicit operator configuration.
func (s *Service) SetLimits(maxPageSize, exportMaxRows int) error {
	if maxPageSize < 1 || exportMaxRows < 1 || exportMaxRows == int(^uint(0)>>1) {
		return errors.New("submission page and export limits must be positive")
	}
	s.maxPageSize = maxPageSize
	s.exportMaxRows = exportMaxRows
	return nil
}
