package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"websiteengine/pkg/storage"
)

var ErrRenditionsPending = errors.New("required media renditions are still processing")

func (s *Service) Prepare(ctx context.Context, id uuid.UUID, profile FieldProfile) (RenditionState, error) {
	if err := profile.Validate(); err != nil {
		return RenditionState{}, &ValidationError{Message: err.Error()}
	}
	object, err := s.repo.Get(ctx, id)
	if err != nil {
		return RenditionState{}, err
	}
	if object.Status != "ready" {
		return RenditionState{}, &ConflictError{Message: "Исходный файл ещё не готов"}
	}
	if err := validateProfileType(profile, object.ContentType); err != nil {
		return RenditionState{}, err
	}
	hash, err := ProfileHash(profile)
	if err != nil {
		return RenditionState{}, err
	}
	if !needsProcessing(profile) {
		return RenditionState{MediaID: id.String(), ProfileHash: hash, Status: "ready", Outputs: map[string]Output{}}, nil
	}
	record, err := s.repo.EnsureRendition(ctx, id, hash, profile)
	if err != nil {
		return RenditionState{}, err
	}
	if record.Status == "failed" {
		record, err = s.repo.RetryRendition(ctx, id, hash)
		if err != nil && !errors.Is(err, ErrConflict) {
			return RenditionState{}, err
		}
		if errors.Is(err, ErrConflict) {
			record, err = s.repo.GetRendition(ctx, id, hash)
			if err != nil {
				return RenditionState{}, err
			}
		}
	}
	return s.renditionState(record), nil
}

func needsProcessing(profile FieldProfile) bool {
	return len(profile.Renditions) > 0 || profile.Video != nil
}

func (s *Service) State(ctx context.Context, id uuid.UUID, profile FieldProfile) (RenditionState, error) {
	hash, err := ProfileHash(profile)
	if err != nil {
		return RenditionState{}, err
	}
	if !needsProcessing(profile) {
		return RenditionState{MediaID: id.String(), ProfileHash: hash, Status: "ready", Outputs: map[string]Output{}}, nil
	}
	record, err := s.repo.GetRendition(ctx, id, hash)
	if err != nil {
		return RenditionState{}, err
	}
	return s.renditionState(record), nil
}

func (s *Service) renditionState(record RenditionRecord) RenditionState {
	state := RenditionState{MediaID: record.MediaID.String(), ProfileHash: record.ProfileHash, Status: record.Status, Outputs: map[string]Output{}, Error: record.Error}
	for name, stored := range record.Outputs {
		state.Outputs[name] = Output{
			URL:      publicRenditionPath(record.MediaID, record.ProfileHash, name),
			Width:    stored.Width,
			Height:   stored.Height,
			MIMEType: stored.MIMEType,
		}
	}
	return state
}

func publicRenditionPath(id uuid.UUID, profileHash, name string) string {
	return fmt.Sprintf("/media/%s?profile=%s&variant=%s", id, url.QueryEscape(profileHash), url.QueryEscape(name))
}

