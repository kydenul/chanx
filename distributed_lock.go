package chanx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Default configuration values
const (
	// DefaultLockTTL is the default TTL for distributed locks (30 seconds)
	DefaultLockTTL = 30 * time.Second

	// DefaultRenewInterval is how often to renew the lock (10 seconds, 1/3 of TTL)
	DefaultRenewInterval = 10 * time.Second

	// DefaultAcquireTimeout is the default timeout for acquiring a lock
	DefaultAcquireTimeout = 0 // 0 means no retry, fail immediately

	// DefaultRetryInterval is the interval between acquire retries
	DefaultRetryInterval = 100 * time.Millisecond

	// DefaultMaxConsecutiveFailures is the default number of consecutive renewal failures
	// before the lock is considered lost
	DefaultMaxConsecutiveFailures = 3
)

// Error type constants for detailed error handling
var (
	// ErrLockNotAcquired indicates that the lock could not be acquired (already held by another process)
	ErrLockNotAcquired = errors.New("lock not acquired")

	// ErrLockAcquireFailed indicates that lock acquisition failed due to an error
	ErrLockAcquireFailed = errors.New("lock acquire failed")

	// ErrLockReleaseFailed indicates that lock release failed due to an error
	ErrLockReleaseFailed = errors.New("lock release failed")

	// ErrLockNotHeld indicates that the lock is not held by this instance (wrong value or expired)
	ErrLockNotHeld = errors.New("lock not held by this instance")

	// ErrLockRenewFailed indicates that lock renewal failed
	ErrLockRenewFailed = errors.New("lock renew failed")

	// ErrNilRedisClient indicates that the Redis client is nil
	ErrNilRedisClient = errors.New("redis client is nil")

	// ErrEmptyLockKey indicates that the lock key is empty
	ErrEmptyLockKey = errors.New("lock key is empty")

	// ErrInvalidTTL indicates that the TTL value is invalid
	ErrInvalidTTL = errors.New("TTL must be positive")

	// ErrLockAlreadyHeld indicates that the lock is already held by this instance
	ErrLockAlreadyHeld = errors.New("lock already held by this instance")

	// ErrInvalidRenewInterval indicates that the renew interval is invalid
	ErrInvalidRenewInterval = errors.New("renew interval must be less than TTL")

	// ErrLockLostRenewalFailure indicates that the lock was lost due to consecutive renewal failures
	ErrLockLostRenewalFailure = errors.New("lock lost due to consecutive renewal failures")

	// ErrLockLostTakenByAnother indicates that the lock was lost because another process took it
	ErrLockLostTakenByAnother = errors.New("lock lost - taken by another process")
)

// Lua scripts for atomic operations
var (
	// releaseLockScript atomically releases the lock only if the value matches
	// This prevents releasing a lock held by another process
	releaseLockScript = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`)

	// renewLockScript atomically renews the lock only if the value matches
	// This prevents renewing a lock that has been taken by another process
	renewLockScript = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("PEXPIRE", KEYS[1], ARGV[2])
		else
			return 0
		end
	`)
)

// Logger defines the logging interface for the distributed lock.
// Users can implement this interface to integrate with their preferred logging framework.
//
// Example implementations:
//   - log/slog: Use slog.Logger directly (it implements these methods)
//   - zap: Wrap zap.SugaredLogger
//   - logrus: Wrap logrus.Entry
//   - zerolog: Wrap zerolog.Logger
type Logger interface {
	// Info logs an informational message with optional key-value pairs
	Info(msg string, args ...any)

	// Error logs an error message with optional key-value pairs
	Error(msg string, args ...any)

	// Debug logs a debug message with optional key-value pairs
	Debug(msg string, args ...any)
}

// DiscardLogger is a no-operation logger that discards all log messages
type DiscardLogger struct{}

func (DiscardLogger) Info(string, ...any)  {}
func (DiscardLogger) Error(string, ...any) {}
func (DiscardLogger) Debug(string, ...any) {}

// LockLostCallback is called when the lock is lost due to renewal failures or expiration.
// The callback receives the lock key and the reason for the loss.
// This callback is invoked from the auto-renewal goroutine, so it should be non-blocking.
type LockLostCallback func(key string, reason error)

