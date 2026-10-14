package archtest

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

// 驱动只能引用 guard.Dial；同时拦截方法调用和把 net.Dial 赋给变量的绕行。
func networkViolations(file *ast.File) []string {
	allowed := map[string]bool{}
	var violations []string
	for _, im := range file.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			violations = append(violations, "invalid import")
			continue
		}
		alias := filepath.Base(path)
		if im.Name != nil {
			alias = im.Name.Name
		}
		if path == "net/http" || path == "syscall" || path == "unsafe" || strings.HasPrefix(path, "golang.org/x/sys/") {
			violations = append(violations, "forbidden network escape import")
		}
		if path == "ups-warden/internal/driver/guard" {
			if alias == "." {
				violations = append(violations, "dot guard import")
			} else {
				allowed[alias] = true
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if strings.HasPrefix(sel.Sel.Name, "Dial") || strings.HasPrefix(sel.Sel.Name, "Listen") {
			id, ok := sel.X.(*ast.Ident)
			if !ok || !allowed[id.Name] || sel.Sel.Name != "Dial" {
				violations = append(violations, "network operation outside guard")
			}
		}
		return true
	})
	return violations
}
func TestDriversUseGuard(t *testing.T) {
	root := "../driver"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "guard" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, v := range networkViolations(file) {
			t.Errorf("%s: %s", path, v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestNetworkRuleAliases(t *testing.T) {
	for _, tc := range []struct {
		source string
		bad    bool
	}{
		{`package p; import n "net"; var dial=n.Dial`, true},
		{`package p; import "net"; func f(){d:=net.Dialer{}; d.DialContext(nil,"tcp","")}`, true},
		{`package p; import "net/http"`, true},
		{`package p; import g "ups-warden/internal/driver/guard"; func f(){g.Dial(nil,g.Config{},"snmp")}`, false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "test.go", tc.source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if (len(networkViolations(file)) > 0) != tc.bad {
			t.Fatal("网络边界规则失效")
		}
	}
}
