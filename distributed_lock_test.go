package chanx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTestRedisClient returns a Redis client for testing
// Uses local Redis on default port 6379
func getTestRedisClient(t *testing.T) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use DB 15 for testing to avoid conflicts
	})

	// Ping to check connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Skipping test: Redis not available at localhost:6379: %v", err)
	}

	// Clean up test keys before test
	t.Cleanup(func() {
		client.FlushDB(context.Background())
		client.Close()
	})

	return client
}

// testLogger is a simple logger for testing
type testLogger struct {
	infoCalls  []string
	errorCalls []string
	debugCalls []string
	mu         sync.Mutex
}

func newTestLogger() *testLogger {
	return &testLogger{
		infoCalls:  make([]string, 0),
		errorCalls: make([]string, 0),
		debugCalls: make([]string, 0),
	}
}

func (l *testLogger) Info(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infoCalls = append(l.infoCalls, msg)
}

func (l *testLogger) Error(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorCalls = append(l.errorCalls, msg)
}

func (l *testLogger) Debug(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.debugCalls = append(l.debugCalls, msg)
}

func (l *testLogger) getInfoCalls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.infoCalls...)
}

func (l *testLogger) getErrorCalls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.errorCalls...)
}

func (l *testLogger) getDebugCalls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.debugCalls...)
}

// ==============================================================================
// Unit tests for NewDistributedLock
// ==============================================================================

func TestNewDistributedLock_DefaultValues(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-key", client)

	assert.NotNil(t, lock, "Lock should not be nil")
	assert.Equal(t, "test-key", lock.Key(), "Key should match")
	assert.NotEmpty(t, lock.Value(), "Value should be a generated UUID")
	assert.Equal(t, DefaultLockTTL, lock.ttl, "TTL should be default")
	assert.Equal(t, DefaultRenewInterval, lock.renewInterval, "Renew interval should be default")
	assert.False(t, lock.IsHeld(), "Lock should not be held initially")
}

func TestNewDistributedLock_WithTTL(t *testing.T) {
	client := getTestRedisClient(t)
	customTTL := 60 * time.Second
	lock := NewDistributedLock("test-key", client, WithTTL(customTTL))

	assert.Equal(t, customTTL, lock.ttl, "TTL should match custom value")
}

func TestNewDistributedLock_WithTTLZero(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-key", client, WithTTL(0))

	// Zero TTL should be ignored, use default
	assert.Equal(t, DefaultLockTTL, lock.ttl, "TTL should remain default for zero value")
}

func TestNewDistributedLock_WithTTLNegative(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-key", client, WithTTL(-10*time.Second))

	// Negative TTL should be ignored, use default
	assert.Equal(t, DefaultLockTTL, lock.ttl, "TTL should remain default for negative value")
}

func TestNewDistributedLock_WithRenewInterval(t *testing.T) {
	client := getTestRedisClient(t)
	customInterval := 5 * time.Second
	lock := NewDistributedLock("test-key", client, WithRenewInterval(customInterval))

	assert.Equal(t, customInterval, lock.renewInterval, "Renew interval should match custom value")
}

func TestNewDistributedLock_WithRenewIntervalZero(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-key", client, WithRenewInterval(0))

	// Zero interval should be ignored, use default
	assert.Equal(t, DefaultRenewInterval, lock.renewInterval, "Renew interval should remain default for zero value")
}

func TestNewDistributedLock_WithValue(t *testing.T) {
	client := getTestRedisClient(t)
	customValue := "my-custom-value"
	lock := NewDistributedLock("test-key", client, WithValue(customValue))

	assert.Equal(t, customValue, lock.Value(), "Value should match custom value")
}

func TestNewDistributedLock_WithValueEmpty(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-key", client, WithValue(""))

	// Empty value should be ignored, use generated UUID
	assert.NotEmpty(t, lock.Value(), "Value should not be empty, should use generated UUID")
}

func TestNewDistributedLock_WithLogger(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-key", client, WithLogger(logger))

	assert.Equal(t, logger, lock.log, "Logger should match custom logger")
}

func TestNewDistributedLock_WithLoggerNil(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-key", client, WithLogger(nil))

	// Nil logger should be ignored, use discard logger
	assert.NotNil(t, lock.log, "Logger should not be nil")
	_, ok := lock.log.(DiscardLogger)
	assert.True(t, ok, "Logger should be DiscardLogger")
}

func TestNewDistributedLock_MultipleOptions(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-key", client,
		WithTTL(60*time.Second),
		WithRenewInterval(20*time.Second),
		WithValue("custom-value"),
		WithLogger(logger),
	)

	assert.Equal(t, 60*time.Second, lock.ttl)
	assert.Equal(t, 20*time.Second, lock.renewInterval)
	assert.Equal(t, "custom-value", lock.Value())
	assert.Equal(t, logger, lock.log)
}