// DistributedLock represents a distributed lock with auto-renewal capability.
// It uses Redis as the backend for coordination across multiple processes.
//
// The lock implements the following safety guarantees:
//   - Mutual exclusion: Only one process can hold the lock at a time
//   - Deadlock-free: Lock automatically expires if holder crashes (via TTL)
//   - Fault tolerance: Auto-renewal keeps lock alive during long operations
//   - Identity verification: Only the lock holder can release or renew the lock
//
// Common Pitfalls:
//   - Not calling Release() will hold the lock until TTL expires
//   - Network partitions may cause lock to expire unexpectedly
//   - Clock drift between Redis and client may affect timing
//   - Using too short TTL may cause premature expiration
//
// Best Practices:
//   - Always use defer lock.Release() after successful acquisition
//   - Set TTL to at least 3x the expected operation duration
//   - Monitor renewal failures in production
//   - Consider using LockGuard for automatic cleanup
//   - Use unique identifiers (UUID) for lock values
//   - Use WithLockLostCallback to handle lock loss events
type DistributedLock struct {
	// Dependencies
	log   Logger
	redis redis.Cmdable

	// Lock configuration
	key   string
	value string
	ttl   time.Duration

	// Renewal configuration
	renewInterval          time.Duration
	maxConsecutiveFailures int // Maximum consecutive renewal failures before considering lock lost

	// Lifecycle management - use channel for cancellation instead of context
	stopRenew   chan struct{}
	stopped     chan struct{}
	acquired    atomic.Bool // Use atomic.Bool to prevent data race
	releaseOnce sync.Once   // Ensures Release() is safe for concurrent calls

	// Callback for lock loss notification
	onLockLost LockLostCallback
}

// LockOption is a functional option for configuring a DistributedLock
type LockOption func(*DistributedLock)

// WithTTL sets the lock TTL (time-to-live).
// The lock will automatically expire after this duration if not renewed.
//
// Default: 30 seconds
//
//	Minimum recommended: 10 seconds (to allow for network latency)
//
// Example:
//
//	lock := NewDistributedLock("my-lock", client, WithTTL(60*time.Second))
func WithTTL(ttl time.Duration) LockOption {
	return func(l *DistributedLock) {
		if ttl > 0 {
			l.ttl = ttl
		}
	}
}

// WithRenewInterval sets the interval for automatic lock renewal.
// Should be less than TTL to ensure lock doesn't expire during renewal.
//
// Default: 10 seconds (1/3 of default TTL)
//
//	Recommended: Set to 1/3 of TTL for safety margin
//
// Example:
//
//	lock := NewDistributedLock("my-lock", client,
//	    WithTTL(60*time.Second),
//	    WithRenewInterval(20*time.Second))
func WithRenewInterval(interval time.Duration) LockOption {
	return func(l *DistributedLock) {
		if interval > 0 {
			l.renewInterval = interval
		}
	}
}

// WithValue sets a custom value for the lock.
// By default, a UUID is generated to uniquely identify this lock holder.
//
// Use cases:
//   - Setting a process ID for debugging
//   - Using a deterministic value for testing
//   - Implementing lock inheritance between processes
//
// Example:
//
//	lock := NewDistributedLock("my-lock", client, WithValue("worker-1"))
func WithValue(value string) LockOption {
	return func(l *DistributedLock) {
		if value != "" {
			l.value = value
		}
	}
}

// WithLogger sets a custom logger for the lock.
// By default, a no-op logger is used that discards all messages.
//
// The logger should implement the Logger interface with Info, Error, and Debug methods.
//
// Example with slog:
//
//	logger := slog.Default()
//	lock := NewDistributedLock("my-lock", client, WithLogger(logger))
func WithLogger(log Logger) LockOption {
	return func(l *DistributedLock) {
		if log != nil {
			l.log = log
		}
	}
}

