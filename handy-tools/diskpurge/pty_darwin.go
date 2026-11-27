//go:build darwin

package main

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

// openPTY opens a pseudo-terminal pair of the given size (macOS).
func openPTY(cols, rows uint16) (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := master.Fd()
	var name [128]byte
	for _, step := range []struct {
		req uintptr
		arg uintptr
	}{
		{syscall.TIOCPTYGRANT, 0},
		{syscall.TIOCPTYUNLK, 0},
		{syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))},
	} {
		if err = ioctl(fd, step.req, step.arg); err != nil {
			_ = master.Close()
			return nil, nil, err
		}
	}
	ws := winsize{row: rows, col: cols}
	_ = ioctl(fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))

	path := string(name[:bytes.IndexByte(name[:], 0)])
	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}