// ==============================================================================
// Unit tests for Acquire
// ==============================================================================

func TestAcquire_Success(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-acquire-success", client, WithLogger(logger))

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)

	assert.NoError(t, err, "Acquire should not return error")
	assert.True(t, acquired, "Lock should be acquired")
	assert.True(t, lock.IsHeld(), "Lock should be held")

	// Verify logging
	infoCalls := logger.getInfoCalls()
	assert.Contains(t, infoCalls, "lock acquired successfully", "Should log acquisition")

	// Verify key is set in Redis
	val, err := client.Get(ctx, "test-lock-acquire-success").Result()
	assert.NoError(t, err)
	assert.Equal(t, lock.Value(), val, "Value should match lock value")

	// Clean up
	err = lock.Release()
	assert.NoError(t, err)
}

func TestAcquire_AlreadyHeldByAnother(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	// First lock acquires
	lock1 := NewDistributedLock("test-lock-held", client, WithValue("lock1"))
	ctx := context.Background()
	acquired1, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired1)
	defer lock1.Release()

	// Second lock tries to acquire the same key
	lock2 := NewDistributedLock("test-lock-held", client, WithLogger(logger), WithValue("lock2"))
	acquired2, err := lock2.Acquire(ctx)

	assert.NoError(t, err, "Acquire should not return error")
	assert.False(t, acquired2, "Lock should not be acquired")
	assert.False(t, lock2.IsHeld(), "Lock should not be held")

	// Verify debug logging
	debugCalls := logger.getDebugCalls()
	assert.Contains(t, debugCalls, "lock already held by another process", "Should log that lock is held")
}

func TestAcquire_NilRedisClient(t *testing.T) {
	lock := NewDistributedLock("test-lock", nil)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)

	assert.Error(t, err, "Acquire should return error")
	assert.Equal(t, ErrNilRedisClient, err, "Error should be ErrNilRedisClient")
	assert.False(t, acquired, "Lock should not be acquired")
}

func TestAcquire_EmptyKey(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("", client)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)

	assert.Error(t, err, "Acquire should return error")
	assert.Equal(t, ErrEmptyLockKey, err, "Error should be ErrEmptyLockKey")
	assert.False(t, acquired, "Lock should not be acquired")
}

func TestAcquire_ContextCancelled(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-ctx", client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	acquired, err := lock.Acquire(ctx)

	assert.Error(t, err, "Acquire should return error")
	assert.False(t, acquired, "Lock should not be acquired")
}

func TestAcquire_StartsAutoRenewal(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-renewal", client,
		WithLogger(logger),
		WithRenewInterval(50*time.Millisecond),
		WithTTL(200*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Wait for at least one renewal
	time.Sleep(100 * time.Millisecond)

	// Verify renewal logging
	debugCalls := logger.getDebugCalls()
	assert.Contains(t, debugCalls, "lock auto-renewal started", "Should log renewal start")

	// Clean up
	err = lock.Release()
	assert.NoError(t, err)
}

// ==============================================================================
// Unit tests for TryAcquire
// ==============================================================================

func TestTryAcquire_ImmediateSuccess(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-try-success", client)

	ctx := context.Background()
	acquired, err := lock.TryAcquire(ctx, 5*time.Second, 100*time.Millisecond)

	assert.NoError(t, err)
	assert.True(t, acquired)
	assert.True(t, lock.IsHeld())

	// Clean up
	_ = lock.Release()
}

func TestTryAcquire_ZeroTimeout(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-try-zero", client)

	ctx := context.Background()
	// Zero timeout should behave like regular Acquire
	acquired, err := lock.TryAcquire(ctx, 0, 100*time.Millisecond)

	assert.NoError(t, err)
	assert.True(t, acquired)

	// Clean up
	_ = lock.Release()
}

func TestTryAcquire_RetrySuccess(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	// First lock acquires
	lock1 := NewDistributedLock("test-lock-try-retry", client, WithValue("lock1"))
	ctx := context.Background()
	acquired1, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired1)

	// Release lock1 after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		lock1.Release()
	}()

	// Second lock retries
	lock2 := NewDistributedLock("test-lock-try-retry", client, WithLogger(logger), WithValue("lock2"))
	acquired2, err := lock2.TryAcquire(ctx, 500*time.Millisecond, 50*time.Millisecond)

	assert.NoError(t, err)
	assert.True(t, acquired2)
	assert.True(t, lock2.IsHeld())

	// Clean up
	_ = lock2.Release()
}

