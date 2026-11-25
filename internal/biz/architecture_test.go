package biz

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFeatureBoundariesDoNotCollapseIntoGenericFiles 防止运营和 RBAC 代码重新退化为
// model.go + 大 service.go 的横向分层。领域合同应与对应功能放在一起，Service 文件只做装配。
func TestFeatureBoundariesDoNotCollapseIntoGenericFiles(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"operations/model.go", "adminrbac/model.go", "adminrbac/service.go"} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s 不应重新出现；请按具体功能或领域维护类型和用例", path)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), "operations/service.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if function.Recv != nil || function.Name.Name != "NewService" {
			t.Errorf("operations/service.go 只允许保留 Service 装配，具体用例 %s 应放入对应领域文件", function.Name.Name)
		}
	}
}

// TestBizLayerKeepsWireAtCompositionRoot 只检查业务装配入口；分层依赖由 internal 的统一架构测试覆盖。
func TestBizLayerKeepsWireAtCompositionRoot(t *testing.T) {
	for _, root := range []string{"."} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(contents), `"github.com/google/wire`) && filepath.Clean(path) != "biz.go" {
				t.Errorf("业务文件 %s 不得直接声明 Wire 装配", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
