// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package internal

import "syscall"

const (
	EventClose uint64 = 1
	EventTick  uint64 = 2
	EventWrite uint64 = 3
)

type (
	EventHandler interface {
		OnEvent(event uint64) error
		OnFdEvent(fd int) error
	}

	// Poll ...
	Poll struct {
		fd      int // epoll fd
		eventFd *EventFd
		notes   noteQueue
	}
)

// OpenPoll ...
func OpenPoll() *Poll {
	l := new(Poll)
	p, err := syscall.EpollCreate1(0)
	if err != nil {
		panic(err)
	}
	l.fd = p
	eventFd, err := newEventFd()
	if err != nil {
		panic(err)
	}
	l.eventFd = eventFd
	l.AddRead(l.eventFd.Fd())
	return l
}

// Close ...
func (p *Poll) Close() error {
	if err := p.eventFd.Close(); err != nil {
		return err
	}

	return syscall.Close(p.fd)
}

func (p *Poll) FireEvent(event uint64) error {
	return p.eventFd.WriteEvent(event)
}

// Trigger ...
func (p *Poll) Trigger(note interface{}) error {
	p.notes.Add(note)
	return p.eventFd.WriteEvent(1)
}

// Wait ...
func (p *Poll) Wait(handler EventHandler) error {
	events := make([]syscall.EpollEvent, 64)
	for {
		n, err := syscall.EpollWait(p.fd, events, -1)
		if err != nil && err != syscall.EINTR {
			return err
		}

		for i := 0; i < n; i++ {
			if fd := int(events[i].Fd); fd == p.eventFd.Fd() {
				if event, err := p.eventFd.ReadEvent(); err != nil {
					return err
				} else if err := handler.OnEvent(event); err != nil {
					return err
				}
			} else if err := handler.OnFdEvent(fd); err != nil {
				return err
			}
		}
	}
}

// AddReadWrite ...
func (p *Poll) AddReadWrite(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_ADD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		},
	); err != nil {
		panic(err)
	}
}

// AddRead ...
func (p *Poll) AddRead(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_ADD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		},
	); err != nil {
		panic(err)
	}
}

// ModRead ...
func (p *Poll) ModRead(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_MOD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		},
	); err != nil {
		panic(err)
	}
}

// ModReadWrite ...
func (p *Poll) ModReadWrite(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_MOD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		},
	); err != nil {
		panic(err)
	}
}

// ModDetach ...
func (p *Poll) ModDetach(fd int) {
	if err := syscall.EpollCtl(p.fd, syscall.EPOLL_CTL_DEL, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		},
	); err != nil {
		panic(err)
	}
}

func SetKeepAlive(fd, secs int) error {
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1); err != nil {
		return err
	}
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, secs); err != nil {
		return err
	}
	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, secs)
}