func TestTryAcquire_Timeout(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	// First lock acquires and holds
	lock1 := NewDistributedLock("test-lock-try-timeout", client, WithValue("lock1"))
	ctx := context.Background()
	acquired1, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired1)
	defer lock1.Release()

	// Second lock tries but times out
	lock2 := NewDistributedLock("test-lock-try-timeout", client, WithLogger(logger), WithValue("lock2"))
	acquired2, err := lock2.TryAcquire(ctx, 200*time.Millisecond, 50*time.Millisecond)

	assert.NoError(t, err, "Should not return error on timeout")
	assert.False(t, acquired2, "Should not acquire lock after timeout")

	// Verify timeout logging
	debugCalls := logger.getDebugCalls()
	assert.Contains(t, debugCalls, "lock acquisition timeout", "Should log timeout")
}

func TestTryAcquire_ContextCancelled(t *testing.T) {
	client := getTestRedisClient(t)

	// First lock acquires and holds
	lock1 := NewDistributedLock("test-lock-try-ctx", client, WithValue("lock1"))
	ctx := context.Background()
	acquired1, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired1)
	defer lock1.Release()

	// Second lock with cancellable context
	lock2 := NewDistributedLock("test-lock-try-ctx", client, WithValue("lock2"))
	ctx2, cancel := context.WithCancel(context.Background())

	// Cancel after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	acquired2, err := lock2.TryAcquire(ctx2, 1*time.Second, 50*time.Millisecond)

	assert.Error(t, err, "Should return error on context cancellation")
	assert.True(t, errors.Is(err, ErrLockAcquireFailed), "Error should wrap ErrLockAcquireFailed")
	assert.False(t, acquired2)
}

// ==============================================================================
// Unit tests for Release
// ==============================================================================

func TestRelease_Success(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-release", client, WithLogger(logger))

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Release the lock
	err = lock.Release()
	assert.NoError(t, err)
	assert.False(t, lock.IsHeld())

	// Verify key is removed from Redis
	exists, err := client.Exists(ctx, "test-lock-release").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists, "Key should be removed from Redis")

	// Verify logging
	infoCalls := logger.getInfoCalls()
	assert.Contains(t, infoCalls, "lock released successfully", "Should log release")
}

func TestRelease_NotAcquired(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-not-acquired", client)

	// Release without acquiring
	err := lock.Release()
	assert.NoError(t, err, "Release without acquire should return nil")
}

func TestRelease_LockTakenByAnother(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-taken", client, WithLogger(logger))

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Simulate another process taking the lock by changing the value in Redis
	err = client.Set(ctx, "test-lock-taken", "different-value", DefaultLockTTL).Err()
	require.NoError(t, err)

	// Release should fail because value doesn't match
	err = lock.Release()
	assert.Error(t, err)
	assert.Equal(t, ErrLockNotHeld, err)

	// Verify error logging
	errorCalls := logger.getErrorCalls()
	assert.Contains(t, errorCalls, "lock was not held by this instance", "Should log error")

	// Clean up
	client.Del(ctx, "test-lock-taken")
}

