package chanx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// Error type constants for detailed error handling
var (
	// ErrInvalidBufferSize indicates that the buffer size is invalid (must be >= 0)
	ErrInvalidBufferSize = errors.New("buffer size must be non-negative")
)

// Chanx is a logic object which can generate or manipulate go channel
// all methods of Chanx are in the book tilted《Concurrency in Go》
type Chanx[T any] struct{}

// NewChanx return a Chanx instance
func NewChanx[T any]() *Chanx[T] { return &Chanx[T]{} }

// Generate creates a channel and sends values to it sequentially.
//
// Time Complexity: O(n) where n is the number of values
// Space Complexity: O(1) for the channel buffer, O(n) for the goroutine stack
// Goroutines: Creates 1 goroutine that exits when all values are sent or context is cancelled
//
// Common Pitfalls:
//   - Not draining the channel completely can cause goroutine leak
//   - Cancelling context before reading all values will lose remaining data
//
// Best Practices:
//   - Always use context with timeout or cancellation capability
//   - Ensure the channel is fully drained or cancel the context when done
//   - Consider using GenerateBuffered for better performance with slow consumers
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//	ch := c.Generate(ctx, 1, 2, 3, 4, 5)
//	for v := range ch {
//	    fmt.Println(v) // Prints: 1, 2, 3, 4, 5
//	}
func (c *Chanx[T]) Generate(ctx context.Context, values ...T) <-chan T {
	dataStream := make(chan T)

	go func() {
		defer close(dataStream)

		for _, v := range values {
			select {
			case <-ctx.Done():
				return
			case dataStream <- v:
			}
		}
	}()

	return dataStream
}

// Repeat creates a channel and repeatedly sends values to it in a loop until context is cancelled.
//
// Time Complexity: O(∞) - runs indefinitely until context cancellation
// Space Complexity: O(1) for the channel buffer, O(n) for the goroutine stack where n is len(values)
// Goroutines: Creates 1 goroutine that exits when context is cancelled
//
// Common Pitfalls:
//   - Forgetting to cancel the context will cause infinite execution and goroutine leak
//   - Not consuming from the channel will block the goroutine
//   - Using with large value slices can increase memory usage
//
// Best Practices:
//   - Always use a cancellable context (WithCancel, WithTimeout, or WithDeadline)
//   - Ensure consumers can keep up with the production rate
//   - Consider using RepeatBuffered for better performance with bursty consumption
//   - Use Take() to limit the number of values if needed
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
//	defer cancel()
//	c := NewChanx[string]()
//	ch := c.Repeat(ctx, "hello", "world")
//	count := 0
//	for v := range ch {
//	    fmt.Println(v) // Prints: hello, world, hello, world, ...
//	    count++
//	    if count >= 4 {
//	        cancel() // Stop after 4 values
//	        break
//	    }
//	}
func (c *Chanx[T]) Repeat(ctx context.Context, values ...T) <-chan T {
	dataStream := make(chan T)

	go func() {
		defer close(dataStream)
		for {
			for _, v := range values {
				select {
				case <-ctx.Done():
					return
				case dataStream <- v:
				}
			}
		}
	}()
	return dataStream
}

// RepeatFn creates a channel, executes fn repeatedly, and sends the results to the channel until context is cancelled.
//
// Time Complexity: O(∞) - runs indefinitely until context cancellation, O(f) per iteration where f is fn's complexity
// Space Complexity: O(1) for the channel buffer, O(s) for the goroutine stack where s depends on fn
// Goroutines: Creates 1 goroutine that exits when context is cancelled
//
// Common Pitfalls:
//   - Forgetting to cancel the context will cause infinite execution and goroutine leak
//   - Using expensive functions without rate limiting can cause high CPU usage
//   - Not consuming from the channel will block the goroutine
//   - If fn panics, the goroutine will crash
//
// Best Practices:
//   - Always use a cancellable context (WithCancel, WithTimeout, or WithDeadline)
//   - Ensure fn is relatively fast or add rate limiting
//   - Consider adding error handling within fn
//   - Use Take() to limit the number of executions if needed
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//	counter := 0
//	ch := c.RepeatFn(ctx, func() int {
//	    counter++
//	    return counter
//	})
//	for v := range ch {
//	    fmt.Println(v) // Prints: 1, 2, 3, 4, ...
//	    if v >= 5 {
//	        cancel()
//	        break
//	    }
//	}
func (c *Chanx[T]) RepeatFn(ctx context.Context, fn func() T) <-chan T {
	dataStream := make(chan T)

	go func() {
		defer close(dataStream)
		for {
			select {
			case <-ctx.Done():
				return
			case dataStream <- fn():
			}
		}
	}()
	return dataStream
}

