package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"

	"websiteengine/pkg/storage"
)

const cacheControl = "public, max-age=31536000, immutable"

type Service struct {
	repo               *Repository
	store              storage.ObjectStore
	maxUpload          int64
	processor          RenditionProcessor
	processorErr       error
	referenceChecker   func(context.Context, uuid.UUID) (bool, error)
	renditionLease     time.Duration
	workerPollInterval time.Duration
}

func NewServiceWithProcessor(repo *Repository, store storage.ObjectStore, maxUpload int64, processor RenditionProcessor) *Service {
	return &Service{repo: repo, store: store, maxUpload: maxUpload, processor: processor}
}

func (s *Service) SetRenditionWorkerConfig(lease, pollInterval time.Duration) error {
	if lease <= 0 || pollInterval <= 0 {
		return fmt.Errorf("media rendition lease and worker poll interval must be explicitly configured")
	}
	s.renditionLease, s.workerPollInterval = lease, pollInterval
	return nil
}

func (s *Service) Upload(ctx context.Context, actor principal.Principal, filename string, data []byte) (Media, error) {
	if len(data) == 0 {
		return Media{}, &ValidationError{Message: "Файл пуст"}
	}
	if int64(len(data)) > s.maxUpload {
		return Media{}, &ValidationError{Message: "Файл превышает допустимый размер"}
	}
	filename = sanitizeFilename(filename)
	contentType := http.DetectContentType(data[:min(len(data), 512)])
	if strings.EqualFold(filepath.Ext(filename), ".woff2") && len(data) >= 4 && string(data[:4]) == "wOF2" {
		contentType = "font/woff2"
	}
	if strings.EqualFold(filepath.Ext(filename), ".svg") && looksLikeSVG(data) {
		contentType = "image/svg+xml"
	}
	ext, ok := allowedContentTypes[contentType]
	if !ok {
		return Media{}, &ValidationError{Message: "Неподдерживаемый тип файла"}
	}
	if contentType == "image/svg+xml" {
		var err error
		data, err = sanitizeSVG(data)
		if err != nil {
			return Media{}, &ValidationError{Message: "Небезопасный или некорректный SVG-файл"}
		}
	}
	if isRasterImage(contentType) {
		if _, err := inspectImage(data); err != nil {
			return Media{}, &ValidationError{Message: "Повреждённое или слишком большое изображение"}
		}
	}
	digest := sha256.Sum256(data)
	creator := actor.TokenID
	id := uuid.New()
	objectKey := fmt.Sprintf("media/%s/original%s", id, ext)
	m := Media{
		ID: id, ObjectKey: objectKey, OriginalFilename: filename, ContentType: contentType,
		SizeBytes: int64(len(data)), Status: "processing", Variants: map[string]string{},
		UploadedBy: &creator, SHA256: hex.EncodeToString(digest[:]),
	}
	_, err := s.repo.Create(ctx, m)
	if err != nil {
		return Media{}, err
	}
	if err := s.store.Put(ctx, objectKey, bytes.NewReader(data), int64(len(data)), storage.PutOptions{
		ContentType: contentType, CacheControl: cacheControl,
	}); err != nil {
		_ = s.repo.SetFailed(context.Background(), id)
		return Media{}, fmt.Errorf("upload original: %w", err)
	}
	ready, err := s.repo.SetReady(ctx, id, map[string]string{"original": objectKey})
	if err != nil {
		return Media{}, err
	}
	return ready, nil
}

// ListFilters carries the optional media list filters.
type ListFilters struct {
	Status      string
	Search      string
	Type        string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	MinSize     *int64
	MaxSize     *int64
	Sort        string
}

