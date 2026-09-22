package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisableDMABufOnNVIDIA(t *testing.T) {
	loaded := t.TempDir()
	absent := filepath.Join(t.TempDir(), "nvidia")
	for _, tc := range []struct {
		name, module string
		preset       *string
		want         string
	}{
		{"nvidia driver loaded", loaded, nil, "1"},
		{"no nvidia driver", absent, nil, ""},
		{"user choice wins", loaded, ptr("0"), "0"},
		{"set but empty is a user choice", loaded, ptr(""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(webkitDMABufEnv, "")
			if tc.preset == nil {
				os.Unsetenv(webkitDMABufEnv)
			} else {
				t.Setenv(webkitDMABufEnv, *tc.preset)
			}
			disableDMABufOnNVIDIA(tc.module)
			if got := os.Getenv(webkitDMABufEnv); got != tc.want {
				t.Fatalf("%s = %q, want %q", webkitDMABufEnv, got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
