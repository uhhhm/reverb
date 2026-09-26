// Package ytdlpupdate installs authenticated pure-Python yt-dlp releases.
// The trust key belongs to Reverb's release pipeline, not the download host.
package ytdlpupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

const maxPackage = 32 << 20
const maxExpanded = 128 << 20

// Manifest is signed as exact JSON bytes with Ed25519. Sequence is a strictly
// increasing release number, independent of upstream's version spelling.
type Manifest struct {
	Format   int    `json:"format"`
	Sequence int64  `json:"sequence"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
}

type record struct {
	Manifest  []byte `json:"manifest"`
	Signature []byte `json:"signature"`
}
type state struct {
	Current   record `json:"current"`
	Previous  record `json:"previous"`
	HighWater int64  `json:"highWater"`
}

// Activate must finish an import/compatibility probe before returning success,
// and restore the previous Python imports itself on error. An empty path means
// restore the bundled package. It must wait for Python jobs to finish.
type Activate func(context.Context, string, string) error

type Updater struct {
	mu         sync.Mutex
	root       string
	key        ed25519.PublicKey
	activate   Activate
	state      state
	version    string
	activePath string
}

func New(root string, key ed25519.PublicKey, activate Activate) *Updater {
	return &Updater{root: root, key: append(ed25519.PublicKey(nil), key...), activate: activate}
}
func (u *Updater) Version() string { u.mu.Lock(); defer u.mu.Unlock(); return u.version }
func (u *Updater) verify(r record) (Manifest, error) {
	var m Manifest
	if len(u.key) != ed25519.PublicKeySize || !ed25519.Verify(u.key, r.Manifest, r.Signature) {
		return m, errors.New("yt-dlp update signature is invalid")
	}
	if err := json.Unmarshal(r.Manifest, &m); err != nil {
		return m, err
	}
	digest, err := hex.DecodeString(m.SHA256)
	if err != nil || len(digest) != sha256.Size || m.SHA256 != strings.ToLower(m.SHA256) || m.Format != 1 || m.Sequence <= 0 || m.Version == "" || len(m.Version) > 80 {
		return m, errors.New("unsupported yt-dlp manifest")
	}
	return m, nil
}
func (u *Updater) archivePath(m Manifest) string { return filepath.Join(u.root, m.SHA256+".zip") }

// Restore re-verifies stored bytes on every process launch. A corrupt or
// incompatible current package falls back to the previous signed package.
func (u *Updater) Restore(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(u.root, "current.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &u.state); err != nil {
		return err
	}
	var last error
	for _, r := range []record{u.state.Current, u.state.Previous} {
		if len(r.Manifest) == 0 {
			continue
		}
		m, err := u.verify(r)
		if err != nil {
			last = err
			continue
		}
		archive, err := readBoundedFile(u.archivePath(m), maxPackage)
		if err != nil {
			last = err
			continue
		}
		dir, err := u.prepare(m, archive)
		if err != nil {
			last = err
			continue
		}
		if err = u.activate(ctx, dir, m.Version); err != nil {
			_ = os.RemoveAll(dir)
			last = err
			continue
		}
		u.state.Current = r
		u.version = m.Version
		u.activePath = dir
		u.cleanup()
		return nil
	}
	return last
}

// Check downloads a signed manifest and content-addressed package, then probes
// and activates it. Network or activation failures leave the working package
// intact. The caller supplies a bounded HTTP client and its trusted feed URL.
func (u *Updater) Check(ctx context.Context, client *http.Client, base string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	defer u.cleanup()
	manifest, err := fetch(ctx, client, base+"/manifest.json", 16<<10)
	if err != nil {
		return err
	}
	sig, err := fetch(ctx, client, base+"/manifest.sig", ed25519.SignatureSize)
	if err != nil {
		return err
	}
	r := record{manifest, sig}
	m, err := u.verify(r)
	if err != nil {
		return err
	}
	if m.Sequence <= u.state.HighWater {
		return nil
	}
	archive, err := fetch(ctx, client, base+"/package.zip", maxPackage)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(u.root, 0700); err != nil {
		return err
	}
	dir, err := u.prepare(m, archive)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	if err = atomicWrite(u.archivePath(m), archive); err != nil {
		return err
	}
	next := state{Current: r, Previous: u.state.Current, HighWater: m.Sequence}
	data, _ := json.Marshal(next)
	// Prepare durable metadata before touching the running interpreter.
	pending, err := os.CreateTemp(u.root, "state-")
	if err != nil {
		return err
	}
	defer os.Remove(pending.Name())
	if _, err = pending.Write(data); err == nil {
		err = pending.Sync()
	}
	closeErr := pending.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	oldPath, oldVersion := u.activePath, u.version
	if err = u.activate(ctx, dir, m.Version); err != nil {
		return err
	}
	if err = os.Rename(pending.Name(), filepath.Join(u.root, "current.json")); err != nil {
		rollback := u.activate(context.Background(), oldPath, oldVersion)
		return errors.Join(err, rollback)
	}
	u.state = next
	u.version = m.Version
	u.activePath = dir
	keep = true
	if oldPath != "" {
		_ = os.RemoveAll(oldPath)
	}
	return nil
}
func fetch(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yt-dlp update: HTTP %d", resp.StatusCode)
	}
	return bounded(resp.Body, limit)
}
func bounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("yt-dlp package exceeds size limit")
	}
	return b, err
}
func readBoundedFile(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return bounded(f, limit)
}
func atomicWrite(p string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), "download-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}
func (u *Updater) prepare(m Manifest, b []byte) (string, error) {
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != m.SHA256 {
		return "", errors.New("yt-dlp package digest mismatch")
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(u.root, "package-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	var size uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || path.Clean(name) != name || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || seen[name] || f.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("unsafe yt-dlp archive path")
		}
		seen[name] = true
		// Wheels carry metadata too; only yt_dlp is executable from this package.
		if name != "yt_dlp" && !strings.HasPrefix(name, "yt_dlp/") {
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if ext == ".so" || ext == ".dylib" || ext == ".dll" || ext == ".pyc" || ext == ".pyd" {
			return "", errors.New("yt-dlp update must contain Python source only")
		}
		if f.UncompressedSize64 > maxExpanded-size || len(seen) > 20000 {
			return "", errors.New("expanded yt-dlp package exceeds limit")
		}
		size += f.UncompressedSize64
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return "", err
		}
		src, err := f.Open()
		if err != nil {
			return "", err
		}
		data, err := bounded(src, int64(f.UncompressedSize64))
		_ = src.Close()
		if err != nil {
			return "", err
		}
		if err = os.WriteFile(dest, data, 0600); err != nil {
			return "", err
		}
	}
	if !seen["yt_dlp/__init__.py"] {
		return "", errors.New("yt-dlp package is missing")
	}
	ok = true
	return dir, nil
}

// Keep only two signed archives and the currently loaded source directory.
// Interrupted downloads and extraction directories are safe to discard.
func (u *Updater) cleanup() {
	keep := map[string]bool{filepath.Base(u.activePath): true, "current.json": true}
	for _, r := range []record{u.state.Current, u.state.Previous} {
		if m, err := u.verify(r); err == nil {
			keep[filepath.Base(u.archivePath(m))] = true
		}
	}
	entries, _ := os.ReadDir(u.root)
	for _, entry := range entries {
		name := entry.Name()
		if !keep[name] && (strings.HasPrefix(name, "package-") || strings.HasPrefix(name, "download-") || strings.HasPrefix(name, "state-") || strings.HasSuffix(name, ".zip")) {
			_ = os.RemoveAll(filepath.Join(u.root, name))
		}
	}
}