// WithLockLostCallback sets a callback function that is invoked when the lock is lost
// due to consecutive renewal failures or when another process takes the lock.
//
// This is useful for:
//   - Gracefully stopping work when lock is lost
//   - Alerting/monitoring lock loss events
//   - Triggering cleanup operations
//
// Note: The callback is invoked from the auto-renewal goroutine, so it should be
// non-blocking or spawn its own goroutine for long-running operations.
//
// Example:
//
//	lock := NewDistributedLock("my-lock", client,
//	    WithLockLostCallback(func(key string, reason error) {
//	        log.Printf("Lock %s lost: %v", key, reason)
//	        cancel() // Cancel the context to stop work
//	    }))
func WithLockLostCallback(callback LockLostCallback) LockOption {
	return func(l *DistributedLock) {
		if callback != nil {
			l.onLockLost = callback
		}
	}
}

// WithMaxConsecutiveFailures sets the maximum number of consecutive renewal failures
// before the lock is considered lost. Default is 3.
//
// Example:
//
//	lock := NewDistributedLock("my-lock", client, WithMaxConsecutiveFailures(5))
func WithMaxConsecutiveFailures(max int) LockOption {
	return func(l *DistributedLock) {
		if max > 0 {
			l.maxConsecutiveFailures = max
		}
	}
}

// NewDistributedLock creates a new distributed lock instance.
//
// Parameters:
//   - key: The Redis key used for the lock. Should be unique per resource being protected.
//   - client: A Redis client (can be *redis.Client, *redis.ClusterClient, or any redis.Cmdable)
//   - opts: Optional configuration options (WithTTL, WithRenewInterval, WithValue, WithLogger)
//
// Time Complexity: O(1) for creation
// Space Complexity: O(1)
// Goroutines: Does not create goroutines until Acquire() is called
//
// Example:
//
//	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
//	lock := NewDistributedLock("resource:123:lock", client,
//	    WithTTL(30*time.Second),
//	    WithLogger(slog.Default()))
//
//	acquired, err := lock.Acquire(ctx)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if acquired {
//	    defer lock.Release()
//	    // Do work while holding the lock
//	}
func NewDistributedLock(key string, client redis.Cmdable, opts ...LockOption) *DistributedLock {
	l := &DistributedLock{
		log:   DiscardLogger{},
		redis: client,

		key:   key,
		value: uuid.New().String(),
		ttl:   DefaultLockTTL,

		renewInterval:          DefaultRenewInterval,
		maxConsecutiveFailures: DefaultMaxConsecutiveFailures,
	}

	for _, opt := range opts {
		opt(l)
	}

	// Validate renewInterval < TTL (best practice)
	if l.renewInterval >= l.ttl {
		// Auto-correct to 1/3 of TTL for safety
		l.renewInterval = max(l.ttl/3, time.Millisecond)
	}

	return l
}

