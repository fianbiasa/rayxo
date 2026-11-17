package actions

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// staleTransferAge is how long a hidden temporary file — a backup, import or
// download in flight — may sit before it counts as left behind by a crashed
// or killed run. No backup operation runs anywhere near this long.
const staleTransferAge = 24 * time.Hour

// sweepStaleTemps removes the hidden temporary files a crashed or killed
// backup, import or download left in dir. They start with a dot, so the panel
// never lists them as a backup, and one is only ever in flight for a single
// operation, so any older than staleTransferAge is abandoned and only wastes
// disk. A file in flight is far younger, so it is left alone. Errors are
// ignored: a sweep is best effort.
func sweepStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, ".") {
			continue
		}

		if !strings.HasSuffix(name, ".tmp") && !strings.HasSuffix(name, ".part") && !strings.HasSuffix(name, ".sql") {
			continue
		}

		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < staleTransferAge {
			continue
		}

		_ = os.Remove(filepath.Join(dir, name))
	}
}
