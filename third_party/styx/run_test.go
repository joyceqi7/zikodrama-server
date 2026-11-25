package styx

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/go-kratos/kratos/v2/transport"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

var errRunTestServer = errors.New("run test server stopped")

type failingServer struct{}

func (failingServer) Start(context.Context) error { return errRunTestServer }
func (failingServer) Stop(context.Context) error  { return nil }

type pointerServer struct{}

func (*pointerServer) Start(context.Context) error { return nil }
func (*pointerServer) Stop(context.Context) error  { return nil }

func TestRunOwnsApplicationAndRuntimeCleanupOrder(t *testing.T) {
	var order []string
	err := Run(context.Background(), Config{App: &configv1.AppInfo{Name: "run-test"}}, func(_ context.Context, runtime *Runtime) ([]transport.Server, func(), error) {
		if err := runtime.Own("test resource", func(context.Context) error {
			order = append(order, "runtime")
			return nil
		}); err != nil {
			return nil, nil, err
		}
		return []transport.Server{failingServer{}}, func() { order = append(order, "wire") }, nil
	}, WithoutProcessGlobals())
	if !errors.Is(err, errRunTestServer) {
		t.Fatalf("Run() error = %v, want server error", err)
	}
	if want := []string{"wire", "runtime"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("cleanup order = %v, want %v", order, want)
	}
}

func TestRunShutsRuntimeDownWhenBuilderFails(t *testing.T) {
	var closed bool
	buildErr := errors.New("build failed")
	err := Run(context.Background(), Config{App: &configv1.AppInfo{Name: "run-build-test"}}, func(_ context.Context, runtime *Runtime) ([]transport.Server, func(), error) {
		if ownErr := runtime.Own("test resource", func(context.Context) error {
			closed = true
			return nil
		}); ownErr != nil {
			return nil, nil, ownErr
		}
		return nil, nil, buildErr
	}, WithoutProcessGlobals())
	if !errors.Is(err, buildErr) {
		t.Fatalf("Run() error = %v, want build error", err)
	}
	if !closed {
		t.Fatal("builder 失败后 Runtime 资源未关闭")
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	config := Config{App: &configv1.AppInfo{Name: "run-invalid-test"}}
	if err := Run(nil, config, func(context.Context, *Runtime) ([]transport.Server, func(), error) {
		return nil, nil, nil
	}); err == nil {
		t.Fatal("nil context 必须失败")
	}
	if err := Run(context.Background(), config, nil); err == nil {
		t.Fatal("nil builder 必须失败")
	}
	if err := Run(context.Background(), config, func(context.Context, *Runtime) ([]transport.Server, func(), error) {
		return nil, nil, nil
	}, WithShutdownTimeout(0)); err == nil {
		t.Fatal("非正关闭超时必须失败")
	}
}

func TestRunRejectsInvalidServerListAndRunsCleanup(t *testing.T) {
	config := Config{App: &configv1.AppInfo{Name: "run-server-test"}}
	for _, test := range []struct {
		name    string
		servers []transport.Server
	}{
		{name: "empty"},
		{name: "contains nil", servers: []transport.Server{nil}},
		{name: "contains typed nil", servers: []transport.Server{(*pointerServer)(nil)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleaned := false
			err := Run(context.Background(), config, func(context.Context, *Runtime) ([]transport.Server, func(), error) {
				return test.servers, func() { cleaned = true }, nil
			}, WithoutProcessGlobals())
			if err == nil {
				t.Fatal("无效 Server 列表必须失败")
			}
			if !cleaned {
				t.Fatal("Server 校验失败后未执行 builder cleanup")
			}
		})
	}
}