func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	if s.renditionLease <= 0 {
		return false, errors.New("media rendition lease is not configured")
	}
	job, ok, err := s.repo.ClaimRendition(ctx, s.renditionLease)
	if err != nil || !ok {
		return ok, err
	}
	object, err := s.repo.Get(ctx, job.MediaID)
	if err != nil {
		return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, err.Error())
	}
	if object.Status != "ready" {
		return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, "original media object is not ready")
	}
	if s.processorErr != nil || s.processor == nil {
		message := "media processor is unavailable"
		if s.processorErr != nil {
			message = s.processorErr.Error()
		}
		return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, message)
	}
	processCtx := ctx
	if timed, ok := s.processor.(interface{ ProcessingTimeout() time.Duration }); ok {
		var cancel context.CancelFunc
		processCtx, cancel = context.WithTimeout(ctx, timed.ProcessingTimeout())
		defer cancel()
	}
	reader, err := s.store.Get(processCtx, object.ObjectKey)
	if err != nil {
		return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, "original media object is unavailable")
	}
	outputs := map[string]StoredOutput{}
	if processor, ok := s.processor.(StreamingRenditionProcessor); ok {
		processed, cleanup, processErr := processor.ProcessStream(processCtx, object, job.Profile, reader)
		closeErr := reader.Close()
		if cleanup != nil {
			defer cleanup()
		}
		if processErr != nil {
			return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, processErr.Error())
		}
		if closeErr != nil {
			return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, closeErr.Error())
		}
		for name, output := range processed {
			extension := ".webp"
			if output.MIMEType == "video/mp4" {
				extension = ".mp4"
			}
			key := fmt.Sprintf("media/%s/renditions/%s/%s%s", job.MediaID, job.ProfileHash, name, extension)
			file, err := os.Open(output.Path)
			if err != nil {
				return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, "read processed rendition: "+err.Error())
			}
			putErr := s.store.Put(processCtx, key, file, output.Size, storage.PutOptions{ContentType: output.MIMEType, CacheControl: cacheControl})
			closeErr := file.Close()
			if putErr != nil {
				return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, "store processed rendition: "+putErr.Error())
			}
			if closeErr != nil {
				return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, closeErr.Error())
			}
			outputs[name] = StoredOutput{ObjectKey: key, MIMEType: output.MIMEType, Width: output.Width, Height: output.Height}
		}
	} else {
		// Compatibility path for injected small test processors. The production
		// constructor always supplies FFmpegProcessor's bounded streaming method.
		data, readErr := io.ReadAll(io.LimitReader(reader, s.maxUpload+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(data)) > s.maxUpload {
			return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, "cannot read original media object")
		}
		processed, err := s.processor.Process(ctx, object, job.Profile, data)
		if err != nil {
			return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, err.Error())
		}
		for name, output := range processed {
			extension := ".webp"
			if output.MIMEType == "video/mp4" {
				extension = ".mp4"
			}
			key := fmt.Sprintf("media/%s/renditions/%s/%s%s", job.MediaID, job.ProfileHash, name, extension)
			if err := s.store.Put(ctx, key, bytes.NewReader(output.Data), int64(len(output.Data)), storage.PutOptions{ContentType: output.MIMEType, CacheControl: cacheControl}); err != nil {
				return true, s.repo.FailRendition(ctx, job.MediaID, job.ProfileHash, "store processed rendition: "+err.Error())
			}
			outputs[name] = StoredOutput{ObjectKey: key, MIMEType: output.MIMEType, Width: output.Width, Height: output.Height}
		}
	}

	if err := s.repo.CompleteRendition(ctx, job.MediaID, job.ProfileHash, outputs); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) RunWorker(ctx context.Context, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if s.workerPollInterval <= 0 || s.renditionLease <= 0 {
		log.Error("media rendition worker configuration is incomplete")
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := s.ProcessOne(ctx)
		if err != nil {
			log.Error("media rendition worker failed", "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.workerPollInterval):
		}
	}
}

func (s *Service) ValidateContent(ctx context.Context, schema, data json.RawMessage, enqueue bool) error {
	var schemaValue, dataValue any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &dataValue); err != nil {
		return err
	}
	return s.walkMedia(ctx, schemaValue, dataValue, enqueue, nil)
}

