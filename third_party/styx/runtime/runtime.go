// Package runtime 定义 Styx 进程运行时、稳定服务身份和统一资源生命周期。
package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/registry"

	"gitee.com/linpbin/styx/observe"
)

// Identity 是初始化后不可变的服务身份快照。
type Identity struct {
	Name        string
	Version     string
	Environment string
	Namespace   string
	InstanceID  string
}

// ShutdownFunc 在调用方截止时间内关闭一个 Runtime 资源。
type ShutdownFunc func(context.Context) error

// Config 声明 Runtime 的稳定身份、显式依赖和可选注册发现能力。
type Config struct {
	Identity     Identity
	Dependencies observe.Dependencies
	Registrar    registry.Registrar
	Discovery    registry.Discovery
}

type runtimeState uint8

const (
	runtimeOpen runtimeState = iota
	runtimeClosing
	runtimeClosed
)

type ownedResource struct {
	name     string
	shutdown ShutdownFunc
}

// Runtime 统一持有服务身份、可观测依赖和按逆序关闭的基础设施资源。
type Runtime struct {
	identity     Identity
	dependencies observe.Dependencies
	registrar    registry.Registrar
	discovery    registry.Discovery

	mu           sync.Mutex
	shutdownMu   sync.Mutex
	state        runtimeState
	resources    []ownedResource
	shutdownErrs []error
	activeStarts int
	startsDone   chan struct{}
}

// New 创建一个不修改进程全局状态的 Runtime。
func New(config Config) (*Runtime, error) {
	identity := config.Identity
	identity.Name = strings.TrimSpace(identity.Name)
	identity.Version = strings.TrimSpace(identity.Version)
	identity.Environment = strings.TrimSpace(identity.Environment)
	identity.Namespace = strings.TrimSpace(identity.Namespace)
	identity.InstanceID = strings.TrimSpace(identity.InstanceID)
	if identity.Name == "" {
		return nil, newError("runtime", "create", KindInvalidConfig, false, errors.New("service name is required"))
	}
	if identity.Version == "" {
		identity.Version = "v0.0.0"
	}
	if err := config.Dependencies.Validate(); err != nil {
		return nil, newError("runtime", "create", KindInvalidConfig, false, err)
	}
	return &Runtime{
		identity:     identity,
		dependencies: config.Dependencies,
		registrar:    config.Registrar,
		discovery:    config.Discovery,
		state:        runtimeOpen,
	}, nil
}

func (runtime *Runtime) Name() string {
	if runtime == nil {
		return "unknown"
	}
	return runtime.identity.Name
}

func (runtime *Runtime) Version() string {
	if runtime == nil {
		return "v0.0.0"
	}
	return runtime.identity.Version
}

func (runtime *Runtime) Identity() Identity {
	if runtime == nil {
		return Identity{}
	}
	return runtime.identity
}

func (runtime *Runtime) Dependencies() observe.Dependencies {
	if runtime == nil {
		return observe.Dependencies{}
	}
	return runtime.dependencies
}

func (runtime *Runtime) Registrar() registry.Registrar {
	if runtime == nil {
		return nil
	}
	return runtime.registrar
}

func (runtime *Runtime) Discovery() registry.Discovery {
	if runtime == nil {
		return nil
	}
	return runtime.discovery
}

