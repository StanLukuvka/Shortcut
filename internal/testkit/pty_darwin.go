package testkit

import (
	"os"
	"syscall"
	"unsafe"
)

// openPty is os.openpty without cgo: /dev/ptmx, grant (TIOCPTYGRANT), unlock (TIOCPTYUNLK), then the slave's
// path (TIOCPTYGNAME into a 128-byte buffer). It runs only on macOS.
// If it fails there, github.com/creack/pty does the same job.
func openPty() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return
	}
	for _, req := range []uintptr{0x20007454, 0x20007452} { // TIOCPTYGRANT, TIOCPTYUNLK
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), req, 0); e != 0 {
			master.Close()
			return nil, nil, e
		}
	}
	var name [128]byte // TIOCPTYGNAME = 0x40807453
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), 0x40807453, uintptr(unsafe.Pointer(&name[0]))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	n := 0
	for n < len(name) && name[n] != 0 {
		n++
	}
	slave, err = os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