func (s *Service) HydrateContent(ctx context.Context, schema, data json.RawMessage) (json.RawMessage, error) {
	var schemaValue, dataValue any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &dataValue); err != nil {
		return nil, err
	}
	value, err := s.hydrateNode(ctx, schemaValue, dataValue)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (s *Service) walkMedia(ctx context.Context, schemaValue, dataValue any, enqueue bool, _ []string) error {
	schema, ok := schemaValue.(map[string]any)
	if !ok {
		return nil
	}
	if schema["format"] == "media" {
		profile, exists, err := profileFromNode(schema)
		if err != nil || !exists || !needsProcessing(profile) {
			return err
		}
		reference, ok := dataValue.(string)
		if !ok || strings.TrimSpace(reference) == "" {
			return nil
		}
		id, err := uuid.Parse(reference)
		if err != nil {
			return fmt.Errorf("media field value %q must be a media ID", reference)
		}
		var state RenditionState
		if enqueue {
			// Prepare is idempotent for pending/processing/ready work and also
			// retries failed jobs. Calling it for every field ensures one pending
			// rendition never prevents later fields from entering the queue.
			state, err = s.Prepare(ctx, id, profile)
		} else {
			state, err = s.State(ctx, id, profile)
		}
		if err != nil {
			return fmt.Errorf("prepare media %s: %w", id, err)
		}
		if state.Status == "failed" {
			return fmt.Errorf("media %s rendition failed: %s", id, state.Error)
		}
		if state.Status != "ready" {
			if enqueue {
				return ErrRenditionsPending
			}
			return fmt.Errorf("media %s rendition is %s", id, state.Status)
		}
		return nil
	}
	var failures []error
	if props, ok := schema["properties"].(map[string]any); ok {
		dataObject, _ := dataValue.(map[string]any)
		for key, childSchema := range props {
			if childData, exists := dataObject[key]; exists {
				if err := s.walkMedia(ctx, childSchema, childData, enqueue, nil); err != nil {
					failures = append(failures, fmt.Errorf("%s: %w", key, err))
				}
			}
		}
	}
	if itemSchema, ok := schema["items"]; ok {
		if items, ok := dataValue.([]any); ok {
			for index, item := range items {
				if err := s.walkMedia(ctx, itemSchema, item, enqueue, nil); err != nil {
					failures = append(failures, fmt.Errorf("%d: %w", index, err))
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) hydrateNode(ctx context.Context, schemaValue, dataValue any) (any, error) {
	schema, ok := schemaValue.(map[string]any)
	if !ok {
		return dataValue, nil
	}
	if schema["format"] == "media" {
		reference, ok := dataValue.(string)
		if !ok || strings.TrimSpace(reference) == "" {
			return dataValue, nil
		}
		id, err := uuid.Parse(reference)
		if err != nil {
			return dataValue, nil
		}
		return s.hydrateMedia(ctx, id, schema)
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		dataObject, ok := dataValue.(map[string]any)
		if !ok {
			return dataValue, nil
		}
		out := make(map[string]any, len(dataObject))
		for key, value := range dataObject {
			childSchema, exists := props[key]
			if !exists {
				out[key] = value
				continue
			}
			hydrated, err := s.hydrateNode(ctx, childSchema, value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out[key] = hydrated
		}
		return out, nil
	}
	if itemSchema, ok := schema["items"]; ok {
		if items, ok := dataValue.([]any); ok {
			out := make([]any, len(items))
			for index, item := range items {
				hydrated, err := s.hydrateNode(ctx, itemSchema, item)
				if err != nil {
					return nil, fmt.Errorf("%d: %w", index, err)
				}
				out[index] = hydrated
			}
			return out, nil
		}
	}
	return dataValue, nil
}

func profileFromNode(schema map[string]any) (FieldProfile, bool, error) {
	raw, exists := schema["x-media"]
	if !exists {
		return FieldProfile{}, false, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return FieldProfile{}, false, err
	}
	profile, err := DecodeFieldProfile(encoded)
	return profile, true, err
}

func (s *Service) hydrateMedia(ctx context.Context, id uuid.UUID, schema map[string]any) (any, error) {
	mediaObject, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if mediaObject.Status != "ready" {
		return nil, ErrNotFound
	}
	profile, hasProfile, err := profileFromNode(schema)
	if err != nil {
		return nil, err
	}
	if !hasProfile || !needsProcessing(profile) {
		return originalImageSource(id), nil
	}
	hash, err := ProfileHash(profile)
	if err != nil {
		return nil, err
	}
	record, err := s.repo.GetRendition(ctx, id, hash)
	if profile.Accept == "raster-image" {
		return hydrateRasterMediaSource(id, profile, record, err)
	}
	if err != nil {
		return nil, fmt.Errorf("required media rendition is missing: %w", err)
	}
	if record.Status != "ready" {
		return nil, fmt.Errorf("required media rendition is %s", record.Status)
	}
	if profile.Accept == "video" {
		video, ok := record.Outputs["video"]
		if !ok {
			return nil, errors.New("required H.264 video rendition is missing")
		}
		posterURL := ""
		if _, ok := record.Outputs["poster"]; ok {
			posterURL = publicRenditionPath(id, hash, "poster")
		}
		return VideoSource{ID: id.String(), Kind: "video", OriginalURL: "/media/" + id.String(), SourceURL: publicRenditionPath(id, hash, "video"), MIMEType: video.MIMEType, PosterURL: posterURL}, nil
	}
	return nil, fmt.Errorf("unsupported media profile type %q", profile.Accept)
}

func originalImageSource(id uuid.UUID) ImageSource {
	url := "/media/" + id.String()
	return ImageSource{ID: id.String(), Kind: "image", OriginalURL: url, Src: url}
}

// hydrateRasterMediaSource keeps published pages available while an optional
// responsive rendition is queued or retried. Once the ready record appears,
// subsequent runtime projections automatically use its width-aware variants.
func hydrateRasterMediaSource(id uuid.UUID, profile FieldProfile, record RenditionRecord, lookupErr error) (ImageSource, error) {
	if lookupErr != nil {
		if errors.Is(lookupErr, ErrNotFound) {
			return originalImageSource(id), nil
		}
		return ImageSource{}, fmt.Errorf("required media rendition lookup failed: %w", lookupErr)
	}
	if record.Status != "ready" {
		return originalImageSource(id), nil
	}
	hash, err := ProfileHash(profile)
	if err != nil {
		return ImageSource{}, err
	}
	variants := make(map[string]Output, len(profile.Renditions))
	for name, output := range record.Outputs {
		variants[name] = Output{URL: publicRenditionPath(id, hash, name), Width: output.Width, Height: output.Height, MIMEType: output.MIMEType}
	}
	imageVariants := responsiveOutputs(profile, variants)
	src := "/media/" + id.String()
	srcSetParts := make([]string, 0, len(imageVariants))
	if len(imageVariants) > 0 {
		src = imageVariants[len(imageVariants)-1].URL
	}
	for _, output := range imageVariants {
		srcSetParts = append(srcSetParts, fmt.Sprintf("%s %dw", output.URL, output.Width))
	}
	return ImageSource{ID: id.String(), Kind: "image", OriginalURL: "/media/" + id.String(), Src: src, SrcSet: strings.Join(srcSetParts, ", "), Variants: variants}, nil
}

func responsiveOutputs(profile FieldProfile, variants map[string]Output) []Output {
	outputs := make([]Output, 0, len(profile.Renditions))
	for _, rendition := range profile.Renditions {
		if output, ok := variants[rendition.Name]; ok && output.Width > 0 && output.Height > 0 {
			outputs = append(outputs, output)
		}
	}
	sort.SliceStable(outputs, func(i, j int) bool { return outputs[i].Width < outputs[j].Width })

	unique := outputs[:0]
	for _, output := range outputs {
		if len(unique) > 0 && unique[len(unique)-1].Width == output.Width {
			previous := &unique[len(unique)-1]
			if output.Height > previous.Height {
				*previous = output
			}
			continue
		}
		unique = append(unique, output)
	}
	return unique
}

func (s *Service) HydrateFeatureContent(ctx context.Context, profileSchema, data json.RawMessage) (json.RawMessage, error) {
	return s.HydrateContent(ctx, profileSchema, data)
}
