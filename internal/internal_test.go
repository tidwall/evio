package internal

import (
	"fmt"
	"testing"
	"time"
)

type queueItem struct {
	timeout time.Time
}

func (item *queueItem) Timeout() time.Time {
	return item.timeout
}

func TestQueue(t *testing.T) {
	q := NewTimeoutQueue()
	item := &queueItem{timeout: time.Unix(0, 5)}
	q.Push(item)
	q.Push(&queueItem{timeout: time.Unix(0, 3)})
	q.Push(&queueItem{timeout: time.Unix(0, 20)})
	q.Push(&queueItem{timeout: time.Unix(0, 13)})
	var out string
	for q.Len() > 0 {
		pitem := q.Peek()
		item := q.Pop()
		out += fmt.Sprintf("(%v:%v) ", pitem.Timeout().UnixNano(), item.Timeout().UnixNano())
	}
	exp := "(3:3) (5:5) (13:13) (20:20) "
	if out != exp {
		t.Fatalf("expected '%v', got '%v'", exp, out)
	}
}
