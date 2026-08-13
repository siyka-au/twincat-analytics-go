package layout

import "sync"

// PendingQueue buffers raw Bin/Tx/Data MQTT payloads whose layout GUID has
// not yet been registered in the Registry.
//
// Typical flow:
//
//  1. A Bin/Tx/Data message arrives before its Bin/Tx/Symbols.
//  2. Caller parses the data message header, finds the layout GUID is unknown.
//  3. Caller calls Enqueue(guid, rawPayload).
//  4. When the matching Bin/Tx/Symbols eventually arrives, caller registers
//     the layout and immediately calls Drain(guid) to retrieve all buffered
//     payloads for reprocessing.
//
// A per-GUID capacity cap prevents unbounded memory growth when a Symbols
// message never arrives (e.g. stale retained messages for a removed device).
// When the cap is reached the oldest entry is silently evicted.
type PendingQueue struct {
	mu         sync.Mutex
	pending    map[GUID][][]byte
	maxPerGUID int
}

// NewPendingQueue returns a PendingQueue that retains at most maxPerGUID
// payloads per layout GUID.  Set maxPerGUID ≤ 0 for an unlimited buffer
// (use with caution in production).
func NewPendingQueue(maxPerGUID int) *PendingQueue {
	return &PendingQueue{
		pending:    make(map[GUID][][]byte),
		maxPerGUID: maxPerGUID,
	}
}

// Enqueue adds a defensive copy of payload to the queue for guid.
// If the per-GUID cap would be exceeded the oldest entry is evicted and
// dropped is returned as true.
func (q *PendingQueue) Enqueue(guid GUID, payload []byte) (dropped bool) {
	cp := make([]byte, len(payload))
	copy(cp, payload)

	q.mu.Lock()
	defer q.mu.Unlock()

	queue := q.pending[guid]
	if q.maxPerGUID > 0 && len(queue) >= q.maxPerGUID {
		queue = queue[1:] // evict oldest
		dropped = true
	}
	q.pending[guid] = append(queue, cp)
	return dropped
}

// Drain removes and returns all queued payloads for guid in FIFO order.
// Returns nil when nothing is queued for that GUID.
func (q *PendingQueue) Drain(guid GUID) [][]byte {
	q.mu.Lock()
	items := q.pending[guid]
	delete(q.pending, guid)
	q.mu.Unlock()
	return items
}

// Len returns the total number of queued payloads across all GUIDs.
func (q *PendingQueue) Len() int {
	q.mu.Lock()
	n := 0
	for _, v := range q.pending {
		n += len(v)
	}
	q.mu.Unlock()
	return n
}

// GUIDCount returns the number of distinct GUIDs currently queued.
func (q *PendingQueue) GUIDCount() int {
	q.mu.Lock()
	n := len(q.pending)
	q.mu.Unlock()
	return n
}
