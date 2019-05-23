package internal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"syscall"
)

const eventBytes = 8

var (
	errRead  = errors.New("failed to read event")
	errWrite = errors.New("failed to write event")
)

type EventFd struct {
	fd    int
	valid bool
}

func newEventFd() (*EventFd, error) {
	fd, _, err := syscall.Syscall(syscall.SYS_EVENTFD2, 0, syscall.O_CLOEXEC, 0)
	if err != 0 {
		return nil, err
	}

	return &EventFd{
		fd:    int(fd),
		valid: true,
	}, nil
}

func (e *EventFd) Close() error {
	if e.valid == false {
		return nil
	}

	e.valid = false
	return syscall.Close(e.fd)
}

func (e *EventFd) Fd() int {
	return e.fd
}

func (e *EventFd) ReadEvent() (uint64, error) {
	buf := make([]byte, eventBytes)
	if n, err := syscall.Read(e.fd, buf[:]); err != nil {
		return 0, err
	} else if n != eventBytes {
		return 0, errRead
	} else {
		return binary.ReadUvarint(bytes.NewBuffer(buf))
	}
}

func (e *EventFd) WriteEvent(val uint64) error {
	buf := make([]byte, eventBytes)
	binary.PutUvarint(buf, val)

	if n, err := syscall.Write(e.fd, buf[:]); err != nil {
		return err
	} else if n != eventBytes {
		return errWrite
	} else {
		return nil
	}
}
