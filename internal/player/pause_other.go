//go:build !unix

package player

// pauseProcess 在不支持 Unix 信号的平台上不可用。
func pauseProcess(pid int, pause bool) error {
	return ErrPauseUnsupported
}
