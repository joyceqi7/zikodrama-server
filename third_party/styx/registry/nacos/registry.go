package nacos

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"gitee.com/linpbin/styx/observe"
	styxruntime "gitee.com/linpbin/styx/runtime"
)

var (
	_ registry.Registrar = (*Registry)(nil)
	_ registry.Discovery = (*Registry)(nil)
)

// Registry 是 Nacos 注册发现实现。
type Registry struct {
	client       naming_client.INamingClient
	opts         *options
	dependencies observe.Dependencies
}

// New 使用已有 Nacos client 创建 Registry。
func New(client naming_client.INamingClient, runtime *styxruntime.Runtime, opts ...Option) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("nacos: naming client is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("nacos: runtime is required")
	}
	dependencies := runtime.Dependencies()
	if err := dependencies.Validate(); err != nil {
		return nil, fmt.Errorf("nacos: runtime dependencies: %w", err)
	}
	resolved, err := newOptions(opts...)
	if err != nil {
		return nil, err
	}
	return &Registry{client: client, opts: resolved, dependencies: dependencies}, nil
}

// Register 将同一服务的所有协议端点注册在原始服务名下。
func (r *Registry) Register(ctx context.Context, service *registry.ServiceInstance) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	params, err := r.registrationParams(service)
	if err != nil {
		return err
	}
	registered := make([]vo.RegisterInstanceParam, 0, len(params))
	for _, param := range params {
		if err := validateContext(ctx); err != nil {
			return errors.Join(err, r.rollback(registered))
		}
		ok, registerErr := r.client.RegisterInstance(param)
		if registerErr != nil || !ok {
			if registerErr == nil {
				registerErr = fmt.Errorf("nacos returned false")
			}
			return errors.Join(fmt.Errorf("nacos: register instance failed: %w", registerErr), r.rollback(registered))
		}
		registered = append(registered, param)
	}
	return nil
}

// Deregister 注销服务的全部协议端点。
func (r *Registry) Deregister(ctx context.Context, service *registry.ServiceInstance) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	params, err := r.registrationParams(service)
	if err != nil {
		return err
	}
	var deregisterErrors []error
	for _, param := range params {
		if err := validateContext(ctx); err != nil {
			deregisterErrors = append(deregisterErrors, err)
			break
		}
		ok, deregisterErr := r.client.DeregisterInstance(deregisterParam(param))
		if deregisterErr != nil {
			deregisterErrors = append(deregisterErrors, deregisterErr)
		} else if !ok {
			deregisterErrors = append(deregisterErrors, fmt.Errorf("nacos returned false"))
		}
	}
	if err := errors.Join(deregisterErrors...); err != nil {
		return fmt.Errorf("nacos: deregister instance failed: %w", err)
	}
	return nil
}

// GetService 查询当前 group 和 cluster 内的健康实例。
func (r *Registry) GetService(ctx context.Context, serviceName string) ([]*registry.ServiceInstance, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if serviceName == "" {
		return nil, fmt.Errorf("nacos: service name is required")
	}
	instances, err := r.client.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		GroupName:   r.opts.group,
		Clusters:    []string{r.opts.cluster},
		HealthyOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("nacos: get service failed: %w", err)
	}
	return r.instancesToServiceInstances(serviceName, instances), nil
}

// Watch 订阅当前 group 和 cluster 内的服务变化。
func (r *Registry) Watch(ctx context.Context, serviceName string) (registry.Watcher, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if serviceName == "" {
		return nil, fmt.Errorf("nacos: service name is required")
	}
	return newWatcher(ctx, r, serviceName)
}

// WithGroupOverride 返回共享 client、使用另一个 group 的 Registry。
func (r *Registry) WithGroupOverride(group string) (*Registry, error) {
	if group == "" {
		return nil, fmt.Errorf("nacos: group override is required")
	}
	newOptions := *r.opts
	newOptions.group = group
	return &Registry{client: r.client, opts: &newOptions, dependencies: r.dependencies}, nil
}

