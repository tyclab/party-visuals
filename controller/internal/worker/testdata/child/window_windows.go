//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	registerClass   = user32.NewProc("RegisterClassExW")
	createWindow    = user32.NewProc("CreateWindowExW")
	defWindowProc   = user32.NewProc("DefWindowProcW")
	getMessage      = user32.NewProc("GetMessageW")
	dispatchMessage = user32.NewProc("DispatchMessageW")
	postQuitMessage = user32.NewProc("PostQuitMessage")
)

type windowClass struct {
	size, style                        uint32
	proc                               uintptr
	classExtra, windowExtra            int32
	instance, icon, cursor, background windows.Handle
	menu, name                         *uint16
	smallIcon                          windows.Handle
}
type message struct {
	window         windows.HWND
	id             uint32
	wParam, lParam uintptr
	time           uint32
	x, y           int32
	private        uint32
}

// window shows one visible top-level window, prints "ready" and exits 0
// once the window is closed, as a browser closed through its window does.
func window() {
	runtime.LockOSThread()
	var instance windows.Handle
	if e := windows.GetModuleHandleEx(0, nil, &instance); e != nil {
		os.Exit(3)
	}
	name, _ := windows.UTF16PtrFromString("PartyVisualsTestWindow")
	class := windowClass{instance: instance, name: name, proc: windows.NewCallback(func(h windows.HWND, id uint32, w, l uintptr) uintptr {
		if id == 0x0002 { // WM_DESTROY
			postQuitMessage.Call(0)
			return 0
		}
		r, _, _ := defWindowProc.Call(uintptr(h), uintptr(id), w, l)
		return r
	})}
	class.size = uint32(unsafe.Sizeof(class))
	registerClass.Call(uintptr(unsafe.Pointer(&class)))
	const visibleOverlapped = 0x10CF0000
	if h, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), visibleOverlapped, 0, 0, 240, 120, 0, 0, uintptr(instance), 0); h == 0 {
		os.Exit(3)
	}
	fmt.Println("ready")
	var m message
	for {
		if r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0); int32(r) <= 0 {
			return
		}
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}
