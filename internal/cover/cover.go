// Package cover stores user-uploaded album and track artwork.
//
// Reverb never rewrites the files in the music library: an uploaded image is
// kept beside the database and swapped in when albums and tracks are read out,
// leaving the library backend's own art untouched.
//
// Blobs are addressed by the sha256 of their bytes, so applying one image to
// fifty albums stores it once. The address is also what travels to a paired
// device: the change log carries the hash and extension, and the bytes follow
// separately.
package cover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Entity kinds that can carry an uploaded cover.
const (
	KindAlbum = "album"
	KindTrack = "track"
)

// MaxBytes bounds one upload. Cover art is not a file transfer.
const MaxBytes = 5 * 1024 * 1024

// Prefix marks a cover art id that names an uploaded blob rather than something
// the library backend knows about. The hash is part of the id so a replaced
// cover gets a new URL and no cache anywhere has to be told about it.
const Prefix = "custom:"

// ErrUnsupportedType is returned for bytes that are not a JPEG, PNG, or WebP.
var ErrUnsupportedType = errors.New("cover: unsupported image type; use jpeg, png, or webp")

// ErrTooLarge is returned for an upload over MaxBytes.
var ErrTooLarge = fmt.Errorf("cover: image exceeds %d bytes", MaxBytes)

// Dir is where uploaded artwork lives, given Reverb's data directory. It sits
// beside the database rather than in the music library, which Reverb never
// writes to.
func Dir(dataDir string) string { return filepath.Join(dataDir, "entity-covers") }

// Querier is the slice of generated queries this package needs.
type Querier interface {
	GetEntityCover(ctx context.Context, arg db.GetEntityCoverParams) (db.EntityCover, error)
	GetEntityCoverByKey(ctx context.Context, arg db.GetEntityCoverByKeyParams) (db.EntityCover, error)
	ListEntityCovers(ctx context.Context) ([]db.EntityCover, error)
	UpsertEntityCover(ctx context.Context, arg db.UpsertEntityCoverParams) error
	DeleteEntityCover(ctx context.Context, arg db.DeleteEntityCoverParams) error
	DeleteEntityCoverByKey(ctx context.Context, arg db.DeleteEntityCoverByKeyParams) error
	CountEntityCoverRefs(ctx context.Context, sha256 string) (int64, error)
}

// Service owns the blob directory and the entity_cover rows pointing into it.
type Service struct {
	q   Querier
	dir string
	// catalogIDs resolves backend track ids to the catalog ids a track cover is
	// keyed on for replication. Optional: without it track covers still work,
	// they just do not survive a library-backend swap.
	catalogIDs func(ctx context.Context, trackIDs []string) map[string]string
}

// SetCatalogResolver supplies the backend-id to catalog-id mapping used to key
// track covers. A backend track id is local to one library backend, so it is
// not an identity a peer can agree on.
func (s *Service) SetCatalogResolver(fn func(ctx context.Context, trackIDs []string) map[string]string) {
	if s != nil {
		s.catalogIDs = fn
	}
}

// New returns a Service storing blobs under dir. A blank dir leaves the service
// usable but inert, which is what a build with no data directory needs.
func New(q Querier, dir string) *Service {
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	return &Service{q: q, dir: dir}
}

func (s *Service) ready() bool { return s != nil && s.q != nil && s.dir != "" }

// ExtFromContentType maps an accepted image content type to a file extension.
func ExtFromContentType(ct string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0])) {
	case "image/jpeg", "image/jpg":
		return "jpg", true
	case "image/png":
		return "png", true
	case "image/webp":
		return "webp", true
	}
	return "", false
}

