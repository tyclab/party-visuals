//go:build !windows

package partysource

// fileACL reports no grants: only Windows files carry a DACL, and the
// controller runs on Windows.
func fileACL(string) (string, []string, error) { return "", nil, nil }
func accountName(string) string                { return "" }
