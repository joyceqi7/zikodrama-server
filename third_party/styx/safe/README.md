# Safe

后台任务先创建显式 Runner：

```go
runner, err := safe.NewRunner(runtime)
if err != nil {
    return err
}
runner.Go(ctx, "refresh-cache", func() { refresh(ctx) })
```

周期任务：

```go
task, err := safe.NewTask(runner, "cleanup", time.Minute, func(ctx context.Context) {
    cleanup(ctx)
})
if err != nil {
    return err
}
if err := task.Start(ctx); err != nil {
    return err
}
```

`Task` 会自动纳入 `Runtime` 的逆序关闭流程。`Task.Stop` 在未启动时立即返回；停止会取消当前执行并遵守调用方 Context，完整停止后可再次 Start。`TaskGroup` 使用同样的 Context 生命周期。

Cron：

```go
scheduler, err := safe.NewCron(runner)
_ = scheduler.Add("daily-report", "0 0 8 * * *", report)
_ = scheduler.Start(ctx)
```

`Cron` 同样由 `Runtime` 统一关闭。

panic 恢复只记录任务名和固定错误，并在当前 Span 记录固定错误；不记录 panic 值、
堆栈，不创建任务指标，也不会重复写 stderr。
