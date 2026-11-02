//go:build windows

package worker

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	gwOwner = 4
	wmClose = 0x0010
)

var (
	// restoredCloseWait matches Close-VisualsBrowser's wait before it ends Chrome.
	restoredCloseWait = 15 * time.Second
	user32            = windows.NewLazySystemDLL("user32.dll")
	getWindow         = user32.NewProc("GetWindow")
	postMessage       = user32.NewProc("PostMessageW")
	search            struct {
		sync.Mutex
		pid   uint32
		found []windows.HWND
	}
	collectWindow = windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		var pid uint32
		if _, e := windows.GetWindowThreadProcessId(h, &pid); e == nil && pid == search.pid && windows.IsWindowVisible(h) {
			if owner, _, _ := getWindow.Call(uintptr(h), gwOwner); owner == 0 {
				search.found = append(search.found, h)
			}
		}
		return 1
	})
)

func closeRestored(ctx context.Context, b *Backend, start bool) error {
	var session uint32
	if e := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); e != nil {
		return e
	}
	pids, e := chromePIDs()
	if e != nil {
		return e
	}
	handles := map[uint32]windows.Handle{}
	defer func() {
		for _, h := range handles {
			windows.CloseHandle(h)
		}
	}()
	var found []browser
	for _, pid := range pids {
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
		if e != nil {
			continue
		}
		handles[pid] = h
		f := browser{browserID: browserID{PID: pid}}
		var created, exit, kernel, user windows.Filetime
		if windows.GetProcessTimes(h, &created, &exit, &kernel, &user) == nil && windows.ProcessIdToSessionId(pid, &f.Process.Session) == nil {
			f.Started = created.Nanoseconds()
			f.Process.Args, _ = arguments(h)
			found = append(found, f)
		}
	}
	return b.sweep(ctx, found, session, start, func(ctx context.Context, f browser) error {
		return closeBrowser(ctx, handles[f.PID], f.PID)
	})
}

func chromePIDs() ([]uint32, error) {
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var pids []uint32
	for e = windows.Process32First(snapshot, &entry); e == nil; e = windows.Process32Next(snapshot, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "chrome.exe") {
			pids = append(pids, entry.ProcessID)
		}
	}
	if !errors.Is(e, windows.ERROR_NO_MORE_FILES) {
		return nil, e
	}
	return pids, nil
}

// arguments reads a process's command line, as Win32_Process.CommandLine does.
func arguments(h windows.Handle) ([]string, error) {
	var size uint32
	e := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, nil, 0, &size)
	if size == 0 {
		return nil, e
	}
	buf := make([]uint64, (size+7)/8)
	if e = windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)*8), &size); e != nil {
		return nil, e
	}
	line := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String()
	runtime.KeepAlive(buf)
	return windows.DecomposeCommandLine(line)
}

// closeBrowser asks each of the browser's windows to close, so Chrome saves
// its screen and session, and ends a browser still running after
// restoredCloseWait.
func closeBrowser(ctx context.Context, h windows.Handle, pid uint32) error {
	asked := map[windows.HWND]bool{}
	for end := time.Now().Add(restoredCloseWait); time.Now().Before(end); {
		for _, w := range topLevelWindows(pid) {
			if !asked[w] {
				asked[w] = true
				postMessage.Call(uintptr(w), wmClose, 0, 0)
			}
		}
		if exited(h, 250*time.Millisecond) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	t, e := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if e == nil {
		e = windows.TerminateProcess(t, 1)
		windows.CloseHandle(t)
	}
	if exited(h, 5*time.Second) {
		return nil
	}
	return endFailure(e, 5*time.Second)
}

func exited(h windows.Handle, wait time.Duration) bool {
	r, e := windows.WaitForSingleObject(h, uint32(wait/time.Millisecond))
	return e == nil && r == windows.WAIT_OBJECT_0
}

func topLevelWindows(pid uint32) []windows.HWND {
	search.Lock()
	defer search.Unlock()
	search.pid, search.found = pid, nil
	_ = windows.EnumWindows(collectWindow, nil)
	return search.found
}