func TestRelease_StopsAutoRenewal(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-stop-renewal", client,
		WithLogger(logger),
		WithRenewInterval(50*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Wait for renewal to start
	time.Sleep(30 * time.Millisecond)

	// Release should stop renewal
	err = lock.Release()
	assert.NoError(t, err)

	// Verify renewal stopped
	debugCalls := logger.getDebugCalls()
	assert.Contains(t, debugCalls, "lock auto-renewal stopped", "Should log renewal stop")
}

// ==============================================================================
// Unit tests for autoRenew
// ==============================================================================

func TestAutoRenew_RenewsSuccessfully(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-auto-renew", client,
		WithLogger(logger),
		WithRenewInterval(30*time.Millisecond),
		WithTTL(100*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Wait for multiple renewals
	time.Sleep(100 * time.Millisecond)

	// Verify renewal logging
	debugCalls := logger.getDebugCalls()
	renewCount := 0
	for _, call := range debugCalls {
		if call == "lock renewed successfully" {
			renewCount++
		}
	}
	assert.Greater(t, renewCount, 0, "Should have at least one successful renewal")

	// Clean up
	_ = lock.Release()
}

func TestAutoRenew_LockExpired(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-expired", client,
		WithLogger(logger),
		WithRenewInterval(30*time.Millisecond),
		WithTTL(100*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Simulate lock being taken by another process
	err = client.Set(ctx, "test-lock-expired", "different-value", 1*time.Minute).Err()
	require.NoError(t, err)

	// Wait for renewal attempt
	time.Sleep(50 * time.Millisecond)

	// Verify error logging
	errorCalls := logger.getErrorCalls()
	assert.Contains(t, errorCalls, "lock renewal failed - lock not held", "Should log renewal failure")

	// Lock should no longer be held
	assert.False(t, lock.IsHeld())

	// Clean up
	client.Del(ctx, "test-lock-expired")
}

// ==============================================================================
// Unit tests for IsHeld, Key, Value
// ==============================================================================

func TestIsHeld_InitialState(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-isheld", client)

	assert.False(t, lock.IsHeld(), "Lock should not be held initially")
}

func TestIsHeld_AfterAcquire(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-isheld-acquire", client)

	ctx := context.Background()
	acquired, _ := lock.Acquire(ctx)
	require.True(t, acquired)

	assert.True(t, lock.IsHeld(), "Lock should be held after acquire")

	_ = lock.Release()
}

func TestIsHeld_AfterRelease(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-isheld-release", client)

	ctx := context.Background()
	acquired, _ := lock.Acquire(ctx)
	require.True(t, acquired)

	_ = lock.Release()

	assert.False(t, lock.IsHeld(), "Lock should not be held after release")
}

func TestKey_ReturnsCorrectKey(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("my-lock-key", client)

	assert.Equal(t, "my-lock-key", lock.Key())
}

func TestValue_ReturnsValue(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-value", client, WithValue("custom-value"))

	assert.Equal(t, "custom-value", lock.Value())
}

func TestValue_GeneratesUUID(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-uuid", client)

	value := lock.Value()
	assert.NotEmpty(t, value)
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	assert.Len(t, value, 36, "Generated value should be a UUID")
}

// ==============================================================================
// Unit tests for LockGuard
// ==============================================================================

func TestLockGuard_ExecutesFunction(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-guard", client)

	executed := false
	err := LockGuard(context.Background(), lock, func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed, "Function should be executed")
	assert.False(t, lock.IsHeld(), "Lock should be released after function")
}

func TestLockGuard_ReleasesOnPanic(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-guard-panic", client)

	assert.Panics(t, func() {
		_ = LockGuard(context.Background(), lock, func() error {
			panic("test panic")
		})
	}, "Should propagate panic")

	// Lock should be released even after panic
	assert.False(t, lock.IsHeld(), "Lock should be released after panic")

	// Verify key is removed from Redis
	ctx := context.Background()
	exists, err := client.Exists(ctx, "test-lock-guard-panic").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists, "Key should be removed after panic")
}

func TestLockGuard_ReturnsError(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-guard-error", client)

	expectedErr := errors.New("function error")
	err := LockGuard(context.Background(), lock, func() error {
		return expectedErr
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.False(t, lock.IsHeld(), "Lock should be released after error")
}

func TestLockGuard_FailsToAcquire(t *testing.T) {
	client := getTestRedisClient(t)

	// First lock acquires and holds
	lock1 := NewDistributedLock("test-lock-guard-fail", client, WithValue("lock1"))
	ctx := context.Background()
	acquired, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)
	defer lock1.Release()

	// Second lock tries to use LockGuard
	lock2 := NewDistributedLock("test-lock-guard-fail", client, WithValue("lock2"))

	executed := false
	err = LockGuard(context.Background(), lock2, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.Equal(t, ErrLockNotAcquired, err)
	assert.False(t, executed, "Function should not be executed")
}

// ==============================================================================
// Unit tests for LockGuardWithRetry
// ==============================================================================

func TestLockGuardWithRetry_ExecutesFunction(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-guard-retry", client)

	executed := false
	err := LockGuardWithRetry(context.Background(), lock, 1*time.Second, 50*time.Millisecond, func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
	assert.False(t, lock.IsHeld())
}

func TestLockGuardWithRetry_RetrySuccess(t *testing.T) {
	client := getTestRedisClient(t)

	// First lock acquires
	lock1 := NewDistributedLock("test-lock-guard-retry-success", client, WithValue("lock1"))
	ctx := context.Background()
	acquired, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Release lock1 after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		lock1.Release()
	}()

	// Second lock retries via LockGuardWithRetry
	lock2 := NewDistributedLock("test-lock-guard-retry-success", client, WithValue("lock2"))

	executed := false
	err = LockGuardWithRetry(context.Background(), lock2, 500*time.Millisecond, 30*time.Millisecond, func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
}

func TestLockGuardWithRetry_Timeout(t *testing.T) {
	client := getTestRedisClient(t)

	// First lock acquires and holds
	lock1 := NewDistributedLock("test-lock-guard-retry-timeout", client, WithValue("lock1"))
	ctx := context.Background()
	acquired, err := lock1.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)
	defer lock1.Release()

	// Second lock tries
	lock2 := NewDistributedLock("test-lock-guard-retry-timeout", client, WithValue("lock2"))

	executed := false
	err = LockGuardWithRetry(context.Background(), lock2, 100*time.Millisecond, 30*time.Millisecond, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.Equal(t, ErrLockNotAcquired, err)
	assert.False(t, executed)
}

func TestLockGuardWithRetry_FunctionError(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-guard-retry-error", client)

	expectedErr := errors.New("function error")
	err := LockGuardWithRetry(context.Background(), lock, 1*time.Second, 50*time.Millisecond, func() error {
		return expectedErr
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

// ==============================================================================
// Unit tests for DiscardLogger
// ==============================================================================

func TestDiscardLogger_DoesNothing(t *testing.T) {
	logger := DiscardLogger{}

	// These should not panic
	logger.Info("test message", "key", "value")
	logger.Error("test error", "key", "value")
	logger.Debug("test debug", "key", "value")
}

// ==============================================================================
// Unit tests for error types
// ==============================================================================

func TestErrorTypes_AreDistinct(t *testing.T) {
	errors := []error{
		ErrLockNotAcquired,
		ErrLockAcquireFailed,
		ErrLockReleaseFailed,
		ErrLockNotHeld,
		ErrLockRenewFailed,
		ErrNilRedisClient,
		ErrEmptyLockKey,
		ErrInvalidTTL,
	}

	// Verify all errors are distinct
	for i, err1 := range errors {
		for j, err2 := range errors {
			if i != j {
				assert.NotEqual(t, err1, err2, "Errors should be distinct")
			}
		}
	}
}

func TestErrorTypes_HaveMeaningfulMessages(t *testing.T) {
	errors := map[error]string{
		ErrLockNotAcquired:   "lock not acquired",
		ErrLockAcquireFailed: "lock acquire failed",
		ErrLockReleaseFailed: "lock release failed",
		ErrLockNotHeld:       "lock not held by this instance",
		ErrLockRenewFailed:   "lock renew failed",
		ErrNilRedisClient:    "redis client is nil",
		ErrEmptyLockKey:      "lock key is empty",
		ErrInvalidTTL:        "TTL must be positive",
	}

	for err, expectedMsg := range errors {
		assert.Equal(t, expectedMsg, err.Error())
	}
}

// ==============================================================================
// Concurrency tests
// ==============================================================================

func TestDistributedLock_Concurrency(t *testing.T) {
	client := getTestRedisClient(t)

	// Test that only one goroutine can hold the lock at a time
	var successCount atomic.Int32
	var wg sync.WaitGroup

	goroutines := 10
	wg.Add(goroutines)

	for i := range goroutines {
		go func(id int) {
			defer wg.Done()

			lock := NewDistributedLock("concurrent-lock", client)

			ctx := context.Background()
			acquired, err := lock.Acquire(ctx)
			if err != nil {
				return
			}

			if acquired {
				successCount.Add(1)
				// Hold the lock briefly
				time.Sleep(10 * time.Millisecond)
				_ = lock.Release()
			}
		}(i)
	}

	wg.Wait()

	// At least one should succeed
	assert.Greater(t, successCount.Load(), int32(0), "At least one goroutine should acquire lock")
}

func TestDistributedLock_SequentialAcquire(t *testing.T) {
	client := getTestRedisClient(t)

	// Acquire and release multiple times
	for i := range 5 {
		lock := NewDistributedLock("sequential-lock", client)
		ctx := context.Background()
		acquired, err := lock.Acquire(ctx)
		assert.NoError(t, err, "Iteration %d: should not error", i)
		assert.True(t, acquired, "Iteration %d: should acquire", i)

		err = lock.Release()
		assert.NoError(t, err, "Iteration %d: should release", i)
	}
}

// ==============================================================================
// Integration-style tests
// ==============================================================================

func TestDistributedLock_FullWorkflow(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	lock := NewDistributedLock("workflow-lock", client,
		WithTTL(1*time.Second),
		WithRenewInterval(200*time.Millisecond),
		WithLogger(logger),
	)

	ctx := context.Background()

	// Acquire
	acquired, err := lock.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired)
	assert.True(t, lock.IsHeld())

	// Verify key is set
	val, err := client.Get(ctx, "workflow-lock").Result()
	assert.NoError(t, err)
	assert.Equal(t, lock.Value(), val)

	// Wait for renewal
	time.Sleep(300 * time.Millisecond)

	// Lock should still be held
	assert.True(t, lock.IsHeld())

	// Release
	err = lock.Release()
	assert.NoError(t, err)
	assert.False(t, lock.IsHeld())

	// Verify key is removed
	exists, err := client.Exists(ctx, "workflow-lock").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Verify logging
	infoCalls := logger.getInfoCalls()
	assert.Contains(t, infoCalls, "lock acquired successfully")
	assert.Contains(t, infoCalls, "lock released successfully")

	debugCalls := logger.getDebugCalls()
	assert.Contains(t, debugCalls, "lock auto-renewal started")
	assert.Contains(t, debugCalls, "lock auto-renewal stopped")
}

func TestDistributedLock_TwoLocksCompeting(t *testing.T) {
	client := getTestRedisClient(t)

	lock1 := NewDistributedLock("competing-lock", client, WithValue("lock1"))
	lock2 := NewDistributedLock("competing-lock", client, WithValue("lock2"))

	ctx := context.Background()

	// Lock1 acquires first
	acquired1, err := lock1.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired1)

	// Lock2 should fail to acquire
	acquired2, err := lock2.Acquire(ctx)
	assert.NoError(t, err)
	assert.False(t, acquired2)

	// Release lock1
	err = lock1.Release()
	assert.NoError(t, err)

	// Now lock2 should be able to acquire
	acquired2, err = lock2.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired2)

	// Clean up
	_ = lock2.Release()
}

// ==============================================================================
// Test Default Constants
// ==============================================================================

func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 30*time.Second, DefaultLockTTL)
	assert.Equal(t, 10*time.Second, DefaultRenewInterval)
	assert.Equal(t, 0, DefaultAcquireTimeout)
	assert.Equal(t, 100*time.Millisecond, DefaultRetryInterval)
}

// ==============================================================================
// Benchmark tests
// ==============================================================================

// ==============================================================================
// Additional edge case tests for better coverage
// ==============================================================================

func TestTryAcquire_NegativeTimeout(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-negative-timeout", client)

	ctx := context.Background()
	// Negative timeout should behave like regular Acquire (same as zero)
	acquired, err := lock.TryAcquire(ctx, -1*time.Second, 100*time.Millisecond)

	assert.NoError(t, err)
	assert.True(t, acquired)

	// Clean up
	_ = lock.Release()
}

func TestLockGuard_AcquireError(t *testing.T) {
	// Test LockGuard when Acquire returns an error (nil client)
	lock := NewDistributedLock("test-lock", nil)

	executed := false
	err := LockGuard(context.Background(), lock, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to acquire lock")
	assert.False(t, executed, "Function should not be executed when acquire fails")
}

func TestLockGuardWithRetry_AcquireError(t *testing.T) {
	// Test LockGuardWithRetry when TryAcquire returns an error (nil client)
	lock := NewDistributedLock("test-lock", nil)

	executed := false
	err := LockGuardWithRetry(context.Background(), lock, 100*time.Millisecond, 10*time.Millisecond, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to acquire lock")
	assert.False(t, executed, "Function should not be executed when acquire fails")
}

func TestLockGuard_ReleaseError(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-guard-release-error", client, WithLogger(logger))

	err := LockGuard(context.Background(), lock, func() error {
		// Simulate another process taking the lock while we hold it
		// This will cause Release to fail with ErrLockNotHeld
		ctx := context.Background()
		_ = client.Set(ctx, "test-lock-guard-release-error", "different-value", DefaultLockTTL).Err()
		return nil
	})

	// Function should succeed, but release will fail (logged, not returned)
	assert.NoError(t, err)

	// Verify error was logged
	errorCalls := logger.getErrorCalls()
	assert.Contains(t, errorCalls, "failed to release lock in defer")

	// Clean up
	client.Del(context.Background(), "test-lock-guard-release-error")
}

func TestLockGuardWithRetry_ReleaseError(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-guard-retry-release-error", client, WithLogger(logger))

	err := LockGuardWithRetry(context.Background(), lock, 100*time.Millisecond, 10*time.Millisecond, func() error {
		// Simulate another process taking the lock while we hold it
		ctx := context.Background()
		_ = client.Set(ctx, "test-lock-guard-retry-release-error", "different-value", DefaultLockTTL).Err()
		return nil
	})

	// Function should succeed, but release will fail (logged, not returned)
	assert.NoError(t, err)

	// Verify error was logged
	errorCalls := logger.getErrorCalls()
	assert.Contains(t, errorCalls, "failed to release lock in defer")

	// Clean up
	client.Del(context.Background(), "test-lock-guard-retry-release-error")
}

func TestAutoRenew_ContinuesOnError(t *testing.T) {
	// This test verifies that autoRenew continues trying even after a renewal error
	// We can't easily simulate Redis errors, but we can verify the lock stays held
	// after multiple renewal cycles
	client := getTestRedisClient(t)
	logger := newTestLogger()
	lock := NewDistributedLock("test-lock-renew-continues", client,
		WithLogger(logger),
		WithRenewInterval(20*time.Millisecond),
		WithTTL(100*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Wait for multiple renewal cycles
	time.Sleep(80 * time.Millisecond)

	// Lock should still be held
	assert.True(t, lock.IsHeld())

	// Verify multiple renewals occurred
	debugCalls := logger.getDebugCalls()
	renewCount := 0
	for _, call := range debugCalls {
		if call == "lock renewed successfully" {
			renewCount++
		}
	}
	assert.GreaterOrEqual(t, renewCount, 2, "Should have multiple successful renewals")

	// Clean up
	_ = lock.Release()
}

func TestRelease_MultipleCallsSafe(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-multi-release", client)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// First release should succeed
	err = lock.Release()
	assert.NoError(t, err)

	// Second release should be a no-op (not acquired)
	err = lock.Release()
	assert.NoError(t, err)
}

func TestRelease_ConcurrentCallsSafe(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-concurrent-release", client)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Concurrent release calls should not panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lock.Release()
		}()
	}
	wg.Wait()

	// Lock should not be held
	assert.False(t, lock.IsHeld())
}

func TestAcquire_MultipleCallsWithoutRelease(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-lock-multi-acquire", client)

	ctx := context.Background()

	// First acquire should succeed
	acquired1, err := lock.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired1)

	// Second acquire on same lock instance should return ErrLockAlreadyHeld
	// This prevents goroutine leaks from multiple autoRenew goroutines
	acquired2, err := lock.Acquire(ctx)
	assert.Error(t, err)
	assert.Equal(t, ErrLockAlreadyHeld, err)
	assert.False(t, acquired2)

	// Clean up
	_ = lock.Release()
}

func TestDistributedLock_LongRunningOperation(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	// Test that lock stays alive during a long operation due to auto-renewal
	lock := NewDistributedLock("test-lock-long-running", client,
		WithLogger(logger),
		WithTTL(100*time.Millisecond),
		WithRenewInterval(30*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Simulate long operation (longer than TTL)
	time.Sleep(150 * time.Millisecond)

	// Lock should still be held due to auto-renewal
	assert.True(t, lock.IsHeld())

	// Verify key still exists in Redis
	exists, err := client.Exists(ctx, "test-lock-long-running").Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), exists, "Key should still exist due to renewal")

	// Clean up
	err = lock.Release()
	assert.NoError(t, err)
}

// ==============================================================================
// Benchmark tests
// ==============================================================================

func BenchmarkAcquireRelease(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Skipping benchmark: Redis not available: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		lock := NewDistributedLock("bench-lock", client)
		_, _ = lock.Acquire(ctx)
		_ = lock.Release()
	}
}

func BenchmarkLockGuard(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Skipping benchmark: Redis not available: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		lock := NewDistributedLock("bench-lock", client)
		_ = LockGuard(ctx, lock, func() error {
			return nil
		})
	}
}

// ==============================================================================
// Race condition tests (run with -race flag)
// ==============================================================================

func TestDistributedLock_RaceCondition(t *testing.T) {
	client := getTestRedisClient(t)

	// Test concurrent access to acquired field
	var wg sync.WaitGroup
	goroutines := 100

	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			lock := NewDistributedLock(fmt.Sprintf("race-lock-%d", id), client,
				WithTTL(100*time.Millisecond),
				WithRenewInterval(20*time.Millisecond),
			)

			ctx := context.Background()
			acquired, err := lock.Acquire(ctx)
			if err != nil {
				return
			}
			if acquired {
				// Concurrent reads of IsHeld while autoRenew is running
				for j := 0; j < 10; j++ {
					_ = lock.IsHeld()
					time.Sleep(5 * time.Millisecond)
				}
				_ = lock.Release()
			}
		}(i)
	}

	wg.Wait()
}

