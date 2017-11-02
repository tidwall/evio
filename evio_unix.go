// +build netbsd openbsd freebsd darwin dragonfly linux

package evio

import (
	"errors"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/tidwall/evio/internal"
)

func (ln *listener) close() {
	if ln.fd != 0 {
		syscall.Close(ln.fd)
	}
	if ln.f != nil {
		ln.f.Close()
	}
	if ln.ln != nil {
		ln.ln.Close()
	}
	if ln.network == "unix" {
		os.RemoveAll(ln.addr)
	}
}

func (ln *listener) system() error {
	var err error
	switch netln := ln.ln.(type) {
	default:
		ln.close()
		return errors.New("network not supported")
	case *net.TCPListener:
		ln.f, err = netln.File()
	case *net.UnixListener:
		ln.f, err = netln.File()
	}
	if err != nil {
		ln.close()
		return err
	}
	ln.fd = int(ln.f.Fd())
	return syscall.SetNonblock(ln.fd, true)
}

type unixConn struct {
	id, fd, p int
	outbuf    []byte
	outpos    int
	action    Action
	opts      Options
	raddr     net.Addr
	laddr     net.Addr
	err       error
	wake      bool
	writeon   bool
	detached  bool
}

func (c *unixConn) Read(p []byte) (n int, err error) {
	return syscall.Read(c.fd, p)
}
func (c *unixConn) Write(p []byte) (n int, err error) {
	if c.detached {
		if len(c.outbuf) > 0 {
			for len(c.outbuf) > 0 {
				n, err = syscall.Write(c.fd, c.outbuf)
				if n > 0 {
					c.outbuf = c.outbuf[n:]
				}
				if err != nil {
					return 0, err
				}
			}
			c.outbuf = nil
		}
		var tn int
		if len(p) > 0 {
			for len(p) > 0 {
				n, err = syscall.Write(c.fd, p)
				if n > 0 {
					p = p[n:]
					tn += n
				}
				if err != nil {
					return tn, err
				}
			}
			p = nil
		}
		return tn, nil
	}
	return syscall.Write(c.fd, p)
}
func (c *unixConn) Close() error {
	err := syscall.Close(c.fd)
	c.fd = 0
	return err
}

