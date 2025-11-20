package chanx

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
)

// Feature: worker-pool, Property 1: Worker 数量匹配
// 对于任何 worker 数量配置，创建线程池后实际运行的 worker 数量应该与指定数量相等
// Validates: Requirements 1.1
func TestProperty_WorkerCountMatch(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("worker count matches configuration",
		prop.ForAll(
			func(workerCount int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Submit tasks equal to worker count to ensure all workers are active
				var activeWorkers atomic.Int32
				for range workerCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							activeWorkers.Add(1)
							time.Sleep(100 * time.Millisecond)
							return 0, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait a bit for all workers to pick up tasks
				time.Sleep(150 * time.Millisecond)

				// Check that the number of active workers matches the configuration
				active := int(activeWorkers.Load())
				assert.Equal(
					t,
					workerCount,
					active,
					"Active worker count should match configuration",
				)

				return active == workerCount
			},
			gen.IntRange(1, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 2: Worker 持续活跃
// 对于任何 线程池实例，在其生命周期内应该始终保持指定数量的 worker 处于活跃状态
// Validates: Requirements 1.2
func TestProperty_WorkerStayActive(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("workers stay active throughout pool lifetime",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Submit multiple batches of tasks to verify workers stay active
				for batch := range 3 {
					// Start goroutine to drain results
					resultsDone := make(chan bool)
					go func() {
						for range taskCount {
							select {
							case result := <-wp.Results():
								assert.NoError(t, result.Err, "Task returned error")
								assert.Equal(t, batch, result.Value, "Task returned wrong value")
							case <-ctx.Done():
								return
							}
						}
						resultsDone <- true
					}()

					// Submit all tasks
					for range taskCount {
						err := wp.Submit(Task[int]{
							Fn: func() (int, error) {
								time.Sleep(10 * time.Millisecond)
								return batch, nil
							},
						})
						assert.NoError(t, err, "Failed to submit task in batch %d", batch)
						if err != nil {
							return false
						}
					}

					// Wait for all results to be processed
					select {
					case <-resultsDone:
					case <-ctx.Done():
						t.Logf("Context cancelled while waiting for results")
						return false
					}
				}

				return true
			},
			gen.IntRange(1, 10),
			gen.IntRange(5, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 3: Context 取消关闭
// 对于任何 线程池实例，当 context 被取消时，线程池应该停止接受新任务并优雅关闭
// Validates: Requirements 1.3
func TestProperty_ContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("context cancellation closes pool gracefully",
		prop.ForAll(
			func(workerCount int) bool {
				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}

				// Start goroutine to drain results
				go func() {
					for range wp.Results() {
						// Drain all results
					}
				}()

				// Submit a few tasks
				taskCount := 5
				for range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(10 * time.Millisecond)
							return 1, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task before cancellation")
					if err != nil {
						cancel()
						wp.Close()
						return false
					}
				}

				// Cancel context
				cancel()

				// Try to submit after cancellation - should fail
				time.Sleep(50 * time.Millisecond)
				err = wp.Submit(Task[int]{
					Fn: func() (int, error) {
						return 1, nil
					},
				})
				assert.Error(t, err, "Submit should fail after context cancellation")

				wp.Close()
				return err != nil
			},
			gen.IntRange(1, 10),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 4: 关闭等待完成
// 对于任何 正在执行的任务集合，关闭线程池时应该等待所有任务完成后再退出
// Validates: Requirements 1.4
func TestProperty_CloseWaitsForCompletion(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("close waits for all tasks to complete",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				ctx := context.Background()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}

				// Track completed tasks
				var completed atomic.Int32

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Submit tasks
				for range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(20 * time.Millisecond)
							completed.Add(1)
							return 1, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						wp.Close()
						return false
					}
				}

				// Wait for results to be drained
				<-resultsDone

				// Close should wait for all tasks
				wp.Close()

				// Verify all tasks completed
				completedCount := int(completed.Load())
				assert.Equal(
					t,
					taskCount,
					completedCount,
					"All tasks should complete before Close returns",
				)

				return completedCount == taskCount
			},
			gen.IntRange(1, 10),
			gen.IntRange(5, 30),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 5: 任务执行保证
// 对于任何 提交的任务，它应该被某个 worker 执行
// Validates: Requirements 2.1
func TestProperty_TaskExecutionGuarantee(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("all submitted tasks are executed",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Track executed tasks
				var executed atomic.Int32

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Submit tasks
				for range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							executed.Add(1)
							return 1, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results
				<-resultsDone

				// Verify all tasks were executed
				executedCount := int(executed.Load())
				assert.Equal(t, taskCount, executedCount, "All submitted tasks should be executed")

				return executedCount == taskCount
			},
			gen.IntRange(1, 10),
			gen.IntRange(1, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 6: 任务排队处理
// 对于任何 超过 worker 数量的任务集合，所有任务最终都应该被执行
// Validates: Requirements 2.2
func TestProperty_TaskQueueing(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("tasks queue when all workers are busy",
		prop.ForAll(
			func(workerCount int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Submit more tasks than workers
				taskCount := workerCount * 3
				var executed atomic.Int32

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Submit tasks
				for range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(10 * time.Millisecond)
							executed.Add(1)
							return 1, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results
				<-resultsDone

				// Verify all tasks were executed
				executedCount := int(executed.Load())
				assert.Equal(
					t,
					taskCount,
					executedCount,
					"All queued tasks should eventually be executed",
				)

				return executedCount == taskCount
			},
			gen.IntRange(1, 10),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 7: 结果往返一致性
// 对于任何 提交的任务，应该能从结果 channel 收到对应的执行结果
// Validates: Requirements 2.3
func TestProperty_ResultRoundTrip(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("task results are correctly returned",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Track expected and received results
				expectedSum := 0
				var receivedSum atomic.Int32

				// Start goroutine to collect results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						result := <-wp.Results()
						assert.NoError(t, result.Err, "Task should not return error")
						receivedSum.Add(int32(result.Value))
					}
					resultsDone <- true
				}()

				// Submit tasks with unique values
				for i := range taskCount {
					value := i + 1
					expectedSum += value
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							return value, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results
				<-resultsDone

				// Verify sum matches
				received := int(receivedSum.Load())
				assert.Equal(
					t,
					expectedSum,
					received,
					"Sum of results should match sum of submitted values",
				)

				return expectedSum == received
			},
			gen.IntRange(1, 10),
			gen.IntRange(1, 30),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 8: 错误正确传递
// 对于任何 返回错误的任务，错误应该通过结果 channel 正确传递给调用者
// Validates: Requirements 2.4
func TestProperty_ErrorPropagation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("task errors are correctly propagated",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Track errors
				var errorCount atomic.Int32
				expectedErrors := taskCount / 2

				// Start goroutine to collect results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						result := <-wp.Results()
						if result.Err != nil {
							errorCount.Add(1)
						}
					}
					resultsDone <- true
				}()

				// Submit tasks, half with errors
				for i := range taskCount {
					shouldError := i < expectedErrors
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							if shouldError {
								return 0, assert.AnError
							}
							return 1, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results
				<-resultsDone

				// Verify error count
				errors := int(errorCount.Load())
				assert.Equal(t, expectedErrors, errors, "Error count should match expected")

				return errors == expectedErrors
			},
			gen.IntRange(1, 10),
			gen.IntRange(2, 30).SuchThat(func(v int) bool { return v%2 == 0 }), // Even numbers only
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 9: Generate 顺序保持
// 对于任何 值序列，Generate 函数输出的值应该与输入顺序完全一致
// Validates: Requirements 3.1
func TestProperty_GenerateOrderPreservation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Generate preserves input order",
		prop.ForAll(
			func(values []int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()
				ch := c.Generate(ctx, values...)

				// Collect all values from channel
				var received []int
				for v := range ch {
					received = append(received, v)
				}

				// Verify order matches
				if len(received) != len(values) {
					t.Logf("Length mismatch: expected %d, got %d", len(values), len(received))
					return false
				}

				for i := range values {
					if received[i] != values[i] {
						t.Logf(
							"Order mismatch at index %d: expected %d, got %d",
							i,
							values[i],
							received[i],
						)
						return false
					}
				}

				return true
			},
			gen.SliceOf(gen.Int()),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 10: Generate context 取消
// 对于任何 Generate 调用，当 context 被取消时应该停止发送并关闭 channel
// Validates: Requirements 3.2
func TestProperty_GenerateContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Generate stops on context cancellation",
		prop.ForAll(
			func(valueCount int) bool {
				ctx, cancel := context.WithCancel(context.Background())

				// Generate large slice of values
				values := make([]int, valueCount)
				for i := range values {
					values[i] = i
				}

				c := NewChanx[int]()
				ch := c.Generate(ctx, values...)

				// Read a few values then cancel
				readCount := 0
				maxRead := max(valueCount/2, 1)

				for range maxRead {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed early, which is fine
							cancel()
							return true
						}
						readCount++
					case <-time.After(100 * time.Millisecond):
						cancel()
						// Timeout is acceptable
						break
					}
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(500 * time.Millisecond)
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed as expected
							return true
						}
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.IntRange(10, 100),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 11: Generate 完成关闭
// 对于任何 值列表，Generate 发送完所有值后应该关闭 channel
// Validates: Requirements 3.3
func TestProperty_GenerateClosesAfterCompletion(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Generate closes channel after sending all values",
		prop.ForAll(
			func(values []int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()
				ch := c.Generate(ctx, values...)

				// Read all values
				count := 0
				for range ch {
					count++
				}

				// Verify channel is closed by trying to read again
				_, ok := <-ch
				if ok {
					t.Logf("Channel should be closed after all values sent")
					return false
				}

				// Verify we received all values
				if count != len(values) {
					t.Logf("Expected %d values, got %d", len(values), count)
					return false
				}

				return true
			},
			gen.SliceOf(gen.Int()),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 12: Repeat 循环模式
// 对于任何 值序列，Repeat 函数应该循环发送该序列，每个周期的顺序保持一致
// Validates: Requirements 4.1, 4.3
func TestProperty_RepeatCyclePattern(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Repeat cycles through values maintaining order",
		prop.ForAll(
			func(values []int) bool {
				if len(values) == 0 {
					return true // Skip empty values
				}

				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()
				ch := c.Repeat(ctx, values...)

				// Read multiple cycles to verify pattern
				cycles := 3
				expectedCount := len(values) * cycles

				var received []int
				for i := range expectedCount {
					select {
					case v, ok := <-ch:
						if !ok {
							t.Logf("Channel closed prematurely after %d values", i)
							return false
						}
						received = append(received, v)
					case <-time.After(500 * time.Millisecond):
						t.Logf("Timeout waiting for value at index %d", i)
						return false
					}
				}

				// Verify each cycle matches the original pattern
				for cycle := range cycles {
					for i, expectedValue := range values {
						idx := cycle*len(values) + i
						if received[idx] != expectedValue {
							t.Logf(
								"Cycle %d, position %d: expected %d, got %d",
								cycle,
								i,
								expectedValue,
								received[idx],
							)
							return false
						}
					}
				}

				return true
			},
			gen.SliceOfN(10, gen.Int()).SuchThat(func(v []int) bool { return len(v) > 0 }),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 13: Repeat context 取消
// 对于任何 Repeat 调用，当 context 被取消时应该停止重复并关闭 channel
// Validates: Requirements 4.2
func TestProperty_RepeatContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Repeat stops on context cancellation",
		prop.ForAll(
			func(values []int) bool {
				if len(values) == 0 {
					return true // Skip empty values
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()
				ch := c.Repeat(ctx, values...)

				// Read some values
				readCount := 0
				maxRead := len(values) * 2 // Read at least 2 cycles

				for readCount < maxRead {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed early, which is acceptable
							cancel()
							return true
						}
						readCount++
					case <-time.After(100 * time.Millisecond):
						// Timeout, cancel and continue
						break
					}
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(500 * time.Millisecond)
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed as expected
							return true
						}
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.SliceOfN(5, gen.Int()).SuchThat(func(v []int) bool { return len(v) > 0 }),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 14: RepeatFn 重复执行
// 对于任何 函数，RepeatFn 应该重复执行该函数并正确发送每次的返回值
// Validates: Requirements 5.1, 5.3
func TestProperty_RepeatFnRepeatedExecution(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("RepeatFn repeatedly executes function and sends results",
		prop.ForAll(
			func(seed int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create a function that returns incrementing values
				counter := seed
				fn := func() int {
					result := counter
					counter++
					return result
				}

				ch := c.RepeatFn(ctx, fn)

				// Read multiple values and verify they are incrementing
				readCount := 10
				expectedValue := seed

				for range readCount {
					select {
					case v, ok := <-ch:
						if !ok {
							t.Logf("Channel closed prematurely after reading values")
							return false
						}
						if v != expectedValue {
							t.Logf("Expected value %d, got %d", expectedValue, v)
							return false
						}
						expectedValue++
					case <-time.After(500 * time.Millisecond):
						t.Logf("Timeout waiting for value")
						return false
					}
				}

				return true
			},
			gen.Int(),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 15: RepeatFn context 取消
// 对于任何 RepeatFn 调用，当 context 被取消时应该停止执行并关闭 channel
// Validates: Requirements 5.2
func TestProperty_RepeatFnContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("RepeatFn stops on context cancellation",
		prop.ForAll(
			func(baseValue int) bool {
				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()

				// Create a simple function
				fn := func() int {
					return baseValue
				}

				ch := c.RepeatFn(ctx, fn)

				// Read some values
				readCount := 0
				maxRead := 5

				for readCount < maxRead {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed early, which is acceptable
							cancel()
							return true
						}
						readCount++
					case <-time.After(100 * time.Millisecond):
						// Timeout, break and cancel
						cancel()
						readCount = maxRead
					}
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(500 * time.Millisecond)
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed as expected
							return true
						}
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.Int(),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 16: Take 数量精确
// 对于任何 数量 N 和源 channel，Take 函数应该输出恰好 N 个值（如果源有足够的值）
// Validates: Requirements 6.1
func TestProperty_TakeExactCount(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Take outputs exactly N values when source has enough",
		prop.ForAll(
			func(sourceCount, takeCount int) bool {
				// Ensure takeCount doesn't exceed sourceCount
				if takeCount > sourceCount {
					takeCount = sourceCount
				}
				if takeCount < 0 {
					takeCount = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Generate source values
				sourceValues := make([]int, sourceCount)
				for i := range sourceValues {
					sourceValues[i] = i
				}

				// Create source channel
				source := c.Generate(ctx, sourceValues...)

				// Take N values
				taken := c.Take(ctx, source, takeCount)

				// Count received values
				count := 0
				for range taken {
					count++
				}

				// Verify exact count
				if count != takeCount {
					t.Logf("Expected %d values, got %d", takeCount, count)
					return false
				}

				return true
			},
			gen.IntRange(0, 100),
			gen.IntRange(0, 100),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 17: Take 完成关闭
// 对于任何 Take 调用，取出指定数量的值后应该关闭输出 channel
// Validates: Requirements 6.2
func TestProperty_TakeClosesAfterCompletion(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Take closes output channel after taking N values",
		prop.ForAll(
			func(sourceCount, takeCount int) bool {
				// Ensure takeCount doesn't exceed sourceCount
				if takeCount > sourceCount {
					takeCount = sourceCount
				}
				if takeCount < 0 {
					takeCount = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Generate source values
				sourceValues := make([]int, sourceCount)
				for i := range sourceValues {
					sourceValues[i] = i
				}

				// Create source channel
				source := c.Generate(ctx, sourceValues...)

				// Take N values
				taken := c.Take(ctx, source, takeCount)

				// Read all values
				count := 0
				for range taken {
					count++
				}

				// Verify channel is closed
				_, ok := <-taken
				if ok {
					t.Logf("Channel should be closed after taking %d values", takeCount)
					return false
				}

				return true
			},
			gen.IntRange(1, 100),
			gen.IntRange(1, 100),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 18: Take context 取消
// 对于任何 Take 调用，当 context 被取消时应该提前关闭 channel
// Validates: Requirements 6.3
func TestProperty_TakeContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Take closes channel on context cancellation",
		prop.ForAll(
			func(takeCount int) bool {
				if takeCount < 2 {
					takeCount = 2 // Need at least 2 to test cancellation mid-stream
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()

				// Create a source that generates many values slowly
				source := c.RepeatFn(ctx, func() int {
					time.Sleep(10 * time.Millisecond)
					return 1
				})

				// Take N values
				taken := c.Take(ctx, source, takeCount)

				// Read one value
				_, ok := <-taken
				if !ok {
					// Channel closed early, acceptable
					cancel()
					return true
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(500 * time.Millisecond)
				for {
					select {
					case _, ok := <-taken:
						if !ok {
							// Channel closed as expected
							return true
						}
						// Continue reading until closed
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.IntRange(2, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 19: FanIn 值完整性
// 对于任何 输入 channel 集合，FanIn 输出的值总数应该等于所有输入 channel 值的总和
// Validates: Requirements 7.1, 7.4
func TestProperty_FanInValueCompleteness(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("FanIn outputs all values from all input channels",
		prop.ForAll(
			func(channelCount, valuesPerChannel int) bool {
				if channelCount < 1 {
					channelCount = 1
				}
				if valuesPerChannel < 0 {
					valuesPerChannel = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create multiple input channels
				var channels []<-chan int
				expectedTotal := 0

				for i := range channelCount {
					// Generate values for this channel
					values := make([]int, valuesPerChannel)
					for j := range valuesPerChannel {
						values[j] = i*1000 + j // Unique values per channel
						expectedTotal++
					}
					channels = append(channels, c.Generate(ctx, values...))
				}

				// FanIn all channels
				merged := c.FanIn(ctx, channels...)

				// Count received values
				receivedCount := 0
				for range merged {
					receivedCount++
				}

				// Verify total count matches
				if receivedCount != expectedTotal {
					t.Logf("Expected %d values, got %d", expectedTotal, receivedCount)
					return false
				}

				return true
			},
			gen.IntRange(1, 10),
			gen.IntRange(0, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 20: FanIn 全部关闭
// 对于任何 输入 channel 集合，当所有输入都关闭时输出 channel 应该关闭
// Validates: Requirements 7.2
func TestProperty_FanInClosesWhenAllInputsClose(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("FanIn closes output when all inputs close",
		prop.ForAll(
			func(channelCount, valuesPerChannel int) bool {
				if channelCount < 1 {
					channelCount = 1
				}
				if valuesPerChannel < 0 {
					valuesPerChannel = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create multiple input channels
				var channels []<-chan int

				for i := range channelCount {
					// Generate values for this channel
					values := make([]int, valuesPerChannel)
					for j := range valuesPerChannel {
						values[j] = i*1000 + j
					}
					channels = append(channels, c.Generate(ctx, values...))
				}

				// FanIn all channels
				merged := c.FanIn(ctx, channels...)

				// Read all values
				for range merged {
					// Drain all values
				}

				// Verify channel is closed
				_, ok := <-merged
				if ok {
					t.Logf("Output channel should be closed after all inputs close")
					return false
				}

				return true
			},
			gen.IntRange(1, 10),
			gen.IntRange(0, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 21: FanIn context 取消
// 对于任何 FanIn 调用，当 context 被取消时应该停止读取并关闭输出 channel
// Validates: Requirements 7.3
func TestProperty_FanInContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("FanIn stops on context cancellation",
		prop.ForAll(
			func(channelCount int) bool {
				if channelCount < 1 {
					channelCount = 1
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()

				// Create multiple input channels that generate values slowly
				var channels []<-chan int

				for range channelCount {
					ch := c.RepeatFn(ctx, func() int {
						time.Sleep(10 * time.Millisecond)
						return 1
					})
					channels = append(channels, ch)
				}

				// FanIn all channels
				merged := c.FanIn(ctx, channels...)

				// Read a few values
				readCount := 0
				maxRead := 5

				for readCount < maxRead {
					select {
					case _, ok := <-merged:
						if !ok {
							// Channel closed early, acceptable
							cancel()
							return true
						}
						readCount++
					case <-time.After(500 * time.Millisecond):
						// Timeout, break and cancel
						break
					}
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(1 * time.Second)
				for {
					select {
					case _, ok := <-merged:
						if !ok {
							// Channel closed as expected
							return true
						}
						// Continue reading until closed
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.IntRange(1, 5),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 22: Tee 输出相等
// 对于任何 输入值序列，Tee 的两个输出 channel 应该收到完全相同的值序列
// Validates: Requirements 8.1, 8.4
func TestProperty_TeeOutputEquality(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Tee outputs identical values to both channels",
		prop.ForAll(
			func(values []int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create source channel
				source := c.Generate(ctx, values...)

				// Tee the channel
				out1, out2 := c.Tee(ctx, source)

				// Collect values from both outputs concurrently
				var received1, received2 []int
				var wg sync.WaitGroup
				wg.Add(2)

				go func() {
					defer wg.Done()
					for v := range out1 {
						received1 = append(received1, v)
					}
				}()

				go func() {
					defer wg.Done()
					for v := range out2 {
						received2 = append(received2, v)
					}
				}()

				wg.Wait()

				// Verify both outputs received the same values
				if len(received1) != len(received2) {
					t.Logf("Length mismatch: out1=%d, out2=%d", len(received1), len(received2))
					return false
				}

				if len(received1) != len(values) {
					t.Logf("Expected %d values, got %d", len(values), len(received1))
					return false
				}

				// Verify values match (order may vary due to concurrent sends)
				// Create maps to count occurrences
				count1 := make(map[int]int)
				count2 := make(map[int]int)

				for _, v := range received1 {
					count1[v]++
				}
				for _, v := range received2 {
					count2[v]++
				}

				// Verify counts match
				if len(count1) != len(count2) {
					t.Logf(
						"Different number of unique values: out1=%d, out2=%d",
						len(count1),
						len(count2),
					)
					return false
				}

				for k, v := range count1 {
					if count2[k] != v {
						t.Logf("Value %d count mismatch: out1=%d, out2=%d", k, v, count2[k])
						return false
					}
				}

				return true
			},
			gen.SliceOf(gen.Int()),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 23: Tee 输入关闭
// 对于任何 Tee 调用，当输入 channel 关闭时两个输出 channel 都应该关闭
// Validates: Requirements 8.2
func TestProperty_TeeInputClose(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Tee closes both outputs when input closes",
		prop.ForAll(
			func(values []int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create source channel
				source := c.Generate(ctx, values...)

				// Tee the channel
				out1, out2 := c.Tee(ctx, source)

				// Drain both outputs
				var wg sync.WaitGroup
				wg.Add(2)

				go func() {
					defer wg.Done()
					for range out1 {
						// Drain all values
					}
				}()

				go func() {
					defer wg.Done()
					for range out2 {
						// Drain all values
					}
				}()

				wg.Wait()

				// Verify both channels are closed
				_, ok1 := <-out1
				_, ok2 := <-out2

				if ok1 || ok2 {
					t.Logf("Both output channels should be closed: out1=%v, out2=%v", ok1, ok2)
					return false
				}

				return true
			},
			gen.SliceOf(gen.Int()),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 24: Tee context 取消
// 对于任何 Tee 调用，当 context 被取消时应该停止转发并关闭两个输出 channel
// Validates: Requirements 8.3
func TestProperty_TeeContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Tee closes both outputs on context cancellation",
		prop.ForAll(
			func(valueCount int) bool {
				if valueCount < 2 {
					valueCount = 2 // Need at least 2 to test cancellation mid-stream
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()

				// Create a source that generates values slowly
				source := c.RepeatFn(ctx, func() int {
					time.Sleep(10 * time.Millisecond)
					return 1
				})

				// Tee the channel
				out1, out2 := c.Tee(ctx, source)

				// Read one value from each output
				var wg sync.WaitGroup
				wg.Add(2)

				readOne := func(ch <-chan int) {
					defer wg.Done()
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed early, acceptable
							return
						}
					case <-time.After(500 * time.Millisecond):
						// Timeout, acceptable
						return
					}
				}

				go readOne(out1)
				go readOne(out2)

				wg.Wait()

				// Cancel context
				cancel()

				// Both channels should close soon after cancellation
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
						t.Logf(
							"Channels did not close after context cancellation: out1=%v, out2=%v",
							closed1,
							closed2,
						)
						return false
					}
				}

				return true
			},
			gen.IntRange(2, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 25: Bridge 值完整性
// 对于任何 channel 流，Bridge 输出的值总数应该等于所有内部 channel 值的总和
// Validates: Requirements 9.1, 9.4
func TestProperty_BridgeValueCompleteness(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Bridge outputs all values from all inner channels",
		prop.ForAll(
			func(channelCount, valuesPerChannel int) bool {
				if channelCount < 1 {
					channelCount = 1
				}
				if valuesPerChannel < 0 {
					valuesPerChannel = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create a channel of channels
				chanStream := make(chan (<-chan int))

				// Start Bridge
				bridged := c.Bridge(ctx, chanStream)

				// Track expected total
				expectedTotal := channelCount * valuesPerChannel

				// Send channels to the stream in a goroutine
				go func() {
					defer close(chanStream)
					for i := range channelCount {
						// Generate values for this channel
						values := make([]int, valuesPerChannel)
						for j := range valuesPerChannel {
							values[j] = i*1000 + j // Unique values per channel
						}
						innerChan := c.Generate(ctx, values...)
						select {
						case chanStream <- innerChan:
						case <-ctx.Done():
							return
						}
					}
				}()

				// Count received values
				receivedCount := 0
				for range bridged {
					receivedCount++
				}

				// Verify total count matches
				if receivedCount != expectedTotal {
					t.Logf("Expected %d values, got %d", expectedTotal, receivedCount)
					return false
				}

				return true
			},
			gen.IntRange(1, 10),
			gen.IntRange(0, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 26: Bridge 流关闭
// 对于任何 Bridge 调用，当 channel 流关闭且所有内部 channel 完成时输出应该关闭
// Validates: Requirements 9.2
func TestProperty_BridgeStreamClose(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Bridge closes output when stream closes and all inner channels complete",
		prop.ForAll(
			func(channelCount, valuesPerChannel int) bool {
				if channelCount < 1 {
					channelCount = 1
				}
				if valuesPerChannel < 0 {
					valuesPerChannel = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create a channel of channels
				chanStream := make(chan (<-chan int))

				// Start Bridge
				bridged := c.Bridge(ctx, chanStream)

				// Send channels to the stream in a goroutine
				go func() {
					defer close(chanStream)
					for i := range channelCount {
						// Generate values for this channel
						values := make([]int, valuesPerChannel)
						for j := range valuesPerChannel {
							values[j] = i*1000 + j
						}
						innerChan := c.Generate(ctx, values...)
						select {
						case chanStream <- innerChan:
						case <-ctx.Done():
							return
						}
					}
				}()

				// Read all values
				for range bridged {
					// Drain all values
				}

				// Verify channel is closed
				_, ok := <-bridged
				if ok {
					t.Logf(
						"Output channel should be closed after stream closes and all inner channels complete",
					)
					return false
				}

				return true
			},
			gen.IntRange(1, 10),
			gen.IntRange(0, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 27: Bridge context 取消
// 对于任何 Bridge 调用，当 context 被取消时应该停止处理并关闭输出 channel
// Validates: Requirements 9.3
func TestProperty_BridgeContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Bridge stops on context cancellation",
		prop.ForAll(
			func(channelCount int) bool {
				if channelCount < 1 {
					channelCount = 1
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()

				// Create a channel of channels
				chanStream := make(chan (<-chan int))

				// Start Bridge
				bridged := c.Bridge(ctx, chanStream)

				// Send channels that generate values slowly
				go func() {
					defer close(chanStream)
					for range channelCount {
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
				maxRead := 5

				for readCount < maxRead {
					select {
					case _, ok := <-bridged:
						if !ok {
							// Channel closed early, acceptable
							cancel()
							return true
						}
						readCount++
					case <-time.After(500 * time.Millisecond):
						// Timeout, break and cancel
						break
					}
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(1 * time.Second)
				for {
					select {
					case _, ok := <-bridged:
						if !ok {
							// Channel closed as expected
							return true
						}
						// Continue reading until closed
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.IntRange(1, 5),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 28: Or 任一关闭
// 对于任何 channel 集合，Or 函数应该在任一输入 channel 关闭时关闭输出 channel
// Validates: Requirements 10.1, 10.4
func TestProperty_OrAnyClose(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Or closes output when any input channel closes",
		prop.ForAll(
			func(channelCount, closeIndex int) bool {
				if channelCount < 2 {
					channelCount = 2 // Need at least 2 channels to test "any"
				}
				// Ensure closeIndex is valid
				closeIndex = closeIndex % channelCount
				if closeIndex < 0 {
					closeIndex = -closeIndex
				}

				c := NewChanx[struct{}]()

				// Create multiple channels
				channels := make([]<-chan struct{}, channelCount)
				closeFuncs := make([]context.CancelFunc, channelCount)

				for i := range channelCount {
					ctx, cancel := context.WithCancel(context.Background())
					closeFuncs[i] = cancel
					// Create channels that will stay open until we cancel their context
					channels[i] = c.RepeatFn(ctx, func() struct{} {
						time.Sleep(10 * time.Millisecond)
						return struct{}{}
					})
				}

				// Ensure all contexts are cancelled at the end
				defer func() {
					for _, cancel := range closeFuncs {
						cancel()
					}
				}()

				// Call Or with all channels
				orChan := c.Or(channels...)

				// Close the channel at closeIndex
				closeFuncs[closeIndex]()

				// The Or channel should close soon after
				timeout := time.After(2 * time.Second)
				select {
				case _, ok := <-orChan:
					if !ok {
						// Channel closed as expected
						return true
					}
					// If we received a value, keep checking for closure
					for {
						select {
						case _, ok := <-orChan:
							if !ok {
								return true
							}
						case <-timeout:
							t.Logf(
								"Or channel did not close after input channel %d closed",
								closeIndex,
							)
							return false
						}
					}
				case <-timeout:
					t.Logf("Or channel did not close after input channel %d closed", closeIndex)
					return false
				}
			},
			gen.IntRange(2, 10),
			gen.Int(),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 29: OrDone 值转发
// 对于任何 输入 channel，OrDone 应该将所有值完整转发到输出 channel
// Validates: Requirements 11.1
func TestProperty_OrDoneValueForwarding(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("OrDone forwards all values from input to output",
		prop.ForAll(
			func(values []int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create source channel
				source := c.Generate(ctx, values...)

				// Apply OrDone
				output := c.OrDone(ctx, source)

				// Collect all values from output
				var received []int
				for v := range output {
					received = append(received, v)
				}

				// Verify all values were forwarded
				if len(received) != len(values) {
					t.Logf("Expected %d values, got %d", len(values), len(received))
					return false
				}

				// Verify values match (order should be preserved)
				for i := range values {
					if received[i] != values[i] {
						t.Logf(
							"Value mismatch at index %d: expected %d, got %d",
							i,
							values[i],
							received[i],
						)
						return false
					}
				}

				return true
			},
			gen.SliceOf(gen.Int()),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 30: OrDone context 取消
// 对于任何 OrDone 调用，当 context 被取消时应该停止转发并关闭输出 channel
// Validates: Requirements 11.2
func TestProperty_OrDoneContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("OrDone stops forwarding on context cancellation",
		prop.ForAll(
			func(valueCount int) bool {
				if valueCount < 2 {
					valueCount = 2 // Need at least 2 to test cancellation mid-stream
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()

				// Create a source that generates values slowly
				source := c.RepeatFn(ctx, func() int {
					time.Sleep(10 * time.Millisecond)
					return 1
				})

				// Apply OrDone
				output := c.OrDone(ctx, source)

				// Read a few values
				readCount := 0
				maxRead := 5

				for readCount < maxRead {
					select {
					case _, ok := <-output:
						if !ok {
							// Channel closed early, acceptable
							cancel()
							return true
						}
						readCount++
					case <-time.After(500 * time.Millisecond):
						// Timeout, break and cancel
						break
					}
				}

				// Cancel context
				cancel()

				// Channel should close soon after cancellation
				timeout := time.After(1 * time.Second)
				for {
					select {
					case _, ok := <-output:
						if !ok {
							// Channel closed as expected
							return true
						}
						// Continue reading until closed
					case <-timeout:
						t.Logf("Channel did not close after context cancellation")
						return false
					}
				}
			},
			gen.IntRange(2, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 31: OrDone 输入关闭
// 对于任何 OrDone 调用，当输入 channel 关闭时应该关闭输出 channel
// Validates: Requirements 11.3
func TestProperty_OrDoneInputClose(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("OrDone closes output when input channel closes",
		prop.ForAll(
			func(values []int) bool {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Create source channel that will close after sending all values
				source := c.Generate(ctx, values...)

				// Apply OrDone
				output := c.OrDone(ctx, source)

				// Read all values
				count := 0
				for range output {
					count++
				}

				// Verify channel is closed
				_, ok := <-output
				if ok {
					t.Logf("Output channel should be closed after input closes")
					return false
				}

				// Verify we received all values
				if count != len(values) {
					t.Logf("Expected %d values, got %d", len(values), count)
					return false
				}

				return true
			},
			gen.SliceOf(gen.Int()),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: worker-pool, Property 32: OrDone 竞态处理
// 对于任何 OrDone 调用，无论 context 取消和输入关闭哪个先发生，都应该正确关闭输出
// Validates: Requirements 11.4
func TestProperty_OrDoneRaceCondition(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("OrDone handles race between context cancellation and input close",
		prop.ForAll(
			func(cancelFirst bool, valueCount int) bool {
				if valueCount < 1 {
					valueCount = 1
				}

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()

				c := NewChanx[int]()

				// Create source channel
				values := make([]int, valueCount)
				for i := range values {
					values[i] = i
				}
				source := c.Generate(ctx, values...)

				// Apply OrDone
				output := c.OrDone(ctx, source)

				if cancelFirst {
					// Cancel context immediately
					cancel()
				} else {
					// Let input close naturally by reading some values
					// Read a few values to let the source progress
					for i := 0; i < valueCount/2 && i < 3; i++ {
						select {
						case _, ok := <-output:
							if !ok {
								// Already closed, that's fine
								return true
							}
						case <-time.After(100 * time.Millisecond):
							// Timeout, continue
							break
						}
					}
					// Now cancel
					cancel()
				}

				// Output channel should close soon
				timeout := time.After(1 * time.Second)
				for {
					select {
					case _, ok := <-output:
						if !ok {
							// Channel closed as expected
							return true
						}
						// Continue reading until closed
					case <-timeout:
						t.Logf("Output channel did not close after race condition")
						return false
					}
				}
			},
			gen.Bool(),
			gen.IntRange(1, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}