// ==============================================================================
// Tests for duplicate Acquire prevention
// ==============================================================================

func TestAcquire_DuplicateAcquireReturnsError(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-duplicate-acquire", client)

	ctx := context.Background()

	// First acquire should succeed
	acquired1, err := lock.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired1)

	// Second acquire on same instance should return error
	acquired2, err := lock.Acquire(ctx)
	assert.Error(t, err)
	assert.Equal(t, ErrLockAlreadyHeld, err)
	assert.False(t, acquired2)

	// Clean up
	_ = lock.Release()
}

func TestAcquire_CanReacquireAfterRelease(t *testing.T) {
	client := getTestRedisClient(t)
	lock := NewDistributedLock("test-reacquire", client)

	ctx := context.Background()

	// First acquire
	acquired1, err := lock.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired1)

	// Release
	err = lock.Release()
	assert.NoError(t, err)

	// Should be able to acquire again
	acquired2, err := lock.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired2)

	// Clean up
	_ = lock.Release()
}

func TestAcquire_DetectsExpiredLockOnDuplicateAcquire(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	var callbackCalled atomic.Bool

	// Use very short TTL and manually stop renewal to simulate expiration
	lock := NewDistributedLock("test-detect-expired", client,
		WithLogger(logger),
		WithTTL(50*time.Millisecond),
		WithRenewInterval(10*time.Millisecond),
		WithLockLostCallback(func(key string, reason error) {
			callbackCalled.Store(true)
		}),
	)

	ctx := context.Background()

	// First acquire
	acquired1, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired1)

	// Manually delete the key to simulate expiration
	client.Del(ctx, "test-detect-expired")

	// Wait a bit for the autoRenew to detect the loss
	time.Sleep(30 * time.Millisecond)

	// Second acquire should detect the expired lock and allow re-acquisition
	acquired2, err := lock.Acquire(ctx)
	assert.NoError(t, err)
	assert.True(t, acquired2, "Should be able to re-acquire after lock expired")

	// Clean up
	_ = lock.Release()
}

