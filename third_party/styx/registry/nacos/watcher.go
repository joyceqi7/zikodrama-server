package nacos

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

var _ registry.Watcher = (*watcher)(nil)

type watcher struct {
	serviceName    string
	registry       *Registry
	ctx            context.Context
	cancel         context.CancelFunc
	watchChan      chan struct{}
	subscribeParam *vo.SubscribeParam

	mu          sync.Mutex
	callbackErr error
	stopOnce    sync.Once
	stopErr     error
}

func newWatcher(ctx context.Context, nacosRegistry *Registry, serviceName string) (*watcher, error) {
	watchContext, cancel := context.WithCancel(ctx)
	w := &watcher{
		serviceName: serviceName,
		registry:    nacosRegistry,
		ctx:         watchContext,
		cancel:      cancel,
		watchChan:   make(chan struct{}, 1),
	}
	w.subscribeParam = &vo.SubscribeParam{
		ServiceName: serviceName,
		GroupName:   nacosRegistry.opts.group,
		Clusters:    []string{nacosRegistry.opts.cluster},
		SubscribeCallback: func(_ []model.Instance, err error) {
			w.notify(err)
		},
	}
	if err := nacosRegistry.client.Subscribe(w.subscribeParam); err != nil {
		cancel()
		return nil, fmt.Errorf("nacos: subscribe failed: %w", err)
	}
	w.notify(nil)
	return w, nil
}

func (w *watcher) notify(err error) {
	w.mu.Lock()
	// 只保留最新回调状态，避免一次瞬时错误覆盖随后到达的成功更新。
	w.callbackErr = err
	w.mu.Unlock()
	select {
	case <-w.ctx.Done():
		return
	default:
	}
	select {
	case w.watchChan <- struct{}{}:
	default:
	}
}

func (w *watcher) Next() ([]*registry.ServiceInstance, error) {
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case <-w.watchChan:
	}
	if err := w.takeCallbackError(); err != nil {
		return nil, fmt.Errorf("nacos: subscription callback failed: %w", err)
	}
	if err := validateContext(w.ctx); err != nil {
		return nil, err
	}
	instances, err := w.registry.client.SelectInstances(vo.SelectInstancesParam{
		ServiceName: w.serviceName,
		GroupName:   w.registry.opts.group,
		Clusters:    []string{w.registry.opts.cluster},
		HealthyOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("nacos: get instances failed: %w", err)
	}
	return w.registry.instancesToServiceInstances(w.serviceName, instances), nil
}

func (w *watcher) Stop() error {
	w.stopOnce.Do(func() {
		w.cancel()
		w.stopErr = w.registry.client.Unsubscribe(w.subscribeParam)
	})
	return w.stopErr
}

func (w *watcher) takeCallbackError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.callbackErr
	w.callbackErr = nil
	return err
}
