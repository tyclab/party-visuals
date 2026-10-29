//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

func releaseAppConsole(root string) error {
	path := filepath.Join(root, "controller.log")
	if info, e := os.Stat(path); e == nil && info.Size() > 1024*1024 {
		os.Remove(path + ".1")
		if e = os.Rename(path, path+".1"); e != nil {
			return e
		}
	}
	log, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
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
