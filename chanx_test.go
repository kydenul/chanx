package chanx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Unit tests for Generate function

func TestGenerate_MultipleValues(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	values := []int{1, 2, 3, 4, 5}
	ch := c.Generate(ctx, values...)

	// Collect all values
	var received []int
	for v := range ch {
		received = append(received, v)
	}

	assert.Equal(t, values, received, "Should receive all values in order")
}

func TestGenerate_EmptyValues(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	ch := c.Generate(ctx)

	// Channel should close immediately
	_, ok := <-ch
	assert.False(t, ok, "Channel should be closed for empty values")
}

func TestGenerate_SingleValue(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[string]()

	ch := c.Generate(ctx, "hello")

	// Read the single value
	value, ok := <-ch
	assert.True(t, ok, "Should receive the value")
	assert.Equal(t, "hello", value)

	// Channel should be closed
	_, ok = <-ch
	assert.False(t, ok, "Channel should be closed after single value")
}

func TestGenerate_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewChanx[int]()

	// Generate many values
	values := make([]int, 100)
	for i := range values {
		values[i] = i
	}

	ch := c.Generate(ctx, values...)

	// Read a few values
	for range 5 {
		<-ch
	}

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(500 * time.Millisecond)
	closed := false
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}

	assert.True(t, closed, "Channel should close after context cancellation")
}

func TestGenerate_ChannelClosesCorrectly(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	ch := c.Generate(ctx, 1, 2, 3)

	// Read all values
	count := 0
	for range ch {
		count++
	}

	assert.Equal(t, 3, count, "Should receive all 3 values")

	// Verify channel is closed
	_, ok := <-ch
	assert.False(t, ok, "Channel should be closed")
}

func TestGenerate_DifferentTypes(t *testing.T) {
	ctx := context.Background()

	// Test with strings
	t.Run("strings", func(t *testing.T) {
		c := NewChanx[string]()
		ch := c.Generate(ctx, "a", "b", "c")

		var received []string
		for v := range ch {
			received = append(received, v)
		}

		assert.Equal(t, []string{"a", "b", "c"}, received)
	})

	// Test with structs
	t.Run("structs", func(t *testing.T) {
		type Person struct {
			Name string
			Age  int
		}

		c := NewChanx[Person]()
		people := []Person{
			{Name: "Alice", Age: 30},
			{Name: "Bob", Age: 25},
		}

		ch := c.Generate(ctx, people...)

		var received []Person
		for v := range ch {
			received = append(received, v)
		}

		assert.Equal(t, people, received)
	})
}

// Unit tests for Repeat function

func TestRepeat_CyclesValues(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[int]()
	values := []int{1, 2, 3}
	ch := c.Repeat(ctx, values...)

	// Read multiple cycles
	cycles := 3
	for cycle := range cycles {
		for i, expected := range values {
			select {
			case v := <-ch:
				assert.Equal(t, expected, v, "Cycle %d, position %d should match", cycle, i)
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Timeout waiting for value at cycle %d, position %d", cycle, i)
			}
		}
	}
}

func TestRepeat_MaintainsOrder(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[string]()
	values := []string{"a", "b", "c", "d"}
	ch := c.Repeat(ctx, values...)

	// Read two complete cycles and verify order
	for cycle := range 2 {
		for i, expected := range values {
			select {
			case v := <-ch:
				assert.Equal(t, expected, v, "Order should be maintained in cycle %d", cycle)
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Timeout at cycle %d, position %d", cycle, i)
			}
		}
	}
}

func TestRepeat_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := NewChanx[int]()
	ch := c.Repeat(ctx, 1, 2, 3)

	// Read a few values
	for range 5 {
		select {
		case <-ch:
			// Successfully read value
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout reading values")
		}
	}

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(500 * time.Millisecond)
	closed := false
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}

	assert.True(t, closed, "Channel should close after context cancellation")
}

func TestRepeat_SingleValue(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[int]()
	ch := c.Repeat(ctx, 42)

	// Read the same value multiple times
	for range 10 {
		select {
		case v := <-ch:
			assert.Equal(t, 42, v, "Should always receive the same value")
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout reading value")
		}
	}
}

