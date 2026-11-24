package nas

import (
	"os"

	"golang.org/x/sys/unix"
)

// Both paths are basenames in the already-opened media directory.
func moveNoReplace(root *os.Root, from, to string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return unix.Renameat2(int(dir.Fd()), from, int(dir.Fd()), to, unix.RENAME_NOREPLACE)
}
