package chanx

import (
	"context"
	"fmt"
	"sync"
)

// Task represents a unit of work to be executed by the worker pool
type Task[T any] struct {
	Fn func() (T, error)
}

// Result represents the result of a task execution
type Result[T any] struct {
	Value T
	Err   error
}

// WorkerPool manages a pool of workers that execute tasks concurrently
type WorkerPool[T any] struct {
	workerCount int
	taskChan    chan Task[T]
	resultChan  chan Result[T]
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewWorkerPool creates a new worker pool with the specified number of workers
func (c *Chanx[T]) NewWorkerPool(ctx context.Context, workerCount int) (*WorkerPool[T], error) {
	if workerCount <= 0 {
		return nil, fmt.Errorf("worker count must be greater than 0, got %d", workerCount)
	}

	poolCtx, cancel := context.WithCancel(ctx)

	wp := &WorkerPool[T]{
		workerCount: workerCount,
		taskChan:    make(chan Task[T]),
		resultChan:  make(chan Result[T]),
		ctx:         poolCtx,
		cancel:      cancel,
	}

	// Start workers
	for range workerCount {
		wp.wg.Add(1)
		go wp.worker()
	}

	return wp, nil
}

// worker is the goroutine that processes tasks from the task channel
func (wp *WorkerPool[T]) worker() {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.ctx.Done():
			return
		case task, ok := <-wp.taskChan:
			if !ok {
				return
			}
			value, err := task.Fn()
			select {
			case wp.resultChan <- Result[T]{Value: value, Err: err}:
			case <-wp.ctx.Done():
				return
			}
		}
	}
}

// Submit submits a task to the worker pool
func (wp *WorkerPool[T]) Submit(task Task[T]) error {
	select {
	case <-wp.ctx.Done():
		return fmt.Errorf("worker pool is closed")
	case wp.taskChan <- task:
		return nil
	}
}

// Results returns the result channel for reading task results
func (wp *WorkerPool[T]) Results() <-chan Result[T] {
	return wp.resultChan
}

// Close gracefully shuts down the worker pool, waiting for all tasks to complete
func (wp *WorkerPool[T]) Close() {
	close(wp.taskChan)
	wp.wg.Wait()
	close(wp.resultChan)
	wp.cancel()
}
