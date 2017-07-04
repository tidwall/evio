//+build linux

package shiny

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type conn struct {
	fd   int
	id   int
	addr string
	err  error
	hup  bool
}

func eventServe(net_, addr string,
	handle func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool),
	accept func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool),
	closed func(id int, err error, ctx interface{}),
	ticker func(ctx interface{}) (keepopen bool),
	ctx interface{}) error {

	lna, err := net.Listen(net_, addr)
	if err != nil {
		return err
	}
	defer lna.Close()
	ln := lna.(*net.TCPListener)
	f, err := ln.File()
	if err != nil {
		return err
	}
	defer f.Close()
	ln.Close()
	sfd := int(f.Fd())

	q, err := syscall.EpollCreate1(0)
	if err != nil {
		return err
	}
	defer syscall.Close(q)
	event := syscall.EpollEvent{
		Fd:     int32(sfd),
		Events: syscall.EPOLLIN,
	}
	if err := syscall.EpollCtl(q, syscall.EPOLL_CTL_ADD, sfd, &event); err != nil {
		return err
	}

	var conns = make(map[int]*conn)
	closeAndRemove := func(c *conn) {
		if c.hup {
			return
		}
		c.hup = true
		syscall.Close(c.fd)
		delete(conns, c.fd)
		closed(c.id, c.err, ctx)
	}

	defer func() {
		for _, c := range conns {
			closeAndRemove(c)
		}
	}()

	var lastwriteorwake time.Time
	write := func(nfd int, send []byte) (err error) {
		res1, res2, errn := syscall.Syscall(syscall.SYS_WRITE, uintptr(nfd),
			uintptr(unsafe.Pointer(&send[0])), uintptr(len(send)))
		if errn != 0 {
			_, _ = res1, res2
			if errn == syscall.EAGAIN {
				// This should not happen because we are running the
				// server in blocking mode and withoug socket timeouts.
				panic("EAGAIN")
			}
			return errn
		}
		lastwriteorwake = time.Now()
		return nil
	}

	// add wake event
	var wakemu sync.Mutex
	var wakeable = true
	var wakers = make(map[int]int) // FD->ID map
	var wakersarr []int
	var wakezero = []byte{0, 1, 2, 3, 4, 5, 6, 7}
	// SYS_EVENTFD is not implemented in Go yet.
	// SYS_EVENTFD                = 284
	// SYS_EVENTFD2               = 290
	r1, _, errn := syscall.Syscall(284, 0, 0, 0)
	if errn != 0 {
		return errn
	}
	var efd = int(r1)
	defer func() {
		wakemu.Lock()
		wakeable = false
		syscall.Close(efd)
		wakemu.Unlock()
	}()

	event = syscall.EpollEvent{
		Fd:     int32(efd),
		Events: syscall.EPOLLIN,
	}
	if err := syscall.EpollCtl(q, syscall.EPOLL_CTL_ADD, efd, &event); err != nil {
		return err
	}

	shandle := func(c *conn, data []byte) {
		send, keepalive := handle(c.id, data, ctx)
		if len(send) > 0 {
			if err := write(c.fd, send); err != nil {
				c.err = err
				closeAndRemove(c)
				return
			}
		}
		if !keepalive {
			closeAndRemove(c)
		}
	}

	var lastts time.Time
	if ticker != nil {
		if !ticker(ctx) {
			syscall.Close(q)
			return nil
		}
		lastts = time.Now()
	}

	var id int
	var packet [65535]byte
	var evs [32]syscall.EpollEvent
	for {
		var ts int
		if time.Since(lastwriteorwake) < time.Second {
			ts = 50
		} else {
			ts = 1000
		}
		n, err := syscall.EpollWait(q, evs[:], ts)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			break
		}
		for i := 0; ; i++ {
			now := time.Now()
			if now.Sub(lastts) >= time.Second/20 {
				if !ticker(ctx) {
					syscall.Close(q)
					break
				}
				lastts = now
			}
			if i >= n {
				break
			}
			if evs[i].Fd == int32(sfd) {
				nfd, sa, err := syscall.Accept(sfd)
				if err != nil {
					continue
				}
				var addr string
				var port int
				switch sa := sa.(type) {
				default:
				case *syscall.SockaddrInet4:
					addr = net.IP(sa.Addr[:]).String()
					port = sa.Port
				case *syscall.SockaddrInet6:
					addr = net.IP(sa.Addr[:]).String()
					port = sa.Port
				}
				var res []byte
				if strings.Contains(addr, ":") {
					res = append(append(append(res, '['), addr...), ']', ':')
				} else {
					res = append(append(res, addr...), ':')
				}
				addr = string(strconv.AppendInt(res, int64(port), 10))

				id++
				wake := func(nfd, id int) func() {
					return func() {
						// NOTE: This is the one and only entrypoint that is
						// not thread-safe. Use a mutex.
						wakemu.Lock()
						if wakeable {
							wakers[nfd] = id
							syscall.Write(efd, wakezero[:])
						}
						wakemu.Unlock()
					}
				}(nfd, id)

				send, keepalive := accept(id, addr, wake, ctx)
				if !keepalive {
					syscall.Close(nfd)
					continue
				}

				// 500 second keepalive
				kerr1 := syscall.SetsockoptInt(nfd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
				kerr2 := syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, 500)
				kerr3 := syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, 500)
				if kerr1 != nil || kerr2 != nil || kerr3 != nil {
					//fmt.Printf("%v %v %v\n", kerr1, kerr2, kerr3)
				}

				// add read
				event := syscall.EpollEvent{
					Fd:     int32(nfd),
					Events: syscall.EPOLLIN | syscall.EPOLLRDHUP, // | (syscall.EPOLLET & 0xffffffff),
				}
				if err := syscall.EpollCtl(q, syscall.EPOLL_CTL_ADD, nfd, &event); err != nil {
					syscall.Close(nfd)
					continue
				}
				c := &conn{fd: nfd, addr: addr, id: id}
				conns[nfd] = c
				if len(send) > 0 {
					if err := write(c.fd, send); err != nil {
						c.err = err
						closeAndRemove(c)
						continue
					}
				}
			} else if evs[i].Fd == int32(efd) {
				// NOTE: This is a wakeup call. Use a mutex when accessing
				// the `wakers` field.
				wakersarr = wakersarr[:0]
				wakemu.Lock()
				for nfd, id := range wakers {
					wakersarr = append(wakersarr, nfd, id)
				}
				wakers = make(map[int]int)
				var data [8]byte
				_, err := syscall.Read(efd, data[:])
				wakemu.Unlock()
				// exit the lock and read from the array
				if err != nil {
					return err
				}
				for i := 0; i < len(wakersarr); i += 2 {
					nfd := wakersarr[i]
					id := wakersarr[i+1]
					c := conns[nfd]
					if c != nil && c.id == id {
						shandle(c, nil)
					}
				}
				lastwriteorwake = time.Now()
			} else if evs[i].Events&syscall.EPOLLRDHUP != 0 {
				closeAndRemove(conns[int(evs[i].Fd)])
			} else {
				c := conns[int(evs[i].Fd)]
				res, _, errn := syscall.Syscall(syscall.SYS_READ, uintptr(c.fd),
					uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)))
				if errn != 0 || res == 0 {
					if errn != 0 {
						c.err = errn
					}
					closeAndRemove(c)
					continue
				}
				shandle(c, packet[:res])
			}
		}
	}
	return nil
}