// Acquire attempts to acquire the distributed lock.
// Returns true if lock was acquired successfully, false if already held by another process.
//
// The acquisition is atomic using Redis SETNX (SET if Not eXists) with expiration.
// Upon successful acquisition, a background goroutine is started to automatically
// renew the lock before it expires.
//
// Parameters:
//   - ctx: Context for cancellation. If cancelled, acquisition attempt stops immediately.
//
// Time Complexity: O(1) for Redis operation
// Space Complexity: O(1)
// Goroutines: Creates 1 goroutine for auto-renewal if lock is acquired
//
// Common Pitfalls:
//   - Calling Acquire multiple times without Release may cause unexpected behavior
//   - Context cancellation does NOT release an already-acquired lock
//   - Network errors are returned as errors, not as acquired=false
//
// Best Practices:
//   - Always check both return values (acquired and error)
//   - Use defer Release() immediately after successful acquisition
//   - Handle the case where lock is not acquired (another process holds it)
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//
//	acquired, err := lock.Acquire(ctx)
//	if err != nil {
//	    return fmt.Errorf("lock acquisition error: %w", err)
//	}
//	if !acquired {
//	    return fmt.Errorf("resource is busy, please try again later")
//	}
//	defer lock.Release()
//
//	// Critical section - only one process executes this at a time
//	doExpensiveOperation()
func (l *DistributedLock) Acquire(ctx context.Context) (bool, error) {
	if l.redis == nil {
		l.log.Error("redis client is nil", "key", l.key)
		return false, ErrNilRedisClient
	}
	if l.key == "" {
		l.log.Error("lock key is empty", "key", l.key)
		return false, ErrEmptyLockKey
	}

	// Check if lock is already held by this instance to prevent goroutine leak
	if l.acquired.Load() {
		// Optionally verify the lock still exists in Redis
		// This helps users understand if the lock has expired
		val, err := l.redis.Get(ctx, l.key).Result()
		if err == redis.Nil {
			// Lock expired in Redis but local state says held
			// This is an inconsistent state - the lock was lost
			l.log.Error("lock state inconsistent - local held but expired in Redis", "key", l.key)
			l.acquired.Store(false)
			// Notify callback if set
			if l.onLockLost != nil {
				l.onLockLost(l.key, ErrLockLostTakenByAnother)
			}
			// Allow re-acquisition by falling through
		} else if err != nil {
			// Redis error - can't verify, return the original error
			return false, ErrLockAlreadyHeld
		} else if val == l.value {
			// Lock is still held by us in Redis
			return false, ErrLockAlreadyHeld
		} else {
			// Lock was taken by another process
			l.log.Error("lock state inconsistent - taken by another process", "key", l.key)
			l.acquired.Store(false)
			if l.onLockLost != nil {
				l.onLockLost(l.key, ErrLockLostTakenByAnother)
			}
			// Allow re-acquisition by falling through
		}
	}

	// Attempt to acquire lock with SETNX and expiration
	acquired, err := l.redis.SetNX(ctx, l.key, l.value, l.ttl).Result()
	if err != nil {
		l.log.Error("failed to acquire lock", "key", l.key, "error", err)
		return false, fmt.Errorf("%w: %v", ErrLockAcquireFailed, err)
	}

	if !acquired {
		l.log.Debug("lock already held by another process", "key", l.key)
		return false, nil
	}

	l.acquired.Store(true)
	l.releaseOnce = sync.Once{} // Reset for new acquisition
	l.stopRenew = make(chan struct{})
	l.stopped = make(chan struct{})
	l.log.Info("lock acquired successfully", "key", l.key, "ttl", l.ttl, "value", l.value)

	// Start auto-renewal goroutine
	// Note: autoRenew intentionally uses its own lifecycle (stopRenew channel) instead of
	// the caller's context, because lock renewal should continue even if the acquisition
	// context is cancelled. The lock should only stop renewing when Release() is called.
	go l.autoRenew() //nolint:contextcheck

	return true, nil
}

// TryAcquire attempts to acquire the lock with retries until timeout.
// This is useful when you want to wait for a lock to become available.
//
// Parameters:
//   - ctx: Context for cancellation
//   - timeout: Maximum time to wait for lock acquisition
//   - retryInterval: Time to wait between retry attempts
//
// Time Complexity: O(n) where n is timeout/retryInterval attempts
// Space Complexity: O(1)
// Goroutines: Creates 1 goroutine for auto-renewal if lock is acquired
//
// Example:
//
//	acquired, err := lock.TryAcquire(ctx, 10*time.Second, 100*time.Millisecond)
//	if err != nil {
//	    return err
//	}
//	if acquired {
//	    defer lock.Release()
//	    // Do work
//	}
func (l *DistributedLock) TryAcquire(
	ctx context.Context,
	timeout, retryInterval time.Duration,
) (bool, error) {
	if timeout <= 0 {
		return l.Acquire(ctx)
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	// First attempt
	acquired, err := l.Acquire(ctx)
	if err != nil || acquired {
		return acquired, err
	}

	// Retry loop
	for {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("%w: %v", ErrLockAcquireFailed, ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				l.log.Debug("lock acquisition timeout", "key", l.key, "timeout", timeout)
				return false, nil
			}

			acquired, err := l.Acquire(ctx)
			if err != nil || acquired {
				return acquired, err
			}
		}
	}
}

