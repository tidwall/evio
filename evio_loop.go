// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build netbsd openbsd freebsd darwin dragonfly linux

package evio

import (
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

// system takes the net listener and detaches it from it's parent
// event loop, grabs the file descriptor, and makes it non-blocking.
func (ln *listener) system() error {
	var err error
	switch netln := ln.ln.(type) {
	default:
		panic("invalid listener type")
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

// unixConn represents the connection as the event loop sees it.
// This is also becomes a detached connection.
type unixConn struct {
	id, fd   int
	outbuf   []byte
	outpos   int
	action   Action
	opts     Options
	timeout  time.Time
	err      error
	dialerr  error
	wake     bool
	writeon  bool
	detached bool
	closed   bool
	opening  bool
}

func (c *unixConn) Timeout() time.Time {
	return c.timeout
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
	if c.closed {
		return syscall.EINVAL
	}
	err := syscall.Close(c.fd)
	c.fd = -1
	c.closed = true
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
	var done bool
	lock := func() { mu.Lock() }
	unlock := func() { mu.Unlock() }
	fdconn := make(map[int]*unixConn)
	idconn := make(map[int]*unixConn)
	timeoutqueue := internal.NewTimeoutQueue()
	var id int
	dial := func(addr string, timeout time.Duration) int {
		lock()
		if done {
			unlock()
			return 0
		}
		id++
		c := &unixConn{id: id, opening: true}
		idconn[id] = c
		if timeout != 0 {
			c.timeout = time.Now().Add(timeout)
			timeoutqueue.Push(c)
		}
		unlock()
		// resolving an address blocks and we don't want blocking, like ever.
		// but since we're leaving the event loop we'll need to complete the
		// socket connection in a goroutine and add the read and write events
		// to the loop to get back into the loop.
		go func() {
			err := func() error {
				sa, err := resolve(addr)
				if err != nil {
					return err
				}
				var fd int
				switch sa.(type) {
				case *syscall.SockaddrUnix:
					fd, err = syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
				case *syscall.SockaddrInet4:
					fd, err = syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
				case *syscall.SockaddrInet6:
					fd, err = syscall.Socket(syscall.AF_INET6, syscall.SOCK_STREAM, 0)
				}
				if err != nil {
					return err
				}
				err = syscall.Connect(fd, sa)
				if err != nil && err != syscall.EINPROGRESS {
					syscall.Close(fd)
					return err
				}
				if err := syscall.SetNonblock(fd, true); err != nil {
					syscall.Close(fd)
					return err
				}
				lock()
				err = internal.AddRead(p, fd)
				if err != nil {
					unlock()
					syscall.Close(fd)
					return err
				}
				err = internal.AddWrite(p, fd, &c.writeon)
				if err != nil {
					unlock()
					syscall.Close(fd)
					return err
				}
				c.fd = fd
				fdconn[fd] = c
				unlock()
				return nil
			}()
			if err != nil {
				// set a dial error and timeout right away
				lock()
				c.dialerr = err
				c.timeout = time.Now()
				timeoutqueue.Push(c)
				unlock()
			}

		}()
		return id
	}

	// wake wakes up a connection
	wake := func(id int) bool {
		var ok = true
		var err error
		lock()
		if done {
			unlock()
			return false
		}
		c := idconn[id]
		if c == nil || c.fd == 0 {
			ok = false
		} else if !c.wake {
			c.wake = true
			err = internal.AddWrite(p, c.fd, &c.writeon)
		}
		unlock()
		if err != nil {
			panic(err)
		}
		return ok
	}
	ctx := Server{Wake: wake, Dial: dial}
	ctx.Addrs = make([]net.Addr, len(lns))
	for i, ln := range lns {
		ctx.Addrs[i] = ln.naddr
	}
	if events.Serving != nil {
		switch events.Serving(ctx) {
		case Shutdown:
			return nil
		}
	}
	defer func() {
		lock()
		done = true
		type fdid struct {
			fd, id  int
			opening bool
		}
		var fdids []fdid
		for _, c := range idconn {
			fdids = append(fdids, fdid{c.fd, c.id, c.opening})
		}
		sort.Slice(fdids, func(i, j int) bool {
			return fdids[j].id < fdids[i].id
		})
		for _, fdid := range fdids {
			if fdid.fd != 0 {
				syscall.Close(fdid.fd)
			}
			if fdid.opening {
				if events.Opened != nil {
					laddr := getlocaladdr(fdid.fd)
					raddr := getremoteaddr(fdid.fd)
					unlock()
					events.Opened(fdid.id, Info{
						Closing:    true,
						AddrIndex:  -1,
						LocalAddr:  laddr,
						RemoteAddr: raddr,
					})
					lock()
				}
			}
			if events.Closed != nil {
				unlock()
				events.Closed(fdid.id, nil)
				lock()
			}
		}
		syscall.Close(p)
		fdconn = nil
		idconn = nil
		unlock()
	}()
	var packet [0xFFFF]byte
	var evs = internal.MakeEvents(64)
	nextTicker := time.Now()
	for {
		delay := nextTicker.Sub(time.Now())
		if delay < 0 {
			delay = 0
		} else if delay > time.Second/4 {
			delay = time.Second / 4
		}
		pn, err := internal.Wait(p, evs, delay)
		if err != nil && err != syscall.EINTR {
			return err
		}
		remain := nextTicker.Sub(time.Now())
		if remain < 0 {
			var tickerDelay time.Duration
			var action Action
			if events.Tick != nil {
				tickerDelay, action = events.Tick()
				if action == Shutdown {
					return nil
				}
			} else {
				tickerDelay = time.Hour
			}
			nextTicker = time.Now().Add(tickerDelay + remain)
		}
		// check for dial connection timeouts
		if timeoutqueue.Len() > 0 {
			var count int
			now := time.Now()
			for {
				v := timeoutqueue.Peek()
				if v == nil {
					break
				}
				c := v.(*unixConn)
				if now.After(v.Timeout()) {
					timeoutqueue.Pop()
					if _, ok := idconn[c.id]; ok && c.opening {
						delete(idconn, c.id)
						delete(fdconn, c.fd)
						syscall.Close(c.fd)
						if events.Opened != nil {
							laddr := getlocaladdr(c.fd)
							raddr := getremoteaddr(c.fd)
							events.Opened(c.id, Info{
								Closing:    true,
								AddrIndex:  -1,
								LocalAddr:  laddr,
								RemoteAddr: raddr,
							})
						}
						if events.Closed != nil {
							if c.dialerr != nil {
								events.Closed(c.id, c.dialerr)
							} else {
								events.Closed(c.id, syscall.ETIMEDOUT)
							}
						}
						count++
					}
				} else {
					break
				}
			}
			if count > 0 {
				// invalidate the current events and wait for more
				continue
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
			for lnidx, ln = range lns {
				if fd == ln.fd {
					goto accept
				}
			}
			ln = nil
			c = fdconn[fd]
			if c == nil {
				syscall.Close(fd)
				goto next
			}
			if c.opening {

				lnidx = -1
				goto opened
			}
			goto read
		accept:
			nfd, _, err = syscall.Accept(fd)
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
			c = &unixConn{id: id, fd: nfd}
			fdconn[nfd] = c
			idconn[id] = c
			goto opened
		opened:
			if events.Opened != nil {
				laddr := getlocaladdr(fd)
				raddr := getremoteaddr(fd)
				unlock()
				out, c.opts, c.action = events.Opened(c.id, Info{
					AddrIndex:  lnidx,
					LocalAddr:  laddr,
					RemoteAddr: raddr,
				})
				lock()
				if c.opts.TCPKeepAlive > 0 {
					internal.SetKeepAlive(c.fd, int(c.opts.TCPKeepAlive/time.Second))
				}
				if len(out) > 0 {
					c.outbuf = append(c.outbuf, out...)
				}
			}
			if c.opening {
				c.opening = false
				goto next
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
						if err = internal.AddWrite(p, c.fd, &c.writeon); err != nil {
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
					c.outbuf = c.outbuf[:0]
				}
			}
			if c.action == Shutdown {
				goto close
			}
			if len(c.outbuf)-c.outpos == 0 {
				if !c.wake {
					if err = internal.DelWrite(p, c.fd, &c.writeon); err != nil {
						goto fail
					}
				}
				if c.action != None {
					goto close
				}
			} else {
				if err = internal.AddWrite(p, c.fd, &c.writeon); err != nil {
					goto fail
				}
			}
			goto next
		close:
			delete(fdconn, c.fd)
			delete(idconn, c.id)
			//delete(idtimeout, c.id)
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

func getlocaladdr(fd int) net.Addr {
	if fd == 0 {
		return nil
	}
	sa, _ := syscall.Getsockname(fd)
	return getaddr(sa)
}
func getremoteaddr(fd int) net.Addr {
	if fd == 0 {
		return nil
	}
	sa, _ := syscall.Getpeername(fd)
	return getaddr(sa)
}
func getaddr(sa syscall.Sockaddr) net.Addr {
	switch sa := sa.(type) {
	default:
		return nil
	case *syscall.SockaddrInet4:
		return &net.TCPAddr{IP: net.IP(sa.Addr[:]), Port: sa.Port}
	case *syscall.SockaddrInet6:
		return &net.TCPAddr{IP: net.IP(sa.Addr[:]), Port: sa.Port, Zone: strconv.FormatInt(int64(sa.ZoneId), 10)}
	case *syscall.SockaddrUnix:
		return &net.UnixAddr{Net: "unix", Name: sa.Name}
	}
}

// resolve resolves an evio address and retuns a sockaddr for socket
// connection to external servers.
func resolve(addr string) (sa syscall.Sockaddr, err error) {
	network, address, _ := parseAddr(addr)
	var taddr net.Addr
	switch network {
	default:
		return nil, net.UnknownNetworkError(network)
	case "unix":
		taddr = &net.UnixAddr{Net: "unix", Name: address}
	case "tcp", "tcp4", "tcp6":
		// use the stdlib resolver because it's good.
		taddr, err = net.ResolveTCPAddr(network, address)
		if err != nil {
			return nil, err
		}
	}
	switch taddr := taddr.(type) {
	case *net.UnixAddr:
		sa = &syscall.SockaddrUnix{Name: taddr.Name}
	case *net.TCPAddr:
		switch len(taddr.IP) {
		case 0:
			var sa4 syscall.SockaddrInet4
			sa4.Port = taddr.Port
			sa = &sa4
		case 4:
			var sa4 syscall.SockaddrInet4
			copy(sa4.Addr[:], taddr.IP[:])
			sa4.Port = taddr.Port
			sa = &sa4
		case 16:
			var sa6 syscall.SockaddrInet6
			copy(sa6.Addr[:], taddr.IP[:])
			sa6.Port = taddr.Port
			sa = &sa6
		}
	}
	return sa, nil
}
