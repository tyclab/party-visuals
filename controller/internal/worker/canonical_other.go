//go:build !windows

package worker

import "path/filepath"

func resolve(path string) (string, bool) {
	p, e := filepath.EvalSymlinks(path)
	return p, e == nil
}
