// Command verify-windows-artifact checks what the Windows desktop build has to
// be before it is published, from the bytes rather than from the toolchain's
// word for it. It runs in CI on the Windows runner and locally against a
// cross-compiled binary.
package main

import (
	"archive/zip"
	"debug/pe"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Resource type ids from winuser.h. An exe carrying an application icon has
// both: the images themselves, and the directory the shell picks one from.
const (
	rtIcon      = 3
	rtGroupIcon = 14
)

// subsystemGUI is IMAGE_SUBSYSTEM_WINDOWS_GUI. A console-subsystem binary opens
// a terminal behind the window on every launch.
const subsystemGUI = 2

func main() {
	exePath := flag.String("exe", "", "reverb-desktop.exe to check")
	zipPath := flag.String("zip", "", "release zip to check")
	bundlePath := flag.String("bundle", "", "first-install bundle zip to check")
	flag.Parse()
	if *exePath == "" && *zipPath == "" && *bundlePath == "" {
		log.Fatal("give -exe, -zip, -bundle, or a combination")
	}
	if *exePath != "" {
		if err := checkExe(*exePath); err != nil {
			log.Fatalf("%s: %v", *exePath, err)
		}
		fmt.Printf("%s: GUI subsystem, icon resource present\n", *exePath)
	}
	if *zipPath != "" {
		if err := checkZip(*zipPath); err != nil {
			log.Fatalf("%s: %v", *zipPath, err)
		}
		fmt.Printf("%s: holds reverb-desktop.exe and decompresses clean\n", *zipPath)
	}
	if *bundlePath != "" {
		if err := checkBundle(*bundlePath); err != nil {
			log.Fatalf("%s: %v", *bundlePath, err)
		}
		fmt.Printf("%s: portable Reverb folder is complete and the app carries its icon\n", *bundlePath)
	}
}

func checkBundle(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	want := map[string]bool{
		"Reverb/reverb-desktop.exe":     false,
		"Reverb/bin/ffmpeg.exe":         false,
		"Reverb/bin/navidrome.exe":      false,
		"Reverb/bin/deno.exe":           false,
		"Reverb/bin/spotdl.exe":         false,
		"Reverb/bin/yt-dlp.exe":         false,
		"Reverb/python/python.exe":      false,
		"Reverb/install-shortcut.ps1":   false,
		"Reverb/uninstall-shortcut.ps1": false,
	}
	var exe *zip.File
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if _, ok := want[name]; ok {
			want[name] = true
		}
		if name == "Reverb/reverb-desktop.exe" {
			exe = f
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, rc)
		closeErr := rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	var missing []string
	for name, found := range want {
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required entries: %s", strings.Join(missing, ", "))
	}
	tmp, err := os.CreateTemp("", "reverb-desktop-*.exe")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	rc, err := exe.Open()
	if err != nil {
		_ = tmp.Close()
		return err
	}
	_, copyErr := io.Copy(tmp, rc)
	closeReadErr := rc.Close()
	closeWriteErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeReadErr != nil {
		return closeReadErr
	}
	if closeWriteErr != nil {
		return closeWriteErr
	}
	return checkExe(tmpPath)
}

func checkExe(path string) error {
	f, err := pe.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var subsystem uint16
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		subsystem = oh.Subsystem
	case *pe.OptionalHeader32:
		subsystem = oh.Subsystem
	default:
		return fmt.Errorf("no optional header")
	}
	if subsystem != subsystemGUI {
		return fmt.Errorf("PE subsystem %d, want %d (GUI)", subsystem, subsystemGUI)
	}

	types, err := resourceTypes(f)
	if err != nil {
		return err
	}
	for _, want := range []uint32{rtIcon, rtGroupIcon} {
		if !types[want] {
			return fmt.Errorf("no resource type %d: the icon in desktop/rsrc_windows_amd64.syso was not linked in", want)
		}
	}
	return nil
}

// resourceTypes reads the ids in the root of the resource directory, which is
// the level .rsrc is keyed by type.
func resourceTypes(f *pe.File) (map[uint32]bool, error) {
	sec := f.Section(".rsrc")
	if sec == nil {
		return nil, fmt.Errorf("no .rsrc section")
	}
	data, err := sec.Data()
	if err != nil {
		return nil, err
	}
	// IMAGE_RESOURCE_DIRECTORY: 12 bytes of header, then the two entry counts.
	if len(data) < 16 {
		return nil, fmt.Errorf(".rsrc is %d bytes, too short for a directory header", len(data))
	}
	named := binary.LittleEndian.Uint16(data[12:])
	ids := binary.LittleEndian.Uint16(data[14:])
	types := make(map[uint32]bool)
	for i := range int(named) + int(ids) {
		off := 16 + i*8
		if off+8 > len(data) {
			return nil, fmt.Errorf(".rsrc directory entry %d runs past the section", i)
		}
		name := binary.LittleEndian.Uint32(data[off:])
		// The high bit marks a string name rather than an id; only ids matter.
		if name&0x80000000 == 0 {
			types[name] = true
		}
	}
	return types, nil
}

// checkZip reads every entry to the end, which is what makes archive/zip
// verify the CRC: listing the central directory alone would accept a corrupt
// payload.
func checkZip(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) != 1 || zr.File[0].Name != "reverb-desktop.exe" {
		names := make([]string, len(zr.File))
		for i, f := range zr.File {
			names[i] = f.Name
		}
		return fmt.Errorf("entries %v, want exactly [reverb-desktop.exe]", names)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}
