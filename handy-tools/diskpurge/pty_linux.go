//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	tiocgptn   = 0x80045430 // TIOCGPTN
	tiocsptlck = 0x40045431 // TIOCSPTLCK
)

// openPTY opens a pseudo-terminal pair of the given size (Linux).
func openPTY(cols, rows uint16) (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := master.Fd()
	var unlock int32
	if err = ioctl(fd, tiocsptlck, uintptr(unsafe.Pointer(&unlock))); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	var ptn uint32
	if err = ioctl(fd, tiocgptn, uintptr(unsafe.Pointer(&ptn))); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	ws := winsize{row: rows, col: cols}
	_ = ioctl(fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))

	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptn), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
