package diskcache

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// walkFiles walks the cache root and invokes fileFn for every non-directory
// entry it can stat, reporting whether the entry lives in the tmp staging dir
// (staging files are not counted toward the size/entry gauges).
func (c *Cache) walkFiles(fileFn func(path string, info fs.FileInfo, inTmp bool), dirFn func(path string)) error {
	tmpDir := filepath.Join(c.root, tmpSubdir)

	walkErr := filepath.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if dirFn != nil && path != c.root && path != tmpDir {
				dirFn(path)
			}

			return nil
		}
		// d.Info errors only if the entry vanished (raced a remover): skip it.
		if info, infoErr := d.Info(); infoErr == nil {
			fileFn(path, info, filepath.Dir(path) == tmpDir)
		}

		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walking cache dir: %w", walkErr)
	}

	return nil
}