func (s *Service) List(ctx context.Context, f ListFilters, rawCursor string, limit int) ([]Media, string, error) {
	if f.Status != "" {
		switch f.Status {
		case "processing", "ready", "failed", "deleted":
		default:
			return nil, "", &ValidationError{Message: "Некорректный status"}
		}
	}
	sort := strings.TrimSpace(f.Sort)
	switch sort {
	case "", "new", "old", "name", "size_desc", "size_asc":
	default:
		return nil, "", &ValidationError{Message: "sort должен быть new, old, name, size_desc, size_asc"}
	}
	params := ListParams{
		Status:             f.Status,
		Search:             strings.TrimSpace(f.Search),
		ContentTypePattern: "",
		CreatedFrom:        f.CreatedFrom,
		CreatedTo:          f.CreatedTo,
		MinSize:            f.MinSize,
		MaxSize:            f.MaxSize,
		Sort:               sort,
		Limit:              limit + 1,
	}
	pattern := ""
	switch strings.TrimSpace(f.Type) {
	case "":
	case "image":
		pattern = "image/%"
	case "video":
		pattern = "video/%"
	case "pdf":
		pattern = "application/pdf"
	default:
		return nil, "", &ValidationError{Message: "type должен быть image, video, pdf или пустым"}
	}
	params.ContentTypePattern = pattern
	if rawCursor != "" {
		c, ok := decodeCursor(rawCursor)
		if !ok {
			return nil, "", &ValidationError{Message: "Некорректный cursor"}
		}
		if c.Sort != sort {
			return nil, "", &ValidationError{Message: "Курсор не соответствует выбранной сортировке"}
		}
		id := c.ID
		params.CursorID = &id
		switch sort {
		case "name":
			name := c.Name
			params.CursorName = &name
		case "size_desc", "size_asc":
			size := c.Size
			params.CursorSize = &size
		default:
			created := c.CreatedAt
			params.CursorTime = &created
		}
	}
	items, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(cursor{
			Sort:      sort,
			CreatedAt: last.CreatedAt,
			Name:      last.OriginalFilename,
			Size:      last.SizeBytes,
			ID:        last.ID,
		})
	}
	return items, next, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Media, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) ReadAsset(ctx context.Context, id uuid.UUID) (Media, io.ReadCloser, error) {
	media, err := s.repo.Get(ctx, id)
	if err != nil {
		return Media{}, nil, err
	}
	if media.Status != "ready" {
		return Media{}, nil, ErrNotFound
	}
	reader, err := s.store.Get(ctx, media.ObjectKey)
	if err != nil {
		return Media{}, nil, err
	}
	return media, reader, nil
}

// Rename changes the display name of a media object. Only the metadata in the
// database changes: the object key stays intact so published content keeps
// resolving to the same file.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, filename string) (Media, error) {
	name := sanitizeFilename(filename)
	if name == "file" && strings.TrimSpace(filename) == "" {
		return Media{}, &ValidationError{Message: "Имя файла не может быть пустым"}
	}
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return Media{}, err
	}
	if current.Status == "deleted" {
		return Media{}, &ConflictError{Message: "Объект удалён"}
	}
	return s.repo.Rename(ctx, id, name)
}

