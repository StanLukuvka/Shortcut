//go:build linux || darwin

package pyos

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

// IsTerminal is isatty(fd): the terminal-attributes ioctl succeeds only on a terminal. Not the char-device bit,
// which /dev/null has too (run uses it as a child's stdin).
func IsTerminal(fd int) bool {
	var t syscall.Termios
	return ioctl(fd, getTermios, unsafe.Pointer(&t)) == nil
}

// TerminalRows is the row count of the window of fd 1, else 24.
func TerminalRows() int {
	var ws struct{ Row, Col, X, Y uint16 }
	if ioctl(1, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)) == nil && ws.Row > 0 {
		return int(ws.Row)
	}
	return 24
}
