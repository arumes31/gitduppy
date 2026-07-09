package gitops

import (
	"os"
	"path/filepath"
)

// DirSize returns the total size in bytes of all regular files under root,
// walking the tree once with WalkDir (one directory read per directory, avoiding
// a stat() syscall per entry). It is best-effort: missing or unreadable entries
// contribute zero rather than erroring, and a completely missing root returns 0.
// Callers use it for reporting (e.g. persisting a repository's on-disk size), not
// for correctness, so it never fails.
func DirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort: skip unreadable entries
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
