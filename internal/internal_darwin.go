package internal

import "syscall"

// SetKeepAlive sets the keepalive for the connection
func SetKeepAlive(fd, secs int) error {
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, 0x8, 1); err != nil {
		return err
	}
	switch err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, 0x101, secs); err {
	case nil, syscall.ENOPROTOOPT: // OS X 10.7 and earlier don't support this option
	default:
		return err
	}
	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPALIVE, secs)
}