// ContentTypeForExt is the inverse of ExtFromContentType, for serving a blob.
func ContentTypeForExt(ext string) string {
	switch ext {
	case "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	}
	return "application/octet-stream"
}

// Store writes the image bytes and returns the id that now names them. The
// declared content type is not trusted: the extension comes from sniffing the
// bytes, so a mislabelled or hostile upload cannot pick its own file name.
func (s *Service) Store(data []byte) (sha, ext string, err error) {
	if !s.ready() {
		return "", "", errors.New("cover: no store")
	}
	if len(data) > MaxBytes {
		return "", "", ErrTooLarge
	}
	ext, ok := ExtFromContentType(http.DetectContentType(data))
	if !ok {
		return "", "", ErrUnsupportedType
	}
	sum := sha256.Sum256(data)
	sha = hex.EncodeToString(sum[:])
	path := s.blobPath(sha, ext)
	if _, statErr := os.Stat(path); statErr == nil {
		return sha, ext, nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", "", err
	}
	return sha, ext, nil
}

// blobPath is where one hash lives on disk. sha and ext are always produced by
// Store or validated by parseID, so neither can escape the directory.
func (s *Service) blobPath(sha, ext string) string {
	return filepath.Join(s.dir, sha+"."+ext)
}

// Assign points one entity at an already-stored blob.
func (s *Service) Assign(ctx context.Context, kind, entityID, key, sha, ext string) error {
	if !s.ready() {
		return errors.New("cover: no store")
	}
	if entityID == "" || sha == "" || ext == "" {
		return errors.New("cover: incomplete assignment")
	}
	prev, hadPrev := s.current(ctx, kind, entityID)
	if err := s.q.UpsertEntityCover(ctx, db.UpsertEntityCoverParams{
		EntityType: kind,
		EntityID:   entityID,
		EntityKey:  key,
		Sha256:     sha,
		Ext:        ext,
		UpdatedAt:  time.Now().Unix(),
	}); err != nil {
		return err
	}
	if hadPrev && prev.Sha256 != sha {
		s.gc(ctx, prev.Sha256, prev.Ext)
	}
	return nil
}

// AssignByKey applies a cover that arrived from a peer, which names the entity
// by its stable key. With no backend id bound to that key yet the row is parked
// under the key, so it takes effect once the id turns up.
func (s *Service) AssignByKey(ctx context.Context, kind, key, sha, ext string) error {
	if !s.ready() {
		return errors.New("cover: no store")
	}
	if key == "" {
		return errors.New("cover: missing entity key")
	}
	entityID := key
	if row, err := s.q.GetEntityCoverByKey(ctx, db.GetEntityCoverByKeyParams{EntityType: kind, EntityKey: key}); err == nil && row.EntityID != "" {
		entityID = row.EntityID
	}
	if sha == "" {
		return s.ClearByKey(ctx, kind, key)
	}
	return s.Assign(ctx, kind, entityID, key, sha, ext)
}

// Clear drops an entity's uploaded cover, so the library's own art shows again.
func (s *Service) Clear(ctx context.Context, kind, entityID string) error {
	if !s.ready() {
		return errors.New("cover: no store")
	}
	prev, had := s.current(ctx, kind, entityID)
	if err := s.q.DeleteEntityCover(ctx, db.DeleteEntityCoverParams{EntityType: kind, EntityID: entityID}); err != nil {
		return err
	}
	if had {
		s.gc(ctx, prev.Sha256, prev.Ext)
	}
	return nil
}

// ClearByKey drops a cover named by its stable key, for changes from a peer.
func (s *Service) ClearByKey(ctx context.Context, kind, key string) error {
	if !s.ready() {
		return errors.New("cover: no store")
	}
	row, err := s.q.GetEntityCoverByKey(ctx, db.GetEntityCoverByKeyParams{EntityType: kind, EntityKey: key})
	if err == nil && row.EntityID != "" {
		return s.Clear(ctx, kind, row.EntityID)
	}
	return s.q.DeleteEntityCoverByKey(ctx, db.DeleteEntityCoverByKeyParams{EntityType: kind, EntityKey: key})
}

func (s *Service) current(ctx context.Context, kind, entityID string) (db.EntityCover, bool) {
	row, err := s.q.GetEntityCover(ctx, db.GetEntityCoverParams{EntityType: kind, EntityID: entityID})
	if err != nil {
		return db.EntityCover{}, false
	}
	return row, true
}

// gc removes a blob once nothing points at it. A failure here costs disk, not
// correctness, so it is not reported.
func (s *Service) gc(ctx context.Context, sha, ext string) {
	if sha == "" {
		return
	}
	n, err := s.q.CountEntityCoverRefs(ctx, sha)
	if err != nil || n > 0 {
		return
	}
	_ = os.Remove(s.blobPath(sha, ext))
}

// Get returns the cover art id for one entity, or "" when it has no upload.
func (s *Service) Get(ctx context.Context, kind, entityID string) string {
	if !s.ready() {
		return ""
	}
	row, ok := s.current(ctx, kind, entityID)
	if !ok {
		return ""
	}
	return Prefix + row.Sha256 + "." + row.Ext
}

// Open resolves a cover art id back to the bytes it names. ok is false for an
// id that is not an uploaded cover, or whose blob this device does not hold yet
// — a cover assigned by a peer whose image has not arrived. Callers fall back
// to the library backend in both cases.
func (s *Service) Open(id string) (data []byte, contentType string, ok bool) {
	if !s.ready() {
		return nil, "", false
	}
	sha, ext, valid := parseID(id)
	if !valid {
		return nil, "", false
	}
	b, err := os.ReadFile(s.blobPath(sha, ext))
	if err != nil {
		return nil, "", false
	}
	return b, ContentTypeForExt(ext), true
}

// parseID splits a "custom:<sha256>.<ext>" id, rejecting anything whose parts
// are not exactly what Store produces. This is the only place an id from a URL
// becomes a file path, so the character checks are the path-traversal defence.
func parseID(id string) (sha, ext string, ok bool) {
	rest, found := strings.CutPrefix(id, Prefix)
	if !found {
		return "", "", false
	}
	sha, ext, found = strings.Cut(rest, ".")
	if !found || len(sha) != 64 {
		return "", "", false
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return "", "", false
	}
	switch ext {
	case "jpg", "png", "webp":
		return sha, ext, true
	}
	return "", "", false
}

// index loads every uploaded cover, keyed by backend id and by stable key.
func (s *Service) index(ctx context.Context) map[string]string {
	if !s.ready() {
		return nil
	}
	rows, err := s.q.ListEntityCovers(ctx)
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := make(map[string]string, len(rows)*2)
	for _, r := range rows {
		id := Prefix + r.Sha256 + "." + r.Ext
		out[r.EntityType+":"+r.EntityID] = id
		if r.EntityKey != "" {
			out[r.EntityType+":k:"+r.EntityKey] = id
		}
	}
	return out
}

// catalogKeys resolves the stable key for each track in one batch, or nil when
// no resolver is installed or no track carries an uploaded cover.
func (s *Service) catalogKeys(ctx context.Context, tracks []core.Track) map[string]string {
	if s == nil || s.catalogIDs == nil || len(tracks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tracks))
	for i := range tracks {
		ids = append(ids, tracks[i].ID)
	}
	return s.catalogIDs(ctx, ids)
}

