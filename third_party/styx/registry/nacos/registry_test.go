package nacos

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"

	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

type lateNamingClient struct {
	naming_client.INamingClient
	closed chan struct{}
	health chan struct{}
}

func (client *lateNamingClient) CloseClient() {
	close(client.closed)
}

func (client *lateNamingClient) ServerHealthy() bool {
	<-client.health
	return true
}

func TestNamingClientCreationHonorsContextAndClosesLateClient(t *testing.T) {
	release := make(chan struct{})
	closed := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err, _ := newNamingClientWithContext(ctx, func() (naming_client.INamingClient, error) {
		<-release
		return &lateNamingClient{closed: closed}, nil
	}, func(created namingClientResult) {
		if created.client != nil {
			created.client.CloseClient()
		}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("创建 Nacos Client 错误 = %v，预期 DeadlineExceeded", err)
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("超时后创建成功的 Nacos Client 未被关闭")
	}
}

func TestNamingClientHealthCheckHonorsContextAndClosesLateClient(t *testing.T) {
	health := make(chan struct{})
	closed := make(chan struct{})
	client := &lateNamingClient{closed: closed, health: health}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := namingClientHealthyWithContext(ctx, client, client.CloseClient)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("检查 Nacos Client 错误 = %v，预期 DeadlineExceeded", err)
	}
	close(health)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("超时后完成健康检查的 Nacos Client 未被关闭")
	}
}

func TestManagedNamingClientKeepsLoggerLeaseUntilLateCreationCleanup(t *testing.T) {
	first := newLoggerRuntime(t, "late-creation-first")
	second := newLoggerRuntime(t, "late-creation-second")
	releaseFactory := make(chan struct{})
	closed := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err := newManagedNamingClient(ctx, first, "warn", slog.Default(), nil, func() (naming_client.INamingClient, error) {
		<-releaseFactory
		return &lateNamingClient{closed: closed}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("创建 Nacos Client 错误 = %v，预期 DeadlineExceeded", err)
	}
	secondRelease, secondErr := acquireSlogAdapter(second, "warn", slog.Default())
	if secondErr == nil {
		secondRelease()
	}
	close(releaseFactory)
	waitForSignal(t, closed, "超时后创建成功的 Nacos Client 未被关闭")
	if secondErr == nil {
		t.Fatal("迟到的 Nacos Client 清理完成前不得释放 SDK Logger 租约")
	}
	waitForLoggerLease(t, second)
}

func TestManagedNamingClientKeepsLoggerLeaseUntilLateHealthCleanup(t *testing.T) {
	first := newLoggerRuntime(t, "late-health-first")
	second := newLoggerRuntime(t, "late-health-second")
	health := make(chan struct{})
	closed := make(chan struct{})
	client := &lateNamingClient{closed: closed, health: health}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err := newManagedNamingClient(ctx, first, "warn", slog.Default(), nil, func() (naming_client.INamingClient, error) {
		return client, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("检查 Nacos Client 错误 = %v，预期 DeadlineExceeded", err)
	}
	secondRelease, secondErr := acquireSlogAdapter(second, "warn", slog.Default())
	if secondErr == nil {
		secondRelease()
	}
	close(health)
	waitForSignal(t, closed, "健康检查超时后的 Nacos Client 未被关闭")
	if secondErr == nil {
		t.Fatal("迟到的 Nacos 健康检查清理完成前不得释放 SDK Logger 租约")
	}
	waitForLoggerLease(t, second)
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func waitForLoggerLease(t *testing.T, runtime *styxruntime.Runtime) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		release, err := acquireSlogAdapter(runtime, "warn", slog.Default())
		if err == nil {
			release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("迟到清理完成后 Nacos SDK Logger 租约仍未释放：%v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRegistrationParamsUseRawServiceNameAndSupportIPv6(t *testing.T) {
	options, err := newOptions()
	if err != nil {
		t.Fatalf("创建 Nacos 选项：%v", err)
	}
	nacosRegistry := &Registry{opts: options, dependencies: observe.NoopDependencies()}
	params, err := nacosRegistry.registrationParams(&registry.ServiceInstance{
		Name:      "user-service",
		Version:   "v1",
		Endpoints: []string{"grpc://[2001:db8::1]:9000", "http://127.0.0.1:8000"},
	})
	if err != nil {
		t.Fatalf("转换注册参数：%v", err)
	}
	if len(params) != 2 {
		t.Fatalf("注册参数数量=%d，预期 2", len(params))
	}
	for _, param := range params {
		if param.ServiceName != "user-service" {
			t.Fatalf("服务名被协议后缀改写：%q", param.ServiceName)
		}
	}
	if params[0].Ip != "2001:db8::1" {
		t.Fatalf("IPv6 地址解析错误：%q", params[0].Ip)
	}
}

func TestInstancesToServiceInstancesFormatsIPv6(t *testing.T) {
	options, _ := newOptions()
	nacosRegistry := &Registry{opts: options, dependencies: observe.NoopDependencies()}
	instances := nacosRegistry.instancesToServiceInstances("user-service", []model.Instance{{
		InstanceId: "instance-1",
		Ip:         "2001:db8::1",
		Port:       9000,
		Enable:     true,
		Healthy:    true,
		Weight:     0.25,
		Metadata:   map[string]string{"kind": "grpcs"},
	}})
	if len(instances) != 1 || instances[0].Endpoints[0] != "grpcs://[2001:db8::1]:9000" {
		t.Fatalf("IPv6 endpoint 格式错误：%+v", instances)
	}
	if instances[0].Name != "user-service" {
		t.Fatalf("发现结果服务名错误：%q", instances[0].Name)
	}
	if instances[0].Metadata["weight"] != "0.25" {
		t.Fatalf("发现结果丢失了小数权重：%q", instances[0].Metadata["weight"])
	}
}

func TestNewOptionsRejectsInvalidKind(t *testing.T) {
	if _, err := newOptions(WithKind("tcp")); err == nil {
		t.Fatal("未知服务协议必须失败")
	}
}

func TestNacosRejectsNonFiniteWeightAndEndpointComponents(t *testing.T) {
	if _, err := newOptions(WithWeight(math.NaN())); err == nil {
		t.Fatal("Nacos 权重不能为 NaN")
	}
	options, _ := newOptions()
	nacosRegistry := &Registry{opts: options, dependencies: observe.NoopDependencies()}
	_, err := nacosRegistry.registrationParams(&registry.ServiceInstance{
		Name: "user-service", Endpoints: []string{"grpc://127.0.0.1:9000/path"},
	})
	if err == nil {
		t.Fatal("Nacos 注册端点不能包含路径")
	}
}

func TestWatcherCoalescesCallbacksWithoutKeepingStaleError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &watcher{ctx: ctx, watchChan: make(chan struct{}, 1)}
	w.notify(errors.New("瞬时错误"))
	w.notify(nil)
	if len(w.watchChan) != 1 {
		t.Fatalf("合并后的通知数=%d，期望 1", len(w.watchChan))
	}
	if err := w.takeCallbackError(); err != nil {
		t.Fatalf("成功回调后仍保留旧错误：%v", err)
	}
}