func (r *Registry) registrationParams(service *registry.ServiceInstance) ([]vo.RegisterInstanceParam, error) {
	if service == nil {
		return nil, fmt.Errorf("nacos: service instance is required")
	}
	if service.Name == "" {
		return nil, fmt.Errorf("nacos: service name is required")
	}
	if len(service.Endpoints) == 0 {
		return nil, fmt.Errorf("nacos: service endpoint is required")
	}
	weight := r.opts.weight
	if configuredWeight, ok := service.Metadata["weight"]; ok {
		parsedWeight, err := strconv.ParseFloat(configuredWeight, 64)
		if err != nil || parsedWeight <= 0 || math.IsNaN(parsedWeight) || math.IsInf(parsedWeight, 0) {
			return nil, fmt.Errorf("nacos: metadata weight must be finite and positive")
		}
		weight = parsedWeight
	}

	params := make([]vo.RegisterInstanceParam, 0, len(service.Endpoints))
	for _, endpoint := range service.Endpoints {
		parsed, err := url.Parse(endpoint)
		if err != nil || !validEndpointKind(parsed.Scheme) {
			return nil, fmt.Errorf("nacos: endpoint must use grpc, grpcs, http, or https scheme")
		}
		host, portText, err := net.SplitHostPort(parsed.Host)
		if err != nil {
			return nil, fmt.Errorf("nacos: invalid endpoint: %w", err)
		}
		if host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("nacos: endpoint must contain only scheme, host, and port")
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("nacos: invalid endpoint port %q", portText)
		}
		metadata := make(map[string]string, len(service.Metadata)+2)
		for key, value := range service.Metadata {
			metadata[key] = value
		}
		metadata["kind"] = parsed.Scheme
		metadata["version"] = service.Version
		params = append(params, vo.RegisterInstanceParam{
			Ip:          host,
			Port:        port,
			Weight:      weight,
			Enable:      true,
			Healthy:     true,
			Ephemeral:   true,
			Metadata:    metadata,
			ClusterName: r.opts.cluster,
			ServiceName: service.Name,
			GroupName:   r.opts.group,
		})
	}
	return params, nil
}

func (r *Registry) rollback(params []vo.RegisterInstanceParam) error {
	var rollbackErrors []error
	for index := len(params) - 1; index >= 0; index-- {
		ok, err := r.client.DeregisterInstance(deregisterParam(params[index]))
		if err != nil {
			rollbackErrors = append(rollbackErrors, err)
		} else if !ok {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("nacos returned false"))
		}
	}
	if err := errors.Join(rollbackErrors...); err != nil {
		return fmt.Errorf("nacos: rollback registration failed: %w", err)
	}
	return nil
}

func deregisterParam(param vo.RegisterInstanceParam) vo.DeregisterInstanceParam {
	return vo.DeregisterInstanceParam{
		Ip:          param.Ip,
		Port:        param.Port,
		Cluster:     param.ClusterName,
		ServiceName: param.ServiceName,
		GroupName:   param.GroupName,
		Ephemeral:   param.Ephemeral,
	}
}

func (r *Registry) instancesToServiceInstances(serviceName string, instances []model.Instance) []*registry.ServiceInstance {
	result := make([]*registry.ServiceInstance, 0, len(instances))
	for _, instance := range instances {
		if !instance.Enable || !instance.Healthy {
			continue
		}
		kind := instance.Metadata["kind"]
		if !validEndpointKind(kind) {
			kind = r.opts.kind
		}
		weight := r.opts.weight
		if instance.Weight > 0 {
			weight = instance.Weight
		}
		metadata := make(map[string]string, len(instance.Metadata)+1)
		for key, value := range instance.Metadata {
			metadata[key] = value
		}
		metadata["weight"] = strconv.FormatFloat(weight, 'f', -1, 64)
		result = append(result, &registry.ServiceInstance{
			ID:        instance.InstanceId,
			Name:      serviceName,
			Version:   instance.Metadata["version"],
			Metadata:  metadata,
			Endpoints: []string{fmt.Sprintf("%s://%s", kind, net.JoinHostPort(instance.Ip, strconv.FormatUint(instance.Port, 10)))},
		})
	}
	return result
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nacos: context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nacos: context: %w", err)
	}
	return nil
}
