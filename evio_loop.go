package evio

import (
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tidwall/evio/internal"
)

var errClosing = errors.New("closing")

type conn struct {
	id        int              // unique id
	fd        int              // file descriptor
	lnidx     int              // listener index in the server lns list
	loopidx   int              // owner loop
	out       []byte           // write buffer
	ctx       interface{}      // user defined context
	sa        syscall.Sockaddr // remote socket address
	reuse     bool             // should reuse input buffer
	opened    bool             // connection opened event fired
	writeable bool             // connection is writable
	wake      bool             // must wake
	action    Action           // next user action
}

type server struct {
	events Events         // user events
	loops  []*loop        // all the loops
	lns    []*listener    // all the listeners
	wg     sync.WaitGroup // loop close waitgroup
	cond   *sync.Cond     // shutdown signaler
	nextid uintptr        // global id counter
}

type loop struct {
	idx     int            // loop index in the server loops list
	poll    *internal.Poll // epoll or kqueue
	packet  []byte         // read packet buffer
	fdconns map[int]*conn  // loop connections fd -> conn
	idconns map[int]*conn  // loop connections id -> conn
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

func serve(events Events, listeners []*listener) error {
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
	s.lns = listeners
	s.cond = sync.NewCond(&sync.Mutex{})

	//println("-- server starting")

	if s.events.Serving != nil {
		var svr Server
		svr.NumLoops = numLoops
		svr.Addrs = make([]net.Addr, len(listeners))
		svr.Wake = func(id int) bool {
			for loopidx := id % 256; loopidx < len(s.loops); loopidx += 256 {
				l := s.loops[loopidx]
				l.poll.Trigger(id)
			}
			return true
		}
		for i, ln := range listeners {
			svr.Addrs[i] = ln.lnaddr
		}
		if s.events.Serving(svr) == Shutdown {
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
		//println("-- server stopped")
	}()

	// create loops locally and bind the listeners.
	for i := 0; i < numLoops; i++ {
		l := &loop{
			idx:     i,
			poll:    internal.OpenPoll(),
			packet:  make([]byte, 0xFFFF),
			fdconns: make(map[int]*conn),
			idconns: make(map[int]*conn),
		}
		for _, ln := range listeners {
			l.poll.AddRead(ln.fd)
		}
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
	delete(l.fdconns, c.fd)
	delete(l.idconns, c.id)
	syscall.Close(c.fd)
	if s.events.Closed != nil {
		if s.events.Closed(c.id, c.ctx, err) == Shutdown {
			return errClosing
		}
	}
	return nil
}

func loopNote(s *server, l *loop, note interface{}) error {
	switch v := note.(type) {
	default: // ??
		return nil
	case error: // shutdown
		return v
	case int: // wake
		c := l.idconns[v]
		if c != nil {
			c.wake = true
			if !c.writeable {
				l.poll.ModReadWrite(c.fd)
				c.writeable = true
			}
		}
		return nil
	}
}

func loopRun(s *server, l *loop) {
	defer func() {
		//fmt.Println("-- loop stopped --", l.idx)
		s.signalShutdown()
		s.wg.Done()
	}()
	//fmt.Println("-- loop started --", l.idx)
	l.poll.Wait(func(fd int, note interface{}) error {
		if fd == 0 {
			return loopNote(s, l, note)
		}
		c := l.fdconns[fd]
		switch {
		case c == nil:
			return loopAccept(s, l, fd)
		case !c.opened:
			return loopOpened(s, l, c)
		case len(c.out) > 0:
			return loopWrite(s, l, c)
		case c.action != None:
			return loopAction(s, l, c)
		default:
			return loopRead(s, l, c)
		}
	})
}

func loopAccept(s *server, l *loop, fd int) error {
	for i, ln := range s.lns {
		if ln.fd == fd {
			nfd, sa, err := syscall.Accept(fd)
			if err != nil {
				if err == syscall.EAGAIN {
					return nil
				}
				return err
			}
			c := &conn{
				fd: nfd, sa: sa, lnidx: i,
				id: (int(atomic.AddUintptr(&s.nextid, 1)) << 8) + (l.idx % 256),
			}
			l.fdconns[c.fd] = c
			l.idconns[c.id] = c
			l.poll.AddReadWrite(c.fd)
			c.writeable = true
			break
		}
	}
	return nil
}

func loopOpened(s *server, l *loop, c *conn) error {
	c.opened = true
	if s.events.Opened != nil {
		var info Info
		info.AddrIndex = c.lnidx
		info.LocalAddr = s.lns[c.lnidx].lnaddr
		info.RemoteAddr = internal.SockaddrToAddr(c.sa)
		out, opts, ctx, action := s.events.Opened(c.id, info)
		if len(out) > 0 {
			c.out = append([]byte{}, out...)
		}
		c.action = action
		c.ctx = ctx
		c.reuse = opts.ReuseInputBuffer
		if opts.TCPKeepAlive > 0 {
			if _, ok := s.lns[c.lnidx].ln.(*net.TCPListener); ok {
				internal.SetKeepAlive(c.fd, int(opts.TCPKeepAlive/time.Second))
			}
		}
	}
	if len(c.out) == 0 && c.action == None {
		l.poll.ModRead(c.fd)
		c.writeable = false
	}
	return nil
}

func loopWrite(s *server, l *loop, c *conn) error {
	if s.events.Prewrite != nil {
		action := s.events.Prewrite(c.id, c.ctx, len(c.out))
		if action > c.action {
			c.action = action
		}
	}
	n, err := syscall.Write(c.fd, c.out)
	if err != nil {
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
	if s.events.Postwrite != nil {
		action := s.events.Postwrite(c.id, c.ctx, n, len(c.out))
		if action > c.action {
			c.action = action
		}
	}
	if len(c.out) == 0 && c.action == None {
		l.poll.ModRead(c.fd)
		c.writeable = false
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
	case Detach:
		return errors.New("detach not supported")
	}
	if len(c.out) == 0 && c.action == None {
		l.poll.ModRead(c.fd)
		c.writeable = false
	}
	return nil
}

func loopRead(s *server, l *loop, c *conn) error {
	var in []byte
	if c.wake {
		c.wake = false
	} else {
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
	}
	if s.events.Data != nil {
		out, action := s.events.Data(c.id, c.ctx, in)
		c.action = action
		if len(out) > 0 {
			c.out = append([]byte{}, out...)
		}
	}
	if len(c.out) != 0 || c.action != None {
		l.poll.ModReadWrite(c.fd)
		c.writeable = true
	}
	return nil
}
