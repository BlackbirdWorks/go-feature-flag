package flagruntime

// pendingChange is a change that waits for the consumer.
type pendingChange struct {
	prev, next *pendingChange
	change     Change
	// fromNothing says that the flag set did not exist where the pending change started: if it is withdrawn
	// before the consumer reads, the net change is nothing.
	fromNothing bool
}

// pendingQueue holds at most one pending change per flag set name, in the order the names first became pending.
// Every operation is O(1).
type pendingQueue struct {
	byName     map[string]*pendingChange
	head, tail *pendingChange
}

func newPendingQueue() *pendingQueue {
	return &pendingQueue{byName: map[string]*pendingChange{}}
}

func (q *pendingQueue) front() *pendingChange { return q.head }

func (q *pendingQueue) push(item *pendingChange) {
	q.byName[item.change.FlagSet] = item

	if q.tail == nil {
		q.head, q.tail = item, item

		return
	}

	item.prev, q.tail.next = q.tail, item
	q.tail = item
}

func (q *pendingQueue) remove(item *pendingChange) {
	delete(q.byName, item.change.FlagSet)

	if item.prev != nil {
		item.prev.next = item.next
	} else {
		q.head = item.next
	}

	if item.next != nil {
		item.next.prev = item.prev
	} else {
		q.tail = item.prev
	}

	item.prev, item.next = nil, nil
}
