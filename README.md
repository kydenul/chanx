# Chanx

[![Go Reference](https://pkg.go.dev/badge/github.com/kydenul/chanx.svg)](https://pkg.go.dev/github.com/kydenul/chanx)
[![Go Report Card](https://goreportcard.com/badge/github.com/kydenul/chanx)](https://goreportcard.com/report/github.com/kydenul/chanx)
[![CI](https://github.com/kydenul/chanx/actions/workflows/ci.yml/badge.svg)](https://github.com/kydenul/chanx/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/kydenul/chanx/branch/main/graph/badge.svg)](https://codecov.io/gh/kydenul/chanx)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A Go library for channel operations and concurrent programming patterns, inspired by "Concurrency in Go". Provides generic channel utilities, production-ready worker pool, and Redis-based distributed locks.

[中文文档](README_CN.md)

## Features

- **Channel Patterns**: Generate, Repeat, Take, FanIn, Tee, Bridge, Or, OrDone
- **Worker Pool**: Production-ready pool with metrics and graceful shutdown
- **Distributed Lock**: Redis-based lock with auto-renewal
- **Generic & Type-Safe**: Full Go 1.18+ generics support
- **Context-Aware**: All operations respect context cancellation

## Installation

```bash
go get github.com/kydenul/chanx
```

## Quick Start

### Channel Operations

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

// Generate: send sequence of values
values := c.Generate(ctx, 1, 2, 3, 4, 5)

// Take: limit number of values
first3 := c.Take(ctx, values, 3)

// FanIn: merge multiple channels
ch1 := c.Generate(ctx, 1, 2, 3)
ch2 := c.Generate(ctx, 4, 5, 6)
merged := c.FanIn(ctx, ch1, ch2)
```

### Worker Pool

```go
ctx := context.Background()
wp, _ := chanx.NewWorkerPool[int](ctx, 5)
defer wp.Close()

// Collect results
go func() {
    for result := range wp.Results() {
        if result.Err != nil {
            fmt.Printf("Error: %v\n", result.Err)
        } else {
            fmt.Printf("Result: %d\n", result.Value)
        }
    }
}()

// Submit tasks
for i := 0; i < 100; i++ {
    taskID := i
    wp.Submit(chanx.Task[int]{
        Fn: func() (int, error) {
            return taskID * 2, nil
        },
    })
}

// Get metrics
metrics := wp.Metrics()
fmt.Printf("Completed: %d, Active: %d\n",
    metrics.CompletedTasks, metrics.ActiveWorkers)
```

### Distributed Lock

```go
import (
    "github.com/kydenul/chanx"
    "github.com/redis/go-redis/v9"
)

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
lock := chanx.NewDistributedLock(
    "resource:lock",
    client,
    chanx.WithTTL(30*time.Second),
)

// Simple usage with LockGuard
err := chanx.LockGuard(ctx, lock, func() error {
    // Critical section
    return processResource()
})

// Or manual control
acquired, err := lock.Acquire(ctx)
if acquired {
    defer lock.Release()
    // Do work
}

// Try with retry
acquired, err = lock.TryAcquire(ctx, 10*time.Second, 100*time.Millisecond)
```

## API Reference

### Channel Patterns

| Function | Description |
|----------|-------------|
| `Generate(ctx, values...)` | Send values sequentially |
| `Repeat(ctx, values...)` | Repeat values infinitely |
| `RepeatFn(ctx, fn)` | Repeat function calls |
| `Take(ctx, ch, n)` | Take first n values |
| `FanIn(ctx, channels...)` | Merge multiple channels |
| `Tee(ctx, ch)` | Split channel into two |
| `Bridge(ctx, chanStream)` | Connect channel stream |
| `Or(channels...)` | Close when any closes |
| `OrDone(ctx, ch)` | Respect context cancellation |

### Worker Pool

```go
// Create pool
wp, err := NewWorkerPool[T](ctx, workerCount)

// Submit tasks
err := wp.Submit(Task[T]{Fn: func() (T, error) {...}})
result := wp.SubmitBatch([]Task[T]{...})

// Get results and metrics
results := wp.Results()
metrics := wp.Metrics()

// Cleanup
wp.Close()
```

### Distributed Lock

```go
// Create lock
lock := NewDistributedLock(key, redisClient, options...)

// Acquire/Release
acquired, err := lock.Acquire(ctx)
err = lock.Release()

// With retry
acquired, err := lock.TryAcquire(ctx, timeout, retryInterval)

// Helper functions
err := LockGuard(ctx, lock, fn)
err := LockGuardWithRetry(ctx, lock, timeout, retryInterval, fn)
```

## Best Practices

1. **Always use context with timeout** to prevent goroutine leaks
2. **Drain channels or cancel context** when done
3. **Use defer for cleanup**: `defer wp.Close()`, `defer lock.Release()`
4. **Choose appropriate worker count**:
   - CPU-bound: `runtime.NumCPU()`
   - I/O-bound: `runtime.NumCPU() * 4`
5. **Set lock TTL** to at least 3x expected operation duration
6. **Use buffered variants** for high-throughput scenarios

## Requirements

- Go 1.18+ (generics support)
- Redis (for distributed locks only)

## License

MIT License - see [LICENSE](LICENSE) file

## Acknowledgments

Inspired by ["Concurrency in Go"](https://www.oreilly.com/library/view/concurrency-in-go/9781491941294/) by Katherine Cox-Buday
