//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"syscall"
	"unsafe"
)

// releaseAppConsole sends the controller's output to log and frees a console
// only it is on. The Go runtime prints a crash to the standard error handle,
// not to os.Stderr, so that handle becomes log as well, and the report names
// every goroutine. The handle is a duplicate that stays open: log closes
// before the process ends, and a late report must not reach what takes its place.
func releaseAppConsole(log *os.File) error {
	os.Stdout, os.Stderr = log, log
	debug.SetTraceback("all")
	dll := windows.NewLazySystemDLL("kernel32.dll")
	var pids [2]uint32
	count, _, _ := dll.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if count == 1 {
		dll.NewProc("FreeConsole").Call()
	}
	process := windows.CurrentProcess()
	var stderr windows.Handle
	if e := windows.DuplicateHandle(process, windows.Handle(log.Fd()), process, &stderr, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
		return e
	}
	return windows.SetStdHandle(windows.STD_ERROR_HANDLE, stderr)
}

func newConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
}
func desktopOpen(url string) error {
	u, e := windows.UTF16PtrFromString(url)
	if e != nil {
		return e
	}
	op, _ := windows.UTF16PtrFromString("open")
	return windows.ShellExecute(0, op, u, nil, nil, windows.SW_SHOWNORMAL)
}

// smallIconSize is SM_CXSMICON at the system DPI, the notification area's
// icon size; 0 when this Windows cannot report it per DPI.
func smallIconSize() int {
	user32 := windows.NewLazySystemDLL("user32.dll")
	aware, system, metric := user32.NewProc("SetThreadDpiAwarenessContext"), user32.NewProc("GetDpiForSystem"), user32.NewProc("GetSystemMetricsForDpi")
	if aware.Find() != nil || system.Find() != nil || metric.Find() != nil {
		return 0
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, _, _ := aware.Call(^uintptr(3)) // per-monitor aware v2 for this thread; an unaware thread reads 96 DPI
	if previous == 0 {
		return 0
	}
	defer aware.Call(previous)
	dpi, _, _ := system.Call()
	size, _, _ := metric.Call(49, dpi) // SM_CXSMICON
	return int(size)
}