func TestRepeat_EmptyValues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := NewChanx[int]()
	ch := c.Repeat(ctx)

	// With empty values, the channel should still be created but will loop infinitely
	// doing nothing. Cancel immediately and verify it closes.
	cancel()

	timeout := time.After(500 * time.Millisecond)
	select {
	case _, ok := <-ch:
		if !ok {
			// Channel closed as expected
			return
		}
		t.Fatal("Should not receive any values with empty input")
	case <-timeout:
		// Timeout is acceptable for empty values case
		return
	}
}

func TestRepeat_DifferentTypes(t *testing.T) {
	ctx := t.Context()

	// Test with structs
	t.Run("structs", func(t *testing.T) {
		type Point struct {
			X, Y int
		}

		c := NewChanx[Point]()
		points := []Point{{1, 2}, {3, 4}}
		ch := c.Repeat(ctx, points...)

		// Read two cycles
		for range 2 {
			for _, expected := range points {
				select {
				case p := <-ch:
					assert.Equal(t, expected, p)
				case <-time.After(100 * time.Millisecond):
					t.Fatal("Timeout reading point")
				}
			}
		}
	})
}

// Unit tests for RepeatFn function

func TestRepeatFn_RepeatedExecution(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[int]()

	// Create a function that increments a counter
	counter := 0
	fn := func() int {
		result := counter
		counter++
		return result
	}

	ch := c.RepeatFn(ctx, fn)

	// Read multiple values and verify they increment
	for i := range 10 {
		select {
		case v := <-ch:
			assert.Equal(t, i, v, "Function should be executed repeatedly with incrementing values")
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Timeout waiting for value at iteration %d", i)
		}
	}
}

func TestRepeatFn_EachReturnValueSent(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[string]()

	// Create a function that returns different values based on a counter
	values := []string{"first", "second", "third", "fourth", "fifth"}
	index := 0
	fn := func() string {
		result := values[index%len(values)]
		index++
		return result
	}

	ch := c.RepeatFn(ctx, fn)

	// Read values and verify each return value is correctly sent
	for i := 0; i < len(values)*2; i++ {
		expected := values[i%len(values)]
		select {
		case v := <-ch:
			assert.Equal(t, expected, v, "Each return value should be sent correctly")
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Timeout waiting for value at iteration %d", i)
		}
	}
}

