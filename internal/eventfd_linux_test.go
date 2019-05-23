package internal

import "testing"

func TestNew(t *testing.T) {
	efd, err := newEventFd()
	if err != nil {
		t.Error("Could not create EventFd")
		return
	}
	defer efd.Close()

	if efd.Fd() < 0 {
		t.Errorf("invalid FD %d", efd.Fd())
		return
	}
}

func TestReadWriteEvent(t *testing.T) {
	efd, err := newEventFd()
	if err != nil {
		t.Error(err)
	}
	defer efd.Close()

	var good uint64 = 0x0102030405060708
	if err := efd.WriteEvent(good); err != nil {
		t.Error(err)
	}

	if actual, err := efd.ReadEvent(); err != nil {
		t.Error(err)
	} else if actual != good {
		t.Errorf("error reading from eventfd, expected: %q, actual: %q", good, actual)
	}
}
