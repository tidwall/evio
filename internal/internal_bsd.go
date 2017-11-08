// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// +build darwin netbsd freebsd openbsd dragonfly

package internal

import (
	"syscall"
	"time"
)

func AddRead(p, fd int, readon, writeon *bool) error {
	if readon != nil {
		if *readon {
			return nil
		}
		*readon = true
	}
	_, err := syscall.Kevent(p,
		[]syscall.Kevent_t{{Ident: uint64(fd),
			Flags: syscall.EV_ADD, Filter: syscall.EVFILT_READ}},
		nil, nil)
	return err
}
func DelRead(p, fd int, readon, writeon *bool) error {
	if readon != nil {
		if !*readon {
			return nil
		}
		*readon = false
	}
	_, err := syscall.Kevent(p,
		[]syscall.Kevent_t{{Ident: uint64(fd),
			Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_READ}},
		nil, nil)
	return err
}

func AddWrite(p, fd int, readon, writeon *bool) error {
	if writeon != nil {
		if *writeon {
			return nil
		}
		*writeon = true
	}
	_, err := syscall.Kevent(p,
		[]syscall.Kevent_t{{Ident: uint64(fd),
			Flags: syscall.EV_ADD, Filter: syscall.EVFILT_WRITE}},
		nil, nil)
	return err
}
func DelWrite(p, fd int, readon, writeon *bool) error {
	if writeon != nil {
		if !*writeon {
			return nil
		}
		*writeon = false
	}
	_, err := syscall.Kevent(p,
		[]syscall.Kevent_t{{Ident: uint64(fd),
			Flags: syscall.EV_DELETE, Filter: syscall.EVFILT_WRITE}},
		nil, nil)
	return err
}

func MakePoll() (p int, err error) {
	return syscall.Kqueue()
}
func MakeEvents(n int) interface{} {
	return make([]syscall.Kevent_t, n)
}
func Wait(p int, evs interface{}, timeout time.Duration) (n int, err error) {
	if timeout < 0 {
		timeout = 0
	}
	ts := syscall.NsecToTimespec(int64(timeout))
	return syscall.Kevent(p, nil, evs.([]syscall.Kevent_t), &ts)
}
func GetFD(evs interface{}, i int) int {
	return int(evs.([]syscall.Kevent_t)[i].Ident)
}
