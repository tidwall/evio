package doppio

import (
	"syscall"
	"time"
)

func (c *conn) addwrite() error {
	if c.writeon {
		return nil
	}
	c.writeon = true
	return syscall.EpollCtl(c.p, syscall.EPOLL_CTL_MOD, c.fd,
		&syscall.EpollEvent{Fd: int32(c.fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		})
}
func (c *conn) delwrite() error {
	if !c.writeon {
		return nil
	}
	c.writeon = false
	return syscall.EpollCtl(c.p, syscall.EPOLL_CTL_MOD, c.fd,
		&syscall.EpollEvent{Fd: int32(c.fd),
			Events: syscall.EPOLLIN,
		})
}

func makepoll() (p int, err error) {
	return syscall.EpollCreate1(0)
}
func addread(p, fd int) error {
	return syscall.EpollCtl(p, syscall.EPOLL_CTL_ADD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		})
}
func makeevents(n int) interface{} {
	return make([]syscall.EpollEvent, n)
}
func wait(p int, evs interface{}, timeout time.Duration) (n int, err error) {
	if timeout < 0 {
		timeout = 0
	}
	ts := int(timeout / time.Millisecond)
	return syscall.EpollWait(p, evs.([]syscall.EpollEvent), ts)
}
func getfd(evs interface{}, i int) int {
	return int(evs.([]syscall.EpollEvent)[i].Fd)
}
func setkeepalive(fd, secs int) error {
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, secs); err != nil {
		return err
	}
	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, secs)
}