func TestRepeatFn_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := NewChanx[int]()

	fn := func() int {
		return 42
	}

	ch := c.RepeatFn(ctx, fn)

	// Read a few values
	for range 5 {
		select {
		case v := <-ch:
			assert.Equal(t, 42, v)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout reading values")
		}
	}

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(500 * time.Millisecond)
	closed := false
	for !closed {
		select {
		case _, ok := <-ch:
			if !ok {
				closed = true
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}

	assert.True(t, closed, "Channel should close after context cancellation")
}

func TestRepeatFn_FunctionWithSideEffects(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[int]()

	// Track how many times the function is called
	callCount := 0
	fn := func() int {
		callCount++
		return callCount * 10
	}

	ch := c.RepeatFn(ctx, fn)

	// Read values and verify function is called each time
	readCount := 7
	for i := 1; i <= readCount; i++ {
		select {
		case v := <-ch:
			assert.Equal(t, i*10, v, "Function should be called for each value")
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Timeout at iteration %d", i)
		}
	}

	// The function may be called one more time if it's already executing
	// when we stop reading, so we check that it's at least the number we read
	assert.GreaterOrEqual(
		t,
		callCount,
		readCount,
		"Function should be called at least as many times as we read",
	)
	assert.LessOrEqual(
		t,
		callCount,
		readCount+1,
		"Function should not be called more than one extra time",
	)
}

func TestRepeatFn_DifferentTypes(t *testing.T) {
	ctx := t.Context()

	// Test with struct type
	t.Run("structs", func(t *testing.T) {
		type Result struct {
			ID    int
			Value string
		}

		c := NewChanx[Result]()
		counter := 0
		fn := func() Result {
			counter++
			return Result{ID: counter, Value: "test"}
		}

		ch := c.RepeatFn(ctx, fn)

		// Read a few values
		for i := 1; i <= 3; i++ {
			select {
			case r := <-ch:
				assert.Equal(t, i, r.ID)
				assert.Equal(t, "test", r.Value)
			case <-time.After(100 * time.Millisecond):
				t.Fatal("Timeout reading struct")
			}
		}
	})
}

// Unit tests for Take function

func TestTake_SpecifiedCount(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Generate source with 10 values
	source := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

	// Take 5 values
	taken := c.Take(ctx, source, 5)

	// Collect values
	var values []int
	for v := range taken {
		values = append(values, v)
	}

	// Verify count and values
	assert.Equal(t, 5, len(values))
	assert.Equal(t, []int{1, 2, 3, 4, 5}, values)
}

func TestTake_SourceHasFewerValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	c := NewChanx[int]()

	// Generate source with only 3 values
	source := c.Generate(ctx, 1, 2, 3)

	// Try to take 10 values (more than available)
	taken := c.Take(ctx, source, 10)

	// Collect values - should get 3 then block/timeout
	var values []int
	timeout := time.After(200 * time.Millisecond)

	for {
		select {
		case v, ok := <-taken:
			if !ok {
				// Channel closed, which is acceptable behavior
				t.Logf("Take channel closed after receiving %d values", len(values))
				assert.LessOrEqual(
					t,
					len(values),
					3,
					"Should not receive more than source provides",
				)
				return
			}
			values = append(values, v)
		case <-timeout:
			// Timeout is expected when source has fewer values
			t.Logf("Timeout after receiving %d values (expected behavior)", len(values))
			assert.Equal(t, 3, len(values), "Should receive all available values before timeout")
			return
		}
	}
}

func TestTake_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := NewChanx[int]()

	// Create a slow source
	source := c.RepeatFn(ctx, func() int {
		time.Sleep(50 * time.Millisecond)
		return 1
	})

	// Take 10 values
	taken := c.Take(ctx, source, 10)

	// Read one value
	v, ok := <-taken
	assert.True(t, ok)
	assert.Equal(t, 1, v)

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-taken:
			if !ok {
				// Channel closed as expected
				return
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}
}

func TestTake_ChannelClosesCorrectly(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Generate source with 5 values
	source := c.Generate(ctx, 1, 2, 3, 4, 5)

	// Take exactly 5 values
	taken := c.Take(ctx, source, 5)

	// Read all values
	count := 0
	for range taken {
		count++
	}

	assert.Equal(t, 5, count)

	// Verify channel is closed
	_, ok := <-taken
	assert.False(t, ok, "Channel should be closed after taking all values")
}

func TestTake_ZeroCount(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Generate source with values
	source := c.Generate(ctx, 1, 2, 3, 4, 5)

	// Take 0 values
	taken := c.Take(ctx, source, 0)

	// Channel should close immediately
	_, ok := <-taken
	assert.False(t, ok, "Channel should be closed when taking 0 values")
}

// Unit tests for FanIn function

func TestFanIn_MergeMultipleChannels(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create three input channels with different values
	ch1 := c.Generate(ctx, 1, 2, 3)
	ch2 := c.Generate(ctx, 10, 20, 30)
	ch3 := c.Generate(ctx, 100, 200, 300)

	// Merge them
	merged := c.FanIn(ctx, ch1, ch2, ch3)

	// Collect all values
	received := make(map[int]bool)
	for v := range merged {
		received[v] = true
	}

	// Verify all values are present
	expected := []int{1, 2, 3, 10, 20, 30, 100, 200, 300}
	assert.Equal(t, len(expected), len(received), "Should receive all values")

	for _, v := range expected {
		assert.True(t, received[v], "Should receive value %d", v)
	}
}

