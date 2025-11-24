# Chanx

[![Go Reference](https://pkg.go.dev/badge/github.com/kydenul/chanx.svg)](https://pkg.go.dev/github.com/kydenul/chanx)
[![Go Report Card](https://goreportcard.com/badge/github.com/kydenul/chanx)](https://goreportcard.com/report/github.com/kydenul/chanx)

一个强大的 Go 语言 channel 操作和并发编程模式库，灵感来源于《Concurrency in Go》一书。Chanx 提供了一套全面的工具集，用于处理 Go channel，包括常见的模式如 fan-in、fan-out、管道以及健壮的 worker pool 实现。

[English Documentation](README.md)

## 特性

- **泛型支持**：充分利用 Go 1.18+ 泛型实现类型安全的 channel 操作
- **丰富的 Channel 模式**：实现了《Concurrency in Go》中的常见并发模式
- **Worker Pool**：生产级别的 worker pool，支持优雅关闭和错误处理
- **Context 感知**：所有操作都支持 context 取消，实现清晰的资源管理
- **完善的测试**：包含全面的单元测试和基于属性的测试（使用 gopter）
- **零依赖**：仅需要标准库（测试依赖：testify、gopter）

## 安装

```bash
go get github.com/kydenul/chanx
```

## 快速开始

```go
package main

import (
    "context"
    "fmt"
    "github.com/kydenul/chanx"
)

func main() {
    ctx := context.Background()
    c := chanx.NewChanx[int]()
    
    // 生成值
    values := c.Generate(ctx, 1, 2, 3, 4, 5)
    
    // 处理值
    for v := range values {
        fmt.Println(v)
    }
}
```

## 核心功能

### Channel 生成器

#### Generate

创建一个 channel 并发送一系列值。

```go
ctx := context.Background()
c := chanx.NewChanx[int]()
ch := c.Generate(ctx, 1, 2, 3, 4, 5)

for v := range ch {
    fmt.Println(v) // 输出: 1, 2, 3, 4, 5
}
```

#### Repeat

持续重复发送一系列值，直到 context 被取消。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChanx[string]()
ch := c.Repeat(ctx, "hello", "world")

// 读取: hello, world, hello, world, hello, world, ...
```

#### RepeatFn

重复执行一个函数并发送其返回值。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChanx[int]()
counter := 0
ch := c.RepeatFn(ctx, func() int {
    counter++
    return counter
})

// 读取: 1, 2, 3, 4, 5, ...
```

### Channel 转换器

#### Take

从 channel 中获取前 N 个值。

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

source := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
first5 := c.Take(ctx, source, 5)

for v := range first5 {
    fmt.Println(v) // 输出: 1, 2, 3, 4, 5
}
```

#### FanIn

将多个 channel 合并为一个 channel。

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

ch1 := c.Generate(ctx, 1, 2, 3)
ch2 := c.Generate(ctx, 4, 5, 6)
ch3 := c.Generate(ctx, 7, 8, 9)

merged := c.FanIn(ctx, ch1, ch2, ch3)

// 接收所有 channel 的值（顺序可能不同）
for v := range merged {
    fmt.Println(v)
}
```

#### Tee

将一个 channel 分割为两个相同的输出 channel。

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

source := c.Generate(ctx, 1, 2, 3, 4, 5)
out1, out2 := c.Tee(ctx, source)

// out1 和 out2 都会接收到相同的值
go func() {
    for v := range out1 {
        fmt.Println("输出 1:", v)
    }
}()

for v := range out2 {
    fmt.Println("输出 2:", v)
}
```

#### Bridge

将一个 channel 流连接到单个输出 channel。

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

chanStream := make(chan (<-chan int))

go func() {
    defer close(chanStream)
    chanStream <- c.Generate(ctx, 1, 2, 3)
    chanStream <- c.Generate(ctx, 4, 5, 6)
    chanStream <- c.Generate(ctx, 7, 8, 9)
}()

bridged := c.Bridge(ctx, chanStream)

for v := range bridged {
    fmt.Println(v) // 输出所有值: 1-9
}
```

### 控制流

#### Or

返回一个 channel，当任意输入 channel 关闭时该 channel 也会关闭。

```go
c := chanx.NewChanx[struct{}]()

ctx1, cancel1 := context.WithCancel(context.Background())
ctx2, cancel2 := context.WithCancel(context.Background())
defer cancel2()

ch1 := c.RepeatFn(ctx1, func() struct{} { return struct{}{} })
ch2 := c.RepeatFn(ctx2, func() struct{} { return struct{}{} })

orChan := c.Or(ch1, ch2)

// 关闭一个 channel
cancel1()

// 当 ch1 关闭时，orChan 也会关闭
<-orChan
```

#### OrDone

包装一个 channel 以支持 context 取消。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChanx[int]()
source := c.Repeat(ctx, 1, 2, 3)

output := c.OrDone(ctx, source)

// 读取一些值
for i := 0; i < 10; i++ {
    fmt.Println(<-output)
}

// 取消 context 以停止
cancel()
```

## Worker Pool

Worker pool 提供了一种健壮的方式来使用固定数量的 worker 并发执行任务。

### 基本用法

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

// 创建一个有 5 个 worker 的线程池
wp, err := c.NewWorkerPool(ctx, 5)
if err != nil {
    log.Fatal(err)
}
defer wp.Close()

// 启动一个 goroutine 来收集结果
go func() {
    for result := range wp.Results() {
        if result.Err != nil {
            fmt.Printf("任务失败: %v\n", result.Err)
        } else {
            fmt.Printf("任务结果: %d\n", result.Value)
        }
    }
}()

// 提交任务
for i := 0; i < 100; i++ {
    taskID := i
    err := wp.Submit(chanx.Task[int]{
        Fn: func() (int, error) {
            // 模拟工作
            time.Sleep(100 * time.Millisecond)
            return taskID * 2, nil
        },
    })
    if err != nil {
        log.Printf("提交任务失败: %v", err)
    }
}
```

### 错误处理

```go
ctx := context.Background()
c := chanx.NewChanx[string]()

wp, _ := c.NewWorkerPool(ctx, 3)
defer wp.Close()

go func() {
    for result := range wp.Results() {
        if result.Err != nil {
            fmt.Printf("错误: %v\n", result.Err)
        } else {
            fmt.Printf("成功: %s\n", result.Value)
        }
    }
}()

// 提交一个可能失败的任务
wp.Submit(chanx.Task[string]{
    Fn: func() (string, error) {
        if rand.Float32() < 0.5 {
            return "", errors.New("随机失败")
        }
        return "成功", nil
    },
})
```

### 优雅关闭

```go
ctx, cancel := context.WithCancel(context.Background())
c := chanx.NewChanx[int]()

wp, _ := c.NewWorkerPool(ctx, 5)

// 提交任务...

// 取消 context 以停止接受新任务
cancel()

// Close 会等待所有正在执行的任务完成
wp.Close()
```

## 高级模式

### 管道模式

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

// 阶段 1: 生成数字
numbers := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

// 阶段 2: 取前 5 个
first5 := c.Take(ctx, numbers, 5)

// 阶段 3: 并行处理
ch1 := c.Generate(ctx, 1, 2, 3)
ch2 := c.Generate(ctx, 4, 5, 6)
merged := c.FanIn(ctx, ch1, ch2)

// 阶段 4: 复制输出
out1, out2 := c.Tee(ctx, merged)
```

### Fan-Out/Fan-In 模式

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

// 输入
input := c.Generate(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

// Fan-out: 将工作分配给多个 worker
workers := make([]<-chan int, 3)
for i := range workers {
    workers[i] = processWorker(ctx, input)
}

// Fan-in: 合并结果
results := c.FanIn(ctx, workers...)

for result := range results {
    fmt.Println(result)
}
```

## 新特性（v2.0）

### 带缓冲的 Channel 变体

创建具有自定义缓冲区大小的 channel 以优化性能：

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

// 创建缓冲区大小为 10 的 channel
ch, err := c.GenerateBuffered(ctx, 10, 1, 2, 3, 4, 5)
if err != nil {
    log.Fatal(err)
}

// 带缓冲的重复 channel
repeatCh, err := c.RepeatBuffered(ctx, 20, "hello", "world")
if err != nil {
    log.Fatal(err)
}
```

### 批量任务提交

一次提交多个任务以获得更好的吞吐量：

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

wp, _ := c.NewWorkerPool(ctx, 5)
defer wp.Close()

// 准备批量任务
tasks := make([]chanx.Task[int], 100)
for i := range tasks {
    taskID := i
    tasks[i] = chanx.Task[int]{
        Fn: func() (int, error) {
            return taskID * 2, nil
        },
    }
}

// 一次性提交所有任务
result := wp.SubmitBatch(tasks)
fmt.Printf("已提交 %d 个任务\n", result.SubmittedCount)
if len(result.Errors) > 0 {
    fmt.Printf("提交失败 %d 个任务\n", len(result.Errors))
}
```

### 性能指标

实时监控你的 worker pool：

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

wp, _ := c.NewWorkerPool(ctx, 10)
defer wp.Close()

// 提交一些任务...

// 获取当前指标
metrics := wp.Metrics()
fmt.Printf("活跃 Worker: %d\n", metrics.ActiveWorkers)
fmt.Printf("排队任务: %d\n", metrics.QueuedTasks)
fmt.Printf("已完成任务: %d\n", metrics.CompletedTasks)
fmt.Printf("失败任务: %d\n", metrics.FailedTasks)
fmt.Printf("平均任务时长: %v\n", metrics.AvgTaskDuration)
```

## 性能优化指南

### 选择缓冲区大小

**无缓冲 Channel（默认）**

- 需要严格同步时使用
- 适合低吞吐量场景
- 确保发送者和接收者同步

**带缓冲 Channel**

- 在高吞吐量场景下使用 `GenerateBuffered` 或 `RepeatBuffered`
- 缓冲区大小为 10-100 适用于大多数情况
- 更大的缓冲区减少阻塞但增加内存使用

```go
// 高吞吐量场景
ch, _ := c.GenerateBuffered(ctx, 50, values...)

// 低延迟场景（小缓冲区）
ch, _ := c.GenerateBuffered(ctx, 5, values...)
```

### Worker Pool 大小设置

**CPU 密集型任务**

```go
// 使用 CPU 核心数
numWorkers := runtime.NumCPU()
wp, _ := c.NewWorkerPool(ctx, numWorkers)
```

**I/O 密集型任务**

```go
// 使用更高的 worker 数量（CPU 核心数的 2-10 倍）
numWorkers := runtime.NumCPU() * 4
wp, _ := c.NewWorkerPool(ctx, numWorkers)
```

**混合工作负载**

```go
// 从 CPU 核心数的 2 倍开始，根据指标调整
numWorkers := runtime.NumCPU() * 2
wp, _ := c.NewWorkerPool(ctx, numWorkers)

// 监控并调整
metrics := wp.Metrics()
if metrics.QueuedTasks > 100 {
    // 考虑增加 worker 数量
}
```

### 批量提交的优势

批量提交可显著提升性能：

- 比单独提交快 **50% 以上**（对于大型任务集）
- 减少锁竞争
- 更好的 CPU 缓存利用率
- 每个任务的开销更低

```go
// 不要这样做（慢）：
for _, task := range tasks {
    wp.Submit(task)
}

// 这样做（快）：
result := wp.SubmitBatch(tasks)
```

### Or 函数优化

`Or` 函数现在使用迭代实现而不是递归：

- 处理大量 channel 时**不会栈溢出**
- 处理 100+ channel 时快 **50% 以上**
- **更低的内存使用** - 创建更少的 goroutine

```go
// 高效处理大量 channel
channels := make([]<-chan int, 500)
for i := range channels {
    channels[i] = c.Generate(ctx, i)
}
orChan := c.Or(channels...)
```

### Bridge 函数性能

`Bridge` 函数现在使用带缓冲的内部 channel：

- **吞吐量提高 30% 以上**
- 减少 channel 流之间的阻塞
- 更好的并发处理

```go
// 针对高吞吐量 channel 流优化
chanStream := make(chan (<-chan int))
bridged := c.Bridge(ctx, chanStream)
```

## 资源管理最佳实践

### 始终使用 Context

每个操作都应该有一个带超时或取消的 context：

```go
// 好：带超时的 Context
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

ch := c.Generate(ctx, values...)

// 不好：没有超时
ctx := context.Background()
ch := c.Generate(ctx, values...)
```

### 清空 Channel 或取消 Context

为防止 goroutine 泄漏，始终：

1. **完全清空 channel**：

```go
ch := c.Generate(ctx, 1, 2, 3, 4, 5)
for v := range ch {
    process(v)
}
// Channel 已清空，goroutine 退出
```

2. **取消 context**：

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

ch := c.Repeat(ctx, 1, 2, 3)
// 读取一些值...
cancel() // Goroutine 退出
```

### Worker Pool 生命周期

始终关闭 worker pool 以确保干净的关闭：

```go
wp, err := c.NewWorkerPool(ctx, 5)
if err != nil {
    return err
}
defer wp.Close() // 等待所有任务完成

// 提交任务...
```

### 错误处理

检查所有操作的错误：

```go
// 检查 worker pool 创建
wp, err := c.NewWorkerPool(ctx, 0)
if err != nil {
    // 处理错误：ErrInvalidWorkerCount
}

// 检查带缓冲 channel 创建
ch, err := c.GenerateBuffered(ctx, -1, values...)
if err != nil {
    // 处理错误：ErrInvalidBufferSize
}

// 检查任务提交
err = wp.Submit(task)
if err != nil {
    // 处理错误：ErrPoolClosed 或 ErrContextCancelled
}
```

### Goroutine 泄漏预防

该库旨在防止 goroutine 泄漏：

- 所有 goroutine 都遵守 context 取消
- Goroutine 在 context 取消后 1 秒内退出
- 操作完成后没有孤立的 goroutine

在测试中验证：

```go
import "go.uber.org/goleak"

func TestMain(m *testing.M) {
    goleak.VerifyTestMain(m)
}
```

## 监控和指标

### 实时监控

使用 `Metrics()` 监控 worker pool 健康状况：

```go
wp, _ := c.NewWorkerPool(ctx, 10)

// 定期监控
ticker := time.NewTicker(5 * time.Second)
defer ticker.Stop()

go func() {
    for range ticker.C {
        metrics := wp.Metrics()
        log.Printf("Pool 状态 - 活跃: %d, 排队: %d, 已完成: %d, 失败: %d, 平均时长: %v",
            metrics.ActiveWorkers,
            metrics.QueuedTasks,
            metrics.CompletedTasks,
            metrics.FailedTasks,
            metrics.AvgTaskDuration,
        )
    }
}()
```

### 关键指标说明

**ActiveWorkers**

- 当前正在执行任务的 worker 数量
- 在负载下应接近 worker 总数
- 低值表示工作不足或存在瓶颈

**QueuedTasks**

- 等待执行的任务数量
- 高值表示 worker 已饱和
- 如果持续较高，考虑增加 worker 数量

**CompletedTasks**

- 成功完成的任务总数
- 用于跟踪随时间变化的吞吐量

**FailedTasks**

- 返回错误的任务总数
- 监控错误率趋势

**AvgTaskDuration**

- 执行任务的平均时间
- 用于识别性能下降
- 与基线比较以检测问题

### 性能告警

基于指标设置告警：

```go
metrics := wp.Metrics()

// 告警：排队任务过多
if metrics.QueuedTasks > 1000 {
    log.Warn("Worker pool 队列积压")
}

// 告警：高失败率
failureRate := float64(metrics.FailedTasks) / float64(metrics.CompletedTasks + metrics.FailedTasks)
if failureRate > 0.1 {
    log.Warn("任务失败率超过 10%")
}

// 告警：任务执行缓慢
if metrics.AvgTaskDuration > 5*time.Second {
    log.Warn("平均任务时长过高")
}
```

## 故障排除

### Goroutine 泄漏

**症状**：Goroutine 数量持续增加

**原因**：

- 未完全清空 channel
- 未取消 context
- Channel 在发送时阻塞

**解决方案**：

```go
// 解决方案 1：始终使用带超时的 context
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

// 解决方案 2：清空 channel 或取消 context
ch := c.Generate(ctx, values...)
for v := range ch {
    // 处理所有值
}

// 解决方案 3：对部分读取使用 OrDone
ch := c.Generate(ctx, values...)
safeCh := c.OrDone(ctx, ch)
// 读取一些值，然后取消 context
```

### Worker Pool 不处理任务

**症状**：任务已提交但未执行

**原因**：

- Context 已取消
- Worker pool 已关闭
- 所有 worker 被阻塞

**解决方案**：

```go
// 检查 context
if ctx.Err() != nil {
    log.Printf("Context 错误: %v", ctx.Err())
}

// 检查指标
metrics := wp.Metrics()
if metrics.ActiveWorkers == 0 {
    log.Println("没有活跃的 worker - pool 可能已关闭")
}

// 确保正在读取结果 channel
go func() {
    for result := range wp.Results() {
        // 必须读取结果，否则 worker 会阻塞
        handleResult(result)
    }
}()
```

### 内存使用过高

**症状**：内存使用随时间增长

**原因**：

- 缓冲区大小过大
- 未从结果 channel 读取
- Goroutine 累积

**解决方案**：

```go
// 解决方案 1：使用较小的缓冲区
ch, _ := c.GenerateBuffered(ctx, 10, values...) // 不是 1000

// 解决方案 2：始终读取结果
go func() {
    for result := range wp.Results() {
        // 立即处理，不要累积
        process(result)
    }
}()

// 解决方案 3：限制并发操作
semaphore := make(chan struct{}, 100)
for _, task := range tasks {
    semaphore <- struct{}{}
    go func(t Task) {
        defer func() { <-semaphore }()
        wp.Submit(t)
    }(task)
}
```

### 性能缓慢

**症状**：操作比预期慢

**原因**：

- Worker 数量错误
- 高吞吐量场景下使用无缓冲 channel
- 单独提交任务而不是批量提交

**解决方案**：

```go
// 解决方案 1：根据工作负载调整 worker 数量
// CPU 密集型：runtime.NumCPU()
// I/O 密集型：runtime.NumCPU() * 4

// 解决方案 2：使用带缓冲的 channel
ch, _ := c.GenerateBuffered(ctx, 50, values...)

// 解决方案 3：使用批量提交
result := wp.SubmitBatch(tasks) // 不是单独 Submit()

// 解决方案 4：监控和调优
metrics := wp.Metrics()
if metrics.QueuedTasks > 100 {
    // 增加 worker
}
```

### Context 取消不起作用

**症状**：Context 取消时操作不停止

**原因**：

- 长时间运行的任务中未检查 context
- 阻塞操作不支持 context

**解决方案**：

```go
// 解决方案：在任务函数中定期检查 context
task := chanx.Task[int]{
    Fn: func() (int, error) {
        for i := 0; i < 1000; i++ {
            // 定期检查 context
            select {
            case <-ctx.Done():
                return 0, ctx.Err()
            default:
            }
            
            // 执行工作
            result := process(i)
        }
        return result, nil
    },
}
```

## 基准测试结果

v2.0 相比 v1.0 的性能改进：

### Or 函数

```
BenchmarkOr/10-channels     - 快 50%
BenchmarkOr/50-channels     - 快 60%  
BenchmarkOr/100-channels    - 快 65%
BenchmarkOr/500-channels    - 快 70%
```

### Bridge 函数

```
BenchmarkBridge/low-concurrency    - 快 25%
BenchmarkBridge/medium-concurrency - 快 30%
BenchmarkBridge/high-concurrency   - 快 35%
```

### Worker Pool 批量提交

```
BenchmarkSubmit/individual-100     - 基准
BenchmarkSubmit/batch-100          - 快 55%
BenchmarkSubmit/individual-1000    - 基准
BenchmarkSubmit/batch-1000         - 快 60%
```

### 内存使用

```
Or 函数（100 个 channel）    - 减少 20% 内存
Bridge 函数                  - 减少 15% 内存
Worker pool 操作             - 减少 10% 内存
```

### Goroutine 效率

```
Or 函数                      - 减少 30% goroutine
Bridge 函数                  - 减少 25% goroutine
整体 goroutine 清理          - 1 秒内 100% 清理
```

## 测试

该库包含全面的测试：

- **单元测试**：使用特定场景测试各个函数
- **基于属性的测试**：使用 gopter 在随机输入上验证属性
- **Goroutine 泄漏检测**：所有测试使用 goleak 验证无 goroutine 泄漏

运行测试：

```bash
# 运行所有测试
go test -v

# 仅运行单元测试
go test -v -run "^Test[^Property]"

# 仅运行基于属性的测试
go test -v -run "TestProperty"

# 使用竞态检测器运行
go test -race -v

# 运行基准测试
go test -bench=. -benchmem
```

## 性能考虑

- 所有 channel 操作都是非阻塞的，支持 context
- Worker pool 使用缓冲 channel 以获得更好的吞吐量
- Goroutine 在 context 取消时会被正确清理
- 无 goroutine 泄漏 - 所有生成的 goroutine 都遵守 context
- Or 函数使用迭代实现以避免栈溢出
- Bridge 函数使用带缓冲的内部 channel 以获得更高的吞吐量
- 批量提交减少锁竞争并提高性能

## 要求

- Go 1.18 或更高版本（支持泛型）

## 贡献

欢迎贡献！请随时提交 Pull Request。

## 许可证

本项目采用 MIT 许可证 - 详见 LICENSE 文件。

## 致谢

- 灵感来源于 Katherine Cox-Buday 的《Concurrency in Go》
- 使用 [gopter](https://github.com/leanovate/gopter) 进行基于属性的测试
- 使用 [testify](https://github.com/stretchr/testify) 进行断言

## 相关项目

- [Go 并发模式](https://go.dev/blog/pipelines)
- [Concurrency in Go（书籍）](https://www.oreilly.com/library/view/concurrency-in-go/9781491941294/)