// Take creates a channel that receives up to 'number' values from the source channel.
//
// Time Complexity: O(n) where n is the number parameter
// Space Complexity: O(1) for the channel buffer
// Goroutines: Creates 1 goroutine that exits after taking n values, when source closes, or context is cancelled
//
// Common Pitfalls:
//   - Using negative or zero number will return an immediately closed channel
//   - Source channel closing early will result in fewer values than requested
//   - Not consuming the output channel can cause goroutine leak
//
// Best Practices:
//   - Use with infinite generators (Repeat, RepeatFn) to limit output
//   - Always drain the output channel or cancel the context
//   - Consider the source channel's lifetime when setting number
//
// Example:
//
//	ctx := context.Background()
//	c := NewChanx[int]()
//	source := c.Repeat(ctx, 1, 2, 3)
//	limited := c.Take(ctx, source, 5)
//	for v := range limited {
//	    fmt.Println(v) // Prints: 1, 2, 3, 1, 2 (then stops)
//	}
func (c *Chanx[T]) Take(ctx context.Context, valueStream <-chan T, number int) <-chan T {
	takeStream := make(chan T)

	go func() {
		defer close(takeStream)

		for range number {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valueStream:
				if !ok {
					// Source channel closed, stop taking
					return
				}
				select {
				case takeStream <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return takeStream
}

// FanIn merges multiple channels into a single output channel.
// Values from all input channels are forwarded to the output channel concurrently.
//
// Time Complexity: O(n*m) where n is number of channels and m is average values per channel
// Space Complexity: O(n) for goroutines, O(1) for the output channel buffer
// Goroutines: Creates n+1 goroutines (1 per input channel + 1 coordinator) that exit when all inputs close or context is cancelled
//
// Common Pitfalls:
//   - Order of values is non-deterministic due to concurrent reading
//   - Not closing input channels will cause goroutine leak
//   - Context cancellation may lose in-flight values
//
// Best Practices:
//   - Ensure all input channels will eventually close
//   - Use context for timeout/cancellation control
//   - Don't rely on value ordering from different channels
//   - Consider buffering input channels if producers are faster than consumer
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//	ch1 := c.Generate(ctx, 1, 2, 3)
//	ch2 := c.Generate(ctx, 4, 5, 6)
//	merged := c.FanIn(ctx, ch1, ch2)
//	for v := range merged {
//	    fmt.Println(v) // Prints: 1, 4, 2, 5, 3, 6 (order may vary)
//	}
func (c *Chanx[T]) FanIn(ctx context.Context, channels ...<-chan T) <-chan T {
	out := make(chan T)

	go func() {
		var wg sync.WaitGroup
		wg.Add(len(channels))

		for _, c := range channels {
			go func(c <-chan T) {
				defer wg.Done()
				for v := range c {
					select {
					case <-ctx.Done():
						return
					case out <- v:
					}
				}
			}(c)
		}
		wg.Wait()
		close(out)
	}()

	return out
}

// Tee splits one input channel into two output channels, duplicating each value.
// Each value from the input is sent to both output channels.
//
// Time Complexity: O(n) where n is the number of values from input channel
// Space Complexity: O(1) for channel buffers
// Goroutines: Creates 1 goroutine that exits when input closes or context is cancelled
//
// Common Pitfalls:
//   - Both output channels must be consumed; slow consumer blocks both outputs
//   - If one output channel is not read, the entire pipeline will block
//   - Context cancellation may cause values to be lost
//
// Best Practices:
//   - Ensure both output channels are consumed concurrently
//   - Use buffered channels if consumers have different speeds
//   - Always use context for cancellation control
//   - Consider using separate goroutines for each consumer
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//	source := c.Generate(ctx, 1, 2, 3)
//	out1, out2 := c.Tee(ctx, source)
//
//	go func() {
//	    for v := range out1 {
//	        fmt.Printf("Consumer 1: %d\n", v)
//	    }
//	}()
//
//	for v := range out2 {
//	    fmt.Printf("Consumer 2: %d\n", v)
//	}
func (c *Chanx[T]) Tee(ctx context.Context, in <-chan T) (<-chan T, <-chan T) {
	out1 := make(chan T)
	out2 := make(chan T)

	go func() {
		defer close(out1)
		defer close(out2)

		for val := range c.OrDone(ctx, in) {
			out1, out2 := out1, out2
			for range 2 {
				select {
				case <-ctx.Done():
				case out1 <- val:
					out1 = nil
				case out2 <- val:
					out2 = nil
				}
			}
		}
	}()

	return out1, out2
}

// Bridge links multiple channels into one output channel by reading from a channel of channels.
// It processes channels concurrently as they arrive and forwards all values to the output.
// Uses a buffered output channel (size 32) to reduce blocking and improve throughput.
//
// Time Complexity: O(n*m) where n is number of channels and m is average values per channel
// Space Complexity: O(k) where k is the number of concurrent channels being processed
// Goroutines: Creates 1 coordinator goroutine + 1 goroutine per channel in the stream
//
//	All goroutines exit when chanStream closes or context is cancelled
//
// Common Pitfalls:
//   - Not closing the channel stream will cause goroutine leak
//   - Context cancellation may lose in-flight values
//   - Very fast producers can overwhelm the buffer
//
// Best Practices:
//   - Ensure the channel stream will eventually close
//   - Use context for timeout/cancellation control
//   - Consider the buffer size (32) when dealing with very fast producers
//   - Ensure all channels in the stream will eventually close
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//
//	chanStream := make(chan (<-chan int))
//	go func() {
//	    defer close(chanStream)
//	    chanStream <- c.Generate(ctx, 1, 2, 3)
//	    chanStream <- c.Generate(ctx, 4, 5, 6)
//	}()
//
//	bridged := c.Bridge(ctx, chanStream)
//	for v := range bridged {
//	    fmt.Println(v) // Prints all values: 1, 2, 3, 4, 5, 6 (order may vary)
//	}
func (c *Chanx[T]) Bridge(ctx context.Context, chanStream <-chan <-chan T) <-chan T {
	// Use buffered channel to reduce blocking and improve throughput
	valStream := make(chan T, 32)
	go func() {
		defer close(valStream)
		wg := sync.WaitGroup{}
		defer wg.Wait()
		for {
			var stream <-chan T
			select {
			case maybeStream, ok := <-chanStream:
				if !ok {
					return
				}
				stream = maybeStream
				wg.Add(1)
			case <-ctx.Done():
				return
			}

			go func(s <-chan T) {
				defer wg.Done()
				for val := range c.OrDone(ctx, s) {
					select {
					case valStream <- val:
					case <-ctx.Done():
						return
					}
				}
			}(stream)
		}
	}()
	return valStream
}

// Or reads from multiple channels and closes the output channel when ANY input channel closes.
// This is useful for implementing cancellation or timeout patterns across multiple channels.
// Uses reflect.Select for efficient iterative implementation, avoiding recursion and stack overflow.
//
// Time Complexity: O(n) for setup where n is number of channels, O(1) for waiting
// Space Complexity: O(n) for the select cases array
// Goroutines: Creates 1 goroutine that exits when any input channel closes
//
// Common Pitfalls:
//   - Passing zero channels returns nil
//   - All input channels should be read-only to prevent accidental writes
//   - Does not forward values, only signals closure
//   - Remaining open channels are not closed
//
// Best Practices:
//   - Use for implementing cancellation across multiple operations
//   - Combine with context.Done() channels for timeout patterns
//   - Ensure at least one channel will eventually close
//   - Consider using context.Context for more idiomatic cancellation
//
// Example:
//
//	c := NewChanx[int]()
//	sig1 := make(chan int)
//	sig2 := make(chan int)
//
//	done := c.Or(sig1, sig2)
//
//	go func() {
//	    time.Sleep(1 * time.Second)
//	    close(sig1) // This will cause done to close
//	}()
//
//	<-done // Blocks until sig1 or sig2 closes
//	fmt.Println("One channel closed")
func (c *Chanx[T]) Or(channels ...<-chan T) <-chan T {
	// Optimization: handle edge cases
	switch len(channels) {
	case 0:
		return nil
	case 1:
		return channels[0]
	}

	orDone := make(chan T)

	go func() {
		defer close(orDone)

		// Use reflect.Select for iterative implementation
		// This avoids recursion and handles any number of channels efficiently
		cases := make([]reflect.SelectCase, len(channels))
		for i, ch := range channels {
			cases[i] = reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(ch),
			}
		}

		// Wait for any channel to close (receive zero value)
		// reflect.Select will return when any case is ready
		_, _, _ = reflect.Select(cases)
		// When any channel closes, we exit and close orDone
	}()

	return orDone
}

// OrDone reads from a channel and forwards values to an output channel until context is cancelled or input closes.
// This is a convenience function for combining context cancellation with channel reading.
//
// Time Complexity: O(n) where n is the number of values read from the channel
// Space Complexity: O(1) for the channel buffer
// Goroutines: Creates 1 goroutine that exits when input closes or context is cancelled
//
// Common Pitfalls:
//   - Context cancellation will stop forwarding immediately, potentially losing values
//   - Not consuming the output channel can cause goroutine leak
//   - Double-wrapping with OrDone is redundant
//
// Best Practices:
//   - Use to add context cancellation to channels that don't support it
//   - Ensure the output channel is consumed or context is cancelled
//   - Prefer using context-aware channel operations when available
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//
//	source := make(chan int)
//	go func() {
//	    for i := 0; i < 100; i++ {
//	        source <- i
//	        time.Sleep(100 * time.Millisecond)
//	    }
//	    close(source)
//	}()
//
//	wrapped := c.OrDone(ctx, source)
//	for v := range wrapped {
//	    fmt.Println(v) // Stops after ~2 seconds due to context timeout
//	}
func (c *Chanx[T]) OrDone(ctx context.Context, channel <-chan T) <-chan T {
	valStream := make(chan T)

	go func() {
		defer close(valStream)
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-channel:
				if !ok {
					return
				}
				select {
				case valStream <- v:
				case <-ctx.Done():
				}
			}
		}
	}()

	return valStream
}

