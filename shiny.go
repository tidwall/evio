package shiny

import (
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Serve
func Serve(net, addr string,
	handle func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool),
	accept func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool),
	closed func(id int, err error, ctx interface{}),
	ticker func(ctx interface{}) (keepopen bool),
	context interface{}) error {
	if handle == nil {
		handle = func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool) { return nil, true }
	}
	if accept == nil {
		accept = func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool) { return nil, true }
	}
	if closed == nil {
		closed = func(id int, err error, ctx interface{}) {}
	}
	if ticker == nil {
		ticker = func(ctx interface{}) (keepopen bool) { return true }
	}
	if strings.HasSuffix(net, "-compat") {
		net = net[:len(net)-len("-compat")]
	} else {
		switch net {
		case "tcp", "tcp4", "tcp6":
			return eventServe(net, addr, handle, accept, closed, ticker, context)
		}
	}
	return compatServe(net, addr, handle, accept, closed, ticker, context)
}

func compatServe(net_, addr string,
	handle func(id int, data []byte, ctx interface{}) (send []byte, keepopen bool),
	accept func(id int, addr string, wake func(), ctx interface{}) (send []byte, keepopen bool),
	closed func(id int, err error, ctx interface{}),
	ticker func(ctx interface{}) (keepopen bool),
	ctx interface{}) error {

	ln, err := net.Listen(net_, addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	if !ticker(ctx) {
		return nil
	}
	var mu sync.Mutex
	var conns = make(map[net.Conn]bool)
	defer func() {
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
	}()
	var id int
	var done bool
	var shutdown bool
	donech := make(chan bool)
	var lastwrite time.Time
	lasttick := time.Now()
	go func() {
		t := time.NewTicker(time.Second / 20)
		defer t.Stop()
		for {
			select {
			case <-donech:
				return
			case <-t.C:
				now := time.Now()
				if now.Sub(lastwrite) < time.Second || now.Sub(lasttick) >= time.Second {
					mu.Lock()
					if !done && !ticker(ctx) {
						shutdown = true
						ln.Close()
					}
					lasttick = now
					mu.Unlock()
				}
			}
		}
	}()
	defer func() {
		mu.Lock()
		done = true
		mu.Unlock()
		donech <- true
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			mu.Lock()
			if shutdown {
				mu.Unlock()
				return nil
			}
			mu.Unlock()
			return err
		}
		id++
		func() {
			wake := func(c net.Conn, id int) func() {
				return func() {
					go func() {
						mu.Lock()
						defer mu.Unlock()
						send, keepopen := handle(id, nil, ctx)
						if len(send) > 0 {
							lastwrite = time.Now()
							if _, err := c.Write(send); err != nil {
								c.Close()
								return
							}
						}
						if !keepopen {
							c.Close()
							return
						}
					}()
				}
			}(c, id)
			if !func() bool {
				mu.Lock()
				defer mu.Unlock()
				send, keepopen := accept(id, c.RemoteAddr().String(), wake, ctx)
				if len(send) > 0 {
					lastwrite = time.Now()
					if _, err := c.Write(send); err != nil {
						c.Close()
						return false
					}
				}
				if !keepopen {
					c.Close()
					return false
				}
				conns[c] = true
				return true
			}() {
				return
			}
			go func(id int, c net.Conn) {
				var ferr error
				defer func() {
					mu.Lock()
					defer mu.Unlock()
					if ferr == io.EOF {
						ferr = nil
					}
					if operr, ok := ferr.(*net.OpError); ok {
						ferr = operr.Err
						switch ferr.Error() {
						case "use of closed network connection",
							"read: connection reset by peer":
							ferr = nil
						}
					}
					delete(conns, c)
					closed(id, ferr, ctx)
					c.Close()
				}()
				packet := make([]byte, 4096)
				for {
					n, err := c.Read(packet)
					if err != nil {
						ferr = err
						return
					}
					func() {
						mu.Lock()
						defer mu.Unlock()
						send, keepopen := handle(id, packet[:n], ctx)
						if len(send) > 0 {
							lastwrite = time.Now()
							if _, err := c.Write(send); err != nil {
								c.Close()
								return
							}
						}
						if !keepopen {
							c.Close()
							return
						}
					}()
				}
			}(id, c)
		}()
	}
}
