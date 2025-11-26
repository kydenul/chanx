package chanx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/stretchr/testify/assert"
)

// TestMain runs the test suite
// Note: goleak is available for individual test use via goleak.VerifyNone(t)
// The goroutine cleanup property tests (Property 14 & 15) manually verify
// goroutine cleanup for channel functions.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

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

				// Start goroutine to drain results
				go func() {
					for range wp.Results() {
						// Drain all results
					}
				}()

				// Submit tasks equal to worker count to ensure all workers are active
				var activeWorkers atomic.Int32
				for range workerCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							activeWorkers.Add(1)
							time.Sleep(2 * time.Millisecond)
							return 0, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait a bit for all workers to pick up tasks
				time.Sleep(5 * time.Millisecond)

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
								time.Sleep(1 * time.Millisecond)
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
							time.Sleep(2 * time.Millisecond)
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
				time.Sleep(5 * time.Millisecond)
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
							time.Sleep(2 * time.Millisecond)
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
							time.Sleep(1 * time.Millisecond)
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
						// Timeout is acceptable, continue to next iteration
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
						// break
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
						// break
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

// Feature: chanx-optimization, Property 3: Bridge 值完整性
// 对于任何 channel 流，Bridge 函数应该转发所有输入值到输出 channel
// Validates: Requirements 2.2
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
						// break
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

