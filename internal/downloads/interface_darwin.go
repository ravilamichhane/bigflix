package downloads

import (
	"golang.org/x/sys/unix"
	"strings"
	"syscall"
)

func interfaceControl(index int) func(string, string, syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var sockErr error
		err := c.Control(func(fd uintptr) {
			if strings.HasSuffix(network, "6") {
				sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, index)
			} else {
				sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, index)
			}
		})
		if err != nil {
			return err
		}
		return sockErr
	}
}
