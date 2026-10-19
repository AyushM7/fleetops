package visitor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const evidenceLimit = 512 << 20
const evidenceFiles = 10000
const diskReserve = 5 << 30

// Evidence is never deleted to recover quota. Admission stops until the owner
// reconciles storage; final archival failure preserves the database schema.
func storageReady(root string) error {
	free, err := availableBytes(root)
	if err != nil || free < diskReserve+32<<20 {
		return errors.New("evidence storage reserve unavailable")
	}
	var size int64
	count := 0
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("evidence storage unavailable")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("evidence symlink denied")
		}
		count++
		if count > evidenceFiles {
			return errors.New("evidence entry budget exhausted")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("evidence entry unavailable")
		}
		size += info.Size()
		if size > evidenceLimit {
			return errors.New("evidence byte budget exhausted")
		}
		return nil
	})
}
