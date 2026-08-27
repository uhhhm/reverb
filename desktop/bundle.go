package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// ResolveBundledTools checks executable-relative Resources/bin and
// desktop/tools/bin, then PATH. Returns "" for any tool not found.
// Env overrides are assumed to have been handled by the caller via
// os.Getenv before invoking this helper.
func ResolveBundledTools() (ffmpeg, navidrome, spotdl, deno string) {
	return findBundledTool("ffmpeg"),
		findBundledTool("navidrome"),
		findBundledTool("spotdl"),
		findBundledTool("deno")
}

func findBundledTool(name string) string {
	var candidates []string

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "../Resources/bin", name),
			filepath.Join(dir, "Resources/bin", name),
			filepath.Join(dir, "bin", name),
		)
		if name == "spotdl" {
			candidates = append(candidates,
				filepath.Join(dir, "../Resources/python/bin", name),
				filepath.Join(dir, "python/bin", name),
			)
		}
	}

	candidates = append(candidates,
		filepath.Join(filepath.Dir(os.Args[0]), "../Resources/bin", name),
		filepath.Join(filepath.Dir(os.Args[0]), "Resources/bin", name),
	)

	if name == "spotdl" {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(os.Args[0]), "../Resources/python/bin", name),
		)
	}

	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "desktop/tools/bin", name),
			filepath.Join(wd, "../desktop/tools/bin", name),
			filepath.Join(wd, "../../desktop/tools/bin", name),
			filepath.Join(wd, "./desktop/tools/bin", name),
		)
		if name == "spotdl" {
			candidates = append(candidates,
				filepath.Join(wd, "desktop/tools/python/bin", name),
				filepath.Join(wd, "../desktop/tools/python/bin", name),
				filepath.Join(wd, "../../desktop/tools/python/bin", name),
			)
		}
	}

	candidates = append(candidates,
		filepath.Join("desktop/tools/bin", name),
		filepath.Join("./desktop/tools/bin", name),
	)
	if name == "spotdl" {
		candidates = append(candidates,
			filepath.Join("desktop/tools/python/bin", name),
			filepath.Join("./desktop/tools/python/bin", name),
		)
	}

	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			c = abs
		}
		c = filepath.Clean(c)
		if isExecutable(c) {
			return c
		}
	}

	if p, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return p
	}

	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	if info.Mode()&0111 == 0 {
		return false
	}
	return true
}
