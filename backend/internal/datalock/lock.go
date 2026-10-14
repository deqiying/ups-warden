// Package datalock 用进程级文件锁防止多个实例共享数据目录。
package datalock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrLocked 表示另一个实例仍持有数据目录锁。
var ErrLocked = errors.New("数据目录已被占用")

// Lock 持有操作系统文件锁；文件保留在磁盘上并不代表锁仍被持有。
type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire 创建私有数据目录并非阻塞获取独占锁。
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	path := filepath.Join(dir, "ups-warden.lock")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("数据目录锁必须是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("检查数据目录锁: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开数据目录锁: %w", err)
	}
	if err := lockFile(f); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &Lock{file: f}, nil
}

// Close 释放锁但不删除锁文件，避免其他进程锁住已被替换的 inode。
// 可重复调用；进程退出时操作系统也会自动释放锁。
func (l *Lock) Close() error {
	l.once.Do(func() { l.err = errors.Join(unlockFile(l.file), l.file.Close()) })
	return l.err
}
