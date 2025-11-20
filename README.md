# Chanx

[![Go Reference](https://pkg.go.dev/badge/github.com/kydenul/chanx.svg)](https://pkg.go.dev/github.com/kydenul/chanx)
[![Go Report Card](https://goreportcard.com/badge/github.com/kydenul/chanx)](https://goreportcard.com/report/github.com/kydenul/chanx)

A powerful Go library for channel operations and concurrent programming patterns, inspired by the book "Concurrency in Go". Chanx provides a comprehensive set of utilities for working with Go channels, including common patterns like fan-in, fan-out, pipelines, and a robust worker pool implementation.

[中文文档](README_ZH.md)

## Features

- **Generic Type Support**: Fully leverages Go 1.18+ generics for type-safe channel operations
- **Rich Channel Patterns**: Implements common concurrency patterns from "Concurrency in Go"
- **Worker Pool**: Production-ready worker pool with graceful shutdown and error handling
- **Context-Aware**: All operations respect context cancellation for clean resource management
- **Well-Tested**: Comprehensive unit tests and property-based tests using gopter
- **Zero Dependencies**: Only requires standard library (test dependencies: testify, gopter)

## Installation

```bash
go get github.com/kydenul/chanx
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "github.com/kydenul/chanx"
)

func main() {
    ctx := context.Background()
    c := chanx.NewChannel[int]()
    
    // Generate values
    values := c.Generate(ctx, 1, 2, 3, 4, 5)
    
    // Process values
    for v := range values {
        fmt.Println(v)
    }
}
```

## Core Functions

### Channel Generators

#### Generate

Creates a channel and sends a sequence of values.

```go
ctx := context.Background()
c := chanx.NewChannel[int]()
ch := c.Generate(ctx, 1, 2, 3, 4, 5)

for v := range ch {
    fmt.Println(v) // Prints: 1, 2, 3, 4, 5
}
```

#### Repeat

Continuously repeats a sequence of values until context is cancelled.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChannel[string]()
ch := c.Repeat(ctx, "hello", "world")

// Reads: hello, world, hello, world, hello, world, ...
```

#### RepeatFn

Repeatedly executes a function and sends its return values.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChannel[int]()
counter := 0
ch := c.RepeatFn(ctx, func() int {
    counter++
    return counter
})

// Reads: 1, 2, 3, 4, 5, ...
```

### Channel Transformers

#### Take

Takes the first N values from a channel.

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

source := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
first5 := c.Take(ctx, source, 5)

for v := range first5 {
    fmt.Println(v) // Prints: 1, 2, 3, 4, 5
}
```

#### FanIn

Merges multiple channels into a single channel.

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

ch1 := c.Generate(ctx, 1, 2, 3)
ch2 := c.Generate(ctx, 4, 5, 6)
ch3 := c.Generate(ctx, 7, 8, 9)

merged := c.FanIn(ctx, ch1, ch2, ch3)

// Receives all values from all channels (order may vary)
for v := range merged {
    fmt.Println(v)
}
```

#### Tee

Splits one channel into two identical output channels.

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

source := c.Generate(ctx, 1, 2, 3, 4, 5)
out1, out2 := c.Tee(ctx, source)

// Both out1 and out2 receive the same values
go func() {
    for v := range out1 {
        fmt.Println("Output 1:", v)
    }
}()

for v := range out2 {
    fmt.Println("Output 2:", v)
}
```

#### Bridge

Connects a stream of channels into a single output channel.

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

chanStream := make(chan (<-chan int))

go func() {
    defer close(chanStream)
    chanStream <- c.Generate(ctx, 1, 2, 3)
    chanStream <- c.Generate(ctx, 4, 5, 6)
    chanStream <- c.Generate(ctx, 7, 8, 9)
}()

bridged := c.Bridge(ctx, chanStream)

for v := range bridged {
    fmt.Println(v) // Prints all values: 1-9
}
```

### Control Flow

#### Or

Returns a channel that closes when any of the input channels close.

```go
c := chanx.NewChannel[struct{}]()

ctx1, cancel1 := context.WithCancel(context.Background())
ctx2, cancel2 := context.WithCancel(context.Background())
defer cancel2()

ch1 := c.RepeatFn(ctx1, func() struct{} { return struct{}{} })
ch2 := c.RepeatFn(ctx2, func() struct{} { return struct{}{} })

orChan := c.Or(ch1, ch2)

// Close one channel
cancel1()

// orChan will close when ch1 closes
<-orChan
```

#### OrDone

Wraps a channel to respect context cancellation.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChannel[int]()
source := c.Repeat(ctx, 1, 2, 3)

output := c.OrDone(ctx, source)

// Read some values
for i := 0; i < 10; i++ {
    fmt.Println(<-output)
}

// Cancel context to stop
cancel()
```

## Worker Pool

The worker pool provides a robust way to execute tasks concurrently with a fixed number of workers.

### Basic Usage

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

// Create a worker pool with 5 workers
wp, err := c.NewWorkerPool(ctx, 5)
if err != nil {
    log.Fatal(err)
}
defer wp.Close()

// Start a goroutine to collect results
go func() {
    for result := range wp.Results() {
        if result.Err != nil {
            fmt.Printf("Task failed: %v\n", result.Err)
        } else {
            fmt.Printf("Task result: %d\n", result.Value)
        }
    }
}()

// Submit tasks
for i := 0; i < 100; i++ {
    taskID := i
    err := wp.Submit(chanx.Task[int]{
        Fn: func() (int, error) {
            // Simulate work
            time.Sleep(100 * time.Millisecond)
            return taskID * 2, nil
        },
    })
    if err != nil {
        log.Printf("Failed to submit task: %v", err)
    }
}
```

### Error Handling

```go
ctx := context.Background()
c := chanx.NewChannel[string]()

wp, _ := c.NewWorkerPool(ctx, 3)
defer wp.Close()

go func() {
    for result := range wp.Results() {
        if result.Err != nil {
            fmt.Printf("Error: %v\n", result.Err)
        } else {
            fmt.Printf("Success: %s\n", result.Value)
        }
    }
}()

// Submit a task that may fail
wp.Submit(chanx.Task[string]{
    Fn: func() (string, error) {
        if rand.Float32() < 0.5 {
            return "", errors.New("random failure")
        }
        return "success", nil
    },
})
```

### Graceful Shutdown

```go
ctx, cancel := context.WithCancel(context.Background())
c := chanx.NewChannel[int]()

wp, _ := c.NewWorkerPool(ctx, 5)

// Submit tasks...

// Cancel context to stop accepting new tasks
cancel()

// Close waits for all in-flight tasks to complete
wp.Close()
```

## Advanced Patterns

### Pipeline Pattern

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

// Stage 1: Generate numbers
numbers := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

// Stage 2: Take first 5
first5 := c.Take(ctx, numbers, 5)

// Stage 3: Process in parallel
ch1 := c.Generate(ctx, 1, 2, 3)
ch2 := c.Generate(ctx, 4, 5, 6)
merged := c.FanIn(ctx, ch1, ch2)

// Stage 4: Duplicate output
out1, out2 := c.Tee(ctx, merged)
```

### Fan-Out/Fan-In Pattern

```go
ctx := context.Background()
c := chanx.NewChannel[int]()

// Input
input := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

// Fan-out: Split work across multiple workers
workers := make([]<-chan int, 3)
for i := range workers {
    workers[i] = processWorker(ctx, input)
}

// Fan-in: Merge results
results := c.FanIn(ctx, workers...)

for result := range results {
    fmt.Println(result)
}
```

## Testing

The library includes comprehensive tests:

- **Unit Tests**: Test individual functions with specific scenarios
- **Property-Based Tests**: Use gopter to verify properties across random inputs

Run tests:

```bash
# Run all tests
go test -v

# Run only unit tests
go test -v -run "^Test[^Property]"

# Run only property-based tests
go test -v -run "TestProperty"

# Run with race detector
go test -race -v
```

## Performance Considerations

- All channel operations are non-blocking with context support
- Worker pool uses buffered channels for better throughput
- Goroutines are properly cleaned up on context cancellation
- No goroutine leaks - all spawned goroutines respect context

## Requirements

- Go 1.18 or higher (for generics support)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Acknowledgments

- Inspired by "Concurrency in Go" by Katherine Cox-Buday
- Uses [gopter](https://github.com/leanovate/gopter) for property-based testing
- Uses [testify](https://github.com/stretchr/testify) for assertions

## Related Projects

- [Go Concurrency Patterns](https://go.dev/blog/pipelines)
- [Concurrency in Go (Book)](https://www.oreilly.com/library/view/concurrency-in-go/9781491941294/)