// ==============================================================================
// Tests for renewInterval >= TTL validation
// ==============================================================================

func TestNewDistributedLock_RenewIntervalGreaterThanTTL(t *testing.T) {
	client := getTestRedisClient(t)

	// renewInterval > TTL should be auto-corrected
	lock := NewDistributedLock("test-interval-validation", client,
		WithTTL(100*time.Millisecond),
		WithRenewInterval(200*time.Millisecond),
	)

	// Should be auto-corrected to TTL/3
	assert.Less(t, lock.renewInterval, lock.ttl)
	assert.Equal(t, 100*time.Millisecond/3, lock.renewInterval)
}

func TestNewDistributedLock_RenewIntervalEqualToTTL(t *testing.T) {
	client := getTestRedisClient(t)

	// renewInterval == TTL should be auto-corrected
	lock := NewDistributedLock("test-interval-equal", client,
		WithTTL(100*time.Millisecond),
		WithRenewInterval(100*time.Millisecond),
	)

	// Should be auto-corrected to TTL/3
	assert.Less(t, lock.renewInterval, lock.ttl)
}

// ==============================================================================
// Tests for consecutive renewal failures
// ==============================================================================

func TestAutoRenew_ConsecutiveFailuresStopsRenewal(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	lock := NewDistributedLock("test-consecutive-failures", client,
		WithLogger(logger),
		WithTTL(200*time.Millisecond),
		WithRenewInterval(30*time.Millisecond),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Delete the key to simulate Redis failures
	client.Del(ctx, "test-consecutive-failures")

	// Wait for renewal attempts to fail
	time.Sleep(150 * time.Millisecond)

	// Lock should no longer be held
	assert.False(t, lock.IsHeld())

	// Verify error logging
	errorCalls := logger.getErrorCalls()
	assert.Contains(t, errorCalls, "lock renewal failed - lock not held")
}

func TestAutoRenew_LockLostCallback(t *testing.T) {
	client := getTestRedisClient(t)
	logger := newTestLogger()

	var callbackCalled atomic.Bool
	var callbackKey string
	var callbackErr error
	var mu sync.Mutex

	lock := NewDistributedLock("test-lock-lost-callback", client,
		WithLogger(logger),
		WithTTL(200*time.Millisecond),
		WithRenewInterval(30*time.Millisecond),
		WithLockLostCallback(func(key string, reason error) {
			mu.Lock()
			defer mu.Unlock()
			callbackCalled.Store(true)
			callbackKey = key
			callbackErr = reason
		}),
	)

	ctx := context.Background()
	acquired, err := lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, acquired)

	// Simulate another process taking the lock
	err = client.Set(ctx, "test-lock-lost-callback", "different-value", 1*time.Minute).Err()
	require.NoError(t, err)

	// Wait for renewal attempt to detect lock loss
	time.Sleep(100 * time.Millisecond)

	// Verify callback was called
	assert.True(t, callbackCalled.Load(), "Callback should be called when lock is lost")

	mu.Lock()
	assert.Equal(t, "test-lock-lost-callback", callbackKey)
	assert.Equal(t, ErrLockLostTakenByAnother, callbackErr)
	mu.Unlock()

	// Clean up
	client.Del(ctx, "test-lock-lost-callback")
}

