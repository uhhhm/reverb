package desktop

import (
	"io"
	"os"
	"path/filepath"
)

// ResolveDesktopDB returns SQLite path for desktop mode.
//
//	macOS: ~/Library/Application Support/Reverb/reverb.db
//	linux: ~/.config/reverb/reverb.db  (XDG via os.UserConfigDir)
//
// Falls back to "./data/reverb.db" if UserConfigDir errors.
//
// If REVERB_DB is set in the environment, its value is returned directly
// so that config.Load (flags > env > defaults) remains authoritative.
// On first launch, if legacy ./data/reverb.db exists and desktop path does
// not, caller may copy (helper provided).
func ResolveDesktopDB() string {
	if v := os.Getenv("REVERB_DB"); v != "" {
		return v
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "./data/reverb.db"
	}
	return filepath.Join(dir, "reverb", "reverb.db")
}

// ResolveDesktopDownloadDir returns ~/Music/Reverb, creating it if missing (mkdir 0755).
// If the home directory cannot be determined, it falls back to "./music" and
// attempts to create that directory.
func ResolveDesktopDownloadDir() string {
	home, err := os.UserHomeDir()
	var dir string
	if err != nil || home == "" {
		dir = "./music"
	} else {
		dir = filepath.Join(home, "Music", "Reverb")
	}
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// ResolveDesktopDataDir returns directory containing DB (Dir(ResolveDesktopDB())).
func ResolveDesktopDataDir() string {
	return filepath.Dir(ResolveDesktopDB())
}

// MaybeMigrateLegacyDB copies ./data/reverb.db -> desktop DB if desktop DB missing and legacy exists. No overwrite.
func MaybeMigrateLegacyDB() error {
	dest := ResolveDesktopDB()
	legacy := "./data/reverb.db"
	if dest == legacy {
		return nil
	}
	if _, err := os.Stat(dest); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(legacy); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	src, err := os.Open(legacy)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}
