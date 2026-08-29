package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Upload limits. A single lossless track can be large, so the per-file cap is
// generous; the request cap bounds a whole multi-file upload.
const (
	maxUploadFileBytes    = 500 << 20  // 500 MB per file
	maxUploadRequestBytes = 2048 << 20 // 2 GB per request
)

// uploadDirName is the folder inside the managed music directory that uploaded
// files land in. Keeping them together makes them identifiable later, and
// Navidrome scans the music directory recursively so nesting costs nothing.
const uploadDirName = "Uploads"

// uploadExtensions is the accepted set. Anything else is refused outright
// rather than written and left for the scanner to ignore.
var uploadExtensions = map[string]bool{
	".mp3":  true,
	".flac": true,
	".m4a":  true,
	".aac":  true,
	".ogg":  true,
	".wav":  true,
}

var errUploadUnavailable = errors.New("uploads need the built-in library")

// uploadTarget reports the directory uploads go into, or an error explaining
// why uploading is unavailable. It requires the built-in library: in external
// mode the music directory belongs to somebody else's Navidrome, so Reverb has
// nowhere it may write and no way to make the file appear in the library.
func (s *Server) uploadTarget() (string, error) {
	if s.deps.MusicDir == "" {
		return "", errUploadUnavailable
	}
	if s.deps.LibraryStatus != nil {
		if mode, _ := s.deps.LibraryStatus(); mode != "built-in" {
			return "", errUploadUnavailable
		}
	}
	return filepath.Join(s.deps.MusicDir, uploadDirName), nil
}

type uploadedFile struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

type uploadResponse struct {
	Uploaded []uploadedFile    `json:"uploaded"`
	Rejected map[string]string `json:"rejected,omitempty"`
	Scanning bool              `json:"scanning"`
}

// handleUploadTracks accepts audio files and drops them into the managed music
// directory, then asks for a rescan through the download manager's existing
// debounced window — the same path a completed download uses, so there is only
// one rescan mechanism.
func (s *Server) handleUploadTracks(w http.ResponseWriter, r *http.Request) {
	dir, err := s.uploadTarget()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form"})
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "files field is required"})
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create the upload folder"})
		return
	}

	resp := uploadResponse{Uploaded: []uploadedFile{}, Rejected: map[string]string{}}
	for _, h := range headers {
		name := safeUploadName(h.Filename)
		if name == "" {
			resp.Rejected[h.Filename] = "unusable file name"
			continue
		}
		if !uploadExtensions[strings.ToLower(filepath.Ext(name))] {
			resp.Rejected[h.Filename] = "unsupported format; use mp3, flac, m4a, aac, ogg or wav"
			continue
		}
		if h.Size > maxUploadFileBytes {
			resp.Rejected[h.Filename] = "file is larger than 500 MB"
			continue
		}
		written, err := saveUpload(func() (multipartFile, error) { return h.Open() }, dir, name)
		if err != nil {
			resp.Rejected[h.Filename] = err.Error()
			continue
		}
		resp.Uploaded = append(resp.Uploaded, uploadedFile{Name: filepath.Base(written.name), Bytes: written.size})
	}

	// Only disturb the library if something actually landed.
	if len(resp.Uploaded) > 0 {
		if scanner, ok := s.downloads().(interface{ ScheduleScan() }); ok && scanner != nil {
			scanner.ScheduleScan()
			resp.Scanning = true
		}
	}
	status := http.StatusOK
	if len(resp.Uploaded) == 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, resp)
}

type savedUpload struct {
	name string
	size int64
}

// saveUpload streams one file to disk, never overwriting an existing track:
// a name collision gets a " (2)" suffix rather than silently replacing whatever
// is already in the library.
func saveUpload(open func() (multipartFile, error), dir, name string) (savedUpload, error) {
	src, err := open()
	if err != nil {
		return savedUpload{}, errors.New("could not read the file")
	}
	defer src.Close()

	dst, path, err := createUnique(dir, name)
	if err != nil {
		return savedUpload{}, errors.New("could not create the file")
	}
	n, copyErr := io.Copy(dst, io.LimitReader(src, maxUploadFileBytes+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return savedUpload{}, errors.New("could not write the file")
	}
	if n > maxUploadFileBytes {
		_ = os.Remove(path)
		return savedUpload{}, errors.New("file is larger than 500 MB")
	}
	return savedUpload{name: path, size: n}, nil
}

// multipartFile is the subset of multipart.File saveUpload needs.
type multipartFile interface {
	io.Reader
	io.Closer
}

func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		path := filepath.Join(dir, candidate)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, path, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("too many files with that name")
}

// safeUploadName reduces a client-supplied filename to a plain base name. The
// name arrives from the browser and must never be able to steer the write out
// of the upload directory, so anything path-shaped is stripped, not escaped.
func safeUploadName(raw string) string {
	// Windows clients send backslash-separated paths; treat both separators.
	raw = strings.ReplaceAll(raw, "\\", "/")
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "." || name == ".." || name == "/" || name == "" {
		return ""
	}
	// Strip anything that is not a safe filename character. This also removes
	// any residual separator or NUL.
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 32, r == 127:
			return -1
		case strings.ContainsRune(`/\:*?"<>|`, r):
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if len(name) > 200 {
		ext := filepath.Ext(name)
		name = name[:200-len(ext)] + ext
	}
	return name
}
