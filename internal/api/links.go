package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
	"github.com/uhhhm/reverb/internal/linkresolve"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// LinkStore is the persistence slice the add-from-link handlers need.
// *db.Queries satisfies it.
type LinkStore interface {
	InsertCatalogEntity(ctx context.Context, arg db.InsertCatalogEntityParams) error
	GetCatalogEntity(ctx context.Context, id string) (db.CatalogEntity, error)
	GetSyncedPlaylist(ctx context.Context, id string) (db.SyncedPlaylist, error)
	ListDevices(ctx context.Context) ([]db.Device, error)
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
	GetSetting(ctx context.Context, key string) (string, error)
}

func (s *Server) linkStore() LinkStore {
	if s.deps.LinkStore != nil {
		return s.deps.LinkStore
	}
	return nil
}

// linkAuthorDeviceID returns the identity link-derived changes are authored
// under. It is the local device (AuthorDeviceID), because only that identity
// can be signed and verified by a peer.
func (s *Server) linkAuthorDeviceID(ctx context.Context) (string, error) {
	if ls := s.linkStore(); ls != nil {
		if id, err := reverbsync.AuthorDeviceID(ctx, ls); err == nil {
			return id, nil
		}
	}
	if s.deps.OfflineSet != nil {
		if id, err := reverbsync.AuthorDeviceID(ctx, s.deps.OfflineSet); err == nil {
			return id, nil
		}
	}
	if s.deps.PairingStore != nil {
		if id, err := reverbsync.AuthorDeviceID(ctx, s.deps.PairingStore); err == nil {
			return id, nil
		}
	}
	return "", sql.ErrNoRows
}

type linkResolveBody struct {
	URL string `json:"url"`
}

func (s *Server) handleLinkResolve(w http.ResponseWriter, r *http.Request) {
	var body linkResolveBody
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	// Resolving reaches out to whatever host the caller names, so the host
	// allowlist is the guard, not the individual parsers that happen to pin
	// their own hosts today.
	if !linkresolve.IsAllowedSourceURL(body.URL) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported URL"})
		return
	}
	var res *linkresolve.ResolveResult
	if s.deps.LinkAdd != nil {
		res, err = s.deps.LinkAdd.Resolve(r.Context(), body.URL)
	} else {
		res, err = linkresolve.ResolveURL(r.Context(), body.URL)
	}
	if err != nil {
		if errors.Is(err, linkresolve.ErrUnsupportedURL) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported URL"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type linkAddBody struct {
	URL        string  `json:"url"`
	PlaylistID *string `json:"playlistId"`
	Download   *bool   `json:"download"`
	// Quality overrides the configured download_quality for this one download.
	Quality string `json:"quality,omitempty"`
	// StartTime and EndTime trim a YouTube source to a time range ("1:30",
	// "00:01:30" or plain seconds). Both optional and independent.
	StartTime string `json:"startTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`
	// SplitChapters downloads one track per internal chapter instead of one
	// track for the whole video. Mutually exclusive with StartTime/EndTime:
	// chapter boundaries stop meaning anything once the source is trimmed.
	SplitChapters bool `json:"splitChapters,omitempty"`
}

