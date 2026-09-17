// Package portablename mints file and directory names that every platform in a
// household can store.
//
// A name is minted once, on whichever device runs the download, and then
// travels to peers that may be any platform. Sanitisation is therefore
// unconditional rather than gated on the running platform: a name minted on
// Linux has to be storable on a Windows peer, and a Linux device that only
// sanitised for its own filesystem would keep minting names its Windows peer
// can never write.
//
// Windows is the narrowest of the three, so its rules define portable:
//
//   - the characters < > : " / \ | ? * and the C0 control characters are illegal
//   - a name may not end in a dot or a space
//   - the DOS device names (CON, NUL, COM1, ...) are reserved, with or without
//     an extension
//
// Every illegal character is replaced rather than deleted, so an artist or
// title stays recognisable and two titles that differ only in punctuation stay
// different. The replacement alphabet is smaller than the input alphabet, so
// distinct inputs can still meet on one name; that residual case is resolved at
// write time by Unique, not here.
//
// LocallyStorable is the deliberate exception, and the only platform-dependent
// thing here: it asks whether *this* device can write a name some other device
// already minted, which is a different question from what a household should
// mint.
package portablename

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// replacements maps each Windows-illegal character to a visible stand-in.
// Deleting a character would collapse "Where?" and "Where" onto one name and
// read as a typo to the owner; substituting keeps both the distinction and the
// shape of the original.
var replacements = map[rune]rune{
	'<':  '(',
	'>':  ')',
	':':  '-',
	'"':  '\'',
	'/':  '-',
	'\\': '-',
	'|':  '-',
	'?':  '_',
	'*':  '+',
}

// reserved is the set of DOS device names Windows refuses as a file's stem,
// whatever extension follows it. COM0 and LPT0 are included: they are not
// reserved by every Windows API but are rejected by enough of them (os.Root
// among them) that minting one is not worth the risk.
var reserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM0": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT0": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// Segment makes one path component storable everywhere — an artist, a title, a
// directory name, or a complete file name. An extension needs no special
// handling: Windows applies its trailing-character and device-name rules to the
// component as a whole, so "CON.mp3" is as reserved as "CON" and "Etc..flac" is
// as legal as "Etc.flac".
//
// The result is never empty. A component that sanitises away entirely becomes
// "_", because a caller joining an empty component would silently produce a
// different path rather than a sanitised one.
func Segment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			// Control characters are illegal in a Windows name and invisible in
			// every UI that would show the result.
			b.WriteRune('_')
		default:
			if sub, ok := replacements[r]; ok {
				b.WriteRune(sub)
			} else {
				b.WriteRune(r)
			}
		}
	}
	// Repeated spaces are usually the residue of a substitution landing next to
	// an existing space; collapsing them keeps the name tidy without changing
	// which characters survived.
	out := strings.Join(strings.Fields(b.String()), " ")
	// Only a dot or space at the very end of the component is illegal, so
	// "Etc..flac" is left alone while "Etc." is not.
	out = strings.TrimRight(out, ". ")
	if out == "" {
		return "_"
	}
	return escapeReserved(out)
}

// RelPath sanitises a slash-separated relative path one component at a time.
// The separators are preserved: it is the components that have to be storable,
// not the path as a single name.
func RelPath(rel string) string {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			// A doubled or trailing slash carries no component; keep the shape.
			out = append(out, p)
			continue
		}
		out = append(out, Segment(p))
	}
	return strings.Join(out, "/")
}

// IsPortable reports whether rel can already be stored on every platform, so a
// caller can tell "needs renaming" from "leave it alone" without comparing
// strings itself.
func IsPortable(rel string) bool {
	return RelPath(rel) == filepath.ToSlash(rel)
}

// Unique returns a name inside dir that is free, deriving it from name by
// inserting " (2)", " (3)" and so on before the extension. It is the answer to
// the collision Segment cannot prevent: two different titles that sanitise onto
// one name, or a migrated name that a file already occupies.
//
// It reports an error rather than looping forever when the neighbourhood is
// exhausted, and it never removes or overwrites the occupant.
func Unique(dir, name string) (string, error) {
	if _, err := os.Lstat(filepath.Join(dir, name)); os.IsNotExist(err) {
		return name, nil
	}
	ext := filepath.Ext(name)
	if ext == name {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < maxAttempts; i++ {
		candidate := Nth(stem, i) + ext
		if _, err := os.Lstat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("portablename: no free name for %q in %s", name, dir)
}

// maxAttempts bounds the search for a free name. A thousand tracks that all
// sanitise onto one name is not a collision, it is a bug somewhere upstream,
// and looping forever would hide it.
const maxAttempts = 1000

// Nth is how a name is distinguished from one already taken: " (2)", " (3)".
// It is exported so a caller that cannot use Unique — one choosing a stem
// before the download tool has picked the extension — still numbers the same
// way, rather than inventing a second convention the owner has to learn.
func Nth(stem string, i int) string {
	if i < 2 {
		return stem
	}
	return fmt.Sprintf("%s (%d)", stem, i)
}

// escapeReserved rescues a component Windows would read as a DOS device. The
// reservation is on the text before the FIRST dot, so "CON.mp3" is reserved as
// surely as "CON"; the underscore goes right after that text, which keeps both
// the name and its extension readable.
func escapeReserved(s string) string {
	head, tail, _ := strings.Cut(s, ".")
	if !reserved[strings.ToUpper(head)] {
		return s
	}
	if tail == "" {
		return head + "_"
	}
	return head + "_." + tail
}
