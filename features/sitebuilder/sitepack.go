package sitebuilder

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"websiteengine/features/files"
	"websiteengine/features/media"
	"websiteengine/pkg/storage"
)

type SitepackOptions struct {
	TempDir             string
	MaxBytes            int64
	MaxExpandedBytes    int64
	MaxEntries          int
	MaxIndexBytes       int64
	MaxSVGBytes         int64
	MaxBrowserBlobBytes int64
}

func (o SitepackOptions) Validate() error {
	if o.TempDir == "" || o.MaxBytes < 1024 || o.MaxExpandedBytes < o.MaxBytes || o.MaxEntries < 1 || o.MaxIndexBytes < 1024 || o.MaxIndexBytes > o.MaxExpandedBytes || o.MaxSVGBytes < 1 || o.MaxBrowserBlobBytes < 1024 || o.MaxBrowserBlobBytes > o.MaxBytes {
		return errors.New("sitepack temp directory and positive size/entry/index limits must be explicitly configured")
	}
	return nil
}

type packBlob struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
}

// packModelContent carries the snapshot-scoped CMS workspace independently
// from the structural payload. Publication actors are intentionally omitted:
// credentials are local to an installation, while timestamps and immutable
// content states remain portable.
type packModelContent struct {
	Revision            int64                   `json:"revision"`
	PublishedRevision   int64                   `json:"publishedRevision"`
	ContentRevision     int64                   `json:"contentRevision"`
	DraftRecords        []ModelContentRecord    `json:"draftRecords"`
	DraftSelections     []ModelContentSelection `json:"draftSelections"`
	PublishedRecords    []ModelContentRecord    `json:"publishedRecords"`
	PublishedSelections []ModelContentSelection `json:"publishedSelections"`
	History             []packModelPublication  `json:"history"`
}

type packModelPublication struct {
	ID              uuid.UUID               `json:"id"`
	DraftRevision   int64                   `json:"draftRevision"`
	ContentRevision int64                   `json:"contentRevision"`
	CreatedAt       time.Time               `json:"createdAt"`
	Records         []ModelContentRecord    `json:"records"`
	Selections      []ModelContentSelection `json:"selections"`
}
type packRendition struct {
	Hash    string                     `json:"hash"`
	Profile json.RawMessage            `json:"profile"`
	Outputs map[string]json.RawMessage `json:"outputs"`
	Blobs   map[string]packBlob        `json:"blobs"`
}
type packFile struct {
	ID         uuid.UUID           `json:"id"`
	Name       string              `json:"name"`
	Visibility string              `json:"visibility"`
	Original   packBlob            `json:"original"`
	Variants   map[string]packBlob `json:"variants"`
	Renditions []packRendition     `json:"renditions"`
}
type sitepackIndex struct {
	FormatVersion int               `json:"formatVersion"`
	Name          string            `json:"name"`
	Payload       SnapshotPayload   `json:"payload"`
	ModelContent  *packModelContent `json:"modelContent,omitempty"`
	Builds        []packBlob        `json:"builds"`
	Files         []packFile        `json:"files"`
}
type SitepackReport struct {
	Snapshot                 SnapshotRecord       `json:"snapshot"`
	NewBuilds                int                  `json:"newBuilds"`
	ReusedBuilds             int                  `json:"reusedBuilds"`
	NewFiles                 int                  `json:"newFiles"`
	ReusedFiles              int                  `json:"reusedFiles"`
	ModelPublishedRecords    int                  `json:"modelPublishedRecords"`
	ModelDraftRecords        int                  `json:"modelDraftRecords"`
	ModelHistoryRevisions    int                  `json:"modelHistoryRevisions"`
	RemappedModelEntityIDs   int                  `json:"remappedModelEntityIds"`
	RemappedModelFieldIDs    int                  `json:"remappedModelFieldIds"`
	RemappedModelRelationIDs int                  `json:"remappedModelRelationIds"`
	RemappedModelRecordIDs   int                  `json:"remappedModelRecordIds"`
	FileIDs                  map[string]string    `json:"fileIds"`
	FileIDsTruncated         bool                 `json:"fileIdsTruncated,omitempty"`
	Items                    []SitepackReportItem `json:"items"`
	ItemsTruncated           bool                 `json:"itemsTruncated,omitempty"`
}

// SitepackReportItem reports only structural changes. It deliberately omits
// file names, field values, credentials, and arbitrary archive paths.
type SitepackReportItem struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Action   string `json:"action"`
	SourceID string `json:"sourceId,omitempty"`
	TargetID string `json:"targetId,omitempty"`
	Count    int    `json:"count,omitempty"`
	Message  string `json:"message"`
}

// SitepackImportDiagnostic is safe to return through the standard API error
// envelope. Paths are fixed locators, never values copied from the archive.
type SitepackImportDiagnostic struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type SitepackImportFailure struct {
	Diagnostic SitepackImportDiagnostic
}

func (e *SitepackImportFailure) Error() string { return e.Diagnostic.Message }

func rejectSitepack(path, code, message string) error {
	return &SitepackImportFailure{Diagnostic: SitepackImportDiagnostic{Path: path, Code: code, Message: message}}
}

const maxSitepackReportItems = 100

func addSitepackReportItem(report *SitepackReport, item SitepackReportItem) {
	if report == nil {
		return
	}
	if len(report.Items) >= maxSitepackReportItems {
		report.ItemsTruncated = true
		return
	}
	report.Items = append(report.Items, item)
}

func addSitepackReportFileID(report *SitepackReport, sourceID, targetID string) {
	if report == nil {
		return
	}
	if len(report.FileIDs) >= maxSitepackReportItems {
		report.FileIDsTruncated = true
		return
	}
	report.FileIDs[sourceID] = targetID
}

