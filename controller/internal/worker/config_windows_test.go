//go:build windows

package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBrowserProfileGuardFollowsJunctionsAndShortNames(t *testing.T) {
	local := filepath.Join(t.TempDir(), "Local Application Data")
	chrome := filepath.Join(local, "Google", "Chrome", "User Data")
	if e := os.MkdirAll(chrome, 0700); e != nil {
		t.Fatal(e)
	}
	refused := func(env, profile string) bool {
		t.Setenv("LOCALAPPDATA", env)
		_, e := Load(writeConfig(t, map[string]any{"browserProfile": profile}))
		return e != nil && strings.Contains(e.Error(), "not a browser's own profile folder")
	}
	junction := filepath.Join(t.TempDir(), "junction")
	if out, e := exec.Command("cmd", "/c", "mklink", "/J", junction, local).CombinedOutput(); e != nil {
		t.Fatalf("mklink /J: %v %s", e, out)
	}
	t.Cleanup(func() { os.Remove(junction) })
	if !refused(local, filepath.Join(junction, "Google", "Chrome", "User Data")) || !refused(junction, chrome) {
		t.Error("a junction to LOCALAPPDATA hid the default root")
	}
	long, _ := windows.UTF16PtrFromString(chrome)
	buf := make([]uint16, windows.MAX_PATH)
	n, e := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	short := windows.UTF16ToString(buf[:n])
	if e != nil || strings.EqualFold(short, chrome) {
		t.Skipf("this volume gives no 8.3 short names: %v", e)
	}
	if !refused(local, short) {
		t.Errorf("the default root spelt %q was accepted", short)
	}
}