// AttachRegistry 一次性安装同时支持注册和发现的适配器，并把其生命周期交给 Runtime。
func (runtime *Runtime) AttachRegistry(name string, registrar registry.Registrar, discovery registry.Discovery, shutdown ShutdownFunc) error {
	if runtime == nil {
		return newError("runtime", "attach registry", KindInvalidConfig, false, errors.New("runtime is nil"))
	}
	name = strings.TrimSpace(name)
	if name == "" || registrar == nil || discovery == nil || shutdown == nil {
		return newError("runtime", "attach registry", KindInvalidConfig, false, errors.New("registry name, adapters and shutdown are required"))
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state != runtimeOpen {
		return newError("runtime", "attach registry", KindConflict, false, errors.New("runtime is closing"))
	}
	if runtime.registrar != nil || runtime.discovery != nil {
		return newError("runtime", "attach registry", KindConflict, false, errors.New("registry already configured"))
	}
	runtime.registrar = registrar
	runtime.discovery = discovery
	runtime.resources = append(runtime.resources, ownedResource{name: name, shutdown: shutdown})
	return nil
}

// KratosOptions 返回与 Runtime 身份和注册能力一致的 Kratos 配置。
func (runtime *Runtime) KratosOptions() []kratos.Option {
	if runtime == nil {
		return nil
	}
	options := []kratos.Option{
		kratos.Name(runtime.Name()),
		kratos.Version(runtime.Version()),
	}
	if runtime.registrar != nil {
		options = append(options, kratos.Registrar(runtime.registrar))
	}
	return options
}

// Own 把资源交由 Runtime 管理。Runtime 关闭后不再接受新资源。
func (runtime *Runtime) Own(name string, shutdown ShutdownFunc) error {
	if runtime == nil {
		return newError("runtime", "own resource", KindInvalidConfig, false, errors.New("runtime is nil"))
	}
	name = strings.TrimSpace(name)
	if name == "" || shutdown == nil {
		return newError("runtime", "own resource", KindInvalidConfig, false, errors.New("resource name and shutdown are required"))
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state != runtimeOpen {
		return newError("runtime", "own resource", KindConflict, false, errors.New("runtime is closing"))
	}
	runtime.resources = append(runtime.resources, ownedResource{name: name, shutdown: shutdown})
	return nil
}

// StartResource 仅在 Runtime 开放期间启动资源，并与 Shutdown 协调在途启动。
// Shutdown 会先阻止新的启动，再等待已进入的启动回调退出，然后关闭已托管资源。
func (runtime *Runtime) StartResource(ctx context.Context, name string, start func(context.Context) error) error {
	if runtime == nil {
		return newError("runtime", "start resource", KindInvalidConfig, false, errors.New("runtime is nil"))
	}
	name = strings.TrimSpace(name)
	if ctx == nil || name == "" || start == nil {
		return newError("runtime", "start resource", KindInvalidConfig, false, errors.New("context, resource name and start function are required"))
	}
	if err := ctx.Err(); err != nil {
		return newError(name, "start", KindLifecycle, true, err)
	}

	runtime.mu.Lock()
	if runtime.state != runtimeOpen {
		runtime.mu.Unlock()
		return newError(name, "start", KindConflict, false, errors.New("runtime is closing"))
	}
	if runtime.activeStarts == 0 {
		runtime.startsDone = make(chan struct{})
	}
	runtime.activeStarts++
	runtime.mu.Unlock()

	defer func() {
		runtime.mu.Lock()
		runtime.activeStarts--
		if runtime.activeStarts == 0 {
			close(runtime.startsDone)
			runtime.startsDone = nil
		}
		runtime.mu.Unlock()
	}()

	if err := start(ctx); err != nil {
		return newError(name, "start", KindLifecycle, true, err)
	}
	return nil
}

// Shutdown 在调用方 Context 内按创建逆序关闭资源，并聚合安全分类错误。
// 已完成的资源不会重复关闭；Context 截止导致未执行的资源可在后续调用中继续关闭。
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		return newError("runtime", "shutdown", KindInvalidConfig, false, errors.New("shutdown context is required"))
	}
	runtime.shutdownMu.Lock()
	defer runtime.shutdownMu.Unlock()

	if err := ctx.Err(); err != nil {
		return newError("runtime", "shutdown", KindLifecycle, true, err)
	}
	runtime.mu.Lock()
	if runtime.state == runtimeClosed {
		err := errors.Join(runtime.shutdownErrs...)
		runtime.mu.Unlock()
		return err
	}
	runtime.state = runtimeClosing
	startsDone := runtime.startsDone
	runtime.mu.Unlock()

	if startsDone != nil {
		select {
		case <-startsDone:
		case <-ctx.Done():
			return errors.Join(errors.Join(runtime.shutdownErrs...), newError("runtime", "wait for resource start", KindLifecycle, true, ctx.Err()))
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(errors.Join(runtime.shutdownErrs...), newError("runtime", "shutdown", KindLifecycle, true, err))
		}
		runtime.mu.Lock()
		if len(runtime.resources) == 0 {
			runtime.state = runtimeClosed
			err := errors.Join(runtime.shutdownErrs...)
			runtime.mu.Unlock()
			return err
		}
		index := len(runtime.resources) - 1
		resource := runtime.resources[index]
		runtime.mu.Unlock()

		err := resource.shutdown(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil && (err == nil || errors.Is(err, ctxErr)) {
			if err == nil {
				runtime.mu.Lock()
				runtime.resources = runtime.resources[:index]
				runtime.mu.Unlock()
			}
			return errors.Join(errors.Join(runtime.shutdownErrs...), newError(resource.name, "shutdown", KindLifecycle, true, ctxErr))
		}

		runtime.mu.Lock()
		runtime.resources = runtime.resources[:index]
		if err != nil {
			runtime.shutdownErrs = append(runtime.shutdownErrs, newError(resource.name, "shutdown", KindLifecycle, false, err))
		}
		runtime.mu.Unlock()
	}
}

func (runtime *Runtime) String() string {
	if runtime == nil {
		return "styx runtime <nil>"
	}
	return fmt.Sprintf("styx runtime %s@%s", runtime.Name(), runtime.Version())
}