// Feature: chanx-optimization, Property 1: Or 函数 Goroutine 清理
// 对于任何数量的输入 channel，当 Or 函数返回后，所有相关的 goroutine 应该在合理时间内（1秒）被清理
// Validates: Requirements 1.2
func TestProperty_OrGoroutineCleanup(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Or function cleans up goroutines after completion",
		prop.ForAll(
			func(channelCount int) bool {
				if channelCount < 1 {
					channelCount = 1
				}
				if channelCount > 50 {
					channelCount = 50 // Cap at 50 for reasonable test time
				}

				// Record initial goroutine count
				initialGoroutines := runtime.NumGoroutine()

				c := NewChanx[struct{}]()

				// Create multiple channels
				channels := make([]<-chan struct{}, channelCount)
				closeFuncs := make([]context.CancelFunc, channelCount)

				for i := range channelCount {
					ctx, cancel := context.WithCancel(context.Background())
					closeFuncs[i] = cancel
					channels[i] = c.RepeatFn(ctx, func() struct{} {
						time.Sleep(5 * time.Millisecond)
						return struct{}{}
					})
				}

				// Call Or with all channels
				orChan := c.Or(channels...)

				// Close one channel to trigger Or completion
				closeFuncs[0]()

				// Wait for Or channel to close
				timeout := time.After(1 * time.Second)
				closed := false
				for !closed {
					select {
					case _, ok := <-orChan:
						if !ok {
							closed = true
						}
					case <-timeout:
						// Clean up
						for _, cancel := range closeFuncs {
							cancel()
						}
						t.Logf("Or channel did not close in time")
						return false
					}
				}

				// Close all other channels
				for _, cancel := range closeFuncs {
					cancel()
				}

				// Give goroutines time to clean up
				time.Sleep(20 * time.Millisecond)

				// Check goroutine count
				finalGoroutines := runtime.NumGoroutine()

				// Allow some tolerance for background goroutines
				// The Or goroutine should be cleaned up
				if finalGoroutines > initialGoroutines+3 {
					t.Logf(
						"Goroutine leak detected: initial=%d, final=%d, diff=%d",
						initialGoroutines,
						finalGoroutines,
						finalGoroutines-initialGoroutines,
					)
					return false
				}

				return true
			},
			gen.IntRange(1, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 2: Or 函数快速响应
// 对于任何输入 channel 集合，当任一 channel 关闭时，Or 函数的输出 channel 应该立即关闭
// Validates: Requirements 1.3
func TestProperty_OrFastResponse(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Or function responds immediately when any channel closes",
		prop.ForAll(
			func(channelCount, closeIndex int) bool {
				if channelCount < 2 {
					channelCount = 2
				}
				if channelCount > 50 {
					channelCount = 50
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

				// Record time before closing
				startTime := time.Now()

				// Close the channel at closeIndex
				closeFuncs[closeIndex]()

				// The Or channel should close quickly (within 500ms)
				timeout := time.After(500 * time.Millisecond)
				select {
				case _, ok := <-orChan:
					if !ok {
						// Channel closed
						elapsed := time.Since(startTime)
						if elapsed > 500*time.Millisecond {
							t.Logf("Or took too long to respond: %v", elapsed)
							return false
						}
						return true
					}
					// If we received a value, keep checking for closure
					for {
						select {
						case _, ok := <-orChan:
							if !ok {
								elapsed := time.Since(startTime)
								if elapsed > 500*time.Millisecond {
									t.Logf("Or took too long to respond: %v", elapsed)
									return false
								}
								return true
							}
						case <-timeout:
							t.Logf("Or channel did not close quickly after input channel closed")
							return false
						}
					}
				case <-timeout:
					t.Logf("Or channel did not close quickly after input channel closed")
					return false
				}
			},
			gen.IntRange(2, 50),
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
						// break
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
							// break
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

// Feature: chanx-optimization, Property 4: 批量提交完整性
// 对于任何任务切片，SubmitBatch 应该尝试提交所有任务，并返回准确的提交计数
// Validates: Requirements 3.1
func TestProperty_BatchSubmitCompleteness(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("SubmitBatch attempts to submit all tasks and returns accurate count",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if taskCount < 0 {
					taskCount = 0
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Create tasks
				tasks := make([]Task[int], taskCount)
				for i := range taskCount {
					value := i
					tasks[i] = Task[int]{
						Fn: func() (int, error) {
							time.Sleep(2 * time.Millisecond)
							return value, nil
						},
					}
				}

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Submit batch
				result := wp.SubmitBatch(tasks)

				// Verify all tasks were submitted
				assert.Equal(t, taskCount, result.SubmittedCount,
					"SubmittedCount should match number of tasks")
				assert.Empty(t, result.Errors, "Should have no errors")

				// Wait for all results
				select {
				case <-resultsDone:
				case <-ctx.Done():
					t.Logf("Context cancelled while waiting for results")
					return false
				}

				return result.SubmittedCount == taskCount && len(result.Errors) == 0
			},
			gen.IntRange(1, 10),
			gen.IntRange(0, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 5: 批量提交状态准确性
// 对于任何批量提交操作，返回的 SubmittedCount 加上 Errors 的数量应该等于输入任务的总数
// Validates: Requirements 3.2
func TestProperty_BatchSubmitStatusAccuracy(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("SubmittedCount plus Errors count equals total tasks",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if taskCount < 1 {
					taskCount = 1
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Create tasks
				tasks := make([]Task[int], taskCount)
				for i := range taskCount {
					value := i
					tasks[i] = Task[int]{
						Fn: func() (int, error) {
							time.Sleep(2 * time.Millisecond)
							return value, nil
						},
					}
				}

				// Start goroutine to drain results
				go func() {
					for range wp.Results() {
						// Drain all results
					}
				}()

				// Submit batch
				result := wp.SubmitBatch(tasks)

				// Verify: SubmittedCount + len(Errors) == taskCount
				totalAccounted := result.SubmittedCount + len(result.Errors)
				assert.Equal(t, taskCount, totalAccounted,
					"SubmittedCount + Errors should equal total tasks")

				return totalAccounted == taskCount
			},
			gen.IntRange(1, 10),
			gen.IntRange(1, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 6: 批量提交 Context 取消
// 对于任何批量提交操作，当 context 被取消时，应该停止提交剩余任务并返回已提交的数量
// Validates: Requirements 3.3
func TestProperty_BatchSubmitContextCancellation(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("SubmitBatch stops on context cancellation",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if taskCount < 10 {
					taskCount = 10 // Need enough tasks to test cancellation mid-batch
				}

				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					cancel()
					return false
				}
				defer wp.Close()

				// Create tasks that take some time
				tasks := make([]Task[int], taskCount)
				for i := range taskCount {
					value := i
					tasks[i] = Task[int]{
						Fn: func() (int, error) {
							time.Sleep(5 * time.Millisecond)
							return value, nil
						},
					}
				}

				// Start goroutine to drain results
				go func() {
					for range wp.Results() {
						// Drain all results
					}
				}()

				// Start goroutine to cancel context after a short delay
				go func() {
					time.Sleep(10 * time.Millisecond)
					cancel()
				}()

				// Submit batch
				result := wp.SubmitBatch(tasks)

				// Verify that we stopped early due to cancellation
				// SubmittedCount should be less than taskCount
				// And we should have at least one error about cancellation
				if result.SubmittedCount == taskCount {
					// All tasks were submitted before cancellation, which is possible
					// but unlikely with our timing
					t.Logf("All tasks submitted before cancellation (edge case)")
					return true
				}

				// We should have stopped early
				assert.Less(t, result.SubmittedCount, taskCount,
					"Should have stopped submitting after context cancellation")
				assert.NotEmpty(t, result.Errors,
					"Should have errors after context cancellation")

				// Verify the error mentions context cancellation
				if len(result.Errors) > 0 {
					errorMsg := result.Errors[0].Error()
					assert.Contains(t, errorMsg, "context cancelled",
						"Error should mention context cancellation")
				}

				return result.SubmittedCount < taskCount && len(result.Errors) > 0
			},
			gen.IntRange(1, 5),
			gen.IntRange(10, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 7: Metrics 活跃 Worker 准确性
// 对于任何 WorkerPool 实例，Metrics 返回的 ActiveWorkers 应该不超过配置的 workerCount
// Validates: Requirements 4.1
func TestProperty_MetricsActiveWorkersAccuracy(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Metrics ActiveWorkers does not exceed configured workerCount",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if taskCount < 1 {
					taskCount = 1
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Start goroutine to drain results
				go func() {
					for range wp.Results() {
						// Drain all results
					}
				}()

				// Submit tasks that take some time
				for i := range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(2 * time.Millisecond)
							return i, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}

					// Check metrics periodically
					if i%5 == 0 {
						metrics := wp.Metrics()
						if metrics.ActiveWorkers > workerCount {
							t.Logf("ActiveWorkers (%d) exceeds workerCount (%d)",
								metrics.ActiveWorkers, workerCount)
							return false
						}
					}
				}

				// Wait a bit for tasks to start executing
				time.Sleep(10 * time.Millisecond)

				// Check final metrics
				metrics := wp.Metrics()
				assert.LessOrEqual(t, metrics.ActiveWorkers, workerCount,
					"ActiveWorkers should not exceed workerCount")

				return metrics.ActiveWorkers <= workerCount
			},
			gen.IntRange(1, 10),
			gen.IntRange(10, 30),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 8: Metrics 队列长度准确性
// 对于任何 WorkerPool 实例，当提交的任务数超过 worker 数量时，QueuedTasks 应该大于 0
// Validates: Requirements 4.2
func TestProperty_MetricsQueuedTasksAccuracy(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Metrics QueuedTasks is greater than 0 when tasks exceed workers",
		prop.ForAll(
			func(workerCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Start goroutine to drain results slowly
				taskCount := workerCount * 5
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
						// Drain results slowly to allow queue to build up
						time.Sleep(10 * time.Millisecond)
					}
					resultsDone <- true
				}()

				// Submit all tasks quickly to build up a queue
				// Use a WaitGroup to track submission goroutines
				var submitWg sync.WaitGroup
				for i := range taskCount {
					submitWg.Add(1)
					go func(taskID int) {
						defer submitWg.Done()
						err := wp.Submit(Task[int]{
							Fn: func() (int, error) {
								time.Sleep(5 * time.Millisecond)
								return taskID, nil
							},
						})
						if err != nil {
							t.Logf("Failed to submit task %d: %v", taskID, err)
						}
					}(i)
				}

				// Wait a bit for submissions to start and queue to build
				time.Sleep(20 * time.Millisecond)

				// Check metrics - should have queued tasks
				metrics := wp.Metrics()

				// With more tasks than workers and slow execution, we should have queued tasks
				assert.Greater(t, metrics.QueuedTasks, 0,
					"QueuedTasks should be greater than 0 when tasks exceed workers")

				// Wait for all submissions and results
				submitWg.Wait()
				<-resultsDone

				return metrics.QueuedTasks > 0
			},
			gen.IntRange(1, 10),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 9: Metrics 完成计数准确性
// 对于任何 WorkerPool 实例，CompletedTasks 应该等于成功执行的任务数量
// Validates: Requirements 4.3
func TestProperty_MetricsCompletedTasksAccuracy(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Metrics CompletedTasks equals number of successfully executed tasks",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if taskCount < 1 {
					taskCount = 1
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Submit tasks that all succeed
				for i := range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(2 * time.Millisecond)
							return i, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results to be processed
				<-resultsDone

				// Check metrics
				metrics := wp.Metrics()
				assert.Equal(t, int64(taskCount), metrics.CompletedTasks,
					"CompletedTasks should equal number of successful tasks")

				return metrics.CompletedTasks == int64(taskCount)
			},
			gen.IntRange(1, 10),
			gen.IntRange(1, 50),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 10: Metrics 失败计数准确性
// 对于任何 WorkerPool 实例，FailedTasks 应该等于返回错误的任务数量
// Validates: Requirements 4.4
func TestProperty_MetricsFailedTasksAccuracy(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Metrics FailedTasks equals number of tasks that returned errors",
		prop.ForAll(
			func(workerCount, successCount, failCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if successCount < 0 {
					successCount = 0
				}
				if failCount < 0 {
					failCount = 0
				}
				totalTasks := successCount + failCount
				if totalTasks < 1 {
					return true // Skip empty test cases
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range totalTasks {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Submit successful tasks
				for i := range successCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(2 * time.Millisecond)
							return i, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Submit failing tasks
				for range failCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(2 * time.Millisecond)
							return 0, assert.AnError
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results to be processed
				<-resultsDone

				// Check metrics
				metrics := wp.Metrics()
				assert.Equal(t, int64(failCount), metrics.FailedTasks,
					"FailedTasks should equal number of tasks that returned errors")
				assert.Equal(t, int64(successCount), metrics.CompletedTasks,
					"CompletedTasks should equal number of successful tasks")

				return metrics.FailedTasks == int64(failCount) &&
					metrics.CompletedTasks == int64(successCount)
			},
			gen.IntRange(1, 10),
			gen.IntRange(0, 25),
			gen.IntRange(0, 25),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 11: Metrics 平均时间合理性
// 对于任何 WorkerPool 实例，AvgTaskDuration 应该在合理范围内（大于0且小于最长任务时间）
// Validates: Requirements 4.5
func TestProperty_MetricsAvgTaskDurationReasonable(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Metrics AvgTaskDuration is reasonable (>0 and <= max task duration)",
		prop.ForAll(
			func(workerCount, taskCount int) bool {
				if workerCount < 1 {
					workerCount = 1
				}
				if taskCount < 1 {
					taskCount = 1
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				assert.NoError(t, err, "Failed to create worker pool")
				if err != nil {
					return false
				}
				defer wp.Close()

				// Start goroutine to drain results
				resultsDone := make(chan bool)
				go func() {
					for range taskCount {
						<-wp.Results()
					}
					resultsDone <- true
				}()

				// Define task duration (reduced for faster tests)
				taskDuration := 3 * time.Millisecond

				// Submit tasks with known duration
				for i := range taskCount {
					err := wp.Submit(Task[int]{
						Fn: func() (int, error) {
							time.Sleep(taskDuration)
							return i, nil
						},
					})
					assert.NoError(t, err, "Failed to submit task")
					if err != nil {
						return false
					}
				}

				// Wait for all results to be processed
				<-resultsDone

				// Check metrics
				metrics := wp.Metrics()

				// Average duration should be greater than 0
				if metrics.AvgTaskDuration <= 0 {
					t.Logf("AvgTaskDuration should be > 0, got %v", metrics.AvgTaskDuration)
					return false
				}

				// Average duration should be reasonable (within expected range)
				// It should be at least the task duration (accounting for some overhead)
				// and not exceed it by too much (allowing for some scheduling overhead)
				minExpected := taskDuration
				maxExpected := taskDuration * 2 // Allow 2x for overhead

				if metrics.AvgTaskDuration < minExpected {
					t.Logf("AvgTaskDuration (%v) is less than expected minimum (%v)",
						metrics.AvgTaskDuration, minExpected)
					return false
				}

				if metrics.AvgTaskDuration > maxExpected {
					t.Logf("AvgTaskDuration (%v) exceeds expected maximum (%v)",
						metrics.AvgTaskDuration, maxExpected)
					return false
				}

				return true
			},
			gen.IntRange(1, 10),
			gen.IntRange(5, 30),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 12: GenerateBuffered 缓冲行为
// 对于任何缓冲大小和值序列，GenerateBuffered 应该能够在不阻塞的情况下发送至少 bufferSize 个值
// Validates: Requirements 5.1
func TestProperty_GenerateBufferedNonBlocking(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("GenerateBuffered sends at least bufferSize values without blocking",
		prop.ForAll(
			func(bufferSize, valueCount int) bool {
				// Ensure valid inputs
				if bufferSize < 1 {
					bufferSize = 1
				}
				if valueCount < bufferSize {
					valueCount = bufferSize
				}
				if valueCount > 100 {
					valueCount = 100 // Cap for reasonable test time
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Generate values
				values := make([]int, valueCount)
				for i := range values {
					values[i] = i
				}

				// Create buffered channel
				ch, err := c.GenerateBuffered(ctx, bufferSize, values...)
				assert.NoError(t, err, "GenerateBuffered should not return error")
				if err != nil {
					return false
				}

				// The key property: the goroutine should be able to send at least
				// bufferSize values without blocking (i.e., without needing a receiver)
				// We verify this by waiting a short time and then checking that
				// at least bufferSize values are available in the buffer

				// Give the goroutine time to send values to the buffer
				time.Sleep(20 * time.Millisecond)

				// Now read values - at least bufferSize should be immediately available
				// (they should already be in the buffer)
				receivedCount := 0
				timeout := time.After(10 * time.Millisecond) // Very short timeout

				// Try to read bufferSize values quickly
				for i := 0; i < bufferSize; i++ {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed early, which is fine if we got enough values
							if receivedCount >= bufferSize {
								// Drain remaining
								for range ch {
								}
								return true
							}
							t.Logf("Channel closed after only %d values (expected at least %d)",
								receivedCount, bufferSize)
							return false
						}
						receivedCount++
					case <-timeout:
						// Timeout means values weren't buffered
						t.Logf("Timeout after receiving %d values (expected at least %d buffered)",
							receivedCount, bufferSize)
						// Drain remaining
						for range ch {
						}
						return false
					}
				}

				// Drain remaining values
				for range ch {
				}

				// We successfully read at least bufferSize values quickly
				return receivedCount >= bufferSize
			},
			gen.IntRange(1, 50),
			gen.IntRange(1, 100),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 13: RepeatBuffered 缓冲行为
// 对于任何缓冲大小和值序列，RepeatBuffered 应该能够在不阻塞的情况下发送至少 bufferSize 个值
// Validates: Requirements 5.2
func TestProperty_RepeatBufferedNonBlocking(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("RepeatBuffered sends at least bufferSize values without blocking",
		prop.ForAll(
			func(bufferSize, valueCount int) bool {
				// Ensure valid inputs
				if bufferSize < 1 {
					bufferSize = 1
				}
				if valueCount < 1 {
					valueCount = 1
				}
				if valueCount > 20 {
					valueCount = 20 // Keep it reasonable
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				c := NewChanx[int]()

				// Generate values
				values := make([]int, valueCount)
				for i := range values {
					values[i] = i
				}

				// Create buffered repeating channel
				ch, err := c.RepeatBuffered(ctx, bufferSize, values...)
				assert.NoError(t, err, "RepeatBuffered should not return error")
				if err != nil {
					return false
				}

				// The key property: the goroutine should be able to send at least
				// bufferSize values without blocking (i.e., without needing a receiver)
				// We verify this by waiting a short time and then checking that
				// at least bufferSize values are available in the buffer

				// Give the goroutine time to send values to the buffer
				time.Sleep(20 * time.Millisecond)

				// Now read values - at least bufferSize should be immediately available
				// (they should already be in the buffer)
				receivedCount := 0
				timeout := time.After(10 * time.Millisecond) // Very short timeout

				// Try to read bufferSize values quickly
				for i := 0; i < bufferSize; i++ {
					select {
					case _, ok := <-ch:
						if !ok {
							// Channel closed early (shouldn't happen with Repeat)
							t.Logf("Channel closed unexpectedly after %d values", receivedCount)
							return false
						}
						receivedCount++
					case <-timeout:
						// Timeout means values weren't buffered
						t.Logf("Timeout after receiving %d values (expected at least %d buffered)",
							receivedCount, bufferSize)
						cancel()
						// Drain remaining
						for range ch {
						}
						return false
					}
				}

				// Cancel context to stop the repeat
				cancel()

				// Drain remaining values
				for range ch {
				}

				// We successfully read at least bufferSize values quickly
				return receivedCount >= bufferSize
			},
			gen.IntRange(1, 50),
			gen.IntRange(1, 20),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 16: WorkerPool 创建错误详细性
// Validates: Requirements 7.1
func TestProperty_WorkerPoolCreationErrorDetail(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("NewWorkerPool returns detailed error for invalid worker count",
		prop.ForAll(
			func(invalidWorkerCount int) bool {
				ctx := context.Background()

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, invalidWorkerCount)

				// Should return an error
				if err == nil {
					t.Logf("Expected error for worker count %d, got nil", invalidWorkerCount)
					return false
				}

				// Should be nil pool
				if wp != nil {
					t.Logf("Expected nil pool for invalid worker count, got non-nil")
					return false
				}

				// Error should wrap ErrInvalidWorkerCount
				if !errors.Is(err, ErrInvalidWorkerCount) {
					t.Logf("Error should wrap ErrInvalidWorkerCount, got: %v", err)
					return false
				}

				// Error message should contain the invalid value
				errMsg := err.Error()
				if !contains(errMsg, fmt.Sprintf("%d", invalidWorkerCount)) {
					t.Logf(
						"Error message should contain worker count %d, got: %s",
						invalidWorkerCount,
						errMsg,
					)
					return false
				}

				return true
			},
			gen.IntRange(-100, 0), // Invalid worker counts (0 and negative)
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// Feature: chanx-optimization, Property 17: 任务提交错误详细性
// Validates: Requirements 7.2
func TestProperty_TaskSubmissionErrorDetail(t *testing.T) {
	properties := gopter.NewProperties(nil)

	properties.Property("Submit returns detailed error when pool is closed",
		prop.ForAll(
			func(workerCount int) bool {
				ctx, cancel := context.WithCancel(context.Background())

				c := NewChanx[int]()
				wp, err := c.NewWorkerPool(ctx, workerCount)
				if err != nil {
					t.Logf("Failed to create worker pool: %v", err)
					return false
				}

				// Start goroutine to drain results
				go func() {
					for range wp.Results() {
						// Drain all results
					}
				}()

				// Cancel context to close the pool
				cancel()

				// Wait a bit for cancellation to propagate
				time.Sleep(20 * time.Millisecond)

				// Try to submit a task after cancellation
				err = wp.Submit(Task[int]{
					Fn: func() (int, error) {
						return 1, nil
					},
				})

				wp.Close()

				// Should return an error
				if err == nil {
					t.Logf("Expected error when submitting to closed pool, got nil")
					return false
				}

				// Error should wrap either ErrContextCancelled or ErrPoolClosed
				if !errors.Is(err, ErrContextCancelled) && !errors.Is(err, ErrPoolClosed) {
					t.Logf("Error should wrap ErrContextCancelled or ErrPoolClosed, got: %v", err)
					return false
				}

				// Error message should be descriptive
				errMsg := err.Error()
				if len(errMsg) == 0 {
					t.Logf("Error message should not be empty")
					return false
				}

				return true
			},
			gen.IntRange(1, 10),
		))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// Feature: chanx-optimization, Property 14: Context 取消后 Goroutine 清理
// 对于任何 Chanx 函数，当 context 被取消后，所有相关 goroutine 应该在 1 秒内退出
// Validates: Requirements 6.3
func TestProperty_ContextCancellationGoroutineCleanup(t *testing.T) {
	// Test each function type individually to avoid timeout
	testCases := []struct {
		name string
		fn   func(context.Context, *Chanx[int])
	}{
		{
			name: "Generate",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch := c.Generate(ctx, 1, 2, 3, 4, 5)
				for i := 0; i < 3; i++ {
					select {
					case <-ch:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
		{
			name: "Repeat",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch := c.Repeat(ctx, 1, 2, 3)
				for i := 0; i < 3; i++ {
					select {
					case <-ch:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
		{
			name: "RepeatFn",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch := c.RepeatFn(ctx, func() int { return 42 })
				for i := 0; i < 3; i++ {
					select {
					case <-ch:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
		{
			name: "Take",
			fn: func(ctx context.Context, c *Chanx[int]) {
				source := c.Repeat(ctx, 1, 2, 3)
				taken := c.Take(ctx, source, 5)
				for i := 0; i < 3; i++ {
					select {
					case <-taken:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
		{
			name: "FanIn",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch1 := c.Repeat(ctx, 1)
				ch2 := c.Repeat(ctx, 2)
				merged := c.FanIn(ctx, ch1, ch2)
				for i := 0; i < 3; i++ {
					select {
					case <-merged:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
		{
			name: "Tee",
			fn: func(ctx context.Context, c *Chanx[int]) {
				source := c.Repeat(ctx, 1, 2, 3)
				out1, out2 := c.Tee(ctx, source)
				for i := 0; i < 2; i++ {
					select {
					case <-out1:
					case <-time.After(50 * time.Millisecond):
					}
					select {
					case <-out2:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
		{
			name: "Bridge",
			fn: func(ctx context.Context, c *Chanx[int]) {
				chanStream := make(chan (<-chan int), 2)
				bridged := c.Bridge(ctx, chanStream)

				go func() {
					defer close(chanStream)
					for i := 0; i < 2; i++ {
						select {
						case chanStream <- c.Generate(ctx, i, i+1):
						case <-ctx.Done():
							return
						}
					}
				}()

				for i := 0; i < 3; i++ {
					select {
					case <-bridged:
					case <-time.After(50 * time.Millisecond):
					}
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Record initial goroutine count
			initialGoroutines := runtime.NumGoroutine()

			ctx, cancel := context.WithCancel(context.Background())
			c := NewChanx[int]()

			// Run the test function
			tc.fn(ctx, c)

			// Cancel context
			cancel()

			// Wait for goroutines to clean up (reduced for faster tests)
			time.Sleep(100 * time.Millisecond)

			// Check goroutine count
			finalGoroutines := runtime.NumGoroutine()

			// Allow some tolerance for background goroutines (up to 5 extra)
			if finalGoroutines > initialGoroutines+5 {
				t.Errorf(
					"Goroutine leak detected: initial=%d, final=%d, diff=%d",
					initialGoroutines,
					finalGoroutines,
					finalGoroutines-initialGoroutines,
				)
			}
		})
	}
}

// Feature: chanx-optimization, Property 15: 操作完成后 Goroutine 清理
// 对于任何 Chanx 函数，当所有 channel 操作完成后，不应该有遗留的 goroutine
// Validates: Requirements 6.4
func TestProperty_OperationCompletionGoroutineCleanup(t *testing.T) {
	// Test each function type to ensure goroutines are cleaned up after completion
	testCases := []struct {
		name string
		fn   func(context.Context, *Chanx[int])
	}{
		{
			name: "Generate completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch := c.Generate(ctx, 1, 2, 3)
				// Read all values
				for range ch {
				}
			},
		},
		{
			name: "Take completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				source := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
				taken := c.Take(ctx, source, 5)
				// Read all taken values
				for range taken {
				}
			},
		},
		{
			name: "FanIn completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch1 := c.Generate(ctx, 1, 2, 3)
				ch2 := c.Generate(ctx, 4, 5, 6)
				merged := c.FanIn(ctx, ch1, ch2)
				// Read all values
				for range merged {
				}
			},
		},
		{
			name: "Tee completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				source := c.Generate(ctx, 1, 2, 3)
				out1, out2 := c.Tee(ctx, source)

				// Read from both outputs concurrently
				var wg sync.WaitGroup
				wg.Add(2)

				go func() {
					defer wg.Done()
					for range out1 {
					}
				}()

				go func() {
					defer wg.Done()
					for range out2 {
					}
				}()

				wg.Wait()
			},
		},
		{
			name: "Bridge completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				chanStream := make(chan (<-chan int))
				bridged := c.Bridge(ctx, chanStream)

				// Send channels
				go func() {
					defer close(chanStream)
					chanStream <- c.Generate(ctx, 1, 2, 3)
					chanStream <- c.Generate(ctx, 4, 5, 6)
				}()

				// Read all values
				for range bridged {
				}
			},
		},
		{
			name: "OrDone completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				source := c.Generate(ctx, 1, 2, 3, 4, 5)
				output := c.OrDone(ctx, source)
				// Read all values
				for range output {
				}
			},
		},
		{
			name: "Or completes",
			fn: func(ctx context.Context, c *Chanx[int]) {
				ch1 := c.Generate(ctx, 1, 2, 3)
				ch2 := c.Generate(ctx, 4, 5, 6)
				ch3 := c.Generate(ctx, 7, 8, 9)
				orChan := c.Or(ch1, ch2, ch3)
				// Wait for Or to close
				for range orChan {
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Record initial goroutine count
			initialGoroutines := runtime.NumGoroutine()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			c := NewChanx[int]()

			// Run the test function (should complete naturally)
			tc.fn(ctx, c)

			// Give a short time for goroutines to clean up after completion
			time.Sleep(20 * time.Millisecond)

			// Check goroutine count
			finalGoroutines := runtime.NumGoroutine()

			// Allow some tolerance for background goroutines (up to 3 extra)
			// After normal completion, there should be no leaked goroutines
			if finalGoroutines > initialGoroutines+3 {
				t.Errorf(
					"Goroutine leak detected after completion: initial=%d, final=%d, diff=%d",
					initialGoroutines,
					finalGoroutines,
					finalGoroutines-initialGoroutines,
				)
			}
		})
	}
}
