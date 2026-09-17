//go:build !windows

package portablename

import "strings"

// LocallyStorable reports whether this device's filesystem can represent rel.
//
// Unix filesystems accept any byte in a name except NUL and the separator, so a
// peer's path is storable here unless it carries a NUL — which no legitimate
// path does, and which the syscall layer would silently truncate rather than
// reject.
//
// Colons, question marks and trailing dots are all perfectly writable here.
// Refusing them because Windows would is not this function's job: the
// household's answer to such a name is the migration, and treating a file this
// device can hold as unfetchable would stop it syncing for no reason.
func LocallyStorable(rel string) bool { return !strings.ContainsRune(rel, 0) }
