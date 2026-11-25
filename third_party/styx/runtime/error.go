package runtime

import "fmt"

// ErrorKind 描述 Runtime 错误的稳定分类，供启动治理和告警使用。
type ErrorKind string

const (
	KindInvalidConfig ErrorKind = "invalid_config"
	KindConflict      ErrorKind = "conflict"
	KindDependency    ErrorKind = "dependency"
	KindLifecycle     ErrorKind = "lifecycle"
)

// Error 保存安全摘要和受控诊断所需的原始 cause。
// Error() 不输出 cause，避免第三方错误携带凭据或业务正文进入普通日志。
type Error struct {
	Component string
	Operation string
	Kind      ErrorKind
	Retryable bool
	err       error
}

func (err *Error) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("styx: %s %s failed (%s)", err.Component, err.Operation, err.Kind)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func newError(component, operation string, kind ErrorKind, retryable bool, cause error) error {
	return &Error{
		Component: component,
		Operation: operation,
		Kind:      kind,
		Retryable: retryable,
		err:       cause,
	}
}

// NewError 为 Runtime 组合根和适配器创建统一的安全分类错误。
func NewError(component, operation string, kind ErrorKind, retryable bool, cause error) error {
	return newError(component, operation, kind, retryable, cause)
}
