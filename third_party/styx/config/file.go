// Package config 提供服务入口使用的配置加载能力。
package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
)

// LoadFile 从单个 Kratos 支持的配置文件加载并扫描到 target。
//
// 文件源和 Loader 的关闭生命周期由本函数完整管理，应用 main 不需要重复创建、Load、
// Scan 和 Close。target 必须是非 nil 指针；其具体 schema 仍由业务项目定义，Styx 不
// 读取或解释业务配置字段。
func LoadFile(path string, target any) (loadErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("styx config: file path is required")
	}
	if !isNonNilPointer(target) {
		return fmt.Errorf("styx config: scan target must be a non-nil pointer")
	}

	loader := kratosconfig.New(kratosconfig.WithSource(file.NewSource(path)))
	defer func() {
		loadErr = errors.Join(loadErr, loader.Close())
	}()
	if err := loader.Load(); err != nil {
		return fmt.Errorf("styx config: load %q: %w", path, err)
	}
	if err := loader.Scan(target); err != nil {
		return fmt.Errorf("styx config: scan %q: %w", path, err)
	}
	return nil
}

// MustLoadFile 与 LoadFile 相同，但在配置路径、内容或目标无效时 panic。
// 它适用于无法继续运行的服务进程入口；库代码应调用 LoadFile 并返回错误。
func MustLoadFile(path string, target any) {
	if err := LoadFile(path, target); err != nil {
		panic(err)
	}
}

func isNonNilPointer(target any) bool {
	if target == nil {
		return false
	}
	value := reflect.ValueOf(target)
	return value.Kind() == reflect.Pointer && !value.IsNil()
}
