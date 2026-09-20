//go:build !unix

package validate

import "io/fs"

// No owner model here yet, so Source refuses every file.
func fileOwner(fs.FileInfo) (int, bool) { return 0, false }
