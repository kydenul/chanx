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
    c := chanx.NewChannel[int]()
    
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
c := chanx.NewChannel[int]()
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

c := chanx.NewChannel[string]()
ch := c.Repeat(ctx, "hello", "world")

// 读取: hello, world, hello, world, hello, world, ...
```

#### RepeatFn

重复执行一个函数并发送其返回值。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

c := chanx.NewChannel[int]()
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
c := chanx.NewChannel[int]()

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
c := chanx.NewChannel[int]()

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
c := chanx.NewChannel[int]()

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
    fmt.Println(v) // 输出所有值: 1-9
}
```

### 控制流

#### Or

返回一个 channel，当任意输入 channel 关闭时该 channel 也会关闭。

```go
c := chanx.NewChannel[struct{}]()

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

c := chanx.NewChannel[int]()
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
c := chanx.NewChannel[int]()

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
c := chanx.NewChannel[string]()

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
c := chanx.NewChannel[int]()

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
c := chanx.NewChannel[int]()

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
c := chanx.NewChannel[int]()

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

## 测试

该库包含全面的测试：

- **单元测试**：使用特定场景测试各个函数
- **基于属性的测试**：使用 gopter 在随机输入上验证属性

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
```

## 性能考虑

- 所有 channel 操作都是非阻塞的，支持 context
- Worker pool 使用缓冲 channel 以获得更好的吞吐量
- Goroutine 在 context 取消时会被正确清理
- 无 goroutine 泄漏 - 所有生成的 goroutine 都遵守 context

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
