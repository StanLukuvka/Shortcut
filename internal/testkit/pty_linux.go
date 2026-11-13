// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (T-0). Don't hand-edit without checking the plan.

package testkit

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// openPty is os.openpty without cgo: /dev/ptmx, unlock (TIOCSPTLCK), ask for the slave's number (TIOCGPTN).
func openPty() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return
	}
	var unlock int32 // TIOCSPTLCK = 0x40045431
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), 0x40045431, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	var n uint32 // TIOCGPTN = 0x80045430
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), 0x80045430, uintptr(unsafe.Pointer(&n))); e != 0 {
		master.Close()
		return nil, nil, e
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