func serve(events Events, lns []*listener) error {
	p, err := internal.MakePoll()
	if err != nil {
		return err
	}
	defer syscall.Close(p)
	for _, ln := range lns {
		if err := internal.AddRead(p, ln.fd); err != nil {
			return err
		}
	}
	var mu sync.Mutex
	lock := func() { mu.Lock() }
	unlock := func() { mu.Unlock() }
	fdconn := make(map[int]*unixConn)
	idconn := make(map[int]*unixConn)
	wake := func(id int) bool {
		var ok = true
		var err error
		lock()
		c := idconn[id]
		if c == nil {
			ok = false
		} else if !c.wake {
			c.wake = true
			err = internal.AddWrite(c.p, c.fd, &c.writeon)
		}
		unlock()
		if err != nil {
			panic(err)
		}
		return ok
	}
	if events.Serving != nil {
		addrs := make([]net.Addr, len(lns))
		for i, ln := range lns {
			addrs[i] = ln.naddr
		}
		switch events.Serving(wake, addrs) {
		case Shutdown:
			return nil
		}
	}
	defer func() {
		lock()
		type fdid struct {
			fd, id int
			opts   Options
		}
		var fdids []fdid
		for fd, c := range fdconn {
			fdids = append(fdids, fdid{fd, c.id, c.opts})
		}
		sort.Slice(fdids, func(i, j int) bool {
			return fdids[j].id < fdids[i].id
		})
		for _, fdid := range fdids {
			syscall.Close(fdid.fd)
			if events.Closed != nil {
				unlock()
				events.Closed(fdid.id, nil)
				lock()
			}
		}
		unlock()
	}()
	var id int
	var packet [0xFFFF]byte
	var evs = internal.MakeEvents(64)
	var lastTicker time.Time
	var tickerDelay time.Duration
	if events.Tick == nil {
		tickerDelay = time.Hour
	}
	for {
		pn, err := internal.Wait(p, evs, tickerDelay)
		if err != nil && err != syscall.EINTR {
			return err
		}
		if events.Tick != nil {
			now := time.Now()
			if now.Sub(lastTicker) > tickerDelay {
				var action Action
				tickerDelay, action = events.Tick()
				if action == Shutdown {
					return nil
				}
				lastTicker = now
			}
		}
		lock()
		for i := 0; i < pn; i++ {
			var in []byte
			var c *unixConn
			var nfd int
			var n int
			var out []byte
			var ln *listener
			var lnidx int
			var fd = internal.GetFD(evs, i)
			var sa syscall.Sockaddr
			for lnidx, ln = range lns {
				if fd == ln.fd {
					goto accept
				}
			}
			c = fdconn[fd]
			if c == nil {
				syscall.Close(fd)
				goto next
			}
			goto read
		accept:
			nfd, sa, err = syscall.Accept(fd)
			if err != nil {
				goto next
			}
			if err = syscall.SetNonblock(nfd, true); err != nil {
				goto fail
			}
			if err = internal.AddRead(p, nfd); err != nil {
				goto fail
			}
			id++
			c = &unixConn{id: id, fd: nfd, p: p}
			fdconn[nfd] = c
			idconn[id] = c
			c.laddr = getlocaladdr(fd, ln.ln)
			c.raddr = getaddr(sa, ln.ln)
			if events.Opened != nil {
				unlock()
				out, c.opts, c.action = events.Opened(c.id, Addr{lnidx, c.laddr, c.raddr})
				lock()
				if c.opts.TCPKeepAlive > 0 {
					if _, ok := ln.ln.(*net.TCPListener); ok {
						if err = internal.SetKeepAlive(c.fd, int(c.opts.TCPKeepAlive/time.Second)); err != nil {
							goto fail
						}
					}
				}
				if len(out) > 0 {
					c.outbuf = append(c.outbuf, out...)
				}
			}
			goto write
		read:
			if c.action != None {
				goto write
			}
			if c.wake {
				c.wake = false
			} else {
				n, err = c.Read(packet[:])
				if n == 0 || err != nil {
					if err == syscall.EAGAIN {
						goto write
					}
					c.err = err
					goto close
				}
				in = append([]byte{}, packet[:n]...)
			}
			if events.Data != nil {
				unlock()
				out, c.action = events.Data(c.id, in)
				lock()
			}
			if len(out) > 0 {
				c.outbuf = append(c.outbuf, out...)
			}
			goto write
		write:
			if len(c.outbuf)-c.outpos > 0 {
				if events.Prewrite != nil {
					unlock()
					action := events.Prewrite(c.id, len(c.outbuf[c.outpos:]))
					lock()
					if action == Shutdown {
						c.action = Shutdown
					}
				}
				n, err = c.Write(c.outbuf[c.outpos:])
				if events.Postwrite != nil {
					amount := n
					if amount < 0 {
						amount = 0
					}
					unlock()
					action := events.Postwrite(c.id, amount, len(c.outbuf)-c.outpos-amount)
					lock()
					if action == Shutdown {
						c.action = Shutdown
					}
				}
				if n == 0 || err != nil {
					if c.action == Shutdown {
						goto close
					}
					if err == syscall.EAGAIN {
						if err = internal.AddWrite(c.p, c.fd, &c.writeon); err != nil {
							goto fail
						}
						goto next
					}
					c.err = err
					goto close
				}
				c.outpos += n
				if len(c.outbuf)-c.outpos == 0 {
					c.outpos = 0
					c.outbuf = nil
				}
			}
			if c.action == Shutdown {
				goto close
			}
			if len(c.outbuf)-c.outpos == 0 {
				if !c.wake {
					if err = internal.DelWrite(c.p, c.fd, &c.writeon); err != nil {
						goto fail
					}
				}
				if c.action != None {
					goto close
				}
			} else {
				if err = internal.AddWrite(c.p, c.fd, &c.writeon); err != nil {
					goto fail
				}
			}
			goto next
		close:
			delete(fdconn, c.fd)
			delete(idconn, c.id)
			if c.action == Detach {
				if events.Detached != nil {
					c.detached = true
					if len(c.outbuf)-c.outpos > 0 {
						c.outbuf = append(c.outbuf[:0], c.outbuf[c.outpos:]...)
					} else {
						c.outbuf = nil
					}
					c.outpos = 0
					syscall.SetNonblock(c.fd, false)
					unlock()
					c.action = events.Detached(c.id, c)
					lock()
					if c.action == Shutdown {
						goto fail
					}
					goto next
				}
			}
			syscall.Close(c.fd)
			if events.Closed != nil {
				unlock()
				action := events.Closed(c.id, c.err)
				lock()
				if action == Shutdown {
					c.action = Shutdown
				}
			}
			if c.action == Shutdown {
				err = nil
				goto fail
			}
			goto next
		fail:
			unlock()
			return err
		next:
		}
		unlock()
	}
}

func getlocaladdr(fd int, ln net.Listener) net.Addr {
	sa, _ := syscall.Getsockname(fd)
	return getaddr(sa, ln)
}

func getaddr(sa syscall.Sockaddr, ln net.Listener) net.Addr {
	switch ln.(type) {
	case *net.UnixListener:
		return ln.Addr()
	case *net.TCPListener:
		var addr net.TCPAddr
		switch sa := sa.(type) {
		case *syscall.SockaddrInet4:
			addr.IP = net.IP(sa.Addr[:])
			addr.Port = sa.Port
			return &addr
		case *syscall.SockaddrInet6:
			addr.IP = net.IP(sa.Addr[:])
			addr.Port = sa.Port
			if sa.ZoneId != 0 {
				addr.Zone = strconv.FormatInt(int64(sa.ZoneId), 10)
			}
			return &addr
		}
	}
	return nil
}
