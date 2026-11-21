//go:build unix

package player

import (
	"fmt"
	"syscall"
)

// pauseProcess 用 SIGSTOP / SIGCONT 暂停和继续普通播放器进程。
func pauseProcess(pid int, pause bool) error {
	sig := syscall.SIGCONT
	if pause {
		sig = syscall.SIGSTOP
	}
	if err := syscall.Kill(pid, sig); err != nil {
		return fmt.Errorf("控制播放器进程失败: %w", err)
	}
	return nil
}