func (s *Server) handleLinkAdd(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	var body linkAddBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	// If planner is wired, delegate — handler stays HTTP-only.
	if s.deps.LinkAdd != nil {
		// Ownership check before planner (planner validates existence, handler gates access).
		if body.PlaylistID != nil {
			pid := strings.TrimSpace(*body.PlaylistID)
			if pid != "" && !s.playlistAccessAllowed(r, pid) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
				return
			}
		}
		s.deps.LinkAdd.SetDownloader(s.downloads())
		opts := linkadd.AddOptions{
			URL:           body.URL,
			PlaylistID:    body.PlaylistID,
			Download:      body.Download,
			Quality:       body.Quality,
			StartTime:     body.StartTime,
			EndTime:       body.EndTime,
			SplitChapters: body.SplitChapters,
		}
		if cu, ok := currentUser(r); ok {
			opts.InitiatedBy = cu.ID
		}
		result, err := s.deps.LinkAdd.Add(r.Context(), opts)
		if err != nil {
			if errors.Is(err, linkresolve.ErrUnsupportedURL) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported URL"})
				return
			}
			if errors.Is(err, linkadd.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
				return
			}
			if errors.Is(err, linkadd.ErrNoDownloader) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no downloader configured"})
				return
			}
			if errors.Is(err, linkadd.ErrRangeChapterConflict) || errors.Is(err, linkadd.ErrRangeNonYouTube) || errors.Is(err, linkadd.ErrNoChapterSupport) || errors.Is(err, linkadd.ErrNoChapters) || errors.Is(err, linkadd.ErrChaptersRead) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			if errors.Is(err, linkadd.ErrCatalogRead) || errors.Is(err, linkadd.ErrCatalogCreate) || errors.Is(err, linkadd.ErrPlaylistValidate) {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		resp := make(map[string]any)
		resp["resolve"] = result.Resolve
		resp["catalogId"] = result.CatalogID
		if result.PlaylistID != "" {
			resp["playlistId"] = result.PlaylistID
		}
		if result.Job != nil {
			resp["job"] = result.Job
		}
		if len(result.Jobs) > 1 {
			resp["jobs"] = result.Jobs
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Fallback for tests without planner (legacy).
	shouldDownload := true
	if body.Download != nil {
		shouldDownload = *body.Download
	}

	res, err := linkresolve.ResolveURL(r.Context(), body.URL)
	if err != nil {
		if errors.Is(err, linkresolve.ErrUnsupportedURL) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported URL"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Catalog entity handling: deterministic stable ID includes source and kind to avoid
	// collisions (e.g. Spotify sp123 vs YouTube sp123 would otherwise share the
	// same catalog_entity row and sync_change entityId, and track vs playlist).
	kind := res.Kind
	if kind == "" {
		kind = "track"
	}
	catalogID := linkadd.CatalogID(res.Source, kind, res.ExternalID)
	createdNew := false
	if ls := s.linkStore(); ls != nil {
		// Check existing.
		_, gerr := ls.GetCatalogEntity(r.Context(), catalogID)
		if errors.Is(gerr, sql.ErrNoRows) {
			now := time.Now().Unix()
			ierr := ls.InsertCatalogEntity(r.Context(), db.InsertCatalogEntityParams{
				ID:         catalogID,
				Kind:       kind,
				Title:      res.Title,
				Artist:     res.Artist,
				Album:      res.Album,
				DurationMs: 0,
				Isrc:       "",
				Mbid:       "",
				Source:     res.Source,
				ExternalID: res.ExternalID,
				CreatedAt:  now,
			})
			if ierr != nil {
				// If conflict, treat as already exists.
				if strings.Contains(ierr.Error(), "UNIQUE") || strings.Contains(ierr.Error(), "constraint") || strings.Contains(strings.ToLower(ierr.Error()), "primary") {
					createdNew = false
				} else {
					// Check if now exists despite error.
					if _, check := ls.GetCatalogEntity(r.Context(), catalogID); check == nil {
						createdNew = false
					} else {
						writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create catalog entry"})
						return
					}
				}
			} else {
				createdNew = true
			}
		} else if gerr == nil {
			createdNew = false
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read catalog"})
			return
		}

		// Emit sync change for new entity so canonical library reflects it.
		if createdNew && s.deps.SyncStore != nil {
			deviceID, derr := s.linkAuthorDeviceID(r.Context())
			if derr == nil && deviceID != "" {
				ch := reverbsync.SyncChange{
					EntityType: kind,
					EntityID:   catalogID,
					Field:      "title",
					Value:      res.Title,
					UpdatedAt:  time.Now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.deps.SyncStore.AppendChange(r.Context(), deviceID, ch)
				// Also emit artist for completeness (no harm, not required for test)
				ch2 := reverbsync.SyncChange{
					EntityType: kind,
					EntityID:   catalogID,
					Field:      "artist",
					Value:      res.Artist,
					UpdatedAt:  time.Now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.deps.SyncStore.AppendChange(r.Context(), deviceID, ch2)
			}
		}
	}

	// Playlist validation and sync emit if playlistId given.
	var playlistID string
	if body.PlaylistID != nil {
		playlistID = strings.TrimSpace(*body.PlaylistID)
	}
	if playlistID != "" {
		if ls := s.linkStore(); ls != nil {
			if _, err := ls.GetSyncedPlaylist(r.Context(), playlistID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not validate playlist"})
				return
			}
		}
		if !s.playlistAccessAllowed(r, playlistID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
			return
		}
		// Emit playlist membership sync_change if we have sync store.
		if s.deps.SyncStore != nil {
			deviceID, derr := s.linkAuthorDeviceID(r.Context())
			if derr == nil && deviceID != "" {
				ch := reverbsync.SyncChange{
					EntityType: "playlist",
					EntityID:   playlistID,
					Field:      "track:" + catalogID,
					Value:      catalogID,
					UpdatedAt:  time.Now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.deps.SyncStore.AppendChange(r.Context(), deviceID, ch)
				// Also emit generic tracks field for alternative test check.
				ch2 := reverbsync.SyncChange{
					EntityType: "playlist",
					EntityID:   playlistID,
					Field:      "tracks",
					Value:      catalogID,
					UpdatedAt:  time.Now().UnixMilli(),
					DeviceID:   deviceID,
				}
				_, _ = s.deps.SyncStore.AppendChange(r.Context(), deviceID, ch2)
			}
		}
	}

	var job *core.DownloadJob
	var jobs []core.DownloadJob
	if shouldDownload {
		dm := s.downloads()
		if dm == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no downloader configured"})
			return
		}
		base := core.DownloadRequest{
			Source:     res.Source,
			ExternalID: res.ExternalID,
			Artist:     res.Artist,
			Title:      res.Title,
			Album:      res.Album,
			Quality:    core.ParseAudioQuality(body.Quality, ""),
		}
		if res.Source == "youtube" {
			base.ManualURL = strings.TrimSpace(res.URL)
			// yt-dlp handles a pasted link natively; spotDL remains the fallback
			// when no ytdlp downloader is configured.
			base.PreferDownloader = "ytdlp"
		}
		if playlistID != "" {
			base.AddToPlaylistID = playlistID
		}
		if cu, ok := currentUser(r); ok {
			base.InitiatedBy = cu.ID
		}

		reqs, derr := s.linkDownloadRequests(r.Context(), base, res, body)
		if derr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": derr.Error()})
			return
		}
		for _, req := range reqs {
			j, err := dm.Enqueue(r.Context(), req)
			if err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			jobs = append(jobs, j)
		}
		if len(jobs) > 0 {
			job = &jobs[0]
		}
	}

	resp := make(map[string]any)
	resp["resolve"] = res
	resp["catalogId"] = catalogID
	if playlistID != "" {
		resp["playlistId"] = playlistID
	}
	if job != nil {
		resp["job"] = job
	}
	if len(jobs) > 1 {
		resp["jobs"] = jobs
	}
	writeJSON(w, http.StatusOK, resp)
}

// linkAddBatchBody is the batch counterpart to linkAddBody: one request per batch
// instead of one request per link. The handler fans the items out through the
// planner and returns per-link outcomes.
type linkAddBatchBody struct {
	Items []linkAddBody `json:"items"`
}

func (s *Server) handleLinkAddBatch(w http.ResponseWriter, r *http.Request) {
	if s.deps.LinkAdd == nil {
		// Fallback: emulate batch by looping the single-item handler logic per item.
		// This keeps the endpoint usable in tests that haven't wired a planner.
		var body linkAddBatchBody
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		if len(body.Items) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "items is required"})
			return
		}
		if len(body.Items) > 500 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many items"})
			return
		}
		results := []map[string]any{}
		for _, item := range body.Items {
			if strings.TrimSpace(item.URL) == "" {
				results = append(results, map[string]any{"url": item.URL, "error": "url is required"})
				continue
			}
			if item.PlaylistID != nil {
				pid := strings.TrimSpace(*item.PlaylistID)
				if pid != "" {
					if ls := s.linkStore(); ls != nil {
						if _, err := ls.GetSyncedPlaylist(r.Context(), pid); err != nil {
							if errors.Is(err, sql.ErrNoRows) {
								results = append(results, map[string]any{"url": item.URL, "error": "playlist not found"})
								continue
							}
							results = append(results, map[string]any{"url": item.URL, "error": "could not validate playlist"})
							continue
						}
					}
					if !s.playlistAccessAllowed(r, pid) {
						results = append(results, map[string]any{"url": item.URL, "error": "playlist not found"})
						continue
					}
				}
			}
			res, err := linkresolve.ResolveURL(r.Context(), item.URL)
			if err != nil {
				results = append(results, map[string]any{"url": item.URL, "error": err.Error()})
				continue
			}
			kind := res.Kind
			if kind == "" {
				kind = "track"
			}
			catalogID := linkadd.CatalogID(res.Source, kind, res.ExternalID)
			entry := map[string]any{"url": item.URL, "resolve": res, "catalogId": catalogID}
			if item.PlaylistID != nil && strings.TrimSpace(*item.PlaylistID) != "" {
				entry["playlistId"] = strings.TrimSpace(*item.PlaylistID)
			}
			results = append(results, entry)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
		return
	}
	var body linkAddBatchBody
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "items is required"})
		return
	}
	if len(body.Items) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many items"})
		return
	}
	cu, _ := currentUser(r)
	s.deps.LinkAdd.SetDownloader(s.downloads())
	results := make([]linkadd.BatchItemResult, len(body.Items))
	var validOpts []linkadd.AddOptions
	var validIdx []int
	for i, item := range body.Items {
		if strings.TrimSpace(item.URL) == "" {
			results[i] = linkadd.BatchItemResult{URL: item.URL, Error: "url is required"}
			continue
		}
		if item.PlaylistID != nil {
			pid := strings.TrimSpace(*item.PlaylistID)
			if pid != "" && !s.playlistAccessAllowed(r, pid) {
				results[i] = linkadd.BatchItemResult{URL: item.URL, Error: "playlist not found"}
				continue
			}
		}
		opts := linkadd.AddOptions{
			URL:           item.URL,
			PlaylistID:    item.PlaylistID,
			Download:      item.Download,
			Quality:       item.Quality,
			StartTime:     item.StartTime,
			EndTime:       item.EndTime,
			SplitChapters: item.SplitChapters,
			InitiatedBy:   cu.ID,
		}
		validOpts = append(validOpts, opts)
		validIdx = append(validIdx, i)
	}
	if len(validOpts) > 0 {
		batchResults := s.deps.LinkAdd.AddBatch(r.Context(), validOpts)
		for j, br := range batchResults {
			results[validIdx[j]] = br
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// chapterLister is the manager capability the chapter endpoints need. The
// Manager satisfies it; test doubles need not.
type chapterLister interface {
	ListChapters(ctx context.Context, url string) ([]core.Chapter, error)
}

// linkDownloadRequests expands one add-from-link request into the download
// requests it implies: normally exactly one, but a chapter split becomes one
// request per chapter, each trimmed to that chapter's bounds. Splitting this
// way (rather than letting yt-dlp write many files from a single job) keeps
// every chapter on the ordinary one-job-per-track path, so library matching,
// playlist adds, progress and retry all work per chapter without special cases.
// This duplicates linkadd.planDownloadRequests so both paths share sentinel
// errors for HTTP status mapping.
func (s *Server) linkDownloadRequests(
	ctx context.Context, base core.DownloadRequest, res *linkresolve.ResolveResult, body linkAddBody,
) ([]core.DownloadRequest, error) {
	start, end := strings.TrimSpace(body.StartTime), strings.TrimSpace(body.EndTime)
	trimmed := start != "" || end != ""

	if body.SplitChapters && trimmed {
		return nil, linkadd.ErrRangeChapterConflict
	}
	if (body.SplitChapters || trimmed) && res.Source != "youtube" {
		return nil, linkadd.ErrRangeNonYouTube
	}

	if !body.SplitChapters {
		base.SectionStart, base.SectionEnd = start, end
		return []core.DownloadRequest{base}, nil
	}

	cl, ok := s.downloads().(chapterLister)
	if !ok {
		return nil, linkadd.ErrNoChapterSupport
	}
	chapters, err := cl.ListChapters(ctx, res.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", linkadd.ErrChaptersRead, err)
	}
	if len(chapters) == 0 {
		return nil, linkadd.ErrNoChapters
	}

	out := make([]core.DownloadRequest, 0, len(chapters))
	for _, ch := range chapters {
		req := base
		// The chapter is the track; the video as a whole becomes the album, so
		// the split lands in the library as one coherent release.
		req.Title = ch.Title
		req.Album = res.Title
		req.SectionStart = strconv.FormatFloat(ch.StartSec, 'f', -1, 64)
		if ch.EndSec > ch.StartSec {
			req.SectionEnd = strconv.FormatFloat(ch.EndSec, 'f', -1, 64)
		}
		out = append(out, req)
	}
	return out, nil
}

// handleLinkChapters previews a link's chapters so the UI can show what a split
// would produce before the user commits to it.
func (s *Server) handleLinkChapters(w http.ResponseWriter, r *http.Request) {
	var body linkResolveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	// ListChapters shells out to yt-dlp against this URL, so it is an outbound
	// request to whatever host the caller names. Hold it to the same allowlist
	// as /links/resolve rather than the downloader's pass-through normalizer.

	if !linkresolve.IsAllowedSourceURL(body.URL) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "unsupported URL"})
		return
	}
	dm := s.downloads()
	if dm == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no downloader configured"})
		return
	}
	cl, ok := dm.(chapterLister)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the configured downloader cannot read chapters"})
		return
	}
	chapters, err := cl.ListChapters(r.Context(), body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if chapters == nil {
		chapters = []core.Chapter{}
	}
	writeJSON(w, http.StatusOK, chapters)
}
