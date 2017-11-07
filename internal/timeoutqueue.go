package internal

import (
	"container/heap"
	"time"
)

// TimeoutQueueItem is an item for TimeoutQueue
type TimeoutQueueItem interface {
	Timeout() time.Time
}

type timeoutPriorityQueue []TimeoutQueueItem

func (pq timeoutPriorityQueue) Len() int { return len(pq) }

func (pq timeoutPriorityQueue) Less(i, j int) bool {
	return pq[i].Timeout().Before(pq[j].Timeout())
}

func (pq timeoutPriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *timeoutPriorityQueue) Push(x interface{}) {
	*pq = append(*pq, x.(TimeoutQueueItem))
}

func (pq *timeoutPriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

// TimeoutQueue is a priority queue ordere be ascending time.Time.
type TimeoutQueue struct {
	pq timeoutPriorityQueue
}

// NewTimeoutQueue returns a new TimeoutQueue.
func NewTimeoutQueue() *TimeoutQueue {
	q := &TimeoutQueue{}
	heap.Init(&q.pq)
	return q
}

// Push adds a new item.
func (q *TimeoutQueue) Push(x TimeoutQueueItem) {
	heap.Push(&q.pq, x)
}

// Pop removes and returns the items with the smallest value.
func (q *TimeoutQueue) Pop() TimeoutQueueItem {
	return heap.Pop(&q.pq).(TimeoutQueueItem)
}

// Peek returns the items with the smallest value, but does not remove it.
func (q *TimeoutQueue) Peek() TimeoutQueueItem {
	if q.Len() > 0 {
		return q.pq[0]
	}
	return nil
}

// Len returns the number of items in the queue
func (q *TimeoutQueue) Len() int {
	return q.pq.Len()
}
