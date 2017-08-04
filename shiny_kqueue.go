//+build darwin freebsd

package shiny

import (
	"fmt"
	"net"
	"runtime"
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
	q, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	defer syscall.Close(q)
	ev := []syscall.Kevent_t{{
		Ident:  uint64(sfd),
		Flags:  syscall.EV_ADD,
		Filter: syscall.EVFILT_READ,
	}}
	if _, err := syscall.Kevent(q, ev, nil, nil); err != nil {
		return err
	}
	var conns = make(map[int]*conn)
	defer func() {
		for _, conn := range conns {
			syscall.Close(conn.fd)
			closed(conn.id, conn.err, ctx)
		}
	}()

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
		return nil
	}

	// add wake event
	var wakemu sync.Mutex
	var wakeable = true
	var wakers = make(map[int]int) // FD->ID map
	var wakersarr []int
	if accept != nil {
		defer func() {
			wakemu.Lock()
			wakeable = false
			wakemu.Unlock()
		}()
		ev = []syscall.Kevent_t{{
			Ident:  0,
			Flags:  syscall.EV_ADD,
			Filter: syscall.EVFILT_USER,
		}}
		if _, err := syscall.Kevent(q, ev, nil, nil); err != nil {
			return err
		}
	}
	shandle := func(c *conn, data []byte) {
		send, keepalive := handle(c.id, data, ctx)
		if len(send) > 0 {
			if err := write(c.fd, send); err != nil {
				c.err = err
				syscall.Close(c.fd)
				return
			}
		}
		if !keepalive {
			syscall.Close(c.fd)
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
	var ts = syscall.Timespec{Sec: 1, Nsec: 0}
	var packet [65535]byte
	var evs [32]syscall.Kevent_t
	for {
		n, err := syscall.Kevent(q, nil, evs[:], &ts)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			break
		}
		for i := 0; ; i++ {
			now := time.Now()
			if now.Sub(lastts) >= time.Second/60 {
				if !ticker(ctx) {
					syscall.Close(q)
					break
				}
				lastts = now
			}
			if i >= n {
				break
			}
			if evs[i].Flags&syscall.EV_EOF != 0 {
				c := conns[int(evs[i].Ident)]
				syscall.Close(int(evs[i].Ident))
				delete(conns, int(evs[i].Ident))
				if c != nil {
					closed(c.id, c.err, ctx)
				}
			} else if evs[i].Ident == uint64(sfd) {
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
				var send []byte
				if accept != nil {
					wake := func(nfd, id int) func() {
						return func() {
							// NOTE: This is the one and only entrypoint that is
							// not thread-safe. Use a mutex.
							wakemu.Lock()
							ev := []syscall.Kevent_t{{
								Ident:  0,
								Flags:  syscall.EV_ENABLE,
								Filter: syscall.EVFILT_USER,
								Fflags: syscall.NOTE_TRIGGER,
							}}
							if wakeable {
								wakers[nfd] = id
								syscall.Kevent(q, ev, nil, nil)
							}
							wakemu.Unlock()
						}
					}(nfd, id)
					var keepalive bool
					send, keepalive = accept(id, addr, wake, ctx)
					if !keepalive {
						syscall.Close(nfd)
						continue
					}
				}
				// 500 second keepalive
				var kerr1, kerr2, kerr3 error
				if runtime.GOOS == "darwin" {
					kerr1 = syscall.SetsockoptInt(nfd, syscall.SOL_SOCKET, 0x8, 1)
					kerr2 = syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, 0x101, 500)
					kerr3 = syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, 0x10, 500)
				} else {
					// freebsd
					kerr1 = syscall.SetsockoptInt(nfd, syscall.SOL_SOCKET, 0x8, 1)
					kerr2 = syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, 0x200, 500)
					kerr3 = syscall.SetsockoptInt(nfd, syscall.IPPROTO_TCP, 0x100, 500)
				}
				if kerr1 != nil || kerr2 != nil || kerr3 != nil {
					fmt.Printf("%v %v %v\n", kerr1, kerr2, kerr3)
				}

				// add read
				ev := []syscall.Kevent_t{{
					Ident:  uint64(nfd),
					Flags:  syscall.EV_ADD,
					Filter: syscall.EVFILT_READ,
				}}
				if _, err := syscall.Kevent(q, ev, nil, nil); err != nil {
					syscall.Close(nfd)
					continue
				}
				c := &conn{fd: nfd, addr: addr, id: id}
				conns[nfd] = c
				if len(send) > 0 {
					if err := write(c.fd, send); err != nil {
						c.err = err
						syscall.Close(c.fd)
						continue
					}
				}
			} else if evs[i].Ident == 0 {
				// NOTE: This is a wakeup call. Use a mutex when accessing
				// the `wakers` field.
				wakersarr = wakersarr[:0]
				wakemu.Lock()
				for nfd, id := range wakers {
					wakersarr = append(wakersarr, nfd, id)
				}
				wakers = make(map[int]int)
				ev := []syscall.Kevent_t{{
					Ident:  0,
					Flags:  syscall.EV_DISABLE,
					Filter: syscall.EVFILT_USER,
					Fflags: syscall.NOTE_TRIGGER,
				}}
				_, err := syscall.Kevent(q, ev, nil, nil)
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
			} else {
				c := conns[int(evs[i].Ident)]
				res, _, errn := syscall.Syscall(syscall.SYS_READ, uintptr(c.fd),
					uintptr(unsafe.Pointer(&packet[0])), uintptr(len(packet)))
				if errn != 0 || res == 0 {
					if errn != 0 {
						c.err = errn
					}
					syscall.Close(c.fd)
					continue
				}
				shandle(c, packet[:res])
			}
		}
	}
	return nil
}