// autoRenew automatically renews the lock before it expires.
// This goroutine runs until the lock is released or stop signal is received.
//
// The renewal uses a Lua script to atomically check ownership and extend TTL,
// preventing renewal of a lock that has been taken by another process.
func (l *DistributedLock) autoRenew() {
	ticker := time.NewTicker(l.renewInterval)
	defer ticker.Stop()
	defer close(l.stopped)

	l.log.Debug("lock auto-renewal started", "key", l.key, "interval", l.renewInterval)

	consecutiveFailures := 0

	for {
		select {
		case <-ticker.C:
			// Renew the lock atomically using Lua script
			// Use background context for renewal as it's an internal operation
			ttlMs := l.ttl.Milliseconds()
			result, err := renewLockScript.Run(
				context.Background(),
				l.redis,
				[]string{l.key},
				l.value,
				ttlMs,
			).Int()
			if err != nil {
				consecutiveFailures++
				l.log.Error("failed to renew lock",
					"key", l.key,
					"error", err,
					"consecutiveFailures", consecutiveFailures)
				// If too many consecutive failures, consider lock lost
				if consecutiveFailures >= l.maxConsecutiveFailures {
					l.log.Error("too many consecutive renewal failures, considering lock lost",
						"key", l.key,
						"failures", consecutiveFailures)
					l.acquired.Store(false)
					// Notify callback if set
					if l.onLockLost != nil {
						l.onLockLost(l.key, ErrLockLostRenewalFailure)
					}
					return
				}
				continue
			}

			// Reset failure counter on success
			consecutiveFailures = 0

			if result == 0 {
				// Lock was taken by another process or expired
				l.log.Error("lock renewal failed - lock not held", "key", l.key)
				l.acquired.Store(false)
				// Notify callback if set
				if l.onLockLost != nil {
					l.onLockLost(l.key, ErrLockLostTakenByAnother)
				}
				return
			}

			l.log.Debug("lock renewed successfully", "key", l.key, "ttl", l.ttl)

		case <-l.stopRenew:
			l.log.Debug("lock auto-renewal stopped", "key", l.key)
			return
		}
	}
}

// Release releases the distributed lock.
// This operation is atomic - it only releases the lock if this instance holds it.
//
// The release uses a Lua script to atomically check ownership and delete the key,
// preventing accidental release of a lock held by another process.
//
// Time Complexity: O(1) for Redis operation
// Space Complexity: O(1)
// Goroutines: Stops the auto-renewal goroutine
//
// Common Pitfalls:
//   - Calling Release without Acquire returns nil (no-op)
//   - Release after lock expiration returns ErrLockNotHeld
//   - Network errors during release may leave lock in Redis until TTL
//
// Best Practices:
//   - Always use defer Release() after successful Acquire
//   - Log release errors but don't necessarily fail the operation
//   - The lock will auto-expire anyway due to TTL
//
// Example:
//
//	acquired, err := lock.Acquire(ctx)
//	if err != nil {
//	    return err
//	}
//	if acquired {
//	    defer func() {
//	        if err := lock.Release(); err != nil {
//	            log.Printf("Warning: failed to release lock: %v", err)
//	        }
//	    }()
//	    // Do work
//	}
func (l *DistributedLock) Release() error {
	if !l.acquired.Load() {
		// Lock was never acquired, nothing to release
		return nil
	}

	var releaseErr error
	l.releaseOnce.Do(func() {
		// Stop the auto-renewal goroutine
		close(l.stopRenew)

		// Wait for the goroutine to finish with dynamic timeout
		// Use 2x renewInterval or minimum 500ms to ensure goroutine has time to stop
		// Cap at 5 seconds to avoid excessive wait
		waitTimeout := min(max(l.renewInterval*2, 500*time.Millisecond), 5*time.Second)

		select {
		case <-l.stopped:
			l.log.Debug("auto-renewal goroutine stopped", "key", l.key)
		case <-time.After(waitTimeout):
			l.log.Error("timeout waiting for auto-renewal goroutine to stop",
				"key", l.key, "timeout", waitTimeout)
		}

		// Release the lock atomically using Lua script
		// Use background context for cleanup operations
		result, err := releaseLockScript.Run(context.Background(), l.redis,
			[]string{l.key}, l.value).Int()
		if err != nil {
			l.log.Error("failed to release lock", "key", l.key, "error", err)
			releaseErr = fmt.Errorf("%w: key=%s, %v", ErrLockReleaseFailed, l.key, err)
			return
		}

		if result == 0 {
			// Lock was not held by us (expired or taken by another process)
			l.log.Error("lock was not held by this instance", "key", l.key, "value", l.value)
			releaseErr = ErrLockNotHeld
			return
		}

		l.acquired.Store(false)
		l.log.Info("lock released successfully", "key", l.key)
	})

	return releaseErr
}

