// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build darwin netbsd freebsd openbsd dragonfly linux

package evio

import (
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"evio/internal"

	reuseport "github.com/kavu/go_reuseport"
)

type conn struct {
	fd         int              // file descriptor
	lock       sync.Mutex       // out buffer guard
	out        []byte           // write buffer
	sa         syscall.Sockaddr // remote socket address
	reuse      bool             // should reuse input buffer
	opened     bool             // connection opened event fired
	action     Action           // next user action
	ctx        interface{}      // user-defined context
	localAddr  net.Addr         // local addre
	remoteAddr net.Addr         // remote addr
	loop       *loop            // connected loop
}

func (c *conn) Context() interface{}       { return c.ctx }
func (c *conn) SetContext(ctx interface{}) { c.ctx = ctx }
func (c *conn) LocalAddr() net.Addr        { return c.localAddr }
func (c *conn) RemoteAddr() net.Addr       { return c.remoteAddr }
func (c *conn) Write(data []byte) {
	c.lock.Lock()
	c.out = append(c.out, data...)
	c.lock.Unlock()
	c.loop.poll.ModReadWrite(c.fd)
}

type server struct {
	events   Events             // user events
	loops    []*loop            // all the loops
	ln       *listener          // all the listeners
	wg       sync.WaitGroup     // loop close waitgroup
	cond     *sync.Cond         // shutdown signaler
	balance  LoadBalance        // load balancing method
	accepted uintptr            // accept counter
	tch      chan time.Duration // ticker channel
}

type loop struct {
	idx     int            // loop index in the server loops list
	poll    *internal.Poll // epoll or kqueue
	packet  []byte         // read packet buffer
	fdconns map[int]*conn  // loop connections fd -> conn
	count   int32          // connection count
}

// waitForShutdown waits for a signal to shutdown
func (s *server) waitForShutdown() {
	s.cond.L.Lock()
	s.cond.Wait()
	s.cond.L.Unlock()
}

// signalShutdown signals a shutdown an begins server closing
func (s *server) signalShutdown() {
	s.cond.L.Lock()
	s.cond.Signal()
	s.cond.L.Unlock()
}

func serve(events Events, listener *listener) error {
	// figure out the correct number of loops/goroutines to use.
	numLoops := events.NumLoops
	if numLoops <= 0 {
		if numLoops == 0 {
			numLoops = 1
		} else {
			numLoops = runtime.NumCPU()
		}
	}

	s := &server{}
	s.events = events
	s.ln = listener
	s.cond = sync.NewCond(&sync.Mutex{})
	s.balance = events.LoadBalance
	s.tch = make(chan time.Duration)

	if s.events.Serving != nil {
		var svr Server
		svr.NumLoops = numLoops
		svr.Addr = listener.lnaddr
		action := s.events.Serving(svr)
		switch action {
		case None:
		case Shutdown:
			return nil
		}
	}

	defer func() {
		// wait on a signal for shutdown
		s.waitForShutdown()

		// notify all loops to close by closing all listeners
		for _, l := range s.loops {
			l.poll.Trigger(errClosing)
		}

		// wait on all loops to complete reading events
		s.wg.Wait()

		// close loops and all outstanding connections
		for _, l := range s.loops {
			for _, c := range l.fdconns {
				loopCloseConn(s, l, c, nil)
			}
			l.poll.Close()
		}
	}()

	// create loops locally and bind the listeners.
	for i := 0; i < numLoops; i++ {
		l := &loop{
			idx:     i,
			poll:    internal.OpenPoll(),
			packet:  make([]byte, 0xFFFF),
			fdconns: make(map[int]*conn),
		}
		l.poll.AddRead(listener.fd)
		s.loops = append(s.loops, l)
	}
	// start loops in background
	s.wg.Add(len(s.loops))
	for _, l := range s.loops {
		go loopRun(s, l)
	}
	return nil
}

func loopCloseConn(s *server, l *loop, c *conn, err error) error {
	atomic.AddInt32(&l.count, -1)
	delete(l.fdconns, c.fd)
	syscall.Close(c.fd)
	if s.events.Closed != nil {
		switch s.events.Closed(c, err) {
		case None:
		case Shutdown:
			return errClosing
		}
	}
	return nil
}

func loopNote(s *server, l *loop, note interface{}) error {
	var err error
	switch v := note.(type) {
	case time.Duration:
		delay, action := s.events.Tick()
		switch action {
		case None:
		case Shutdown:
			err = errClosing
		}
		s.tch <- delay
	case error: // shutdown
		err = v
	}
	return err
}

