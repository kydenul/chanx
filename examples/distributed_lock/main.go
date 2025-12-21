// Package main demonstrates the usage of distributed lock from chanx package.
//
// This example shows:
//   - Basic lock acquisition and release
//   - Using LockGuard for automatic cleanup
//   - TryAcquire with timeout and retry
//   - Concurrent access protection
//
// Prerequisites:
//   - Redis server running on localhost:6379
//
// Run with: go run main.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kydenul/chanx"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer func() {
		_ = client.Close()
	}()

	// Verify Redis connection
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		fmt.Printf("Failed to connect to Redis: %v\n", err)
		fmt.Println("Please ensure Redis is running on localhost:6379")
		os.Exit(1)
	}

	fmt.Println("=== Distributed Lock Examples ===")
	fmt.Println()

	// Example 1: Basic lock usage
	basicLockExample(ctx, client)

	// Example 2: Using LockGuard for automatic cleanup
	lockGuardExample(ctx, client)

	// Example 3: TryAcquire with timeout
	tryAcquireExample(ctx, client)

	// Example 4: Concurrent access protection
	concurrentAccessExample(ctx, client)

	// Example 5: Handling duplicate acquire error
	duplicateAcquireExample(ctx, client)

	fmt.Println("\n=== All examples completed ===")
}

// basicLockExample demonstrates basic lock acquisition and release
func basicLockExample(ctx context.Context, client *redis.Client) {
	fmt.Println("--- Example 1: Basic Lock Usage ---")

	// Create a logger for visibility
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create a distributed lock with custom options
	lock := chanx.NewDistributedLock(
		"example:basic:lock",
		client,
		chanx.WithTTL(10*time.Second),
		chanx.WithRenewInterval(3*time.Second),
		chanx.WithLogger(logger),
	)

	// Acquire the lock
	acquired, err := lock.Acquire(ctx)
	if err != nil {
		fmt.Printf("Error acquiring lock: %v\n", err)
		return
	}

	if !acquired {
		fmt.Println("Lock is held by another process")
		return
	}

	// Always release the lock when done
	//nolint:contextcheck // Release() intentionally uses background context for cleanup
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Printf("Warning: failed to release lock: %v\n", err)
		}
	}()

	// Do some work while holding the lock
	fmt.Println("Lock acquired! Doing critical work...")
	time.Sleep(2 * time.Second)
	fmt.Println("Work completed!")

	fmt.Println()
}

// lockGuardExample demonstrates using LockGuard for automatic cleanup
func lockGuardExample(ctx context.Context, client *redis.Client) {
	fmt.Println("--- Example 2: LockGuard Usage ---")

	lock := chanx.NewDistributedLock(
		"example:guard:lock",
		client,
		chanx.WithTTL(10*time.Second),
	)

	// LockGuard automatically acquires and releases the lock
	err := chanx.LockGuard(ctx, lock, func() error {
		fmt.Println("Executing protected function...")
		time.Sleep(1 * time.Second)
		fmt.Println("Protected function completed!")
		return nil
	})
	if err != nil {
		if err == chanx.ErrLockNotAcquired {
			fmt.Println("Could not acquire lock - resource is busy")
		} else {
			fmt.Printf("Error: %v\n", err)
		}
		return
	}

	fmt.Println("LockGuard completed successfully!")
	fmt.Println()
}