// GenerateBuffered creates a buffered channel and sends values to it sequentially.
// The buffer allows the producer to send values without blocking until the buffer is full.
//
// Parameters:
//   - bufferSize: Channel buffer size (must be >= 0). Use 0 for unbuffered channel.
//   - values: Values to send to the channel
//
// Time Complexity: O(n) where n is the number of values
// Space Complexity: O(b) where b is the bufferSize
// Goroutines: Creates 1 goroutine that exits when all values are sent or context is cancelled
//
// Common Pitfalls:
//   - Negative bufferSize returns an error
//   - Buffer too small may not provide performance benefits
//   - Buffer too large wastes memory
//   - Not draining the channel can cause goroutine leak
//
// Best Practices:
//   - Choose buffer size based on expected consumer speed
//   - Use buffer size equal to len(values) for non-blocking sends
//   - Consider using unbuffered (size 0) for synchronization
//   - Always drain the channel or cancel the context
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	c := NewChanx[int]()
//
//	// Buffer size of 10 allows all values to be sent without blocking
//	ch, err := c.GenerateBuffered(ctx, 10, 1, 2, 3, 4, 5)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	for v := range ch {
//	    fmt.Println(v) // Prints: 1, 2, 3, 4, 5
//	}
func (c *Chanx[T]) GenerateBuffered(ctx context.Context, bufferSize int, values ...T) (<-chan T, error) {
	if bufferSize < 0 {
		return nil, fmt.Errorf("%w: got %d, must be at least 0", ErrInvalidBufferSize, bufferSize)
	}

	dataStream := make(chan T, bufferSize)

	go func() {
		defer close(dataStream)

		for _, v := range values {
			select {
			case <-ctx.Done():
				return
			case dataStream <- v:
			}
		}
	}()

	return dataStream, nil
}

