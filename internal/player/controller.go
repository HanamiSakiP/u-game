package player

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	// ErrPauseUnsupported 表示当前播放器无法通过 ugal 暂停。
	ErrPauseUnsupported = errors.New("当前播放器不支持暂停")
	// ErrTrackUnsupported 表示当前播放器无法由 ugal 切换音轨。
	ErrTrackUnsupported = errors.New("当前播放器不支持切换音轨")
)

// Controller 管理一个可持续控制的播放进程，支持暂停、继续、切换音轨。
type Controller struct {
	mu sync.Mutex

	spec  Spec
	index int

	paused bool
	idle   bool
	exited bool

	cmd       *exec.Cmd
	ipc       *mpvIPC
	socketDir string
}

// Start 解析播放器并从指定音轨开始播放。
func Start(opts Options, index int) (*Controller, error) {
	spec, err := Resolve(opts)
	if err != nil {
		return nil, err
	}
	return StartSpec(spec, index)
}

// StartSpec 使用已经解析好的播放器信息开始播放。
func StartSpec(spec Spec, index int) (*Controller, error) {
	if len(spec.Files) == 0 {
		return nil, errors.New("没有可播放的音频文件")
	}
	if index < 0 {
		index = 0
	}
	if index >= len(spec.Files) {
		index = len(spec.Files) - 1
	}
	c := &Controller{spec: spec, index: index}

	if spec.Kind == KindMPV && runtime.GOOS != "windows" {
		if err := c.startMPV(); err == nil {
			return c, nil
		}
		c.cleanupMPV()
	}

	if err := c.startGenericLocked(index); err != nil {
		return nil, err
	}
	return c, nil
}

// startMPV 启动一个带 IPC 的 mpv，用播放列表加载全部音轨。
func (c *Controller) startMPV() error {
	dir, err := os.MkdirTemp("", "ugal-mpv-")
	if err != nil {
		return err
	}
	socket := filepath.Join(dir, "ipc.sock")
	if len(socket) > 100 {
		// Unix socket 路径长度有限，过长时退回普通播放模式。
		os.RemoveAll(dir)
		return errors.New("IPC socket 路径过长")
	}

	base := make([]string, 0, len(c.spec.BaseArgs)+8)
	for _, arg := range c.spec.BaseArgs {
		if arg == "--" {
			continue
		}
		base = append(base, arg)
	}
	args := append(base,
		"--idle=yes",
		"--force-window=no",
		"--pause=no",
		fmt.Sprintf("--playlist-start=%d", c.index),
		"--input-ipc-server="+socket,
		"--",
	)
	args = append(args, c.spec.Files...)

	cmd := exec.Command(c.spec.Path, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("启动 %s 失败: %w", c.spec.Name, err)
	}

	ipc, err := waitIPC(socket, 3*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		os.RemoveAll(dir)
		return fmt.Errorf("连接 %s IPC 失败: %w", c.spec.Name, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.ipc = ipc
	c.socketDir = dir
	c.paused = false
	c.idle = false
	c.exited = false
	c.mu.Unlock()
	c.watch(cmd)
	return nil
}

// startGenericLocked 用普通播放器播放单个音轨。调用方需持有锁或独占控制器。
func (c *Controller) startGenericLocked(index int) error {
	args := c.spec.argsForIndex(index)
	cmd := exec.Command(c.spec.Path, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 失败: %w", c.spec.Name, err)
	}
	c.cmd = cmd
	c.ipc = nil
	c.index = index
	c.paused = false
	c.idle = false
	c.exited = false
	c.watch(cmd)
	return nil
}

func (c *Controller) watch(cmd *exec.Cmd) {
	go func() {
		_ = cmd.Wait()
		c.mu.Lock()
		if c.cmd == cmd {
			c.exited = true
		}
		c.mu.Unlock()
	}()
}

// State 返回当前音轨下标、是否暂停、是否仍在播放。
func (c *Controller) State() (index int, paused bool, playing bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	playing = c.cmd != nil && !c.exited && !c.idle
	return c.index, c.paused, playing
}

// Files 返回播放列表中的全部音轨。
func (c *Controller) Files() []string {
	return append([]string{}, c.spec.Files...)
}

// Refresh 从 mpv 读取最新状态；普通播放器只依赖 Wait 的结果。
func (c *Controller) Refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ipc == nil || c.exited {
		return
	}
	if data, err := c.ipc.send("get_property", "playlist-pos"); err == nil {
		var pos int
		if json.Unmarshal(data, &pos) == nil && pos >= 0 {
			c.index = pos
		}
	}
	if data, err := c.ipc.send("get_property", "pause"); err == nil {
		var paused bool
		if json.Unmarshal(data, &paused) == nil {
			c.paused = paused
		}
	}
	if data, err := c.ipc.send("get_property", "idle-active"); err == nil {
		var idle bool
		if json.Unmarshal(data, &idle) == nil {
			c.idle = idle
		}
	}
}

// TogglePause 在暂停和继续之间切换，返回切换后的暂停状态。
func (c *Controller) TogglePause() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.togglePauseLocked()
}