// A package produced by this service must fit the same compressed-byte limit
// that is enforced on import, including ZIP headers and the central directory.
type sitepackLimitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *sitepackLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("%w: sitepack exceeds compressed size limit", ErrInvalid)
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// stageSitepackBlob writes one preflighted archive entry to a seekable file.
// The file-backed reader is required by S3 request signing, and staging only
// one blob at a time bounds both RAM and temporary-disk use independently of
// the total media size in the archive.
func stageSitepackBlob(tempDir string, entry *zip.File, expectedSize int64) (*os.File, error) {
	if entry == nil || expectedSize < 0 || entry.UncompressedSize64 != uint64(expectedSize) {
		return nil, fmt.Errorf("sitepack blob size does not match preflight metadata")
	}
	source, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer source.Close()

	staged, err := os.CreateTemp(tempDir, "sitepack-blob-*.tmp")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = staged.Close()
			_ = os.Remove(staged.Name())
		}
	}()

	copied, err := io.CopyN(staged, source, expectedSize)
	if err != nil {
		return nil, fmt.Errorf("stage sitepack blob: %w", err)
	}
	if copied != expectedSize {
		return nil, fmt.Errorf("sitepack blob size differs from preflight metadata")
	}
	var extra [1]byte
	read, readErr := io.ReadFull(source, extra[:])
	if read > 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("verify staged sitepack blob: %w", readErr)
		}
		return nil, fmt.Errorf("sitepack blob exceeds preflight size")
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	keep = true
	return staged, nil
}

