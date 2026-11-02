package worker

import "path/filepath"

// canonical spells path as the file system resolves its longest existing
// part (links, junctions, 8.3 short names and mapped drives followed) and
// appends the rest as written.
func canonical(path string) string {
	p, e := filepath.Abs(path)
	if e != nil {
		return filepath.Clean(path)
	}
	rest := ""
	for {
		if resolved, ok := resolve(p); ok {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