// IsHeld returns whether this lock instance currently holds the lock.
// Note: This is a local check and doesn't verify with Redis.
// The actual lock state in Redis may differ due to expiration.
func (l *DistributedLock) IsHeld() bool { return l.acquired.Load() }

// Key returns the Redis key used for this lock.
func (l *DistributedLock) Key() string { return l.key }

// Value returns the unique value identifying this lock holder.
func (l *DistributedLock) Value() string { return l.value }

// LockGuard is a helper function that acquires a lock, executes a function, and releases the lock.
// This ensures the lock is always released even if the function panics.
//
// Parameters:
//   - ctx: Context for lock acquisition
//   - lock: The distributed lock to use
//   - fn: The function to execute while holding the lock
//
// Time Complexity: O(1) for lock operations + O(f) for fn execution
// Space Complexity: O(1) + O(s) where s is fn's space usage
// Goroutines: Creates 1 goroutine for auto-renewal during execution
//
// Common Pitfalls:
//   - If fn panics, the lock is released but the panic is re-raised
//   - Context cancellation only affects lock acquisition, not fn execution
//   - Long-running fn should check context periodically
//
// Best Practices:
//   - Use for short, critical sections
//   - Pass context to fn if it needs cancellation support
//   - Handle ErrLockNotAcquired to implement retry logic
//   - Consider timeout on ctx for bounded wait time
//
// Example:
//
//	lock := NewDistributedLock("order:123:lock", redisClient)
//	err := LockGuard(ctx, lock, func() error {
//	    // This code runs with the lock held
//	    return processOrder(orderID)
//	})
//	if errors.Is(err, ErrLockNotAcquired) {
//	    // Another process is handling this order
//	    return nil
func LockGuard(ctx context.Context, lock *DistributedLock, fn func() error) error {
	// Acquire the lock
	acquired, err := lock.Acquire(ctx)
	if err != nil {
		lock.log.Error("failed to acquire lock", "error", err)
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	if !acquired {
		lock.log.Error("lock not acquired")
		return ErrLockNotAcquired
	}

	// Ensure lock is released even if function panics
	// Note: Release() intentionally doesn't take context because cleanup should always
	// complete regardless of context cancellation state
	defer func() {
		//nolint:contextcheck // Release() intentionally uses background context for cleanup
		if releaseErr := lock.Release(); releaseErr != nil {
			lock.log.Error("failed to release lock in defer", "error", releaseErr)
		}
	}()

	// Execute the protected function
	return fn()
}

// LockGuardWithRetry is like LockGuard but retries lock acquisition until timeout.
//
// Parameters:
//   - ctx: Context for cancellation
//   - lock: The distributed lock to use
//   - timeout: Maximum time to wait for lock acquisition
//   - retryInterval: Time to wait between retry attempts
//   - fn: The function to execute while holding the lock
//
// Example:
//
//	err := LockGuardWithRetry(ctx, lock, 10*time.Second, 100*time.Millisecond, func() error {
//	    return processOrder(orderID)
//	})
func LockGuardWithRetry(
	ctx context.Context,
	lock *DistributedLock,
	timeout, retryInterval time.Duration,
	fn func() error,
) error {
	// Acquire the lock with retries
	acquired, err := lock.TryAcquire(ctx, timeout, retryInterval)
	if err != nil {
		lock.log.Error("failed to acquire lock", "error", err)
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	if !acquired {
		lock.log.Error("lock not acquired")
		return ErrLockNotAcquired
	}

	// Ensure lock is released even if function panics
	// Note: Release() intentionally doesn't take context because cleanup should always
	// complete regardless of context cancellation state
	defer func() {
		//nolint:contextcheck // Release() intentionally uses background context for cleanup
		if releaseErr := lock.Release(); releaseErr != nil {
			lock.log.Error("failed to release lock in defer", "error", releaseErr)
		}
	}()

	// Execute the protected function
	return fn()
}
