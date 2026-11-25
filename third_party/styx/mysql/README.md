# MySQL

```go
db, err := mysql.NewMySQL(ctx, "default", config, runtime)
```

工厂使用 GORM，禁用 GORM 自动 Ping，并通过调用方 Context 执行 `PingContext`。连接池指标绑定 Runtime 的 MeterProvider；指标注销和 `sql.DB` 关闭自动纳入 Runtime。

日志和 Span 只记录数据库类型、操作类型、耗时和行数，不记录 SQL、参数或原始驱动错误。读副本通过 `slaves` 配置，所有显式 duration 必须大于 0。