func lookup(idx map[string]string, kind, id, key string) string {
	if idx == nil {
		return ""
	}
	if v := idx[kind+":"+id]; v != "" {
		return v
	}
	if key != "" {
		return idx[kind+":k:"+key]
	}
	return ""
}

// ApplyAlbums swaps in uploaded art in place, cascading into nested tracks.
func (s *Service) ApplyAlbums(ctx context.Context, albums []core.Album) {
	idx := s.index(ctx)
	if len(albums) == 0 || idx == nil {
		return
	}
	s.applyAlbums(ctx, albums, idx)
}

// ApplyArtists reaches the albums and tracks under each artist. Artists carry
// no uploaded art of their own.
func (s *Service) ApplyArtists(ctx context.Context, artists []core.Artist) {
	if len(artists) == 0 {
		return
	}
	idx := s.index(ctx)
	if idx == nil {
		return
	}
	for i := range artists {
		s.applyAlbums(ctx, artists[i].Albums, idx)
	}
}

// ApplyTracks swaps in uploaded art in place. A track's own upload wins over
// the album's, which is what uploading art for one track of an album means.
func (s *Service) ApplyTracks(ctx context.Context, tracks []core.Track) {
	idx := s.index(ctx)
	if len(tracks) == 0 || idx == nil {
		return
	}
	s.applyTracks(ctx, tracks, idx)
}

func (s *Service) applyAlbums(ctx context.Context, albums []core.Album, idx map[string]string) {
	for i := range albums {
		if id := lookup(idx, KindAlbum, albums[i].ID, override.AlbumKey(albums[i].Artist, albums[i].Name)); id != "" {
			albums[i].CoverArtID = id
		}
		s.applyTracks(ctx, albums[i].Tracks, idx)
	}
}

func (s *Service) applyTracks(ctx context.Context, tracks []core.Track, idx map[string]string) {
	catalog := s.catalogKeys(ctx, tracks)
	for i := range tracks {
		if id := lookup(idx, KindTrack, tracks[i].ID, catalog[tracks[i].ID]); id != "" {
			tracks[i].CoverArtID = id
			continue
		}
		if id := lookup(idx, KindAlbum, tracks[i].AlbumID, override.AlbumKey(tracks[i].Artist, tracks[i].Album)); id != "" {
			tracks[i].CoverArtID = id
		}
	}
}
