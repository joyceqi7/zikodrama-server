package internal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestLayerDependencyDirection 固化 biz、data、service、server 的单向依赖。
// 目录采用 Kratos 项目的常见组织，但依赖方向仍遵循领域在内、适配器在外的原则。
func TestLayerDependencyDirection(t *testing.T) {
	t.Parallel()
	rules := map[string][]string{
		"biz": {
			"drama-server/internal/data",
			"drama-server/internal/service",
			"drama-server/internal/server",
			"gitee.com/linpbin/styx", "github.com/go-kratos/kratos", "gorm.io/",
		},
		"service": {
			"drama-server/internal/data",
			"drama-server/internal/server",
			"gitee.com/linpbin/styx", "gorm.io/",
		},
		"data": {
			"drama-server/internal/service",
			"drama-server/internal/server",
		},
	}
	for layer, forbidden := range rules {
		layer, forbidden := layer, forbidden
		t.Run(layer, func(t *testing.T) {
			t.Parallel()
			checkLayerImports(t, layer, forbidden)
		})
	}
}

func checkLayerImports(t *testing.T, root string, forbidden []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Decls {
			declaration, ok := spec.(*ast.GenDecl)
			if !ok || declaration.Tok != token.IMPORT {
				continue
			}
			for _, item := range declaration.Specs {
				pathSpec := item.(*ast.ImportSpec)
				importPath, err := strconv.Unquote(pathSpec.Path.Value)
				if err != nil {
					return err
				}
				for _, prefix := range forbidden {
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") || strings.HasSuffix(prefix, "/") && strings.HasPrefix(importPath, prefix) {
						t.Errorf("%s 层文件 %s 反向依赖 %s", root, path, importPath)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOperationsHTTPHandlerOnlyComposesFeatures 防止运营接口重新堆回单个 handler.go。
// 各领域自行注册路由并维护协议适配，公共 Handler 文件只负责构造与组合。
func TestOperationsHTTPHandlerOnlyComposesFeatures(t *testing.T) {
	t.Parallel()
	parsed, err := parser.ParseFile(token.NewFileSet(), "service/operations/handler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]struct{}{"NewHandler": {}, "ServeHTTP": {}, "routes": {}}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, ok := allowed[function.Name.Name]; !ok {
			t.Errorf("service/operations/handler.go 不应承载功能处理方法 %s，请放入对应领域文件", function.Name.Name)
		}
	}
}

// TestProductionCodeDoesNotUseTestSupport 覆盖 data 和 server 等外层目录，
// 保证内存仓储只能由测试文件引用，不能意外进入生产 Wire 图。
func TestProductionCodeDoesNotUseTestSupport(t *testing.T) {
	t.Parallel()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") || strings.HasPrefix(path, "testsupport/") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, item := range parsed.Imports {
			importPath, err := strconv.Unquote(item.Path.Value)
			if err != nil {
				return err
			}
			if importPath == "drama-server/internal/testsupport" || strings.HasPrefix(importPath, "drama-server/internal/testsupport/") {
				t.Errorf("生产文件 %s 不得依赖测试支持包 %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestProductionSynchronizationIsExplicit 防止业务权威状态再次藏进某个进程的锁或 map。
// 当前允许的同步点只有 Worker 生命周期、进程内唤醒优化和按数据库 revision 重建 SDK；
// 新增同步原语必须先说明另一实例如何观察同一业务状态，再显式扩展本清单。
func TestProductionSynchronizationIsExplicit(t *testing.T) {
	t.Parallel()
	allowed := map[string]struct{}{
		// 仅限实例级、可丢弃且有界的请求计数；跨实例入口配额交给网关，不涉及业务一致性。
		filepath.Clean("pkg/ratelimit/memory.go"):      {},
		filepath.Clean("biz/operations/media_jobs.go"): {},
		// 发布事实与租约均在 MySQL；进程内同步只负责 Worker 生命周期和低延迟唤醒。
		filepath.Clean("biz/operations/publication.go"): {},
		filepath.Clean("biz/worker.go"):                 {},
		// 每个实例独立复用 TikTok SDK 与可重建 OAuth Token；缓存不参与业务状态判定，
		// 应用凭据变更通过密钥指纹在本实例下一次请求时立即重建客户端。
		filepath.Clean("data/providers.go"): {},
		// Redis 每次读取是权威配置；互斥锁只保护本实例可重建的 SDK 客户端，不缓存业务事实。
		filepath.Clean("data/drama_media_provider.go"): {},
		// OAuth Token 是可重建的进程内凭据缓存，不参与跨实例业务状态判定。
		filepath.Clean("data/platform/tiktokmedia/transport.go"): {},
		// MySQL 水位永久预留号段，Redis Lua 共享消费和切换；进程内同步仅缓存数据库
		// 对应的 Redis key，并合并首次身份查询，不保存任何发号游标或补段租约。
		filepath.Clean("data/persistence/mysqlstore/id_allocator.go"): {},
		// UID 游标仍由 Redis Lua 原子维护；此处的 sync.Once 只防止专用连接池重复关闭。
		filepath.Clean("data/redisstore/uid_cache.go"): {},
		// 指标分表由 MySQL 的 CREATE TABLE IF NOT EXISTS 保证多实例一致；进程内状态
		// 只抑制同一实例重复执行 DDL，丢失后会从模板重新确认，不保存业务数据。
		filepath.Clean("data/persistence/mysqlstore/metric_partition_ddl.go"): {},
	}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") || strings.HasPrefix(path, "testsupport/") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, item := range parsed.Imports {
			importPath, err := strconv.Unquote(item.Path.Value)
			if err != nil {
				return err
			}
			if importPath != "sync" {
				continue
			}
			if _, ok := allowed[filepath.Clean(path)]; !ok {
				t.Errorf("生产文件 %s 新增了进程内同步状态，必须先完成多实例一致性设计", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
