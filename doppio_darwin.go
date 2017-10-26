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
	_, err := syscall.Kevent(c.p,
		[]syscall.Kevent_t{{Ident: uint64(c.fd),
			Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE}},
		nil, nil)
	return err
}
func (c *conn) delwrite() error {
	if !c.writeon {
		return nil
	}
	c.writeon = false
	_, err := syscall.Kevent(c.p,
		[]syscall.Kevent_t{{Ident: uint64(c.fd),
			Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE}},
		nil, nil)
	return err
}

func makepoll() (p int, err error) {
	return syscall.Kqueue()
}
func addread(p, fd int) error {
	_, err := syscall.Kevent(p,
		[]syscall.Kevent_t{{Ident: uint64(fd),
			Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ}},
		nil, nil)
	return err
}
func makeevents(n int) interface{} {
	return make([]syscall.Kevent_t, n)
}
func wait(p int, evs interface{}, timeout time.Duration) (n int, err error) {
	if timeout < 0 {
		timeout = 0
	}
	ts := syscall.NsecToTimespec(int64(timeout))
	return syscall.Kevent(p, nil, evs.([]syscall.Kevent_t), &ts)
}
func getfd(evs interface{}, i int) int {
	return int(evs.([]syscall.Kevent_t)[i].Ident)
}
func setkeepalive(fd, secs int) error {
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
