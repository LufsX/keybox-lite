package targets

import (
	"errors"
	"os"
	"syscall"
)

// removeOwnedLink never follows the link or creates missing backend directories.
// Keep the parent pinned through inspection and unlink, as in config writes.
func (d *disk) removeOwnedLink(p, source string) error {
	dir, n, err := d.parent(p, false, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	pointsTo, err := os.Readlink(fdPath(dir, n))
	if os.IsNotExist(err) || errors.Is(err, syscall.EINVAL) {
		return nil
	}
	if err != nil {
		return err
	}
	if pointsTo != source {
		return nil
	}
	if d.beforeCommit != nil {
		if err = d.beforeCommit(p, snapshot{}); err != nil {
			return err
		}
	}
	// Recheck immediately before unlink, including after the test write boundary.
	pointsTo, err = os.Readlink(fdPath(dir, n))
	if os.IsNotExist(err) || errors.Is(err, syscall.EINVAL) {
		return nil
	}
	if err != nil {
		return err
	}
	if pointsTo != source {
		return nil
	}
	if err = syscall.Unlinkat(int(dir.Fd()), n); err != nil && !errors.Is(err, syscall.ENOENT) {
		return err
	}
	return dir.Sync()
}
