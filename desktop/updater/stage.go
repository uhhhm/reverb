package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// StagedUpdate is the manifest describing a downloaded update waiting to be
// applied on the next restart. It lives at <dataDir>/updates/staged.json.
type StagedUpdate struct {
	Tag    string `json:"tag"`
	Notes  string `json:"notes"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

// StagingDir is the directory holding downloaded updates and the marker files
// the successor instance reads at boot.
func StagingDir(dataDir string) string { return filepath.Join(dataDir, "updates") }

func manifestPath(dataDir string) string { return filepath.Join(StagingDir(dataDir), "staged.json") }

// WriteStaged records su as the update to apply on the next restart.
func WriteStaged(dataDir string, su StagedUpdate) error {
	if err := os.MkdirAll(StagingDir(dataDir), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(su, "", "  ")
	if err != nil {
		return err
	}
	tmp := manifestPath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, manifestPath(dataDir))
}

// ReadStaged returns the staged update, or ok=false when nothing is staged.
// A manifest naming a missing file, a file whose digest no longer matches, or a
// build for another platform is reported as not staged: a half-written download
// must never be swapped over a working binary.
func ReadStaged(dataDir string) (StagedUpdate, bool) {
	b, err := os.ReadFile(manifestPath(dataDir))
	if err != nil {
		return StagedUpdate{}, false
	}
	var su StagedUpdate
	if err := json.Unmarshal(b, &su); err != nil {
		return StagedUpdate{}, false
	}
	if su.Tag == "" || su.File == "" {
		return StagedUpdate{}, false
	}
	if su.GOOS != "" && (su.GOOS != runtime.GOOS || su.GOARCH != runtime.GOARCH) {
		return StagedUpdate{}, false
	}
	sum, err := FileSHA256(su.File)
	if err != nil || sum != su.SHA256 {
		return StagedUpdate{}, false
	}
	return su, true
}

// ClearStaged removes the manifest and every downloaded payload beside it.
func ClearStaged(dataDir string) error {
	if su, ok := ReadStaged(dataDir); ok {
		_ = os.Remove(su.File)
	}
	err := os.Remove(manifestPath(dataDir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// FileSHA256 is the hex digest of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyExecutable rejects a payload that is not a plausible executable for
// this platform, so a truncated or HTML error-page download cannot be renamed
// over the running binary.
func verifyExecutable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() < 1<<20 {
		return fmt.Errorf("update payload is only %d bytes; refusing to install", fi.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "linux":
		if string(magic[:]) != "\x7fELF" {
			return fmt.Errorf("update payload is not an ELF binary")
		}
	case "darwin":
		// Mach-O 64-bit (LE/BE) or a universal ("fat") archive.
		be := uint32(magic[0])<<24 | uint32(magic[1])<<16 | uint32(magic[2])<<8 | uint32(magic[3])
		switch be {
		case 0xfeedfacf, 0xcffaedfe, 0xcafebabe, 0xbebafeca:
		default:
			return fmt.Errorf("update payload is not a Mach-O binary")
		}
	}
	return nil
}