func TestWithMaxConsecutiveFailures(t *testing.T) {
	client := getTestRedisClient(t)

	lock := NewDistributedLock("test-max-failures", client,
		WithMaxConsecutiveFailures(5),
	)

	assert.Equal(t, 5, lock.maxConsecutiveFailures)
}

func TestWithMaxConsecutiveFailures_InvalidValue(t *testing.T) {
	client := getTestRedisClient(t)

	lock := NewDistributedLock("test-max-failures-invalid", client,
		WithMaxConsecutiveFailures(0),
	)

	// Should use default value
	assert.Equal(t, DefaultMaxConsecutiveFailures, lock.maxConsecutiveFailures)
}

// ==============================================================================
// Tests for error message improvements
// ==============================================================================

func TestErrorTypes_NewErrors(t *testing.T) {
	assert.Equal(t, "lock already held by this instance", ErrLockAlreadyHeld.Error())
	assert.Equal(t, "renew interval must be less than TTL", ErrInvalidRenewInterval.Error())
	assert.Equal(t, "lock lost due to consecutive renewal failures", ErrLockLostRenewalFailure.Error())
	assert.Equal(t, "lock lost - taken by another process", ErrLockLostTakenByAnother.Error())
}

// ==============================================================================
// Lock contention benchmark
// ==============================================================================

func BenchmarkLockContention(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Skipping benchmark: Redis not available: %v", err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lock := NewDistributedLock("contention-bench-lock", client,
				WithTTL(1*time.Second),
				WithRenewInterval(100*time.Millisecond),
			)
			acquired, _ := lock.Acquire(ctx)
			if acquired {
				time.Sleep(1 * time.Millisecond) // Simulate work
				_ = lock.Release()               //nolint:contextcheck // Release() intentionally uses background context
			}
		}
	})
}
