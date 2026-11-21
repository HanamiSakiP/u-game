package cli

import (
	"strings"
	"testing"
)

func TestPickMusic(t *testing.T) {
	list := []entry{
		{AppID: 100, Name: "Alpha OST", Type: "music"},
		{AppID: 200, Name: "Alpha OST Deluxe", Type: "music"},
		{AppID: 300, Name: "Beta Soundtrack", Type: "music"},
	}
	if got, err := pickMusic(list, "300"); err != nil || got.AppID != 300 {
		t.Fatalf("按 APPID 选择失败: %#v %v", got, err)
	}
	if got, err := pickMusic(list, "beta"); err != nil || got.AppID != 300 {
		t.Fatalf("按名称选择失败: %#v %v", got, err)
	}
	// 名称完全匹配时优先于子串匹配。
	if got, err := pickMusic(list, "alpha ost"); err != nil || got.AppID != 100 {
		t.Fatalf("完全匹配优先失败: %#v %v", got, err)
	}
	if _, err := pickMusic(list, "alpha"); err == nil || !strings.Contains(err.Error(), "匹配到多款") {
		t.Fatalf("多个匹配应报错: %v", err)
	}
	if _, err := pickMusic(list, "404"); err == nil {
		t.Fatal("不存在的 APPID 应报错")
	}
	if _, err := pickMusic(list, "missing"); err == nil {
		t.Fatal("不存在的名称应报错")
	}
}