// RepeatBuffered creates a buffered channel and repeatedly sends values to it in a loop until context is cancelled.
// The buffer allows the producer to send values without blocking until the buffer is full.
//
// Parameters:
//   - bufferSize: Channel buffer size (must be >= 0). Use 0 for unbuffered channel.
//   - values: Values to send repeatedly to the channel
//
// Time Complexity: O(∞) - runs indefinitely until context cancellation
// Space Complexity: O(b) where b is the bufferSize
// Goroutines: Creates 1 goroutine that exits when context is cancelled
//
// Common Pitfalls:
//   - Negative bufferSize returns an error
//   - Forgetting to cancel context causes infinite execution and goroutine leak
//   - Buffer too small may cause blocking if consumer is slow
//   - Not consuming from the channel will block the goroutine
//
// Best Practices:
//   - Always use a cancellable context (WithCancel, WithTimeout, or WithDeadline)
//   - Choose buffer size based on expected burst consumption patterns
//   - Ensure consumers can keep up with production rate
//   - Use Take() to limit the number of values if needed
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
//	defer cancel()
//	c := NewChanx[string]()
//
//	// Buffer size of 5 allows bursts of up to 5 values
//	ch, err := c.RepeatBuffered(ctx, 5, "hello", "world")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	count := 0
//	for v := range ch {
//	    fmt.Println(v) // Prints: hello, world, hello, world, ...
//	    count++
//	    if count >= 6 {
//	        cancel()
//	        break
//	    }
//	}
func (c *Chanx[T]) RepeatBuffered(ctx context.Context, bufferSize int, values ...T) (<-chan T, error) {
	if bufferSize < 0 {
		return nil, fmt.Errorf("%w: got %d, must be at least 0", ErrInvalidBufferSize, bufferSize)
	}

	dataStream := make(chan T, bufferSize)

	go func() {
		defer close(dataStream)
		for {
			for _, v := range values {
				select {
				case <-ctx.Done():
					return
				case dataStream <- v:
				}
			}
		}
	}()

	return dataStream, nil
}
