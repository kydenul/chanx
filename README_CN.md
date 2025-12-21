# Chanx

[![Go Reference](https://pkg.go.dev/badge/github.com/kydenul/chanx.svg)](https://pkg.go.dev/github.com/kydenul/chanx)
[![Go Report Card](https://goreportcard.com/badge/github.com/kydenul/chanx)](https://goreportcard.com/report/github.com/kydenul/chanx)
[![CI](https://github.com/kydenul/chanx/actions/workflows/ci.yml/badge.svg)](https://github.com/kydenul/chanx/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/kydenul/chanx/branch/main/graph/badge.svg)](https://codecov.io/gh/kydenul/chanx)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Go 语言并发编程库，灵感来源于《Concurrency in Go》。提供泛型 channel 工具、生产级 worker pool 和基于 Redis 的分布式锁。

[English Documentation](README.md)

## 特性

- **Channel 模式**: Generate、Repeat、Take、FanIn、Tee、Bridge、Or、OrDone
- **Worker Pool**: 生产级任务池，支持指标监控和优雅关闭
- **分布式锁**: 基于 Redis 的分布式锁，支持自动续期
- **泛型支持**: 完整支持 Go 1.18+ 泛型，类型安全
- **Context 感知**: 所有操作都支持 context 取消

## 安装

```bash
go get github.com/kydenul/chanx
```

## 快速开始

### Channel 操作

```go
ctx := context.Background()
c := chanx.NewChanx[int]()

// Generate: 顺序发送值
values := c.Generate(ctx, 1, 2, 3, 4, 5)

// Take: 限制数量
first3 := c.Take(ctx, values, 3)

// FanIn: 合并多个 channel
ch1 := c.Generate(ctx, 1, 2, 3)
ch2 := c.Generate(ctx, 4, 5, 6)
merged := c.FanIn(ctx, ch1, ch2)
```

### Worker Pool

```go
ctx := context.Background()
wp, _ := chanx.NewWorkerPool[int](ctx, 5)
defer wp.Close()

// 收集结果
go func() {
    for result := range wp.Results() {
        if result.Err != nil {
            fmt.Printf("错误: %v\n", result.Err)
        } else {
            fmt.Printf("结果: %d\n", result.Value)
        }
    }
}()

// 提交任务
for i := 0; i < 100; i++ {
    taskID := i
    wp.Submit(chanx.Task[int]{
        Fn: func() (int, error) {
            return taskID * 2, nil
        },
    })
}

// 获取指标
metrics := wp.Metrics()
fmt.Printf("已完成: %d, 活跃: %d\n",
    metrics.CompletedTasks, metrics.ActiveWorkers)
```

### 分布式锁

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

// 使用 LockGuard 简化代码
err := chanx.LockGuard(ctx, lock, func() error {
    // 临界区代码
    return processResource()
})

// 或手动控制
acquired, err := lock.Acquire(ctx)
if acquired {
    defer lock.Release()
    // 执行工作
}

// 带重试的获取
acquired, err = lock.TryAcquire(ctx, 10*time.Second, 100*time.Millisecond)
```

## API 参考

### Channel 模式

| 函数 | 说明 |
|------|------|
| `Generate(ctx, values...)` | 顺序发送值 |
| `Repeat(ctx, values...)` | 无限重复值 |
| `RepeatFn(ctx, fn)` | 重复调用函数 |
| `Take(ctx, ch, n)` | 获取前 n 个值 |
| `FanIn(ctx, channels...)` | 合并多个 channel |
| `Tee(ctx, ch)` | 分割为两个 channel |
| `Bridge(ctx, chanStream)` | 连接 channel 流 |
| `Or(channels...)` | 任意一个关闭时关闭 |
| `OrDone(ctx, ch)` | 尊重 context 取消 |

### Worker Pool

```go
// 创建任务池
wp, err := NewWorkerPool[T](ctx, workerCount)

// 提交任务
err := wp.Submit(Task[T]{Fn: func() (T, error) {...}})
result := wp.SubmitBatch([]Task[T]{...})

// 获取结果和指标
results := wp.Results()
metrics := wp.Metrics()

// 清理
wp.Close()
```

### 分布式锁

```go
// 创建锁
lock := NewDistributedLock(key, redisClient, options...)

// 获取/释放
acquired, err := lock.Acquire(ctx)
err = lock.Release()

// 带重试
acquired, err := lock.TryAcquire(ctx, timeout, retryInterval)

// 辅助函数
err := LockGuard(ctx, lock, fn)
err := LockGuardWithRetry(ctx, lock, timeout, retryInterval, fn)
```

## 最佳实践

1. **始终使用带超时的 context** 防止 goroutine 泄漏
2. **消费完 channel 或取消 context** 使用完毕后
3. **使用 defer 清理**: `defer wp.Close()`, `defer lock.Release()`
4. **选择合适的 worker 数量**:
   - CPU 密集型: `runtime.NumCPU()`
   - I/O 密集型: `runtime.NumCPU() * 4`
5. **设置锁 TTL** 至少为预期操作时间的 3 倍
6. **高吞吐场景使用缓冲版本** 如 `GenerateBuffered`

## 环境要求

- Go 1.18+ (泛型支持)
- Redis (仅分布式锁需要)

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

## 致谢

灵感来源于 Katherine Cox-Buday 的 ["Concurrency in Go"](https://www.oreilly.com/library/view/concurrency-in-go/9781491941294/)
