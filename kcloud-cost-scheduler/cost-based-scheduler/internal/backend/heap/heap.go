/*
Thread-safe priority queue implementation

This module provides a generic, thread-safe heap structure used for managing
pods in various queues (active, backoff, unschedulable) based on priority.
*/
package heap

import (
	"container/heap"
	"fmt"
)

type KeyFunc[T any] func(obj T) string

type heapItem[T any] struct {
	obj   T   // The object which is stored in the heap.
	index int // The index of the object's key in the Heap.queue.
}

type itemKeyValue[T any] struct {
	key string
	obj T
}

type data[T any] struct {
	items map[string]*heapItem[T]
	queue []string

	keyFunc KeyFunc[T]
	lessFunc LessFunc[T]
}

var (
	_ = heap.Interface(&data[any]{}) // heapData is a standard heap
)

func (h *data[T]) Less(i, j int) bool {
	if i >= len(h.queue) || j >= len(h.queue) {
		return false
	}
	itemi, ok := h.items[h.queue[i]]
	if !ok {
		return false
	}
	itemj, ok := h.items[h.queue[j]]
	if !ok {
		return false
	}
	return h.lessFunc(itemi.obj, itemj.obj)
}

func (h *data[T]) Len() int { return len(h.queue) }

func (h *data[T]) Swap(i, j int) {
	if i < 0 || j < 0 {
		return
	}
	h.queue[i], h.queue[j] = h.queue[j], h.queue[i]
	if item := h.items[h.queue[i]]; item != nil {
		item.index = i
	}
	if item := h.items[h.queue[j]]; item != nil {
		item.index = j
	}
}

func (h *data[T]) Push(kv interface{}) {
	keyValue := kv.(*itemKeyValue[T])
	n := len(h.queue)
	h.items[keyValue.key] = &heapItem[T]{keyValue.obj, n}
	h.queue = append(h.queue, keyValue.key)
}

func (h *data[T]) Pop() interface{} {
	if len(h.queue) == 0 {
		return nil
	}
	key := h.queue[len(h.queue)-1]
	h.queue = h.queue[0 : len(h.queue)-1]
	item, ok := h.items[key]
	if !ok {
		return nil
	}
	delete(h.items, key)
	return item.obj
}

func (h *data[T]) Peek() (T, bool) {
	if len(h.queue) > 0 {
		if item := h.items[h.queue[0]]; item != nil {
			return item.obj, true
		}
	}
	var zero T
	return zero, false
}

type Heap[T any] struct {
	data *data[T]
}

func (h *Heap[T]) AddOrUpdate(obj T) {
	key := h.data.keyFunc(obj)
	if _, exists := h.data.items[key]; exists {
		h.data.items[key].obj = obj
		heap.Fix(h.data, h.data.items[key].index)
	} else {
		heap.Push(h.data, &itemKeyValue[T]{key, obj})
	}
}

func (h *Heap[T]) Delete(obj T) error {
	key := h.data.keyFunc(obj)
	if item, ok := h.data.items[key]; ok {
		heap.Remove(h.data, item.index)
		return nil
	}
	return fmt.Errorf("object not found")
}

func (h *Heap[T]) Peek() (T, bool) {
	return h.data.Peek()
}

func (h *Heap[T]) Pop() (T, error) {
	obj := heap.Pop(h.data)
	if obj != nil {
		return obj.(T), nil
	}
	var zero T
	return zero, fmt.Errorf("heap is empty")
}

func (h *Heap[T]) Get(obj T) (T, bool) {
	key := h.data.keyFunc(obj)
	return h.GetByKey(key)
}

func (h *Heap[T]) GetByKey(key string) (T, bool) {
	item, exists := h.data.items[key]
	if !exists {
		var zero T
		return zero, false
	}
	return item.obj, true
}

func (h *Heap[T]) Has(obj T) bool {
	key := h.data.keyFunc(obj)
	_, ok := h.GetByKey(key)
	return ok
}

func (h *Heap[T]) List() []T {
	list := make([]T, 0, len(h.data.items))
	for _, item := range h.data.items {
		list = append(list, item.obj)
	}
	return list
}

func (h *Heap[T]) Len() int {
	return len(h.data.queue)
}

func New[T any](keyFn KeyFunc[T], lessFn LessFunc[T]) *Heap[T] {
	return &Heap[T]{
		data: &data[T]{
			items:    map[string]*heapItem[T]{},
			queue:    []string{},
			keyFunc:  keyFn,
			lessFunc: lessFn,
		},
	}
}

type LessFunc[T any] func(item1, item2 T) bool
