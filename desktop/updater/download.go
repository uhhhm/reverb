package updater

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// progressWriter reports cumulative bytes written to onProgress.
type progressWriter struct {
	total      int64
	written    int64
	onProgress func(fraction float64)
	lastPct    int
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.onProgress != nil && p.total > 0 {
		pct := int(float64(p.written) * 100 / float64(p.total))
		if pct != p.lastPct {
			p.lastPct = pct
			p.onProgress(float64(p.written) / float64(p.total))
		}
	}
	return n, nil
}

// DownloadAsset fetches a into destDir and returns the path of the executable
// it carries: the file itself for a bare binary or AppImage, or the single
// entry unpacked from a .zip. onProgress, when non-nil, is called with a 0..1
// fraction as the body arrives.
//
// A .deb is rejected — installing one needs root, which the app does not have
// and should not ask for.
func DownloadAsset(ctx context.Context, a Asset, destDir string, onProgress func(float64)) (string, error) {
	if strings.HasSuffix(strings.ToLower(a.Name), ".deb") {
		return "", fmt.Errorf("%s must be installed with the system package manager", a.Name)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "reverb-updater")
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %d", a.Name, resp.StatusCode)
	}

	// The asset name comes from the release feed, so it is untrusted input on a
	// path: take only its final element, or a name like "../../x.zip" would
	// write outside the staging dir.
	name := filepath.Base(a.Name)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return "", fmt.Errorf("refusing asset with unusable name %q", a.Name)
	}
	raw := filepath.Join(destDir, name)
	tmp := raw + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	pw := &progressWriter{total: resp.ContentLength, onProgress: onProgress}
	_, copyErr := io.Copy(io.MultiWriter(f, pw), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if err := os.Rename(tmp, raw); err != nil {
		return "", err
	}

	if !strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
		if err := os.Chmod(raw, 0o755); err != nil {
			return "", err
		}
		return raw, nil
	}
	bin, err := unzipSingle(raw, destDir)
	// The archive has served its purpose either way; the payload is what we keep.
	_ = os.Remove(raw)
	if err != nil {
		return "", err
	}
	return bin, nil
}

// unzipSingle extracts the largest regular file from the archive at zipPath —
// the release zips hold exactly one executable — and returns its path.
func unzipSingle(zipPath, destDir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	var best *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Reject traversal: entries are flat by construction, and anything
		// naming a parent directory is not an archive we produced.
		if strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) {
			return "", fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		if best == nil || f.UncompressedSize64 > best.UncompressedSize64 {
			best = f
		}
	}
	if best == nil {
		return "", fmt.Errorf("archive %s is empty", filepath.Base(zipPath))
	}
	rc, err := best.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	out := filepath.Join(destDir, filepath.Base(best.Name))
	dst, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, rc); err != nil {
		dst.Close()
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	return out, nil
}
