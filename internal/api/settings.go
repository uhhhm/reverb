package api

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

const (
	keyAccentColor        = "accent_color"
	keyDynamicBackground  = "dynamic_background"
	keyLibraryBackendMode = "library_backend_mode"
	keyDownloadQuality    = "download_quality"
	keyTheme              = "theme"
	keyAudioNormalization = "audio_normalization"
	defaultAccentColor    = "#F0354B"
	defaultTheme          = "default-dark"
)

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type settingsDTO struct {
	AccentColor        string `json:"accentColor"`
	DynamicBackground  bool   `json:"dynamicBackground"`
	LibraryBackendMode string `json:"libraryBackendMode"`
	DownloadQuality    string `json:"downloadQuality"`
	Theme              string `json:"theme"`
	// AudioNormalization applies a per-track playback gain so tracks sound
	// level against each other. The files are never re-encoded.
	AudioNormalization bool `json:"audioNormalization"`
}

// allowedThemes mirrors THEMES in web/src/lib/themes.ts. Each id needs a
// matching [data-theme] block in web/src/index.css to have any effect.
var allowedThemes = map[string]bool{
	"default-dark":         true,
	"light":                true,
	"catppuccin-mocha":     true,
	"catppuccin-macchiato": true,
	"catppuccin-frappe":    true,
}

// themeChoices renders allowedThemes for the rejection message, so adding a
// theme cannot leave the error listing a stale set.
func themeChoices() string {
	ids := make([]string, 0, len(allowedThemes))
	for id := range allowedThemes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

func (s *Server) currentSettings(r *http.Request) settingsDTO {
	out := settingsDTO{
		AccentColor:       defaultAccentColor,
		DynamicBackground: true,
		DownloadQuality:   string(core.DefaultAudioQuality),
		Theme:             defaultTheme,
	}
	if s.deps.Adapters == nil {
		return out
	}
	if v, err := s.deps.Adapters.GetSetting(r.Context(), keyAccentColor); err == nil && v != "" {
		out.AccentColor = v
	}
	if v, err := s.deps.Adapters.GetSetting(r.Context(), keyDynamicBackground); err == nil {
		out.DynamicBackground = v != "false"
	}
	if v, err := s.deps.Adapters.GetSetting(r.Context(), keyLibraryBackendMode); err == nil && v != "" {
		out.LibraryBackendMode = v
	}
	if v, err := s.deps.Adapters.GetSetting(r.Context(), keyDownloadQuality); err == nil && v != "" {
		out.DownloadQuality = string(core.ParseAudioQuality(v, core.DefaultAudioQuality))
	}
	if v, err := s.deps.Adapters.GetSetting(r.Context(), keyAudioNormalization); err == nil {
		out.AudioNormalization = v == "true"
	}
	if v, err := s.deps.Adapters.GetSetting(r.Context(), keyTheme); err == nil && v != "" {
		if allowedThemes[v] {
			out.Theme = v
		}
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentSettings(r))
}

// putSettingsBody uses pointers so an omitted field is left unchanged.
type putSettingsBody struct {
	AccentColor        *string `json:"accentColor"`
	DynamicBackground  *bool   `json:"dynamicBackground"`
	LibraryBackendMode *string `json:"libraryBackendMode"`
	DownloadQuality    *string `json:"downloadQuality"`
	Theme              *string `json:"theme"`
	AudioNormalization *bool   `json:"audioNormalization"`
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	if s.deps.Adapters == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config store unavailable"})
		return
	}
	var body putSettingsBody
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if body.AccentColor != nil {
		if !hexColorRE.MatchString(*body.AccentColor) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "accentColor must be a valid hex color (e.g. #F0354B)"})
			return
		}
		if err := s.deps.Adapters.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: keyAccentColor, Value: *body.AccentColor}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save settings"})
			return
		}
	}
	if body.DynamicBackground != nil {
		v := "true"
		if !*body.DynamicBackground {
			v = "false"
		}
		if err := s.deps.Adapters.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: keyDynamicBackground, Value: v}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save settings"})
			return
		}
	}
	if body.LibraryBackendMode != nil {
		mode := *body.LibraryBackendMode
		if mode != "" && mode != "built-in" && mode != "external" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "libraryBackendMode must be empty, \"built-in\", or \"external\""})
			return
		}
		if err := s.deps.Adapters.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: keyLibraryBackendMode, Value: mode}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save settings"})
			return
		}
	}
	if body.DownloadQuality != nil {
		q := core.AudioQuality(strings.ToLower(strings.TrimSpace(*body.DownloadQuality)))
		if !q.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "downloadQuality must be one of: low, medium, high, best"})
			return
		}
		if err := s.deps.Adapters.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: keyDownloadQuality, Value: string(q)}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save settings"})
			return
		}
	}
	if body.AudioNormalization != nil {
		v := "false"
		if *body.AudioNormalization {
			v = "true"
		}
		if err := s.deps.Adapters.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: keyAudioNormalization, Value: v}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save settings"})
			return
		}
	}
	if body.Theme != nil {
		t := strings.TrimSpace(*body.Theme)
		if !allowedThemes[t] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "theme must be one of: " + themeChoices()})
			return
		}
		if err := s.deps.Adapters.UpsertSetting(r.Context(), db.UpsertSettingParams{Key: keyTheme, Value: t}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save settings"})
			return
		}
	}
	writeJSON(w, http.StatusOK, s.currentSettings(r))
}
