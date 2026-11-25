package styx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/transport"

	styxruntime "gitee.com/linpbin/styx/runtime"
)

const defaultShutdownTimeout = 15 * time.Second

// ServerBuilder 是应用组合根的唯一扩展点。
//
// builder 通常由 Wire 生成：Styx 负责创建 Runtime 和 Kratos App，业务项目只负责
// 根据配置装配 biz、service 以及需要启动的 transport.Server。cleanup 用于释放不属于
// Styx 基础设施工厂的应用资源；Run 保证它在 Runtime 关闭之前执行。
type ServerBuilder func(context.Context, *styxruntime.Runtime) ([]transport.Server, func(), error)

// RunOption 调整应用运行阶段的框架行为。
type RunOption func(*runOptions) error

type runOptions struct {
	// shutdownTimeout 限制 Runtime 资源关闭的单次等待时间。
	shutdownTimeout time.Duration
	// installGlobals 表示是否由当前单服务进程 Runtime 独占全局 Logger/OTel。
	installGlobals bool
}

// WithShutdownTimeout 配置 Kratos App 退出后等待 Styx 资源逆序关闭的最长时间。
func WithShutdownTimeout(timeout time.Duration) RunOption {
	return func(options *runOptions) error {
		if timeout <= 0 {
			return fmt.Errorf("styx: shutdown timeout must be positive")
		}
		options.shutdownTimeout = timeout
		return nil
	}
}

// WithoutProcessGlobals 禁止 Run 安装 slog 和 OpenTelemetry 进程全局值。
//
// Run 默认安装全局值，因为它本身就是单服务进程的组合根；只有同进程承载多个
// Runtime 的工具或测试才应关闭该行为。Styx 基础设施工厂仍始终使用 Runtime 中的
// 显式依赖，不依赖全局状态。
func WithoutProcessGlobals() RunOption {
	return func(options *runOptions) error {
		options.installGlobals = false
		return nil
	}
}

// Run 完整管理一个 Kratos 服务进程的框架生命周期。
//
// 顺序固定为：创建 Runtime、安装进程全局依赖、装配 Server、创建并运行 Kratos App、执行 Wire
// cleanup、逆序关闭 Runtime。应用项目不应再自行复制这套关闭顺序，也不需要把
// Logger、TracerProvider 或资源注册表传播到业务包。
func Run(ctx context.Context, config Config, builder ServerBuilder, optionList ...RunOption) (runErr error) {
	if ctx == nil {
		return fmt.Errorf("styx: run context is required")
	}
	if builder == nil {
		return fmt.Errorf("styx: server builder is required")
	}
	options := runOptions{shutdownTimeout: defaultShutdownTimeout, installGlobals: true}
	for _, option := range optionList {
		if option == nil {
			return fmt.Errorf("styx: run option is required")
		}
		if err := option(&options); err != nil {
			return err
		}
	}

	runtime, err := New(ctx, config)
	if err != nil {
		return err
	}
	// 先登记 Runtime 关闭，再登记 Wire cleanup；defer 的逆序执行保证应用资源先于
	// Runtime 基础设施关闭，并且 app.Run 或 builder panic 时也不会泄漏已创建资源。
	defer func() {
		runErr = errors.Join(runErr, shutdownRuntime(runtime, options.shutdownTimeout))
	}()
	if options.installGlobals {
		if err := runtime.InstallGlobals(); err != nil {
			return err
		}
	}

	servers, cleanup, buildErr := builder(ctx, runtime)
	if cleanup != nil {
		defer cleanup()
	}
	if buildErr != nil {
		return buildErr
	}
	if err := validateServers(servers); err != nil {
		return err
	}
	appOptions := runtime.KratosOptions()
	appOptions = append(appOptions, kratos.Server(servers...))
	app := kratos.New(appOptions...)
	return app.Run()
}

// MustRun 与 Run 相同，但在初始化、装配、运行或关闭失败时 panic。
func MustRun(ctx context.Context, config Config, builder ServerBuilder, options ...RunOption) {
	if err := Run(ctx, config, builder, options...); err != nil {
		panic(err)
	}
}

func validateServers(servers []transport.Server) error {
	if len(servers) == 0 {
		return fmt.Errorf("styx: server builder returned no servers")
	}
	for index, server := range servers {
		if server == nil || isNilServer(server) {
			return fmt.Errorf("styx: server builder returned nil server at index %d", index)
		}
	}
	return nil
}

func isNilServer(server transport.Server) bool {
	value := reflect.ValueOf(server)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func shutdownRuntime(runtime *styxruntime.Runtime, timeout time.Duration) error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runtime.Shutdown(shutdownContext)
}
