//go:build darwin || linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func terminalColumns() int {
	var size struct{ rows, columns, xpixel, ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(),
		syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	if errno != 0 {
		return 0
	}
	return int(size.columns)
}
