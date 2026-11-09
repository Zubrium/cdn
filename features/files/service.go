package files

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"websiteengine/pkg/config"
	"websiteengine/pkg/storage"
)

type serviceKey struct {
	digest      [32]byte
	owner       Owner
	permissions map[string]bool
}
type Service struct {
	pool  *pgxpool.Pool
	store storage.ObjectStore
	cfg   config.FilesConfig
	admin Owner
	keys  []serviceKey
	refs  ReferenceManager
}

func NewService(pool *pgxpool.Pool, store storage.ObjectStore, cfg config.FilesConfig) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.TempDir, 0700); err != nil {
		return nil, err
	}
	return &Service{pool: pool, store: store, cfg: cfg}, nil
}
func (s *Service) SetReferenceManager(refs ReferenceManager) { s.refs = refs }
func (s *Service) Config() config.FilesConfig                { return s.cfg }
func (s *Service) AdminOwner() Owner                         { return s.admin }
func (s *Service) InitOwners(ctx context.Context) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_file_owners'))`); e != nil {
		return e
	}
	if e = tx.QueryRow(ctx, `SELECT id,name,kind FROM file_owners WHERE kind='admin'`).Scan(&s.admin.ID, &s.admin.Name, &s.admin.Kind); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE file_owners SET enabled=false WHERE kind='service'`); e != nil {
		return e
	}
	var keys []serviceKey
	seen := map[[32]byte]bool{}
	for _, c := range s.cfg.Services {
		var o Owner
		e = tx.QueryRow(ctx, `INSERT INTO file_owners(id,kind,name,enabled) VALUES($1,'service',$2,true) ON CONFLICT(kind,name) DO UPDATE SET enabled=true RETURNING id,name,kind`, uuid.New(), c.Name).Scan(&o.ID, &o.Name, &o.Kind)
		if e != nil {
			return e
		}
		for _, r := range c.Keys {
			raw, e := config.ReadFileSecret(r)
			if e != nil {
				return e
			}
			hash := sha256.Sum256(raw)
			if seen[hash] {
				return fmt.Errorf("duplicate file service key")
			}
			seen[hash] = true
			p := map[string]bool{}
			for _, permission := range c.Permissions {
				p[permission] = true
			}
			keys = append(keys, serviceKey{hash, o, p})
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	s.keys = keys
	return nil
}

// RenameOwner preserves generated identity. Change configuration before restart.
func (s *Service) RenameOwner(ctx context.Context, oldName, newName string) (Owner, error) {
	return RenameConfiguredService(ctx, s.pool, oldName, newName)
}
func (s *Service) Authenticate(token, permission string) (Owner, error) {
	hash := sha256.Sum256([]byte(token))
	for _, k := range s.keys {
		if subtle.ConstantTimeCompare(hash[:], k.digest[:]) == 1 {
			if !k.permissions[permission] {
				return Owner{}, ErrForbidden
			}
			return k.owner, nil
		}
	}
	return Owner{}, ErrForbidden
}
func (s *Service) CreateUpload(ctx context.Context, owner Owner, input UploadInput) (Upload, error) {
	if owner.ID == uuid.Nil || input.Size < 1 || input.Size > s.cfg.MaxBytes || !validHash(input.SHA256) || (input.Visibility != "public" && input.Visibility != "private") {
		return Upload{}, ErrInvalid
	}
	name := sanitizeName(input.Name)
	if name == "" || len(name) > 255 {
		return Upload{}, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Upload{}, e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var enabled bool
	if e = tx.QueryRow(ctx, `SELECT enabled FROM file_owners WHERE id=$1 FOR UPDATE`, owner.ID).Scan(&enabled); e != nil || !enabled {
		return Upload{}, ErrForbidden
	}
	var used, held int64
	var sessions int
	if e = tx.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes),0) FROM media_objects WHERE owner_id=$1 AND status<>'deleted'`, owner.ID).Scan(&used); e != nil {
		return Upload{}, e
	}
	if e = tx.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes),0),count(*) FROM file_upload_sessions WHERE owner_id=$1 AND state='uploading' AND expires_at>now()`, owner.ID).Scan(&held, &sessions); e != nil {
		return Upload{}, e
	}
	if input.Size > s.cfg.OwnerQuotaBytes-used-held || sessions >= s.cfg.MaxSessions {
		return Upload{}, ErrQuota
	}
	u := Upload{ID: uuid.New(), FileID: uuid.New(), OwnerID: owner.ID, Name: name, Size: input.Size, SHA256: input.SHA256, Visibility: input.Visibility, PartSize: s.cfg.PartBytes, State: "uploading", ExpiresAt: time.Now().UTC().Add(s.cfg.UploadTTL), Parts: []Part{}}
	_, e = tx.Exec(ctx, `INSERT INTO file_upload_sessions(id,file_id,owner_id,name,size_bytes,sha256,visibility,part_bytes,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, u.ID, u.FileID, u.OwnerID, u.Name, u.Size, u.SHA256, u.Visibility, u.PartSize, u.State, u.ExpiresAt)
	if e != nil {
		return Upload{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) VALUES($1) ON CONFLICT DO NOTHING`, "media/"+u.FileID.String()+"/original"); e != nil {
		return Upload{}, e
	}
	return u, tx.Commit(ctx)
}
func (s *Service) UploadStatus(ctx context.Context, owner Owner, id uuid.UUID) (Upload, error) {
	return uploadStatus(ctx, s.pool, owner, id, false)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func uploadStatus(ctx context.Context, q queryer, owner Owner, id uuid.UUID, lock bool) (Upload, error) {
	var u Upload
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	e := q.QueryRow(ctx, `SELECT id,file_id,owner_id,name,size_bytes,sha256,visibility,part_bytes,state,expires_at FROM file_upload_sessions WHERE id=$1 AND owner_id=$2`+suffix, id, owner.ID).Scan(&u.ID, &u.FileID, &u.OwnerID, &u.Name, &u.Size, &u.SHA256, &u.Visibility, &u.PartSize, &u.State, &u.ExpiresAt)
	if e != nil {
		return u, mapNotFound(e)
	}
	rows, e := q.Query(ctx, `SELECT number,size_bytes,sha256,ready,object_key FROM file_upload_parts WHERE session_id=$1 ORDER BY number`, id)
	if e != nil {
		return u, e
	}
	defer rows.Close()
	u.Parts = []Part{}
	for rows.Next() {
		var p Part
		if e := rows.Scan(&p.Number, &p.Size, &p.SHA256, &p.Ready, &p.ObjectKey); e != nil {
			return u, e
		}
		u.Parts = append(u.Parts, p)
	}
	return u, rows.Err()
}
func (s *Service) PutPart(ctx context.Context, owner Owner, id uuid.UUID, number int, size int64, digest string, body io.Reader) (Upload, error) {
	if number < 1 || !validHash(digest) {
		return Upload{}, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Upload{}, e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	u, e := uploadStatus(ctx, tx, owner, id, true)
	if e != nil {
		return u, e
	}
	if e = canUpload(u); e != nil {
		return u, e
	}
	count := (u.Size + u.PartSize - 1) / u.PartSize
	if int64(number) > count {
		return u, ErrInvalid
	}
	expected := u.PartSize
	if int64(number) == count {
		expected = u.Size - int64(number-1)*u.PartSize
	}
	if size != expected {
		return u, fmt.Errorf("%w: incorrect part length", ErrInvalid)
	}
	for _, p := range u.Parts {
		if p.Number == number && (p.SHA256 != digest || p.Size != size) {
			return u, ErrConflict
		}
	}
	file, e := os.CreateTemp(s.cfg.TempDir, "engine-part-*")
	if e != nil {
		return u, e
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, h), io.LimitReader(body, size+1))
	if e != nil {
		return u, e
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return u, fmt.Errorf("%w: part checksum or length mismatch", ErrInvalid)
	}
	for _, p := range u.Parts {
		if p.Number == number && p.Ready {
			return u, nil
		}
	}
	key := fmt.Sprintf("uploads/%s/%d", id, number)
	// Persist reservation first; an interrupted S3 Put is discoverable by cleanup.
	if _, e = tx.Exec(ctx, `INSERT INTO file_upload_parts(session_id,number,object_key,size_bytes,sha256,ready) VALUES($1,$2,$3,$4,$5,false) ON CONFLICT(session_id,number) DO NOTHING`, id, number, key, size, digest); e != nil {
		return u, e
	}
	if e = tx.Commit(ctx); e != nil {
		return u, e
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return u, e
	}
	if e = s.store.Put(ctx, key, file, size, storage.PutOptions{ContentType: "application/octet-stream", CacheControl: "private, no-store"}); e != nil {
		return u, e
	}
	// Abort may race with a slow storage write. Recheck under lock and queue a late object for deletion.
	tx, e = s.pool.Begin(ctx)
	if e != nil {
		return u, e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	fresh, e := uploadStatus(ctx, tx, owner, id, true)
	if e != nil {
		return u, e
	}
	if e = canUpload(fresh); e != nil {
		_, queueErr := tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) VALUES($1) ON CONFLICT DO NOTHING`, key)
		if queueErr != nil {
			return u, queueErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return u, commitErr
		}
		return u, e
	}
	if _, e = tx.Exec(ctx, `UPDATE file_upload_parts SET ready=true WHERE session_id=$1 AND number=$2`, id, number); e != nil {
		return u, e
	}
	if _, e = tx.Exec(ctx, `UPDATE file_upload_sessions SET expires_at=now()+$2::interval,updated_at=now() WHERE id=$1`, id, s.cfg.UploadTTL.String()); e != nil {
		return u, e
	}
	if e = tx.Commit(ctx); e != nil {
		return u, e
	}
	return s.UploadStatus(ctx, owner, id)
}
func (s *Service) Commit(ctx context.Context, owner Owner, id uuid.UUID) (File, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return File{}, e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	u, e := uploadStatus(ctx, tx, owner, id, true)
	if e != nil {
		return File{}, e
	}
	if u.State == "complete" {
		return getFile(ctx, tx, owner, u.FileID)
	}
	if e = canUpload(u); e != nil {
		return File{}, e
	}
	count := (u.Size + u.PartSize - 1) / u.PartSize
	if int64(len(u.Parts)) != count {
		return File{}, fmt.Errorf("%w: upload incomplete", ErrConflict)
	}
	composer, canCompose := s.store.(storage.ComposeStore)
	// S3 requires all but the last multipart part to be at least 5 MiB.
	canCompose = canCompose && (u.PartSize >= 5<<20 || count == 1)
	var assembled *os.File
	var target io.Writer = io.Discard
	if !canCompose {
		assembled, e = os.CreateTemp(s.cfg.TempDir, "engine-commit-*")
		if e != nil {
			return File{}, e
		}
		defer func() { _ = assembled.Close(); _ = os.Remove(assembled.Name()) }()
		target = assembled
	}
	prefix := &prefixWriter{limit: 512}
	hash := sha256.New()
	var written int64
	for i, p := range u.Parts {
		if !p.Ready || p.Number != i+1 {
			return File{}, fmt.Errorf("%w: upload incomplete", ErrConflict)
		}
		reader, e := s.store.Get(ctx, p.ObjectKey)
		if e != nil {
			return File{}, e
		}
		partHash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(target, hash, partHash, prefix), io.LimitReader(reader, p.Size+1))
		closeErr := reader.Close()
		if copyErr != nil {
			return File{}, copyErr
		}
		if closeErr != nil {
			return File{}, closeErr
		}
		if n != p.Size || hex.EncodeToString(partHash.Sum(nil)) != p.SHA256 {
			return File{}, fmt.Errorf("%w: stored part checksum mismatch", ErrInvalid)
		}
		written += n
	}
	if written != u.Size || hex.EncodeToString(hash.Sum(nil)) != u.SHA256 {
		return File{}, fmt.Errorf("%w: file checksum mismatch", ErrInvalid)
	}
	contentType := ClassifyContentType(u.Name, prefix.data, nil)
	// XML sniffing alone reports text/xml. Classify a bounded, validated
	// presentation SVG without rewriting its immutable bytes/checksum.
	if strings.EqualFold(filepath.Ext(u.Name), ".svg") && u.Size <= u.PartSize && len(u.Parts) == 1 {
		reader, readErr := s.store.Get(ctx, u.Parts[0].ObjectKey)
		if readErr != nil {
			return File{}, readErr
		}
		contentType = ClassifyContentType(u.Name, prefix.data, io.LimitReader(reader, u.Size+1))
		closeErr := reader.Close()
		if closeErr != nil {
			return File{}, closeErr
		}
	}
	key := "media/" + u.FileID.String() + "/original"
	opts := storage.PutOptions{ContentType: contentType, CacheControl: "private, no-store"}
	if canCompose {
		keys := make([]string, 0, len(u.Parts))
		for _, part := range u.Parts {
			keys = append(keys, part.ObjectKey)
		}
		if e = composer.Compose(ctx, key, keys, opts); e != nil {
			return File{}, e
		}
	} else {
		if _, e = assembled.Seek(0, io.SeekStart); e != nil {
			return File{}, e
		}
		if e = s.store.Put(ctx, key, assembled, u.Size, opts); e != nil {
			return File{}, e
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO media_objects(id,object_key,original_filename,content_type,size_bytes,status,variants,owner_id,visibility,sha256) VALUES($1,$2,$3,$4,$5,'ready',jsonb_build_object('original',$2::text),$6,$7,$8)`, u.FileID, key, u.Name, contentType, u.Size, owner.ID, u.Visibility, u.SHA256)
	if e != nil {
		return File{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE file_upload_sessions SET state='complete',updated_at=now() WHERE id=$1`, id); e != nil {
		return File{}, e
	}
	for _, p := range u.Parts {
		if _, e = tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) VALUES($1) ON CONFLICT DO NOTHING`, p.ObjectKey); e != nil {
			return File{}, e
		}
	}
	if _, e = tx.Exec(ctx, `DELETE FROM file_object_deletions WHERE object_key=$1`, key); e != nil {
		return File{}, e
	}
	f, e := getFile(ctx, tx, owner, u.FileID)
	if e != nil {
		return f, e
	}
	return f, tx.Commit(ctx)
}
func (s *Service) Abort(ctx context.Context, owner Owner, id uuid.UUID) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	u, e := uploadStatus(ctx, tx, owner, id, true)
	if e != nil {
		return e
	}
	if u.State == "complete" {
		return ErrConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) SELECT object_key FROM file_upload_parts WHERE session_id=$1 ON CONFLICT DO NOTHING`, id); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE file_upload_sessions SET state='cancelled',updated_at=now() WHERE id=$1`, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

const fileColumns = `id,owner_id,original_filename,size_bytes,content_type,COALESCE(sha256,''),visibility,created_at,object_key`

func scanFile(r pgx.Row) (File, error) {
	var f File
	e := r.Scan(&f.ID, &f.OwnerID, &f.Name, &f.Size, &f.ContentType, &f.SHA256, &f.Visibility, &f.CreatedAt, &f.ObjectKey)
	return f, mapNotFound(e)
}
func getFile(ctx context.Context, q queryer, owner Owner, id uuid.UUID) (File, error) {
	return scanFile(q.QueryRow(ctx, `SELECT `+fileColumns+` FROM media_objects WHERE id=$1 AND owner_id=$2 AND status='ready'`, id, owner.ID))
}
func (s *Service) Get(ctx context.Context, owner Owner, id uuid.UUID) (File, error) {
	return getFile(ctx, s.pool, owner, id)
}
func (s *Service) PublicFile(ctx context.Context, id uuid.UUID) (File, error) {
	return scanFile(s.pool.QueryRow(ctx, `SELECT `+fileColumns+` FROM media_objects WHERE id=$1 AND status='ready' AND (visibility='public' OR EXISTS(SELECT 1 FROM file_owners WHERE id=owner_id AND enabled))`, id))
}
func (s *Service) List(ctx context.Context, owner Owner, before *uuid.UUID, search string) ([]File, *uuid.UUID, error) {
	rows, e := s.pool.Query(ctx, `SELECT `+fileColumns+` FROM media_objects WHERE owner_id=$1 AND status='ready' AND ($2::uuid IS NULL OR id<$2) AND original_filename ILIKE '%'||$3||'%' ORDER BY id DESC LIMIT $4`, owner.ID, before, search, s.cfg.ListLimit+1)
	if e != nil {
		return nil, nil, e
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		f, e := scanFile(rows)
		if e != nil {
			return nil, nil, e
		}
		out = append(out, f)
	}
	if e := rows.Err(); e != nil {
		return nil, nil, e
	}
	var next *uuid.UUID
	if len(out) > s.cfg.ListLimit {
		out = out[:s.cfg.ListLimit]
		id := out[len(out)-1].ID
		next = &id
	}
	return out, next, nil
}
func (s *Service) References(ctx context.Context, owner Owner, id uuid.UUID) ([]Reference, error) {
	if _, e := s.Get(ctx, owner, id); e != nil {
		return nil, e
	}
	if s.refs == nil {
		return []Reference{}, nil
	}
	return s.refs.References(ctx, id)
}
func (s *Service) Delete(ctx context.Context, owner Owner, id uuid.UUID, confirm bool) error {
	return s.DeleteIfReferencesUnchanged(ctx, owner, id, confirm, "")
}

// ReferencesRevision describes the exact known references presented before a
// destructive confirmation. Stable sorting prevents database row order from
// producing false conflicts.
func ReferencesRevision(refs []Reference) string {
	ordered := append([]Reference(nil), refs...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind < ordered[j].Kind
		}
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Description < ordered[j].Description
	})
	hash := sha256.New()
	for _, ref := range ordered {
		fmt.Fprintf(hash, "%d:%s%d:%s%d:%s", len(ref.Kind), ref.Kind, len(ref.ID), ref.ID, len(ref.Description), ref.Description)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Service) DeleteIfReferencesUnchanged(ctx context.Context, owner Owner, id uuid.UUID, confirm bool, expectedReferences string) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('websiteengine_file_references'))`); e != nil {
		return e
	}
	var key string
	var variants []byte
	e = tx.QueryRow(ctx, `SELECT object_key,variants FROM media_objects WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner.ID).Scan(&key, &variants)
	if e != nil {
		return mapNotFound(e)
	}
	if s.refs != nil {
		refs, e := s.refs.ReferencesTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if len(refs) > 0 && !confirm {
			return ErrInUse
		}
		if len(refs) > 0 && confirm && expectedReferences == "" {
			return fmt.Errorf("%w: необходимо подтвердить актуальный список использований", ErrConflict)
		}
		if expectedReferences != "" && expectedReferences != ReferencesRevision(refs) {
			return fmt.Errorf("%w: список использований изменился; проверьте его снова", ErrConflict)
		}
		if confirm {
			if e = s.refs.RemoveReferences(ctx, tx, id); e != nil {
				return e
			}
		}
	}
	// Capture all derivative keys before the rows disappear. JSON field values are server-owned object keys.
	if _, e = tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) SELECT $2 UNION SELECT value FROM media_objects,jsonb_each_text(variants) WHERE id=$1 UNION SELECT output->>'objectKey' FROM media_renditions,jsonb_each(outputs) AS o(name,output) WHERE media_id=$1 AND output->>'objectKey' IS NOT NULL ON CONFLICT DO NOTHING`, id, key); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM media_objects WHERE id=$1`, id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) Link(ctx context.Context, owner Owner, id uuid.UUID, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 || ttl > s.cfg.LinkMaxTTL {
		return "", time.Time{}, ErrInvalid
	}
	if _, e := s.Get(ctx, owner, id); e != nil {
		return "", time.Time{}, e
	}
	expiry := time.Now().UTC().Add(ttl).Truncate(time.Second)
	q := url.Values{"expires": {strconv.FormatInt(expiry.Unix(), 10)}, "signature": {s.signature(id, expiry.Unix())}}
	return s.cfg.PublicBaseURL + "/api/v2/files/objects/" + id.String() + "/content?" + q.Encode(), expiry, nil
}
func (s *Service) signature(id uuid.UUID, expiry int64) string {
	m := hmac.New(sha256.New, s.cfg.SigningKey)
	_, _ = fmt.Fprintf(m, "file:%s:%d", id, expiry)
	return hex.EncodeToString(m.Sum(nil))
}
func (s *Service) ValidSignature(id uuid.UUID, expiryRaw, signature string) bool {
	expiry, e := strconv.ParseInt(expiryRaw, 10, 64)
	if e != nil || expiry <= time.Now().Unix() || expiry-time.Now().Unix() > int64(s.cfg.LinkMaxTTL/time.Second) {
		return false
	}
	expected := s.signature(id, expiry)
	return hmac.Equal([]byte(signature), []byte(expected))
}
func (s *Service) Cleanup(ctx context.Context) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// SKIP LOCKED avoids cancelling an active part/commit transaction.
	rows, e := tx.Query(ctx, `SELECT id FROM file_upload_sessions WHERE state='uploading' AND expires_at<now() ORDER BY expires_at FOR UPDATE SKIP LOCKED`)
	if e != nil {
		return e
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if e := rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) SELECT object_key FROM file_upload_parts WHERE session_id=$1 ON CONFLICT DO NOTHING`, id); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE file_upload_sessions SET state='cancelled',updated_at=now() WHERE id=$1`, id); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO file_object_deletions(object_key) SELECT p.object_key FROM file_upload_parts p JOIN file_upload_sessions u ON u.id=p.session_id WHERE u.state='cancelled' UNION SELECT 'media/'||file_id::text||'/original' FROM file_upload_sessions WHERE state='cancelled' ON CONFLICT DO NOTHING`); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	rows, e = s.pool.Query(ctx, `SELECT object_key FROM file_object_deletions ORDER BY created_at`)
	if e != nil {
		return e
	}
	var keys []string
	for rows.Next() {
		var k string
		if e := rows.Scan(&k); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, k)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, key := range keys { // A committed ready file always wins over a stale reservation.
		var used bool
		if e = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_objects WHERE object_key=$1 AND status='ready' UNION ALL SELECT 1 FROM file_upload_sessions WHERE state='uploading' AND 'media/'||file_id::text||'/original'=$1)`, key).Scan(&used); e != nil {
			return e
		}
		if used {
			continue
		}
		if e = s.store.Delete(ctx, key); e != nil {
			return e
		}
		if _, e = s.pool.Exec(ctx, `DELETE FROM file_object_deletions WHERE object_key=$1`, key); e != nil {
			return e
		}
	}
	if composer, ok := s.store.(storage.ComposeStore); ok {
		return composer.CleanupMultipart(ctx, time.Now().Add(-s.cfg.UploadTTL))
	}
	return nil
}
func (s *Service) RunCleanup(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(s.cfg.CleanupInterval)
	defer ticker.Stop()
	for {
		if e := s.Cleanup(ctx); e != nil && ctx.Err() == nil {
			log.Error("file cleanup failed", "error", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func canUpload(u Upload) error {
	if u.State != "uploading" {
		return ErrConflict
	}
	if !u.ExpiresAt.After(time.Now()) {
		return ErrExpired
	}
	return nil
}
func validHash(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}
func sanitizeName(s string) string {
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if !utf8.ValidString(s) || s == "." || s == "/" {
		return ""
	}
	return s
}
func mapNotFound(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return e
}

// prefixWriter records only the sniffing prefix while reporting the full write.
type prefixWriter struct {
	data  []byte
	limit int
}

func (p *prefixWriter) Write(data []byte) (int, error) {
	n := len(data)
	remaining := p.limit - len(p.data)
	if remaining > 0 {
		p.data = append(p.data, data[:min(remaining, n)]...)
	}
	return n, nil
}