func loopRun(s *server, l *loop) {
	defer func() {
		s.signalShutdown()
		s.wg.Done()
	}()

	if l.idx == 0 && s.events.Tick != nil {
		go loopTicker(s, l)
	}

	l.poll.Wait(func(fd int, note interface{}) error {
		if fd == 0 {
			return loopNote(s, l, note)
		}

		c := l.fdconns[fd]
		if c == nil {
			return loopAccept(s, l)
		}

		c.lock.Lock()
		empty := len(c.out) == 0
		c.lock.Unlock()
		switch {
		case !c.opened:
			return loopOpened(s, l, c)
		case !empty:
			return loopWrite(s, l, c)
		case c.action != None:
			return loopAction(s, l, c)
		default:
			return loopRead(s, l, c)
		}
	})
}

func loopTicker(s *server, l *loop) {
	for {
		if err := l.poll.Trigger(time.Duration(0)); err != nil {
			break
		}
		time.Sleep(<-s.tch)
	}
}

func loopAccept(s *server, l *loop) error {
	if len(s.loops) > 1 {
		switch s.balance {
		case LeastConnections:
			n := atomic.LoadInt32(&l.count)
			for _, lp := range s.loops {
				if lp.idx != l.idx {
					if atomic.LoadInt32(&lp.count) < n {
						return nil // do not accept
					}
				}
			}
		case RoundRobin:
			idx := int(atomic.LoadUintptr(&s.accepted)) % len(s.loops)
			if idx != l.idx {
				return nil // do not accept
			}
			atomic.AddUintptr(&s.accepted, 1)
		}
	}
	nfd, sa, err := syscall.Accept(s.ln.fd)
	if err != nil {
		if err == syscall.EAGAIN {
			return nil
		}
		return err
	}
	if err := syscall.SetNonblock(nfd, true); err != nil {
		return err
	}

	c := &conn{fd: nfd, sa: sa, loop: l}
	l.fdconns[c.fd] = c
	l.poll.AddReadWrite(c.fd)
	atomic.AddInt32(&l.count, 1)

	return nil
}

func loopOpened(s *server, l *loop, c *conn) error {
	c.opened = true
	c.localAddr = s.ln.lnaddr
	c.remoteAddr = internal.SockaddrToAddr(c.sa)
	if s.events.Opened != nil {
		out, opts, action := s.events.Opened(c)
		if len(out) > 0 {
			c.lock.Lock()
			c.out = append([]byte{}, out...)
			c.lock.Unlock()
		}
		c.action = action
		c.reuse = opts.ReuseInputBuffer
		if opts.TCPKeepAlive > 0 {
			if _, ok := s.ln.ln.(*net.TCPListener); ok {
				internal.SetKeepAlive(c.fd, int(opts.TCPKeepAlive/time.Second))
			}
		}
	}

	c.lock.Lock()
	empty := len(c.out) == 0
	c.lock.Unlock()
	if empty && c.action == None {
		l.poll.ModRead(c.fd)
	}

	return nil
}

func loopWrite(s *server, l *loop, c *conn) error {
	c.lock.Lock()
	n, err := syscall.Write(c.fd, c.out)
	if err != nil {
		c.lock.Unlock()
		if err == syscall.EAGAIN {
			return nil
		}
		return loopCloseConn(s, l, c, err)
	}

	if n == len(c.out) {
		c.out = nil
	} else {
		c.out = c.out[n:]
	}
	empty := len(c.out) == 0
	c.lock.Unlock()
	if empty && c.action == None {
		l.poll.ModRead(c.fd)
	}

	return nil
}

func loopAction(s *server, l *loop, c *conn) error {
	switch c.action {
	default:
		c.action = None
	case Close:
		return loopCloseConn(s, l, c, nil)
	case Shutdown:
		return errClosing
	}

	c.lock.Lock()
	empty := len(c.out) == 0
	c.lock.Unlock()
	if empty && c.action == None {
		l.poll.ModRead(c.fd)
	}
	return nil
}

func loopRead(s *server, l *loop, c *conn) error {
	var in []byte
	n, err := syscall.Read(c.fd, l.packet)
	if n == 0 || err != nil {
		if err == syscall.EAGAIN {
			return nil
		}
		return loopCloseConn(s, l, c, err)
	}
	in = l.packet[:n]
	if !c.reuse {
		in = append([]byte{}, in...)
	}
	if s.events.Data != nil {
		out, action := s.events.Data(c, in)
		c.action = action
		if len(out) > 0 {
			c.lock.Lock()
			c.out = append([]byte{}, out...)
			c.lock.Unlock()
		}
	}

	c.lock.Lock()
	empty := len(c.out) == 0
	c.lock.Unlock()
	if !empty || c.action != None {
		l.poll.ModReadWrite(c.fd)
	}

	return nil
}

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

func reuseportListen(proto, addr string) (l net.Listener, err error) {
	return reuseport.Listen(proto, addr)
}