func (s *Service) Retry(ctx context.Context, id uuid.UUID) (Media, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return Media{}, err
	}
	if m.Status == "deleted" {
		return Media{}, &ConflictError{Message: "Объект удалён"}
	}
	if m.Status == "ready" {
		return Media{}, &ConflictError{Message: "Объект уже обработан"}
	}
	if err := s.repo.SetProcessing(ctx, id); err != nil {
		return Media{}, err
	}
	reader, err := s.store.Get(ctx, m.ObjectKey)
	if err != nil {
		_ = s.repo.SetFailed(context.Background(), id)
		return Media{}, fmt.Errorf("%w: original object unavailable", ErrProcessing)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, s.maxUpload+1))
	if err != nil || int64(len(data)) > s.maxUpload {
		_ = s.repo.SetFailed(context.Background(), id)
		return Media{}, fmt.Errorf("%w: cannot read original", ErrProcessing)
	}
	variants := map[string]string{"original": m.ObjectKey}
	if isRasterImage(m.ContentType) {
		generated, err := buildImageVariants(ctx, s.store, id.String(), data, m.ContentType)
		if err != nil {
			_ = s.repo.SetFailed(context.Background(), id)
			return Media{}, fmt.Errorf("%w: %v", ErrProcessing, err)
		}
		for name, key := range generated {
			variants[name] = key
		}
	}
	return s.repo.SetReady(ctx, id, variants)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if s.referenceChecker != nil {
		referenced, err := s.referenceChecker(ctx, id)
		if err != nil {
			return fmt.Errorf("check system asset references: %w", err)
		}
		if referenced {
			return &ConflictError{Message: "Файл используется дизайн-системой или снапшотом и не может быть удалён"}
		}
	}
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if m.Status == "deleted" {
		return nil
	}
	keys := make(map[string]bool)
	keys[m.ObjectKey] = true
	for _, key := range m.Variants {
		keys[key] = true
	}
	renditionKeys, err := s.repo.RenditionObjectKeys(ctx, id)
	if err != nil {
		return fmt.Errorf("list media rendition objects: %w", err)
	}
	for _, key := range renditionKeys {
		keys[key] = true
	}
	var errs []error
	for key := range keys {
		if err := s.store.Delete(ctx, key); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("delete media objects: %w", err)
	}
	return s.repo.SetDeleted(ctx, id)
}

func (s *Service) SetReferenceChecker(checker func(context.Context, uuid.UUID) (bool, error)) {
	s.referenceChecker = checker
}

// RedirectURL returns only a URL built from database-owned S3 keys.
func (s *Service) RedirectURL(ctx context.Context, id uuid.UUID, variant string) (string, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if m.Status != "ready" {
		return "", ErrNotFound
	}
	if variant == "" {
		variant = "original"
	}
	key, ok := m.Variants[variant]
	if !ok {
		return "", ErrNotFound
	}
	return s.store.PublicURL(key)
}

func (s *Service) RedirectRenditionURL(ctx context.Context, id uuid.UUID, profileHash, variant string) (string, error) {
	if profileHash == "" {
		return "", ErrNotFound
	}
	if _, err := hex.DecodeString(profileHash); err != nil || len(profileHash) != 64 {
		return "", ErrNotFound
	}
	mediaObject, err := s.repo.Get(ctx, id)
	if err != nil || mediaObject.Status != "ready" {
		return "", ErrNotFound
	}
	record, err := s.repo.GetRendition(ctx, id, profileHash)
	if err != nil || record.Status != "ready" {
		return "", ErrNotFound
	}
	output, ok := record.Outputs[variant]
	if !ok {
		return "", ErrNotFound
	}
	return s.store.PublicURL(output.ObjectKey)
}

func (s *Service) Ready(ctx context.Context) error {
	if err := s.store.Ready(ctx); err != nil {
		return err
	}
	if s.processorErr != nil {
		return s.processorErr
	}
	if s.processor == nil {
		return errors.New("media processor is not configured")
	}
	return s.processor.Ready(ctx)
}

func isRasterImage(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

var allowedContentTypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/svg+xml":   ".svg",
	"application/pdf": ".pdf",
	"video/mp4":       ".mp4",
	"font/woff2":      ".woff2",
}

// AcceptedContentTypes lists the content types accepted by the upload API.
func AcceptedContentTypes() []string {
	types := make([]string, 0, len(allowedContentTypes))
	for t := range allowedContentTypes {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

// ImageVariantNames lists the generated image variants.
func ImageVariantNames() []string {
	names := make([]string, 0, len(imageVariants))
	for _, v := range imageVariants {
		names = append(names, v.Name)
	}
	return names
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	if utf8.RuneCountInString(name) > 255 {
		runes := []rune(name)
		name = string(runes[:255])
	}
	return name
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrValidation }

type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }
func (e *ConflictError) Unwrap() error { return ErrConflict }