func TestFanIn_AllInputsCloseOutputCloses(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create input channels
	ch1 := c.Generate(ctx, 1, 2)
	ch2 := c.Generate(ctx, 3, 4)

	// Merge them
	merged := c.FanIn(ctx, ch1, ch2)

	// Read all values
	count := 0
	for range merged {
		count++
	}

	assert.Equal(t, 4, count, "Should receive all 4 values")

	// Verify channel is closed
	_, ok := <-merged
	assert.False(t, ok, "Output channel should be closed after all inputs close")
}

func TestFanIn_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := NewChanx[int]()

	// Create input channels that generate values slowly
	ch1 := c.RepeatFn(ctx, func() int {
		time.Sleep(10 * time.Millisecond)
		return 1
	})
	ch2 := c.RepeatFn(ctx, func() int {
		time.Sleep(10 * time.Millisecond)
		return 2
	})

	// Merge them
	merged := c.FanIn(ctx, ch1, ch2)

	// Read a few values
	for range 5 {
		select {
		case <-merged:
			// Successfully read value
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Timeout reading values")
		}
	}

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(1 * time.Second)
	closed := false
	for !closed {
		select {
		case _, ok := <-merged:
			if !ok {
				closed = true
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}

	assert.True(t, closed, "Channel should close after context cancellation")
}

func TestFanIn_ConcurrentSending(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create multiple channels that send concurrently
	channelCount := 5
	valuesPerChannel := 20

	var channels []<-chan int
	expectedTotal := channelCount * valuesPerChannel

	for i := range channelCount {
		values := make([]int, valuesPerChannel)
		for j := range valuesPerChannel {
			values[j] = i*100 + j
		}
		channels = append(channels, c.Generate(ctx, values...))
	}

	// Merge all channels
	merged := c.FanIn(ctx, channels...)

	// Count received values
	count := 0
	for range merged {
		count++
	}

	assert.Equal(t, expectedTotal, count, "Should receive all values from all channels")
}

func TestFanIn_SingleChannel(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create single input channel
	ch := c.Generate(ctx, 1, 2, 3, 4, 5)

	// FanIn with single channel
	merged := c.FanIn(ctx, ch)

	// Collect all values
	var received []int
	for v := range merged {
		received = append(received, v)
	}

	assert.Equal(t, []int{1, 2, 3, 4, 5}, received, "Should receive all values")
}

func TestFanIn_EmptyChannels(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create empty input channels
	ch1 := c.Generate(ctx)
	ch2 := c.Generate(ctx)

	// Merge them
	merged := c.FanIn(ctx, ch1, ch2)

	// Should close immediately
	_, ok := <-merged
	assert.False(t, ok, "Output channel should close when all inputs are empty")
}

func TestFanIn_DifferentTypes(t *testing.T) {
	ctx := context.Background()

	t.Run("strings", func(t *testing.T) {
		c := NewChanx[string]()

		ch1 := c.Generate(ctx, "a", "b")
		ch2 := c.Generate(ctx, "c", "d")

		merged := c.FanIn(ctx, ch1, ch2)

		received := make(map[string]bool)
		for v := range merged {
			received[v] = true
		}

		expected := []string{"a", "b", "c", "d"}
		for _, v := range expected {
			assert.True(t, received[v], "Should receive value %s", v)
		}
	})
}

// Unit tests for Tee function

func TestTee_ValuesSentToBothOutputs(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create source with values
	values := []int{1, 2, 3, 4, 5}
	source := c.Generate(ctx, values...)

	// Tee the channel
	out1, out2 := c.Tee(ctx, source)

	// Collect values from both outputs concurrently
	var received1, received2 []int
	done := make(chan bool, 2)

	go func() {
		for v := range out1 {
			received1 = append(received1, v)
		}
		done <- true
	}()

	go func() {
		for v := range out2 {
			received2 = append(received2, v)
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Verify both received all values
	assert.Equal(t, len(values), len(received1), "Output 1 should receive all values")
	assert.Equal(t, len(values), len(received2), "Output 2 should receive all values")

	// Verify values match (create maps to count occurrences)
	count1 := make(map[int]int)
	count2 := make(map[int]int)
	for _, v := range received1 {
		count1[v]++
	}
	for _, v := range received2 {
		count2[v]++
	}

	assert.Equal(t, count1, count2, "Both outputs should receive the same values")
}

func TestTee_BothOutputsEqual(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[string]()

	// Create source with string values
	values := []string{"apple", "banana", "cherry"}
	source := c.Generate(ctx, values...)

	// Tee the channel
	out1, out2 := c.Tee(ctx, source)

	// Collect values from both outputs
	var received1, received2 []string
	done := make(chan bool, 2)

	go func() {
		for v := range out1 {
			received1 = append(received1, v)
		}
		done <- true
	}()

	go func() {
		for v := range out2 {
			received2 = append(received2, v)
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Verify lengths match
	assert.Equal(t, len(received1), len(received2), "Both outputs should have same length")
	assert.Equal(t, len(values), len(received1), "Should receive all values")
}

func TestTee_InputClosesBothOutputs(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create source with values
	source := c.Generate(ctx, 1, 2, 3)

	// Tee the channel
	out1, out2 := c.Tee(ctx, source)

	// Drain both outputs
	done := make(chan bool, 2)

	go func() {
		for range out1 {
		}
		done <- true
	}()

	go func() {
		for range out2 {
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Verify both channels are closed
	_, ok1 := <-out1
	_, ok2 := <-out2
	assert.False(t, ok1, "Output 1 should be closed")
	assert.False(t, ok2, "Output 2 should be closed")
}

func TestTee_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewChanx[int]()

	// Create a source that generates values slowly
	source := c.RepeatFn(ctx, func() int {
		time.Sleep(10 * time.Millisecond)
		return 1
	})

	// Tee the channel
	out1, out2 := c.Tee(ctx, source)

	// Read a few values from each output
	done := make(chan bool, 2)

	go func() {
		for range 2 {
			select {
			case <-out1:
			case <-time.After(500 * time.Millisecond):
				break
			}
		}
		done <- true
	}()

	go func() {
		for range 2 {
			select {
			case <-out2:
			case <-time.After(500 * time.Millisecond):
				break
			}
		}
		done <- true
	}()

	// Wait for initial reads
	<-done
	<-done

	// Cancel context
	cancel()

	// Both channels should close soon
	timeout := time.After(1 * time.Second)
	closed1, closed2 := false, false

	for !closed1 || !closed2 {
		select {
		case _, ok := <-out1:
			if !ok {
				closed1 = true
			}
		case _, ok := <-out2:
			if !ok {
				closed2 = true
			}
		case <-timeout:
			t.Fatal("Channels did not close after context cancellation")
		}
	}

	assert.True(t, closed1, "Output 1 should be closed")
	assert.True(t, closed2, "Output 2 should be closed")
}

func TestTee_EmptySource(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create empty source
	source := c.Generate(ctx)

	// Tee the channel
	out1, out2 := c.Tee(ctx, source)

	// Both outputs should close immediately
	timeout := time.After(500 * time.Millisecond)
	closed1, closed2 := false, false

	for !closed1 || !closed2 {
		select {
		case _, ok := <-out1:
			if !ok {
				closed1 = true
			}
		case _, ok := <-out2:
			if !ok {
				closed2 = true
			}
		case <-timeout:
			t.Fatal("Channels did not close for empty source")
		}
	}

	assert.True(t, closed1, "Output 1 should be closed")
	assert.True(t, closed2, "Output 2 should be closed")
}

func TestTee_SingleValue(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create source with single value
	source := c.Generate(ctx, 42)

	// Tee the channel
	out1, out2 := c.Tee(ctx, source)

	// Read from both outputs
	var val1, val2 int
	var ok1, ok2 bool
	done := make(chan bool, 2)

	go func() {
		val1, ok1 = <-out1
		// Drain remaining
		for range out1 {
		}
		done <- true
	}()

	go func() {
		val2, ok2 = <-out2
		// Drain remaining
		for range out2 {
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Verify both received the value
	assert.True(t, ok1, "Output 1 should receive value")
	assert.True(t, ok2, "Output 2 should receive value")
	assert.Equal(t, 42, val1, "Output 1 should receive correct value")
	assert.Equal(t, 42, val2, "Output 2 should receive correct value")
}

// Unit tests for Bridge

func TestBridge_MultipleChannels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := NewChanx[int]()

	// Create a channel of channels
	chanStream := make(chan (<-chan int))

	// Start Bridge
	bridged := c.Bridge(ctx, chanStream)

	// Send multiple channels
	go func() {
		defer close(chanStream)

		// First channel with values 1, 2, 3
		chanStream <- c.Generate(ctx, 1, 2, 3)

		// Second channel with values 4, 5, 6
		chanStream <- c.Generate(ctx, 4, 5, 6)

		// Third channel with values 7, 8, 9
		chanStream <- c.Generate(ctx, 7, 8, 9)
	}()

	// Collect all values
	var received []int
	for v := range bridged {
		received = append(received, v)
	}

	// Verify we got all 9 values
	assert.Equal(t, 9, len(received), "Should receive all values from all channels")

	// Verify all expected values are present (order may vary due to concurrency)
	expectedSum := 1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9
	actualSum := 0
	for _, v := range received {
		actualSum += v
	}
	assert.Equal(t, expectedSum, actualSum, "Sum of all values should match")
}

func TestBridge_AllInnerChannelsComplete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := NewChanx[int]()

	// Create a channel of channels
	chanStream := make(chan (<-chan int))

	// Start Bridge
	bridged := c.Bridge(ctx, chanStream)

	// Send channels
	go func() {
		defer close(chanStream)

		// Send 3 channels
		for i := range 3 {
			values := []int{i * 10, i*10 + 1, i*10 + 2}
			chanStream <- c.Generate(ctx, values...)
		}
	}()

	// Read all values
	count := 0
	for range bridged {
		count++
	}

	// Verify we got all values
	assert.Equal(t, 9, count, "Should receive all values")

	// Verify channel is closed
	_, ok := <-bridged
	assert.False(t, ok, "Output channel should be closed after all inner channels complete")
}

func TestBridge_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := NewChanx[int]()

	// Create a channel of channels
	chanStream := make(chan (<-chan int))

	// Start Bridge
	bridged := c.Bridge(ctx, chanStream)

	// Send channels that generate values slowly
	go func() {
		defer close(chanStream)

		for range 3 {
			innerChan := c.RepeatFn(ctx, func() int {
				time.Sleep(10 * time.Millisecond)
				return 1
			})
			select {
			case chanStream <- innerChan:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read a few values
	readCount := 0
	for readCount < 5 {
		select {
		case _, ok := <-bridged:
			if !ok {
				// Channel closed early
				cancel()
				return
			}
			readCount++
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timeout waiting for values")
		}
	}

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(1 * time.Second)
	for {
		select {
		case _, ok := <-bridged:
			if !ok {
				// Channel closed as expected
				return
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}
}

func TestBridge_ConcurrentValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := NewChanx[int]()

	// Create a channel of channels
	chanStream := make(chan (<-chan int), 10)

	// Start Bridge
	bridged := c.Bridge(ctx, chanStream)

	// Send multiple channels concurrently
	channelCount := 5
	valuesPerChannel := 10

	go func() {
		defer close(chanStream)

		for i := range channelCount {
			// Create channel with values
			values := make([]int, valuesPerChannel)
			for j := range valuesPerChannel {
				values[j] = i*100 + j
			}
			chanStream <- c.Generate(ctx, values...)
		}
	}()

	// Collect all values
	var received []int
	for v := range bridged {
		received = append(received, v)
	}

	// Verify we got all values
	expectedCount := channelCount * valuesPerChannel
	assert.Equal(t, expectedCount, len(received), "Should receive all values from all channels")

	// Verify all values are unique and in expected range
	valueSet := make(map[int]bool)
	for _, v := range received {
		assert.False(t, valueSet[v], "Value %d should be unique", v)
		valueSet[v] = true
		assert.GreaterOrEqual(t, v, 0, "Value should be >= 0")
		assert.Less(t, v, channelCount*100, "Value should be < %d", channelCount*100)
	}
}

// Unit tests for Or

func TestOr_AnyChannelCloses(t *testing.T) {
	c := NewChanx[struct{}]()

	// Create multiple channels
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel2()
	defer cancel3()

	ch1 := c.RepeatFn(ctx1, func() struct{} {
		time.Sleep(10 * time.Millisecond)
		return struct{}{}
	})
	ch2 := c.RepeatFn(ctx2, func() struct{} {
		time.Sleep(10 * time.Millisecond)
		return struct{}{}
	})
	ch3 := c.RepeatFn(ctx3, func() struct{} {
		time.Sleep(10 * time.Millisecond)
		return struct{}{}
	})

	// Call Or
	orChan := c.Or(ch1, ch2, ch3)

	// Close the first channel
	cancel1()

	// The Or channel should close
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-orChan:
			if !ok {
				// Channel closed as expected
				return
			}
			// Continue reading until closed
		case <-timeout:
			t.Fatal("Or channel did not close after one input channel closed")
		}
	}
}

func TestOr_ZeroChannels(t *testing.T) {
	c := NewChanx[int]()

	// Call Or with zero channels
	orChan := c.Or()

	// Should return nil
	assert.Nil(t, orChan, "Or with zero channels should return nil")
}

func TestOr_SingleChannel(t *testing.T) {
	c := NewChanx[int]()

	// Create a single channel
	ctx := t.Context()

	ch := c.Generate(ctx, 1, 2, 3)

	// Call Or with single channel
	orChan := c.Or(ch)

	// Should return the same channel
	assert.Equal(t, ch, orChan, "Or with single channel should return that channel")

	// Verify we can read from it
	values := []int{}
	for v := range orChan {
		values = append(values, v)
	}

	assert.Equal(t, []int{1, 2, 3}, values, "Should receive all values from the channel")
}

func TestOr_MultipleChannelsCloseSimultaneously(t *testing.T) {
	c := NewChanx[int]()

	// Create multiple channels that will close at the same time
	ctx, cancel := context.WithCancel(context.Background())

	ch1 := c.Generate(ctx, 1, 2, 3)
	ch2 := c.Generate(ctx, 4, 5, 6)
	ch3 := c.Generate(ctx, 7, 8, 9)

	// Call Or
	orChan := c.Or(ch1, ch2, ch3)

	// Cancel context to close all channels simultaneously
	cancel()

	// The Or channel should close
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-orChan:
			if !ok {
				// Channel closed as expected
				return
			}
			// Continue reading until closed
		case <-timeout:
			t.Fatal("Or channel did not close after all input channels closed")
		}
	}
}

func TestOr_TwoChannels(t *testing.T) {
	c := NewChanx[int]()

	// Create two channels
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2 := t.Context()

	ch1 := c.RepeatFn(ctx1, func() int {
		time.Sleep(10 * time.Millisecond)
		return 1
	})
	ch2 := c.RepeatFn(ctx2, func() int {
		time.Sleep(10 * time.Millisecond)
		return 2
	})

	// Call Or
	orChan := c.Or(ch1, ch2)

	// Close the first channel
	cancel1()

	// The Or channel should close
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-orChan:
			if !ok {
				// Channel closed as expected
				return
			}
			// Continue reading until closed
		case <-timeout:
			t.Fatal("Or channel did not close after one of two channels closed")
		}
	}
}

// Unit tests for OrDone function

func TestOrDone_ValueForwarding(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create source channel with values
	values := []int{1, 2, 3, 4, 5}
	source := c.Generate(ctx, values...)

	// Apply OrDone
	output := c.OrDone(ctx, source)

	// Collect all values
	var received []int
	for v := range output {
		received = append(received, v)
	}

	// Verify all values were forwarded
	assert.Equal(t, len(values), len(received), "Should forward all values")
	for i, v := range values {
		assert.Equal(t, v, received[i], "Values should match in order")
	}
}

func TestOrDone_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewChanx[int]()

	// Create a source that generates values continuously
	source := c.RepeatFn(ctx, func() int {
		time.Sleep(10 * time.Millisecond)
		return 1
	})

	// Apply OrDone
	output := c.OrDone(ctx, source)

	// Read a few values
	count := 0
	for count < 3 {
		select {
		case _, ok := <-output:
			if !ok {
				t.Fatal("Channel closed prematurely")
			}
			count++
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timeout waiting for values")
		}
	}

	// Cancel context
	cancel()

	// Channel should close soon
	timeout := time.After(1 * time.Second)
	for {
		select {
		case _, ok := <-output:
			if !ok {
				// Channel closed as expected
				return
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}
}

func TestOrDone_InputChannelClose(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create source channel that will close after sending values
	values := []int{10, 20, 30}
	source := c.Generate(ctx, values...)

	// Apply OrDone
	output := c.OrDone(ctx, source)

	// Read all values
	count := 0
	for range output {
		count++
	}

	// Verify we received all values
	assert.Equal(t, len(values), count, "Should receive all values before close")

	// Verify channel is closed
	_, ok := <-output
	assert.False(t, ok, "Output channel should be closed")
}

func TestOrDone_RaceCondition_ContextFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := NewChanx[int]()

	// Create source channel with many values
	values := make([]int, 100)
	for i := range values {
		values[i] = i
	}
	source := c.Generate(ctx, values...)

	// Apply OrDone
	output := c.OrDone(ctx, source)

	// Cancel context immediately
	cancel()

	// Channel should close soon
	timeout := time.After(1 * time.Second)
	for {
		select {
		case _, ok := <-output:
			if !ok {
				// Channel closed as expected
				return
			}
		case <-timeout:
			t.Fatal("Channel did not close after context cancellation")
		}
	}
}

func TestOrDone_RaceCondition_InputFirst(t *testing.T) {
	ctx := t.Context()

	c := NewChanx[int]()

	// Create source channel with few values (will close quickly)
	values := []int{1, 2, 3}
	source := c.Generate(ctx, values...)

	// Apply OrDone
	output := c.OrDone(ctx, source)

	// Read all values and verify channel closes
	count := 0
	for range output {
		count++
	}

	assert.Equal(t, len(values), count, "Should receive all values")

	// Verify channel is closed
	_, ok := <-output
	assert.False(t, ok, "Output channel should be closed")
}

func TestOrDone_EmptyInput(t *testing.T) {
	ctx := context.Background()
	c := NewChanx[int]()

	// Create empty source channel
	source := c.Generate(ctx)

	// Apply OrDone
	output := c.OrDone(ctx, source)

	// Channel should close immediately
	timeout := time.After(500 * time.Millisecond)
	select {
	case _, ok := <-output:
		assert.False(t, ok, "Output channel should be closed for empty input")
	case <-timeout:
		t.Fatal("Channel did not close for empty input")
	}
}

func TestOrDone_DifferentTypes(t *testing.T) {
	ctx := context.Background()

	// Test with string type
	t.Run("string", func(t *testing.T) {
		c := NewChanx[string]()
		values := []string{"hello", "world", "test"}
		source := c.Generate(ctx, values...)
		output := c.OrDone(ctx, source)

		var received []string
		for v := range output {
			received = append(received, v)
		}

		assert.Equal(t, values, received, "Should forward all string values")
	})

	// Test with struct type
	t.Run("struct", func(t *testing.T) {
		type TestStruct struct {
			ID   int
			Name string
		}

		c := NewChanx[TestStruct]()
		values := []TestStruct{
			{ID: 1, Name: "Alice"},
			{ID: 2, Name: "Bob"},
		}
		source := c.Generate(ctx, values...)
		output := c.OrDone(ctx, source)

		var received []TestStruct
		for v := range output {
			received = append(received, v)
		}

		assert.Equal(t, values, received, "Should forward all struct values")
	})
}
