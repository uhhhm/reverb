package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uhhhm/reverb/internal/store/db"
)

// aliasKindCatalog maps a catalog id to the entity it names. Every entity
// carries one for its own id, so an id minted on another device resolves here
// even though this device would have minted a different one — and keeps
// resolving after a merge, because merging repoints aliases.
const aliasKindCatalog = "catalog"

// Emitter publishes a newly minted catalog entity to the sync log. Catalog ids
// are random, so a peer cannot derive them: without this, every change keyed on
// a catalog id would name an entity the receiver has never heard of.
type Emitter interface {
	EmitCatalogEntity(ctx context.Context, catalogID string, id Identity)
}

// WithEmitter attaches the sync emitter. Nil-safe: a Service without one still
// mints ids, it just does not replicate them.
func (s *Service) WithEmitter(e Emitter) *Service {
	s.emitter = e
	return s
}

func (s *Service) emitEntity(ctx context.Context, cid string, id Identity) {
	if s.emitter != nil {
		s.emitter.EmitCatalogEntity(ctx, cid, id)
	}
}

// Resolve maps a catalog id — possibly one minted on another device — onto the
// id this device stores the entity under. An id it has never seen resolves to
// itself, so callers get a usable key either way.
func (s *Service) Resolve(ctx context.Context, catalogID string) string {
	if s == nil || catalogID == "" {
		return catalogID
	}
	cid, err := s.q.GetAliasCatalogID(ctx, db.GetAliasCatalogIDParams{
		AliasKind:  aliasKindCatalog,
		AliasValue: catalogID,
	})
	if err != nil || cid == "" {
		return catalogID
	}
	return cid
}

// Adopt records an entity a peer minted, and returns the id this device will
// store it under. That is not always the peer's id: if the same track already
// exists here under a locally minted id, the two are fused by the ordinary
// alias-collision merge and the local id wins. The peer's id keeps resolving
// afterwards through its catalog alias, so changes it sends still land.
//
// Adopt does not emit: the entity came from the log, and re-publishing it would
// echo back to the peer that sent it.
func (s *Service) Adopt(ctx context.Context, remoteID string, id Identity) (string, error) {
	if remoteID == "" {
		return "", errors.New("catalog: adopt needs an id")
	}
	now := s.now().Unix()
	aliases := aliasesFor(id)

	// Already known, under this id or one it was merged into.
	if local := s.Resolve(ctx, remoteID); local != remoteID {
		return s.attachAliases(ctx, local, id, aliases)
	}
	if _, err := s.q.GetCatalogEntity(ctx, remoteID); err == nil {
		return s.attachAliases(ctx, remoteID, id, aliases)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	// The id is new here. If the track itself is not, adopt onto the entity we
	// already have rather than minting a second one for the same track.
	winner := remoteID
	if cid, found, err := s.Lookup(ctx, id); err != nil {
		return "", err
	} else if found {
		winner = cid
	} else if err := s.q.InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
		ID:         remoteID,
		Kind:       id.Kind,
		Title:      id.Title,
		Artist:     id.Artist,
		Album:      id.Album,
		DurationMs: int64(id.DurationMs),
		Isrc:       id.ISRC,
		Mbid:       id.MBID,
		Source:     id.Source,
		ExternalID: id.ExternalID,
		CreatedAt:  now,
	}); err != nil {
		return "", err
	}

	if err := s.q.InsertCatalogAlias(ctx, db.InsertCatalogAliasParams{
		AliasKind:  aliasKindCatalog,
		AliasValue: remoteID,
		CatalogID:  winner,
		CreatedAt:  now,
	}); err != nil {
		return "", err
	}
	return s.attachAliases(ctx, winner, id, aliases)
}

// EntityIdentity reads back the identity stored under a catalog id, so an
// emitter can publish an entity it did not mint itself.
func (s *Service) EntityIdentity(ctx context.Context, catalogID string) (Identity, error) {
	e, err := s.q.GetCatalogEntity(ctx, catalogID)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Kind:       e.Kind,
		Title:      e.Title,
		Artist:     e.Artist,
		Album:      e.Album,
		ISRC:       e.Isrc,
		MBID:       e.Mbid,
		Source:     e.Source,
		ExternalID: e.ExternalID,
		DurationMs: int(e.DurationMs),
	}, nil
}