// ExportSitepack writes binaries directly to ZIP rather than buffering files.
// The reference lock keeps confirmed deletion from removing bytes mid-export.
func (s *SnapshotService) ExportSitepack(ctx context.Context, id uuid.UUID, writer io.Writer, options SitepackOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return err
	}
	var raw []byte
	var name string
	err = tx.QueryRow(ctx, `SELECT name,payload FROM site_snapshots WHERE id=$1 FOR SHARE`, id).Scan(&name, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	index := sitepackIndex{FormatVersion: 2, Name: name, Builds: []packBlob{}, Files: []packFile{}}
	if err := json.Unmarshal(raw, &index.Payload); err != nil {
		return err
	}
	if index.Payload.Config.DataModel != nil {
		index.ModelContent, err = exportSitepackModelContent(ctx, tx, id)
		if err != nil {
			return err
		}
	} else {
		state, err := readStoredModelContent(ctx, tx, id, false)
		if err != nil {
			return err
		}
		var hasHistory bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM snapshot_model_content_publications WHERE snapshot_id=$1)`, id).Scan(&hasHistory); err != nil {
			return err
		}
		if state.Exists || hasHistory {
			return fmt.Errorf("%w: model content exists without a snapshot dataModel; refusing an incomplete export", ErrInvalid)
		}
	}
	if err := validateSitepackModelContent(&index); err != nil {
		return fmt.Errorf("%w: invalid snapshot model content: %v", ErrInvalid, err)
	}
	if index.Payload.Config.DataModel != nil && index.ModelContent != nil {
		if err := validateModelContentEntityCardinality(*index.Payload.Config.DataModel, index.ModelContent.DraftRecords, true); err != nil {
			return fmt.Errorf("%w: cannot export an invalid singleton draft: %v", ErrInvalid, err)
		}
	}
	refs, err := checkPackReferences(&index)
	if err != nil {
		return fmt.Errorf("%w: invalid snapshot file reference: %v", ErrInvalid, err)
	}
	modelRefs, err := sitepackModelMediaReferences(index.ModelContent, index.Payload.Config.DataModel)
	if err != nil {
		return fmt.Errorf("%w: invalid model media reference: %v", ErrInvalid, err)
	}
	refs = appendUniqueFileIDs(refs, modelRefs...)
	zipWriter := zip.NewWriter(&sitepackLimitWriter{writer: writer, remaining: options.MaxBytes})
	var total int64
	entries := 0
	writeObject := func(name, key, contentType string) (packBlob, error) {
		// Keep one slot for the index, which is itself an archive entry.
		if entries+1 >= options.MaxEntries {
			return packBlob{}, fmt.Errorf("%w: sitepack entry limit exceeded", ErrInvalid)
		}
		body, err := s.builds.store.Get(ctx, key)
		if err != nil {
			return packBlob{}, err
		}
		defer body.Close()
		entry, err := zipWriter.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return packBlob{}, err
		}
		hash := sha256.New()
		size, err := io.Copy(io.MultiWriter(entry, hash), io.LimitReader(body, options.MaxExpandedBytes-total+1))
		if err != nil {
			return packBlob{}, err
		}
		total += size
		if total > options.MaxExpandedBytes {
			return packBlob{}, fmt.Errorf("%w: expanded sitepack exceeds limit", ErrInvalid)
		}
		entries++
		return packBlob{Path: name, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), ContentType: contentType}, nil
	}
	for _, build := range index.Payload.Builds {
		blob, err := writeObject("builds/"+build.Digest+".zip", path.Join("feature-builds", build.Digest, "artifact.zip"), "application/zip")
		if err != nil {
			return fmt.Errorf("export original build %s@%s (re-upload its identical ZIP if missing): %w", build.Key, build.Version, err)
		}
		if blob.SHA256 != build.Digest {
			return fmt.Errorf("%w: stored build archive checksum mismatch", ErrInvalid)
		}
		index.Builds = append(index.Builds, blob)
	}
	fileRows, err := tx.Query(ctx, `SELECT m.id,m.original_filename,m.content_type,m.object_key,m.visibility,m.variants,o.kind FROM media_objects m JOIN file_owners o ON o.id=m.owner_id WHERE m.id=ANY($1::uuid[]) AND m.status='ready' ORDER BY m.id`, refs)
	if err != nil {
		return err
	}
	type sourceFile struct {
		item      packFile
		mime, key string
		variants  map[string]string
	}
	sources := []sourceFile{}
	for fileRows.Next() {
		var source sourceFile
		var variants []byte
		var kind string
		if err := fileRows.Scan(&source.item.ID, &source.item.Name, &source.mime, &source.key, &source.item.Visibility, &variants, &kind); err != nil {
			fileRows.Close()
			return err
		}
		if source.item.Visibility == "private" && kind != "admin" {
			fileRows.Close()
			return fmt.Errorf("%w: private service-owned files cannot be exported by an administrator", ErrInvalid)
		}
		if err := json.Unmarshal(variants, &source.variants); err != nil {
			fileRows.Close()
			return err
		}
		sources = append(sources, source)
	}
	if err := fileRows.Err(); err != nil {
		fileRows.Close()
		return err
	}
	fileRows.Close()
	if len(sources) != len(refs) {
		return fmt.Errorf("%w: one or more referenced files are missing or not ready", ErrInvalid)
	}
	for _, source := range sources {
		item := source.item
		prefix := "files/" + item.ID.String() + "/"
		item.Variants = map[string]packBlob{}
		item.Renditions = []packRendition{}
		item.Original, err = writeObject(prefix+"original", source.key, source.mime)
		if err != nil {
			return err
		}
		keys := make([]string, 0, len(source.variants))
		for key := range source.variants {
			// The normal file registry also lists the original as a variant.
			// Repeating it would double a multi-gigabyte sitepack.
			if key == "original" && source.variants[key] == source.key {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for number, key := range keys {
			blob, err := writeObject(fmt.Sprintf("%svariants/%d", prefix, number), source.variants[key], source.mime)
			if err != nil {
				return err
			}
			item.Variants[key] = blob
		}
		renditions, err := tx.Query(ctx, `SELECT profile_hash,profile,outputs FROM media_renditions WHERE media_id=$1 AND status='ready' ORDER BY profile_hash`, item.ID)
		if err != nil {
			return err
		}
		for renditions.Next() {
			var rendition packRendition
			var outputs []byte
			if err := renditions.Scan(&rendition.Hash, &rendition.Profile, &outputs); err != nil {
				renditions.Close()
				return err
			}
			if err := json.Unmarshal(outputs, &rendition.Outputs); err != nil {
				renditions.Close()
				return err
			}
			item.Renditions = append(item.Renditions, rendition)
		}
		if err := renditions.Err(); err != nil {
			renditions.Close()
			return err
		}
		renditions.Close()
		for ri := range item.Renditions {
			rendition := &item.Renditions[ri]
			rendition.Blobs = map[string]packBlob{}
			for key, raw := range rendition.Outputs {
				var output struct {
					ObjectKey string `json:"objectKey"`
					MIMEType  string `json:"mimeType"`
				}
				if err := json.Unmarshal(raw, &output); err != nil {
					return err
				}
				blob, err := writeObject(prefix+"renditions/"+rendition.Hash+"/"+key, output.ObjectKey, output.MIMEType)
				if err != nil {
					return err
				}
				rendition.Blobs[key] = blob
			}
		}
		index.Files = append(index.Files, item)
	}
	encoded, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if int64(len(encoded)) > options.MaxIndexBytes || total+int64(len(encoded)) > options.MaxExpandedBytes {
		return fmt.Errorf("%w: sitepack index exceeds limit", ErrInvalid)
	}
	entry, err := zipWriter.Create("sitepack.json")
	if err != nil {
		return err
	}
	if _, err := entry.Write(encoded); err != nil {
		return err
	}
	if err := zipWriter.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func exportSitepackModelContent(ctx context.Context, tx pgx.Tx, snapshotID uuid.UUID) (*packModelContent, error) {
	state, err := readStoredModelContent(ctx, tx, snapshotID, false)
	if err != nil {
		return nil, err
	}
	result := &packModelContent{
		Revision: state.Revision, PublishedRevision: state.PublishedRevision,
		DraftRecords: nonNilModelRecords(state.DraftRecords), DraftSelections: nonNilModelSelections(state.DraftSelections),
		PublishedRecords: nonNilModelRecords(state.PublishedRecords), PublishedSelections: nonNilModelSelections(state.PublishedSelection),
		History: []packModelPublication{},
	}
	if err := tx.QueryRow(ctx, `SELECT content_revision FROM site_snapshots WHERE id=$1`, snapshotID).Scan(&result.ContentRevision); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,draft_revision,content_revision,created_at,records,selections FROM snapshot_model_content_publications WHERE snapshot_id=$1 ORDER BY content_revision`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item packModelPublication
		var records, selections []byte
		if err := rows.Scan(&item.ID, &item.DraftRevision, &item.ContentRevision, &item.CreatedAt, &records, &selections); err != nil {
			return nil, err
		}
		if err := decodeModelJSON(records, &item.Records); err != nil {
			return nil, err
		}
		if err := decodeModelJSON(selections, &item.Selections); err != nil {
			return nil, err
		}
		item.Records = nonNilModelRecords(item.Records)
		item.Selections = nonNilModelSelections(item.Selections)
		result.History = append(result.History, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func appendUniqueFileIDs(target []uuid.UUID, additions ...uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(target)+len(additions))
	result := make([]uuid.UUID, 0, len(target)+len(additions))
	for _, id := range append(target, additions...) {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

// ImportSitepack validates the complete package before registering any rows.
func (s *SnapshotService) ImportSitepack(ctx context.Context, actor uuid.UUID, body io.Reader, options SitepackOptions) (SitepackReport, error) {
	report := SitepackReport{FileIDs: map[string]string{}, Items: []SitepackReportItem{}}
	if err := options.Validate(); err != nil {
		return report, err
	}
	if err := os.MkdirAll(options.TempDir, 0700); err != nil {
		return report, err
	}
	temporary, err := os.CreateTemp(options.TempDir, "sitepack-*.zip")
	if err != nil {
		return report, err
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	size, err := io.Copy(temporary, io.LimitReader(body, options.MaxBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return report, rejectSitepack("archive", "sitepack_too_large", "Архив превышает допустимый размер")
		}
		return report, err
	}
	if size > options.MaxBytes {
		return report, rejectSitepack("archive", "sitepack_too_large", "Архив превышает допустимый размер")
	}
	reader, err := zip.NewReader(temporary, size)
	if err != nil {
		return report, rejectSitepack("archive", "invalid_zip", "Не удалось прочитать ZIP-архив")
	}
	entries := map[string]*zip.File{}
	var expanded uint64
	for _, file := range reader.File {
		if !validAssetPath(file.Name) || strings.Contains(file.Name, "//") || !file.Mode().IsRegular() || entries[file.Name] != nil {
			return report, rejectSitepack("archive.entries", "unsafe_archive_entry", "Архив содержит небезопасный или повторяющийся элемент")
		}
		if file.UncompressedSize64 > uint64(options.MaxExpandedBytes)-expanded || len(entries) >= options.MaxEntries {
			return report, rejectSitepack("archive.entries", "archive_limit_exceeded", "Архив превышает лимит количества элементов или распакованного размера")
		}
		expanded += file.UncompressedSize64
		entries[file.Name] = file
	}
	indexFile := entries["sitepack.json"]
	if indexFile == nil || indexFile.UncompressedSize64 > uint64(options.MaxIndexBytes) {
		return report, rejectSitepack("sitepack.json", "invalid_index_size", "Индекс sitepack.json отсутствует или превышает лимит")
	}
	readEntry := func(file *zip.File) ([]byte, error) {
		stream, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		return io.ReadAll(io.LimitReader(stream, int64(file.UncompressedSize64)+1))
	}
	raw, err := readEntry(indexFile)
	if err != nil {
		return report, rejectSitepack("sitepack.json", "invalid_index", "Не удалось прочитать индекс архива")
	}
	if retiredSitepackContract(raw) {
		return report, rejectSitepack("sitepack.json", "retired_content_contract", "Архив содержит удалённый per-feature content контракт; экспортируйте его из версии с snapshot dataModel")
	}
	var index sitepackIndex
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&index); err != nil || index.FormatVersion != 2 {
		return report, rejectSitepack("sitepack.json", "unsupported_index", "Индекс повреждён или использует неподдерживаемый формат")
	}
	if err := requireJSONEnd(dec); err != nil {
		return report, rejectSitepack("sitepack.json", "invalid_index", "Индекс содержит лишние данные")
	}
	fileIDs := map[uuid.UUID]bool{}
	for i, file := range index.Files {
		if file.ID == uuid.Nil || fileIDs[file.ID] || strings.TrimSpace(file.Name) == "" || len(file.Name) > 255 ||
			(file.Visibility != "public" && file.Visibility != "private") {
			return report, rejectSitepack(fmt.Sprintf("files[%d]", i), "invalid_file_metadata", "Метаданные файла неполны или повторяются")
		}
		fileIDs[file.ID] = true
	}
	used := map[string]bool{"sitepack.json": true}
	blobs := []packBlob{}
	blobLocators := []string{}
	for i, blob := range index.Builds {
		blobs = append(blobs, blob)
		blobLocators = append(blobLocators, fmt.Sprintf("builds[%d]", i))
	}
	for i, file := range index.Files {
		blobs = append(blobs, file.Original)
		blobLocators = append(blobLocators, fmt.Sprintf("files[%d].original", i))
		for range file.Variants {
			// Variant map keys are user supplied filenames; report only the
			// collection locator to avoid echoing arbitrary archive metadata.
			blobLocators = append(blobLocators, fmt.Sprintf("files[%d].variants", i))
		}
		for j, rendition := range file.Renditions {
			for range rendition.Blobs {
				blobLocators = append(blobLocators, fmt.Sprintf("files[%d].renditions[%d]", i, j))
			}
		}
		for _, blob := range file.Variants {
			blobs = append(blobs, blob)
		}
		for _, rendition := range file.Renditions {
			for _, blob := range rendition.Blobs {
				blobs = append(blobs, blob)
			}
		}
	}
	for i, blob := range blobs {
		file := entries[blob.Path]
		if file == nil || used[blob.Path] || blob.Size < 0 || file.UncompressedSize64 != uint64(blob.Size) {
			return report, rejectSitepack(blobLocators[i], "invalid_blob_index", "Файл архива отсутствует, указан повторно или имеет неверный размер")
		}
		used[blob.Path] = true
		stream, err := file.Open()
		if err != nil {
			return report, rejectSitepack(blobLocators[i], "unreadable_archive_blob", "Не удалось прочитать бинарный элемент архива")
		}
		hash := sha256.New()
		count, copyErr := io.Copy(hash, stream)
		closeErr := stream.Close()
		if copyErr != nil || closeErr != nil || count != blob.Size || hex.EncodeToString(hash.Sum(nil)) != blob.SHA256 {
			return report, rejectSitepack(blobLocators[i], "blob_checksum_mismatch", "Контрольная сумма элемента архива не совпадает")
		}
	}
	if len(used) != len(entries) {
		return report, rejectSitepack("archive.entries", "unindexed_archive_entry", "Архив содержит элементы, не описанные в индексе")
	}
	// The package is user-supplied. A MIME label is not evidence that a blob
	// is safe to serve inline: classify its verified bytes like normal uploads.
	for i, file := range index.Files {
		if err := validatePackFile(entries, file, options.MaxSVGBytes); err != nil {
			return report, rejectSitepack(fmt.Sprintf("files[%d]", i), "invalid_file", "Тип или содержимое файла не прошло проверку")
		}
		for j, rendition := range file.Renditions {
			profile, err := media.DecodeFieldProfile(rendition.Profile)
			if err != nil {
				return report, rejectSitepack(fmt.Sprintf("files[%d].renditions[%d]", i, j), "invalid_rendition_profile", "Параметры варианта файла некорректны")
			}
			hash, err := media.ProfileHash(profile)
			if err != nil || hash != rendition.Hash || len(rendition.Outputs) != len(rendition.Blobs) {
				return report, rejectSitepack(fmt.Sprintf("files[%d].renditions[%d]", i, j), "rendition_mismatch", "Вариант файла не совпадает с его профилем")
			}
			for name, blob := range rendition.Blobs {
				var output media.StoredOutput
				if err := json.Unmarshal(rendition.Outputs[name], &output); err != nil || output.MIMEType != blob.ContentType || output.Width < 0 || output.Height < 0 {
					return report, rejectSitepack(fmt.Sprintf("files[%d].renditions[%d]", i, j), "invalid_rendition_output", "Описание результата варианта файла некорректно")
				}
				if err := validatePackBlobMIME(entries[blob.Path], name, blob.ContentType, options.MaxSVGBytes); err != nil {
					return report, rejectSitepack(fmt.Sprintf("files[%d].renditions[%d]", i, j), "invalid_rendition_file", "Содержимое варианта файла не прошло проверку")
				}
			}
		}
	}
	archives := []VerifiedArchive{}
	assetMaps := []map[string]AssetRecord{}
	for i, blob := range index.Builds {
		if blob.Size > s.builds.maxArchiveBytes {
			return report, rejectSitepack(fmt.Sprintf("builds[%d]", i), "build_archive_too_large", "ZIP-сборка функции превышает допустимый размер")
		}
		raw, err := readEntry(entries[blob.Path])
		if err != nil {
			return report, rejectSitepack(fmt.Sprintf("builds[%d]", i), "build_archive_unreadable", "Не удалось прочитать ZIP-сборку функции")
		}
		archive, err := VerifyArchive(raw, s.builds.maxArchiveBytes)
		if err != nil {
			return report, rejectSitepack(fmt.Sprintf("builds[%d]", i), "invalid_build_archive", "ZIP-сборка функции повреждена или несовместима")
		}
		if archive.Digest != blob.SHA256 {
			return report, rejectSitepack(fmt.Sprintf("builds[%d]", i), "build_digest_mismatch", "Контрольная сумма ZIP-сборки не совпадает")
		}
		archives = append(archives, archive)
		assets := map[string]AssetRecord{}
		for _, asset := range archive.Assets {
			key := path.Join("feature-builds", archive.Digest, asset.Path)
			assets[asset.Path] = AssetRecord{ObjectKey: key, ContentType: asset.ContentType, Size: asset.Size, SHA256: asset.SHA256}
		}
		assetMaps = append(assetMaps, assets)
	}
	// The hash of each original ZIP is authoritative. Do not accept a
	// separately edited manifest or duplicate key/version in the index.
	archiveByDigest := make(map[string]VerifiedArchive, len(archives))
	for i, archive := range archives {
		if _, exists := archiveByDigest[archive.Digest]; exists {
			return report, rejectSitepack(fmt.Sprintf("builds[%d]", i), "duplicate_build_archive", "ZIP-сборка указана повторно")
		}
		archiveByDigest[archive.Digest] = archive
	}
	if len(index.Payload.Builds) != len(archiveByDigest) {
		return report, rejectSitepack("snapshot.builds", "build_index_mismatch", "Список ZIP-сборок не совпадает с конфигурацией снапшота")
	}
	seenBuilds := make(map[string]bool, len(index.Payload.Builds))
	for i, expected := range index.Payload.Builds {
		archive, ok := archiveByDigest[expected.Digest]
		manifest, marshalErr := json.Marshal(archive.Manifest)
		if !ok || marshalErr != nil || archive.Manifest.Key != expected.Key || archive.Manifest.Version != expected.Version ||
			seenBuilds[expected.Key] || !jsonEqual(manifest, expected.Manifest) {
			return report, rejectSitepack(fmt.Sprintf("snapshot.builds[%d]", i), "manifest_mismatch", "Манифест закреплённой сборки не совпадает с ZIP сборки")
		}
		seenBuilds[expected.Key] = true
	}
	if err := validateSitepackPayload(&index); err != nil {
		var rejected *SitepackImportFailure
		if errors.As(err, &rejected) {
			return report, err
		}
		return report, rejectSitepack("snapshot.payload", "invalid_snapshot_payload", "Конфигурация, контент или схема снапшота несовместимы")
	}
	if err := validateSitepackModelContent(&index); err != nil {
		return report, rejectSitepack("modelContent", "invalid_model_content", "Модель, записи, связи или история публикаций некорректны")
	}
	modelIDs, err := remapSitepackModelIdentities(&index)
	if err != nil {
		return report, rejectSitepack("modelContent.identities", "invalid_model_identities", "Идентификаторы модели, записей или ссылок некорректны")
	}
	if err := index.Payload.Config.Validate(payloadBuildRegistry(index.Payload.Builds)); err != nil {
		return report, rejectSitepack("snapshot.config", "invalid_snapshot_config", "Конфигурация сайта не соответствует закреплённым сборкам")
	}
	if err := validateSitepackModelContent(&index); err != nil {
		return report, rejectSitepack("modelContent", "invalid_model_content", "Переназначенные модель, записи или связи не прошли повторную проверку")
	}
	if err := seedSitepackSingletonDraft(&index); err != nil {
		return report, rejectSitepack("modelContent.draftRecords", "invalid_singleton_draft", "Черновик должен содержать одну запись для каждой singleton-сущности")
	}
	if index.ModelContent != nil {
		report.ModelPublishedRecords = len(index.ModelContent.PublishedRecords)
		report.ModelDraftRecords = len(index.ModelContent.DraftRecords)
		report.ModelHistoryRevisions = len(index.ModelContent.History)
		report.RemappedModelEntityIDs = len(modelIDs.entities)
		report.RemappedModelFieldIDs = len(modelIDs.fields)
		report.RemappedModelRelationIDs = len(modelIDs.relations)
		report.RemappedModelRecordIDs = len(modelIDs.records)
	}
	references, err := checkPackReferences(&index)
	if err != nil {
		return report, rejectSitepack("media.references", "invalid_media_reference", "Ссылка на медиа не разрешается в файл архива")
	}
	modelRefs, err := sitepackModelMediaReferences(index.ModelContent, index.Payload.Config.DataModel)
	if err != nil {
		return report, rejectSitepack("modelContent.media", "invalid_model_media_reference", "Медиа-поле модели содержит некорректную ссылку")
	}
	references = appendUniqueFileIDs(references, modelRefs...)
	if len(references) != len(fileIDs) {
		return report, rejectSitepack("media.references", "unreferenced_or_missing_file", "Состав файлов не совпадает с медиа-ссылками снапшота")
	}
	for _, id := range references {
		if !fileIDs[id] {
			return report, rejectSitepack("media.references", "missing_referenced_file", "В архиве отсутствует файл, используемый снапшотом")
		}
	}
	if err := s.validateSitepackBuildConflicts(ctx, archives); err != nil {
		return report, err
	}
	// Do not create even content-addressed objects until the complete package,
	// its pinned manifests, and all editorial/design data have passed preflight.
	// A later DB race can still reject registration; these build keys are
	// immutable and safe to reuse on retry.
	for i, archive := range archives {
		blob := index.Builds[i]
		raw, err := readEntry(entries[blob.Path])
		if err != nil {
			return report, err
		}
		if err := s.builds.store.Put(ctx, path.Join("feature-builds", archive.Digest, "artifact.zip"), bytes.NewReader(raw), blob.Size, storage.PutOptions{ContentType: "application/zip", CacheControl: "private, no-store"}); err != nil {
			return report, err
		}
		for _, asset := range archive.Assets {
			key := path.Join("feature-builds", archive.Digest, asset.Path)
			if err := s.builds.store.Put(ctx, key, bytes.NewReader(asset.Data), asset.Size, storage.PutOptions{ContentType: asset.ContentType, CacheControl: "public, max-age=31536000, immutable"}); err != nil {
				return report, err
			}
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockFileReferences(ctx, tx); err != nil {
		return report, err
	}
	if err := lockSnapshotTree(ctx, tx); err != nil {
		return report, err
	}
	builds := []PinnedBuild{}
	for i, archive := range archives {
		record, created, err := s.builds.RegisterArchiveTx(ctx, tx, actor, archive, assetMaps[i])
		if err != nil {
			return report, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if created {
			report.NewBuilds++
			addSitepackReportItem(&report, SitepackReportItem{Kind: "build", Path: fmt.Sprintf("builds[%d]", i), Action: "registered", Message: fmt.Sprintf("Сборка %s@%s зарегистрирована", record.Key, record.Version)})
		} else {
			report.ReusedBuilds++
			addSitepackReportItem(&report, SitepackReportItem{Kind: "build", Path: fmt.Sprintf("builds[%d]", i), Action: "reused", Message: fmt.Sprintf("Сборка %s@%s уже была зарегистрирована и повторно использована", record.Key, record.Version)})
		}
		assets := map[string]PinnedAsset{}
		for key, asset := range record.Assets {
			assets[key] = PinnedAsset{ContentType: asset.ContentType, Size: asset.Size, SHA256: asset.SHA256}
		}
		builds = append(builds, PinnedBuild{Key: record.Key, Name: record.Name, Version: record.Version, Digest: record.Digest, Manifest: record.Manifest, Assets: assets, SubmissionSchemaVersionID: record.SubmissionSchemaVersionID})
	}
	if len(builds) != len(index.Payload.Builds) {
		return report, fmt.Errorf("%w: build index does not match payload", ErrInvalid)
	}
	for _, expected := range index.Payload.Builds {
		found := false
		for _, build := range builds {
			if build.Key == expected.Key && build.Version == expected.Version && build.Digest == expected.Digest {
				found = true
				break
			}
		}
		if !found {
			return report, fmt.Errorf("%w: missing pinned feature build", ErrInvalid)
		}
	}
	var owner uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM file_owners WHERE kind='admin' AND enabled FOR UPDATE`).Scan(&owner); err != nil {
		return report, err
	}
	namespace := "sitepacks/" + uuid.NewString() + "/"
	staged := []string{}
	committed := false
	remappedFileIDs := make(map[string]string, len(index.Files))
	defer func() {
		if !committed {
			// Return the transaction's connection before requesting another for
			// cleanup. This also works with a one-connection test/deployment pool.
			_ = tx.Rollback(context.Background())
			for _, key := range staged {
				_, _ = s.pool.Exec(context.Background(), `INSERT INTO file_object_deletions(object_key) VALUES($1) ON CONFLICT DO NOTHING`, key)
			}
		}
	}()
	storeBlob := func(blob packBlob, key, cacheControl string) error {
		stagedFile, err := stageSitepackBlob(options.TempDir, entries[blob.Path], blob.Size)
		if err != nil {
			return err
		}
		defer func() {
			name := stagedFile.Name()
			_ = stagedFile.Close()
			_ = os.Remove(name)
		}()
		// Reserve the S3 key before Put because a failed response can follow a
		// successful or partial write. The rollback queue then removes the key.
		staged = append(staged, key)
		return s.builds.store.Put(ctx, key, stagedFile, blob.Size, storage.PutOptions{ContentType: blob.ContentType, CacheControl: cacheControl})
	}
	fileCacheControl := func(visibility string) string {
		if visibility == "public" {
			return "public, max-age=31536000, immutable"
		}
		return "private, no-store"
	}
	restoreRenditions := func(file packFile, id uuid.UUID) error {
		for _, rendition := range file.Renditions {
			var status string
			var existingRaw []byte
			lookupErr := tx.QueryRow(ctx, `SELECT status,outputs FROM media_renditions WHERE media_id=$1 AND profile_hash=$2 FOR UPDATE`, id, rendition.Hash).Scan(&status, &existingRaw)
			if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
				return lookupErr
			}
			exists := lookupErr == nil
			existing := map[string]media.StoredOutput{}
			if exists && len(existingRaw) > 0 {
				if err := json.Unmarshal(existingRaw, &existing); err != nil {
					return fmt.Errorf("decode existing media renditions: %w", err)
				}
			}
			if exists && status == "processing" {
				continue
			}
			if exists && status == "ready" {
				complete := true
				for name := range rendition.Blobs {
					if existing[name].ObjectKey == "" {
						complete = false
						break
					}
				}
				if complete {
					continue
				}
			}
			if exists && status != "ready" && status != "pending" && status != "failed" {
				return fmt.Errorf("cannot restore rendition with status %q", status)
			}

			names := make([]string, 0, len(rendition.Blobs))
			for name := range rendition.Blobs {
				if existing[name].ObjectKey == "" {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			for _, name := range names {
				blob := rendition.Blobs[name]
				target := namespace + id.String() + "/renditions/" + uuid.NewString()
				if err := storeBlob(blob, target, fileCacheControl(file.Visibility)); err != nil {
					return err
				}
				var output media.StoredOutput
				if err := json.Unmarshal(rendition.Outputs[name], &output); err != nil {
					return err
				}
				output.ObjectKey = target
				existing[name] = output
			}
			encoded, err := json.Marshal(existing)
			if err != nil {
				return err
			}
			if exists {
				_, err = tx.Exec(ctx, `UPDATE media_renditions SET profile=$3::jsonb,status='ready',outputs=$4::jsonb,lease_expires_at=NULL,last_error=NULL,updated_at=now() WHERE media_id=$1 AND profile_hash=$2`, id, rendition.Hash, string(rendition.Profile), string(encoded))
			} else {
				_, err = tx.Exec(ctx, `INSERT INTO media_renditions(media_id,profile_hash,profile,status,outputs) VALUES($1,$2,$3::jsonb,'ready',$4::jsonb)`, id, rendition.Hash, string(rendition.Profile), string(encoded))
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	for i, file := range index.Files {
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM media_objects WHERE owner_id=$1 AND visibility=$2 AND sha256=$3 AND status='ready' LIMIT 1`, owner, file.Visibility, file.Original.SHA256).Scan(&id)
		if err == nil {
			report.ReusedFiles++
			remappedFileIDs[file.ID.String()] = id.String()
			addSitepackReportFileID(&report, file.ID.String(), id.String())
			addSitepackReportItem(&report, SitepackReportItem{Kind: "file", Path: fmt.Sprintf("files[%d]", i), Action: "reused", SourceID: file.ID.String(), TargetID: id.String(), Message: "Существующий файл повторно использован; медиа-ссылки переназначены"})
			if err := restoreRenditions(file, id); err != nil {
				return report, fmt.Errorf("restore renditions for reused file: %w", err)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return report, err
		}
		id = uuid.New()
		remappedFileIDs[file.ID.String()] = id.String()
		addSitepackReportFileID(&report, file.ID.String(), id.String())
		prefix := namespace + id.String() + "/"
		key := prefix + "original"
		if err := storeBlob(file.Original, key, fileCacheControl(file.Visibility)); err != nil {
			return report, err
		}
		variants := map[string]string{"original": key}
		for name, blob := range file.Variants {
			target := prefix + "variants/" + uuid.NewString()
			if err := storeBlob(blob, target, fileCacheControl(file.Visibility)); err != nil {
				return report, err
			}
			variants[name] = target
		}
		encoded, _ := json.Marshal(variants)
		if _, err := tx.Exec(ctx, `INSERT INTO media_objects(id,owner_id,visibility,sha256,object_key,original_filename,content_type,size_bytes,status,variants,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'ready',$9::jsonb,$10)`, id, owner, file.Visibility, file.Original.SHA256, key, file.Name, file.Original.ContentType, file.Original.Size, string(encoded), actor); err != nil {
			return report, err
		}
		if err := restoreRenditions(file, id); err != nil {
			return report, fmt.Errorf("restore renditions for imported file: %w", err)
		}
		report.NewFiles++
		addSitepackReportItem(&report, SitepackReportItem{Kind: "file", Path: fmt.Sprintf("files[%d]", i), Action: "imported", SourceID: file.ID.String(), TargetID: id.String(), Message: "Файл перенесён; медиа-ссылки переназначены"})
	}
	index.Payload.Builds = builds
	index.Payload.FormatVersion = 4
	if err := rewritePackReferences(&index, remappedFileIDs); err != nil {
		return report, err
	}
	if err := rewriteSitepackModelMediaReferences(index.ModelContent, index.Payload.Config.DataModel, remappedFileIDs); err != nil {
		return report, fmt.Errorf("%w: model media reference remap: %v", ErrInvalid, err)
	}
	addSitepackModelReportItems(&report, modelIDs)
	registry := payloadBuildRegistry(builds)
	if err := index.Payload.Config.Validate(registry); err != nil {
		return report, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if index.Payload.Metadata != nil {
		if err := index.Payload.Metadata.Validate(); err != nil {
			return report, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	if index.Payload.DesignSystem != nil {
		normalized, err := NormalizeSnapshotDesignSystem(*index.Payload.DesignSystem, builds)
		if err != nil {
			return report, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		index.Payload.DesignSystem = &normalized
	}
	name := strings.TrimSpace(index.Name)
	if name == "" || len([]rune(name)) > 160 {
		return report, fmt.Errorf("%w: invalid imported snapshot name", ErrInvalid)
	}
	index.Payload.CreationSummary = nil
	creationSummary := SnapshotCreationSummary{Origin: snapshotOriginSitepack}
	index.Payload.CreationSummary = &creationSummary
	digest, err := SnapshotDigest(index.Payload)
	if err != nil {
		return report, err
	}
	payloadRaw, err := json.Marshal(index.Payload)
	if err != nil {
		return report, err
	}
	record := SnapshotRecord{ID: uuid.New(), Name: name, Digest: digest, CreationSummary: &creationSummary}
	record.CreatedByName, err = snapshotActorName(ctx, tx, actor)
	if err != nil {
		return report, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO site_snapshots(id,name,digest,payload,created_by) VALUES($1,$2,$3,$4::jsonb,$5) RETURNING sequence,created_at`, record.ID, name, digest, string(payloadRaw), actor).Scan(&record.Sequence, &record.CreatedAt); err != nil {
		return report, err
	}
	if modelContent := index.ModelContent; modelContent != nil {
		state := storedModelContentWorkspace{Exists: false}
		if err := writeModelContentWorkspace(ctx, tx, actor, record.ID, state,
			modelContent.Revision, modelContent.PublishedRevision,
			modelContent.DraftRecords, modelContent.DraftSelections,
			modelContent.PublishedRecords, modelContent.PublishedSelections,
		); err != nil {
			return report, err
		}
		for _, publication := range modelContent.History {
			records, err := json.Marshal(publication.Records)
			if err != nil {
				return report, err
			}
			selections, err := json.Marshal(publication.Selections)
			if err != nil {
				return report, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO snapshot_model_content_publications(id,snapshot_id,draft_revision,content_revision,records,selections,actor_token_id,created_at) VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,NULL,$7)`, publication.ID, record.ID, publication.DraftRevision, publication.ContentRevision, string(records), string(selections), publication.CreatedAt); err != nil {
				return report, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE site_snapshots SET content_revision=$2 WHERE id=$1`, record.ID, modelContent.ContentRevision); err != nil {
			return report, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return report, err
	}
	committed = true
	report.Snapshot = record
	return report, nil
}

func seedSitepackSingletonDraft(index *sitepackIndex) error {
	if index == nil {
		return fmt.Errorf("%w: missing sitepack index", ErrInvalid)
	}
	model := index.Payload.Config.DataModel
	if model == nil {
		return nil
	}
	if index.ModelContent == nil {
		index.ModelContent = &packModelContent{
			DraftRecords: []ModelContentRecord{}, DraftSelections: []ModelContentSelection{},
			PublishedRecords: []ModelContentRecord{}, PublishedSelections: []ModelContentSelection{},
			History: []packModelPublication{},
		}
	}
	draftRecords, seeded, err := seedSingletonDraftRecords(*model, index.ModelContent.DraftRecords)
	if err != nil {
		return err
	}
	if seeded {
		if index.ModelContent.Revision == math.MaxInt64 {
			return fmt.Errorf("%w: model-content revision is exhausted", ErrInvalid)
		}
		index.ModelContent.DraftRecords = draftRecords
		index.ModelContent.Revision++
	}
	if err := validateModelContentEntityCardinality(*model, index.ModelContent.DraftRecords, true); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := validateModelContentEntityCardinality(*model, index.ModelContent.PublishedRecords, false); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// validatePackBlobMIME uses the same byte classifier as the file service;
// a generic download is always conservative, while an inline-capable type is
// accepted only if the bytes actually match. SVG inspection is bounded and
// uses the same configured threshold as ordinary uploads.
func validatePackBlobMIME(file *zip.File, name, declared string, maxSVGBytes int64) error {
	if file == nil || declared == "" {
		return errors.New("missing file or content type")
	}
	if strings.EqualFold(strings.TrimSpace(strings.Split(declared, ";")[0]), "application/octet-stream") {
		return nil
	}
	stream, err := file.Open()
	if err != nil {
		return err
	}
	var head [512]byte
	n, readErr := io.ReadFull(stream, head[:])
	closeErr := stream.Close()
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual := files.ClassifyContentType(name, head[:n], nil)
	if strings.EqualFold(path.Ext(name), ".svg") && strings.EqualFold(strings.TrimSpace(strings.Split(declared, ";")[0]), "image/svg+xml") {
		if file.UncompressedSize64 > uint64(maxSVGBytes) {
			return errors.New("SVG exceeds the configured inspection limit")
		}
		svg, err := file.Open()
		if err != nil {
			return err
		}
		actual = files.ClassifyContentType(name, head[:n], io.LimitReader(svg, maxSVGBytes+1))
		closeErr = svg.Close()
		if closeErr != nil {
			return closeErr
		}
	}
	actual = strings.ToLower(strings.TrimSpace(strings.Split(actual, ";")[0]))
	expected := strings.ToLower(strings.TrimSpace(strings.Split(declared, ";")[0]))
	if actual != expected {
		return fmt.Errorf("declared type %q does not match verified bytes (%s)", expected, actual)
	}
	return nil
}

func validatePackFile(entries map[string]*zip.File, file packFile, maxSVGBytes int64) error {
	if strings.ContainsAny(file.Name, "\x00\r\n") {
		return errors.New("invalid filename")
	}
	if err := validatePackBlobMIME(entries[file.Original.Path], file.Name, file.Original.ContentType, maxSVGBytes); err != nil {
		return err
	}
	for name, blob := range file.Variants {
		if err := validatePackBlobMIME(entries[blob.Path], name, blob.ContentType, maxSVGBytes); err != nil {
			return fmt.Errorf("variant %s: %w", name, err)
		}
	}
	return nil
}

func (s *SnapshotService) validateSitepackBuildConflicts(ctx context.Context, archives []VerifiedArchive) error {
	for i, archive := range archives {
		var existingDigest string
		err := s.pool.QueryRow(ctx, `SELECT COALESCE(b.digest,'') FROM features f LEFT JOIN feature_builds b ON b.feature_id=f.id AND b.version=$2 WHERE f.key=$1`, archive.Manifest.Key, archive.Manifest.Version).Scan(&existingDigest)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if existingDigest != "" && existingDigest != archive.Digest {
			return rejectSitepack(fmt.Sprintf("builds[%d]", i), "build_version_conflict", "Эта версия функции уже зарегистрирована с другим содержимым")
		}
	}
	return nil
}

func validateSitepackPayload(index *sitepackIndex) error {
	if index == nil {
		return rejectSitepack("sitepack.json", "missing_index", "Индекс sitepack.json отсутствует")
	}
	if strings.TrimSpace(index.Name) == "" || len([]rune(strings.TrimSpace(index.Name))) > 160 {
		return rejectSitepack("snapshot.name", "invalid_snapshot_name", "Название снапшота пустое или превышает допустимую длину")
	}
	registry := payloadBuildRegistry(index.Payload.Builds)
	if err := index.Payload.Config.Validate(registry); err != nil {
		return rejectSitepack("snapshot.config", "invalid_snapshot_config", "Конфигурация сайта не соответствует закреплённым сборкам")
	}
	if index.Payload.Metadata != nil {
		if err := index.Payload.Metadata.Validate(); err != nil {
			return rejectSitepack("snapshot.metadata", "invalid_site_metadata", "Метаданные сайта некорректны")
		}
	}
	if index.Payload.DesignSystem != nil {
		normalized, err := NormalizeSnapshotDesignSystem(*index.Payload.DesignSystem, index.Payload.Builds)
		if err != nil {
			return rejectSitepack("snapshot.designSystem", "invalid_design_system", "Дизайн-система не соответствует манифестам закреплённых сборок")
		}
		index.Payload.DesignSystem = &normalized
	}
	return nil
}