func (c *Controller) togglePauseLocked() (bool, error) {
	if c.cmd == nil || c.exited {
		return c.paused, errors.New("没有正在播放的音乐")
	}
	target := !c.paused

	if c.ipc != nil {
		if _, err := c.ipc.send("set_property", "pause", target); err != nil {
			return c.paused, fmt.Errorf("暂停失败: %w", err)
		}
		c.paused = target
		return c.paused, nil
	}

	if c.spec.Kind == KindOpener {
		return c.paused, ErrPauseUnsupported
	}
	if err := pauseProcess(c.cmd.Process.Pid, target); err != nil {
		return c.paused, err
	}
	c.paused = target
	return c.paused, nil
}

// Next 切到下一首并返回新的下标，最后一首会回到第一首。
func (c *Controller) Next() (int, error) {
	return c.moveTrack(1)
}

// Prev 切到上一首并返回新的下标，第一首会回到最后一首。
func (c *Controller) Prev() (int, error) {
	return c.moveTrack(-1)
}

func (c *Controller) moveTrack(delta int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spec.Kind == KindOpener {
		return c.index, ErrTrackUnsupported
	}
	if len(c.spec.Files) == 0 {
		return c.index, errors.New("播放列表为空")
	}
	target := (c.index + delta) % len(c.spec.Files)
	if target < 0 {
		target += len(c.spec.Files)
	}
	return c.selectLocked(target)
}

// Select 切换到指定音轨并返回实际生效的下标。
func (c *Controller) Select(index int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spec.Kind == KindOpener {
		return c.index, ErrTrackUnsupported
	}
	if index < 0 || index >= len(c.spec.Files) {
		return c.index, fmt.Errorf("音轨下标 %d 超出范围", index)
	}
	return c.selectLocked(index)
}

func (c *Controller) selectLocked(index int) (int, error) {
	if c.ipc != nil {
		if c.exited {
			return c.index, errors.New("播放器已经退出")
		}
		if _, err := c.ipc.send("set_property", "playlist-pos", index); err != nil {
			return c.index, fmt.Errorf("切换音轨失败: %w", err)
		}
		if !c.paused {
			_, _ = c.ipc.send("set_property", "pause", false)
		}
		c.index = index
		c.idle = false
		return index, nil
	}

	if c.cmd != nil && !c.exited && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if err := c.startGenericLocked(index); err != nil {
		return c.index, err
	}
	return index, nil
}

// Stop 结束播放并清理临时文件。
func (c *Controller) Stop() {
	c.mu.Lock()
	cmd := c.cmd
	ipc := c.ipc
	dir := c.socketDir
	c.cmd = nil
	c.ipc = nil
	c.socketDir = ""
	c.exited = true
	c.mu.Unlock()

	if ipc != nil {
		_, _ = ipc.send("quit")
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (c *Controller) cleanupMPV() {
	c.mu.Lock()
	dir := c.socketDir
	c.socketDir = ""
	c.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// argsForIndex 生成只播放第 index 首时的完整参数。
func (s Spec) argsForIndex(index int) []string {
	if index < 0 || index >= len(s.Files) {
		return nil
	}
	file := s.Files[index]
	if s.Kind == KindOpener {
		return openerArgs(filepath.Base(s.Path), []string{file})
	}
	return append(append([]string{}, s.BaseArgs...), file)
}

// mpvIPC 通过 Unix socket 与 mpv 的 JSON IPC 通信。
type mpvIPC struct {
	socket string
}

type mpvResponse struct {
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

func (c *mpvIPC) send(command ...interface{}) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.socket, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))

	payload, err := json.Marshal(map[string]interface{}{"command": command})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, err
	}
	var resp mpvResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Error != "success" {
		return resp.Data, fmt.Errorf("mpv: %s", strings.TrimSpace(resp.Error))
	}
	return resp.Data, nil
}

func waitIPC(socket string, timeout time.Duration) (*mpvIPC, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return &mpvIPC{socket: socket}, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(25 * time.Millisecond)
	}
}
