package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gitee.com/linpbin/styx"
	styxotel "gitee.com/linpbin/styx/otel"
)

// TestLocalTraceConfigurationWithoutCollector 确认各环境能在没有 Collector 时为请求生成关联标识。
func TestLocalTraceConfigurationWithoutCollector(t *testing.T) {
	for _, filename := range []string{"config.yaml", "config.test.yaml", "config.prod.yaml"} {
		t.Run(filename, func(t *testing.T) {
			bootstrap, err := loadBootstrap(filepath.Join("..", "..", "configs", filename))
			if err != nil {
				t.Fatal(err)
			}
			otelConfig := bootstrap.GetOtel()
			if !otelConfig.GetTrace().GetEnabled() || otelConfig.GetEndpoint() != "" || otelConfig.GetLog().GetEnabled() {
				t.Fatal("本地 Trace 必须开启，Collector endpoint 和远程日志导出必须关闭")
			}
			provider, err := styxotel.New(context.Background(), otelConfig, bootstrap.GetApp())
			if err != nil {
				t.Fatalf("无 Collector 的本地 Trace 启动失败：%v", err)
			}
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			ctx, span := provider.TracerProvider().Tracer("drama-server-config-test").Start(context.Background(), "HTTP GET")
			defer span.End()
			if ctx == nil || !span.SpanContext().TraceID().IsValid() || span.IsRecording() {
				t.Fatal("本地 Trace 未生成可用 Trace ID，或意外记录了 Span")
			}
		})
	}
}

// TestLogFileConfigurationComesOnlyFromYAML 验证 Styx 直接读取文件日志配置，环境变量不能覆盖。
func TestLogFileConfigurationComesOnlyFromYAML(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRAMA_LOG_DIRECTORY", "/tmp/ignored")
	t.Setenv("DRAMA_LOG_ROTATION", "day")
	t.Setenv("DRAMA_LOG_RETENTION_DAYS", "1")
	t.Setenv("DRAMA_MYSQL_DSN", "ignored-dsn")
	for _, tt := range []struct {
		name       string
		filename   string
		configured string
		directory  string
		rotation   styx.LogRotation
		retention  int
	}{
		{name: "dev", filename: "config.yaml", configured: "/tmp/drama-server/dev", directory: "/tmp/drama-server/dev", rotation: styx.LogRotationDay, retention: 14},
		{name: "test", filename: "config.test.yaml", configured: "./logs", directory: filepath.Join(workingDirectory, "logs"), rotation: styx.LogRotationHour, retention: 3},
		{name: "prod", filename: "config.prod.yaml", configured: "./logs", directory: filepath.Join(workingDirectory, "logs"), rotation: styx.LogRotationDay, retention: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bootstrap, err := loadBootstrap(filepath.Join("..", "..", "configs", tt.filename))
			if err != nil {
				t.Fatal(err)
			}
			file, err := styx.ParseLogFileConfig(bootstrap.GetOtel().GetLog().GetFile())
			if err != nil {
				t.Fatal(err)
			}
			if bootstrap.GetOtel().GetLog().GetFile().GetDirectory() != tt.configured || file.Directory != tt.directory || file.Prefix != "drama-server" || file.Rotation != tt.rotation || file.RetentionDays != tt.retention {
				t.Fatalf("未使用文件策略：%+v", file)
			}
			if bootstrap.GetData().GetMysql().GetMaster() == "ignored-dsn" {
				t.Fatal("数据库配置被环境变量覆盖")
			}
		})
	}
}
