// Package archtest 将采集与业务层的依赖边界纳入每次 go test。
package archtest

import (
	"errors"
	"go/build"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func within(pkg, root string) bool { return pkg == root || strings.HasPrefix(pkg, root+"/") }

func forbidden(from, to string) bool {
	if within(to, "internal/sim") && from != "cmd/upssim" {
		return true
	}
	if within(from, "internal/driver") && within(to, "internal/profile") {
		return true
	}
	for _, layer := range []string{"state", "history", "rule", "action", "notify", "host", "plan", "store", "audit", "auth", "webui"} {
		if within(from, "internal/"+layer) && (within(to, "internal/driver") || within(to, "internal/profile") || within(to, "internal/normalize")) {
			return true
		}
	}
	return false
}

func TestPackageBoundaries(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ os, arch string }{{"windows", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		t.Run(target.os+"/"+target.arch, func(t *testing.T) {
			bc := build.Default
			bc.GOOS, bc.GOARCH, bc.CgoEnabled = target.os, target.arch, false
			graph := map[string][]string{}
			for _, source := range []string{"internal", "cmd"} {
				err := filepath.WalkDir(filepath.Join(root, source), func(path string, d fs.DirEntry, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if !d.IsDir() {
						return nil
					}
					if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" {
						return filepath.SkipDir
					}
					p, err := bc.ImportDir(path, build.ImportComment)
					var noGo *build.NoGoError
					if errors.As(err, &noGo) {
						return nil
					}
					if err != nil {
						return err
					}
					rel, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					from := filepath.ToSlash(rel)
					// 仅生产 import 参与规则；测试允许引用模拟器和跨层集成。
					for _, dep := range p.Imports {
						if strings.HasPrefix(dep, "ups-warden/") {
							graph[from] = append(graph[from], strings.TrimPrefix(dep, "ups-warden/"))
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			for from := range graph {
				visited := map[string]bool{}
				var visit func(string, []string)
				visit = func(node string, path []string) {
					if visited[node] {
						return
					}
					visited[node] = true
					for _, to := range graph[node] {
						next := append(append([]string{}, path...), to)
						if forbidden(from, to) {
							t.Errorf("禁止的依赖链: %s", strings.Join(next, " -> "))
						}
						visit(to, next)
					}
				}
				visit(from, []string{from})
			}
		})
	}
}

func TestRules(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		denied   bool
	}{
		{"internal/state", "internal/driver/snmp", true},
		{"internal/history", "internal/normalize", true},
		{"internal/store", "internal/profile", true},
		{"internal/driver/ayi9", "internal/profile", true},
		{"internal/app", "internal/sim", true},
		{"cmd/upssim", "internal/sim", false},
		{"internal/api", "internal/profile", false},
		{"internal/api", "internal/collector", false},
		{"internal/collector", "internal/normalize", false},
		{"internal/state", "internal/model", false},
		{"internal/state", "internal/profileextra", false},
	} {
		if got := forbidden(tc.from, tc.to); got != tc.denied {
			t.Errorf("%s -> %s: %v", tc.from, tc.to, got)
		}
	}
}
