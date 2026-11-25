package visitor

import "errors"

// errServiceNotConfigured 表示程序装配缺失，不能归责为客户端参数或凭据错误。
var errServiceNotConfigured = errors.New("visitor service is not configured")

// dependencyError 同时保留安全分类和底层原因；字符串不包含原始数据库或平台响应。
type dependencyError struct {
	// category 是调用方指定的稳定依赖分类。
	category error
	// cause 保留取消、超时等原因；Service 在包装前打印该错误，HTTP 仍使用稳定分类。
	cause error
}

// DependencyError 为端口失败标记服务依赖分类，不把它误判为用户凭据失效。
// category 应为 ErrServiceUnavailable 或 ErrIdentityUnavailable。
func DependencyError(category, cause error) error {
	if cause == nil {
		return nil
	}
	return dependencyError{category: category, cause: cause}
}

// Error 提供稳定的依赖分类；原始故障由 Service 在包装前记录。
func (err dependencyError) Error() string { return err.category.Error() }

// Unwrap 保留取消/超时和依赖分类，HTTP 层必须优先判断取消与超时。
func (err dependencyError) Unwrap() []error { return []error{err.category, err.cause} }
