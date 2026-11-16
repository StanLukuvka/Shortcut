//go:build linux || darwin

package platform

import (
	"syscall"
	"unsafe"
)

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// IsTerminal reports whether fd is a terminal: the terminal-attributes ioctl succeeds only there. The
// character-device bit is not enough, /dev/null has it and run uses /dev/null as a child's stdin.
func IsTerminal(fd int) bool {
	var t syscall.Termios
	return ioctl(fd, getTermios, unsafe.Pointer(&t)) == nil
}
