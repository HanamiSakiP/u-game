package player

import (
	"os"
	"testing"
	"time"
)

func TestMPVIntegration(t *testing.T) {
	dir := os.Getenv("UGAL_TEST_MPV_DIR")
	if dir == "" {
		t.Skip("设置 UGAL_TEST_MPV_DIR 后运行 mpv 集成测试")
	}
	ctrl, err := Start(Options{Dir: dir, Player: "mpv --ao=null"}, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ctrl.Stop()

	time.Sleep(400 * time.Millisecond)
	ctrl.Refresh()
	index, paused, playing := ctrl.State()
	if index != 0 || paused || !playing {
		t.Fatalf("初始状态 = %d %v %v", index, paused, playing)
	}
	if paused, err := ctrl.TogglePause(); err != nil || !paused {
		t.Fatalf("暂停失败: %v %v", paused, err)
	}
	if paused, err := ctrl.TogglePause(); err != nil || paused {
		t.Fatalf("继续失败: %v %v", paused, err)
	}
	if index, err := ctrl.Next(); err != nil || index != 1 {
		t.Fatalf("下一首 = %d %v", index, err)
	}
	time.Sleep(200 * time.Millisecond)
	ctrl.Refresh()
	if index, _, _ := ctrl.State(); index != 1 {
		t.Fatalf("Refresh 后索引 = %d", index)
	}
	if index, err := ctrl.Select(2); err != nil || index != 2 {
		t.Fatalf("选择第三首 = %d %v", index, err)
	}
}