// tryAcquireExample demonstrates TryAcquire with timeout and retry
func tryAcquireExample(ctx context.Context, client *redis.Client) {
	fmt.Println("--- Example 3: TryAcquire with Timeout ---")

	// First, acquire a lock to simulate contention
	blocker := chanx.NewDistributedLock("example:try:lock", client)
	_, _ = blocker.Acquire(ctx)

	// Now try to acquire the same lock with timeout
	lock := chanx.NewDistributedLock(
		"example:try:lock",
		client,
		chanx.WithTTL(10*time.Second),
	)

	fmt.Println("Attempting to acquire lock with 2s timeout...")
	start := time.Now()

	// Release the blocker after 1 second in a goroutine
	//nolint:contextcheck // Release() intentionally uses background context for cleanup
	go func() {
		time.Sleep(1 * time.Second)
		_ = blocker.Release()
		fmt.Println("Blocker released the lock")
	}()

	// Try to acquire with retry
	acquired, err := lock.TryAcquire(ctx, 2*time.Second, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if acquired {
		fmt.Printf("Lock acquired after %v!\n", elapsed.Round(time.Millisecond))
		_ = lock.Release() //nolint:contextcheck // Release() intentionally uses background context
	} else {
		fmt.Printf("Failed to acquire lock after %v\n", elapsed.Round(time.Millisecond))
	}

	fmt.Println()
}

// concurrentAccessExample demonstrates protecting shared resources
func concurrentAccessExample(ctx context.Context, client *redis.Client) {
	fmt.Println("--- Example 4: Concurrent Access Protection ---")

	var counter int64
	var wg sync.WaitGroup
	numWorkers := 5
	incrementsPerWorker := 3

	fmt.Printf("Starting %d workers, each incrementing counter %d times\n",
		numWorkers, incrementsPerWorker)

	for i := range numWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < incrementsPerWorker; j++ {
				lock := chanx.NewDistributedLock(
					"example:counter:lock",
					client,
					chanx.WithTTL(5*time.Second),
				)

				err := chanx.LockGuardWithRetry(
					ctx, lock,
					5*time.Second, 50*time.Millisecond,
					func() error {
						// Simulate read-modify-write operation
						current := atomic.LoadInt64(&counter)
						time.Sleep(10 * time.Millisecond) // Simulate some work
						atomic.StoreInt64(&counter, current+1)
						fmt.Printf("Worker %d: counter = %d\n", workerID, current+1)
						return nil
					},
				)
				if err != nil {
					fmt.Printf("Worker %d: error - %v\n", workerID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	expected := int64(numWorkers * incrementsPerWorker)
	actual := atomic.LoadInt64(&counter)
	fmt.Printf("\nFinal counter value: %d (expected: %d)\n", actual, expected)

	if actual == expected {
		fmt.Println("✓ All increments were properly synchronized!")
	} else {
		fmt.Println("✗ Race condition detected!")
	}
}

// duplicateAcquireExample demonstrates handling duplicate acquire attempts
func duplicateAcquireExample(ctx context.Context, client *redis.Client) {
	fmt.Println("--- Example 5: Handling Duplicate Acquire ---")

	lock := chanx.NewDistributedLock(
		"example:duplicate:lock",
		client,
		chanx.WithTTL(10*time.Second),
	)

	// First acquire should succeed
	acquired, err := lock.Acquire(ctx)
	if err != nil {
		fmt.Printf("Error acquiring lock: %v\n", err)
		return
	}
	fmt.Printf("First acquire: acquired=%v\n", acquired)

	// Second acquire on the same instance should return ErrLockAlreadyHeld
	// This prevents goroutine leaks from multiple autoRenew goroutines
	acquired2, err := lock.Acquire(ctx)
	if err == chanx.ErrLockAlreadyHeld {
		fmt.Println("Second acquire correctly returned ErrLockAlreadyHeld")
		fmt.Println("This prevents goroutine leaks from multiple autoRenew goroutines")
	} else if err != nil {
		fmt.Printf("Unexpected error: %v\n", err)
	} else {
		fmt.Printf("Second acquire: acquired=%v (unexpected)\n", acquired2)
	}

	// Release the lock
	//nolint:contextcheck // Release() intentionally uses background context for cleanup
	if err := lock.Release(); err != nil {
		fmt.Printf("Error releasing lock: %v\n", err)
	}

	// After release, we can acquire again
	acquired3, err := lock.Acquire(ctx)
	if err != nil {
		fmt.Printf("Error re-acquiring lock: %v\n", err)
		return
	}
	fmt.Printf("Re-acquire after release: acquired=%v\n", acquired3)

	_ = lock.Release() //nolint:contextcheck // Release() intentionally uses background context
	fmt.Println()
}
