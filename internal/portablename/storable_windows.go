package portablename

// LocallyStorable reports whether this device's filesystem can represent rel.
//
// On Windows the portable rules are the local rules: the illegal characters,
// the trailing dot or space, and the DOS device names are exactly what this
// filesystem refuses. os.Root refuses the device names too, so a fetch that
// tried anyway would fail on the write rather than on the create.
func LocallyStorable(rel string) bool { return IsPortable(rel) }
