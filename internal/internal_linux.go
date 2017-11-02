// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package internal

import (
	"syscall"
	"time"
)

func AddWrite(p, fd int, on *bool) error {
	if *on {
		return nil
	}
	*on = true
	return syscall.EpollCtl(p, syscall.EPOLL_CTL_MOD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN | syscall.EPOLLOUT,
		})
}
func DelWrite(p, fd int, on *bool) error {
	if !*on {
		return nil
	}
	*on = false
	return syscall.EpollCtl(p, syscall.EPOLL_CTL_MOD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		})
}
func MakePoll() (p int, err error) {
	return syscall.EpollCreate1(0)
}
func AddRead(p, fd int) error {
	return syscall.EpollCtl(p, syscall.EPOLL_CTL_ADD, fd,
		&syscall.EpollEvent{Fd: int32(fd),
			Events: syscall.EPOLLIN,
		})
}
func MakeEvents(n int) interface{} {
	return make([]syscall.EpollEvent, n)
}
func Wait(p int, evs interface{}, timeout time.Duration) (n int, err error) {
	if timeout < 0 {
		timeout = 0
	}
	ts := int(timeout / time.Millisecond)
	return syscall.EpollWait(p, evs.([]syscall.EpollEvent), ts)
}
func GetFD(evs interface{}, i int) int {
	return int(evs.([]syscall.EpollEvent)[i].Fd)
}
