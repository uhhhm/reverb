package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
	"github.com/uhhhm/reverb/internal/linkresolve"
)

// linkAddUnavailable answers a link request on a server with no add-from-link
// service.
func linkAddUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "add from link unavailable"})
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
	if s.deps.LinkAdd == nil {
		linkAddUnavailable(w)
		return
	}
	res, err := s.deps.LinkAdd.Resolve(r.Context(), body.URL)
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
	if s.deps.LinkAdd == nil {
		linkAddUnavailable(w)
		return
	}
	// Ownership is checked here; the service validates existence.
	if body.PlaylistID != nil {
		pid := strings.TrimSpace(*body.PlaylistID)
		if pid != "" && !s.playlistAccessAllowed(r, pid) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "playlist not found"})
			return
		}
	}
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
		status, msg := linkAddError(err)
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	resp := map[string]any{"resolve": result.Resolve, "catalogId": result.CatalogID}
	if result.PlaylistID != "" {
		resp["playlistId"] = result.PlaylistID
	}
	if result.Job != nil {
		resp["job"] = result.Job
	}
	if len(result.Jobs) > 1 {
		resp["jobs"] = result.Jobs
	}
	if result.DownloadError != "" {
		resp["downloadError"] = result.DownloadError
	}
	writeJSON(w, http.StatusOK, resp)
}

// linkAddError maps an add-from-link failure to its HTTP status and message.
func linkAddError(err error) (int, string) {
	switch {
	case errors.Is(err, linkresolve.ErrUnsupportedURL):
		return http.StatusUnprocessableEntity, "unsupported URL"
	case errors.Is(err, linkadd.ErrNotFound):
		return http.StatusNotFound, "playlist not found"
	case errors.Is(err, linkadd.ErrNotEditable):
		return http.StatusConflict, err.Error()
	case errors.Is(err, linkadd.ErrNoDownloader):
		return http.StatusServiceUnavailable, "no downloader configured"
	case errors.Is(err, linkadd.ErrNoPlaylists):
		return http.StatusServiceUnavailable, err.Error()
	case errors.Is(err, linkadd.ErrRangeChapterConflict), errors.Is(err, linkadd.ErrRangeNonYouTube),
		errors.Is(err, linkadd.ErrNoChapterSupport), errors.Is(err, linkadd.ErrNoChapters),
		errors.Is(err, linkadd.ErrChaptersRead), errors.Is(err, linkadd.ErrCollectionNotListed):
		return http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, linkadd.ErrCatalogRead), errors.Is(err, linkadd.ErrCatalogCreate),
		errors.Is(err, linkadd.ErrPlaylistValidate):
		return http.StatusInternalServerError, err.Error()
	case errors.Is(err, linkadd.ErrSourceLookup):
		return http.StatusBadGateway, err.Error()
	}
	return http.StatusUnprocessableEntity, err.Error()
}

// linkAddBatchBody is the batch counterpart to linkAddBody: one request per batch
// instead of one request per link. The handler fans the items out through the
// planner and returns per-link outcomes.
type linkAddBatchBody struct {
	Items []linkAddBody `json:"items"`
}

func (s *Server) handleLinkAddBatch(w http.ResponseWriter, r *http.Request) {
	if s.deps.LinkAdd == nil {
		linkAddUnavailable(w)
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
