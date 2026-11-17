// Package ugal 提供内嵌的 ugal 源码，并支持把源码释放到本地目录。
//
// 源码在构建时通过 go:embed 打包进二进制；运行任意 ugal 命令时，
// cmd/ugal 会把源码写入 ./.ugal/src（已存在则不覆盖），方便离线查看、
// 修改和本地构建。更新远程版本仍使用 `ugal update`。
package ugal

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:cmd all:internal go.mod README.md source.go source_test.go
var embedded embed.FS

// DefaultSourceDir 返回默认源码目录：当前工作目录下的 .ugal/src。
func DefaultSourceDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".ugal", "src"), nil
}

// Bootstrap 在需要时把内嵌源码写入 ./.ugal/src，并返回该目录。
//
// 当前工作目录本身已经是 ugal 源码目录时不做任何写入，避免在源码树里
// 再生成一层 .ugal/src。written 表示本次是否真的写入了源码。
func Bootstrap() (dir string, written bool, err error) {
	dir, err = DefaultSourceDir()
	if err != nil {
		return "", false, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return dir, false, err
	}
	if IsSourceDir(cwd) {
		return dir, false, nil
	}
	written, err = EnsureSource(dir)
	return dir, written, err
}

// IsSourceDir 判断 dir 是否是 ugal 源码目录。
func IsSourceDir(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			return mod == "github.com/HanamiSakiP/u-game"
		}
	}
	return false
}

// EnsureSource 把内嵌源码写入 dest。
//
// 如果 dest 中已经有完整的源码（go.mod 和 cmd/ugal/main.go），不覆盖并返回
// false。返回 true 表示本次写入了源码。
func EnsureSource(dest string) (bool, error) {
	if sourceExists(dest) {
		return false, nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return false, err
	}

	err := fs.WalkDir(embedded, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// sourceExists 判断 dest 里是否已经有可用的 ugal 源码。
func sourceExists(dest string) bool {
	for _, name := range []string{"go.mod", "cmd/ugal/main.go"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(name))); err != nil {
			return false
		}
	}
	return true
}
