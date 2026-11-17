package downloads

import (
	"golang.org/x/sys/unix"
	"net"
	"syscall"
)

func interfaceControl(index int) func(string, string, syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		iface, err := net.InterfaceByIndex(index)
		if err != nil {
			return err
		}
		var sockErr error
		err = c.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface.Name)
		})
		if err != nil {
			return err
		}
		return sockErr
	}
}
