package chanx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Unit tests for WorkerPool

func TestWorkerPool_BasicTaskSubmission(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 2)
	assert.NoError(t, err)
	defer wp.Close()

	// Submit a simple task
	err = wp.Submit(Task[int]{
		Fn: func() (int, error) {
			return 42, nil
		},
	})
	assert.NoError(t, err)

	// Get result
	result := <-wp.Results()
	assert.NoError(t, result.Err)
	assert.Equal(t, 42, result.Value)
}

func TestWorkerPool_ZeroTasks(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 2)
	assert.NoError(t, err)

	// Close immediately without submitting tasks
	wp.Close()

	// Results channel should be closed
	_, ok := <-wp.Results()
	assert.False(t, ok, "Results channel should be closed")
}

func TestWorkerPool_SingleTask(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 3)
	assert.NoError(t, err)
	defer wp.Close()

	// Submit single task
	err = wp.Submit(Task[int]{
		Fn: func() (int, error) {
			return 100, nil
		},
	})
	assert.NoError(t, err)

	// Get result
	result := <-wp.Results()
	assert.NoError(t, result.Err)
	assert.Equal(t, 100, result.Value)
}

func TestWorkerPool_ManyTasks(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 5)
	assert.NoError(t, err)
	defer wp.Close()

	taskCount := 100

	// Start goroutine to drain results
	resultsDone := make(chan bool)
	go func() {
		for range taskCount {
			result := <-wp.Results()
			assert.NoError(t, result.Err)
		}
		resultsDone <- true
	}()

	// Submit many tasks
	for i := range taskCount {
		err := wp.Submit(Task[int]{
			Fn: func() (int, error) {
				return i, nil
			},
		})
		assert.NoError(t, err)
	}

	// Wait for all results
	<-resultsDone
}

func TestWorkerPool_TaskReturnsError(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 2)
	assert.NoError(t, err)
	defer wp.Close()

	// Submit task that returns error
	err = wp.Submit(Task[int]{
		Fn: func() (int, error) {
			return 0, assert.AnError
		},
	})
	assert.NoError(t, err)

	// Get result with error
	result := <-wp.Results()
	assert.Error(t, result.Err)
	assert.Equal(t, assert.AnError, result.Err)
}

func TestWorkerPool_MixedSuccessAndError(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 3)
	assert.NoError(t, err)
	defer wp.Close()

	taskCount := 10
	errorCount := 0
	successCount := 0

	// Start goroutine to collect results
	resultsDone := make(chan bool)
	go func() {
		for range taskCount {
			result := <-wp.Results()
			if result.Err != nil {
				errorCount++
			} else {
				successCount++
			}
		}
		resultsDone <- true
	}()

	// Submit mixed tasks
	for i := range taskCount {
		shouldError := i%2 == 0
		err := wp.Submit(Task[int]{
			Fn: func() (int, error) {
				if shouldError {
					return 0, assert.AnError
				}
				return i, nil
			},
		})
		assert.NoError(t, err)
	}

	// Wait for results
	<-resultsDone

	assert.Equal(t, 5, errorCount)
	assert.Equal(t, 5, successCount)
}

func TestWorkerPool_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 2)
	assert.NoError(t, err)

	// Start draining results
	go func() {
		for range wp.Results() {
		}
	}()

	// Submit a task
	err = wp.Submit(Task[int]{
		Fn: func() (int, error) {
			time.Sleep(10 * time.Millisecond)
			return 1, nil
		},
	})
	assert.NoError(t, err)

	// Cancel context
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Try to submit after cancellation
	err = wp.Submit(Task[int]{
		Fn: func() (int, error) {
			return 2, nil
		},
	})
	assert.Error(t, err)

	wp.Close()
}

func TestWorkerPool_ContextCancellationDuringExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewChanx[int]()
	wp, err := c.NewWorkerPool(ctx, 2)
	assert.NoError(t, err)

	// Start draining results
	go func() {
		for range wp.Results() {
		}
	}()

	// Submit long-running task
	err = wp.Submit(Task[int]{
		Fn: func() (int, error) {
			time.Sleep(100 * time.Millisecond)
			return 1, nil
		},
	})
	assert.NoError(t, err)

	// Cancel immediately
	cancel()

	// Close should still work
	wp.Close()
}

func TestWorkerPool_InvalidWorkerCount(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Test zero workers
	wp, err := c.NewWorkerPool(ctx, 0)
	assert.Error(t, err)
	assert.Nil(t, wp)

	// Test negative workers
	wp, err = c.NewWorkerPool(ctx, -5)
	assert.Error(t, err)
	assert.Nil(t, wp)
}
