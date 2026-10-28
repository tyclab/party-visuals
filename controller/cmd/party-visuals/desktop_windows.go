//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

func releaseAppConsole(log *os.File) error {
	os.Stdout, os.Stderr = log, log
	dll := windows.NewLazySystemDLL("kernel32.dll")
	var pids [2]uint32
	count, _, _ := dll.NewProc("GetConsoleProcessList").Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	if count == 1 {
		dll.NewProc("FreeConsole").Call()
	}
	return nil
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
