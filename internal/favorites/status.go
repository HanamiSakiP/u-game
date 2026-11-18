package favorites

import "strings"

// 收藏状态预设值。收藏状态本身是自由文本，这里只把常见的英文/简写
// 归一到预设文案，其他自定义状态保持原样。
const (
	StatusWant     = "想玩"
	StatusPlaying  = "在玩"
	StatusFinished = "已通关"
	StatusOnHold   = "搁置"
)

// StatusValues 返回预设的收藏状态，按展示顺序排列。
func StatusValues() []string {
	return []string{StatusWant, StatusPlaying, StatusFinished, StatusOnHold}
}

// statusAliases 把常见写法归一到预设状态。
var statusAliases = map[string]string{
	// 想玩
	"想玩": StatusWant, "待玩": StatusWant,
	"want": StatusWant, "wanted": StatusWant, "wish": StatusWant,
	"wishlist": StatusWant, "plan": StatusWant, "planned": StatusWant,
	"todo": StatusWant, "backlog": StatusWant,
	// 在玩
	"在玩": StatusPlaying, "正在玩": StatusPlaying, "游玩中": StatusPlaying, "进行中": StatusPlaying,
	"play": StatusPlaying, "playing": StatusPlaying, "current": StatusPlaying,
	"inprogress": StatusPlaying, "in-progress": StatusPlaying, "in_progress": StatusPlaying,
	// 已通关
	"已通关": StatusFinished, "通关": StatusFinished, "已玩": StatusFinished, "玩完": StatusFinished,
	"finished": StatusFinished, "done": StatusFinished, "complete": StatusFinished,
	"completed": StatusFinished, "cleared": StatusFinished, "beat": StatusFinished,
	// 搁置
	"搁置": StatusOnHold, "暂停": StatusOnHold, "弃坑": StatusOnHold, "放置": StatusOnHold,
	"onhold": StatusOnHold, "on-hold": StatusOnHold, "on_hold": StatusOnHold,
	"hold": StatusOnHold, "paused": StatusOnHold, "dropped": StatusOnHold,
}

// clearStatusAliases 表示清除收藏状态。
var clearStatusAliases = map[string]bool{
	"-": true, "无": true, "没有": true, "未标记": true, "清除": true, "清空": true,
	"none": true, "clear": true, "unset": true, "off": true,
}

// NormalizeStatus 归一化收藏状态：空值或清除写法返回空字符串，
// 常见写法转为预设文案，其他自定义状态保留原文。
func NormalizeStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	key := strings.ToLower(status)
	if clearStatusAliases[key] {
		return ""
	}
	if canonical, ok := statusAliases[key]; ok {
		return canonical
	}
	return status
}

// IsClearStatus 判断输入是否表示清除收藏状态。
func IsClearStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status != "" && clearStatusAliases[status]
}

// StatusLabel 返回展示用收藏状态，未标记时返回「未标记」。
func StatusLabel(status string) string {
	if s := NormalizeStatus(status); s != "" {
		return s
	}
	return "未标记"
}
