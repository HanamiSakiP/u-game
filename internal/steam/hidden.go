package steam

// DefaultHiddenAppIDs 是默认隐藏的 Steam 运行库 / 工具。它们会出现在本地
// appmanifest 中，但不是用户安装的游戏，默认不写入 installed.json，
// 也不在列表和 TUI 中显示。需要保留时可以使用 --include-hidden。
var DefaultHiddenAppIDs = []int{
	228980,  // Steamworks Common Redistributables
	4183110, // Steam Linux Runtime 4.0
	1628350, // Steam Linux Runtime 3.0 (sniper)
	1391110, // Steam Linux Runtime 2.0 (soldier)
	1070560, // Steam Linux Runtime 1.0 (scout)
	1493710, // Proton Experimental
}

var defaultHiddenAppIDs = func() map[int]bool {
	out := make(map[int]bool, len(DefaultHiddenAppIDs))
	for _, id := range DefaultHiddenAppIDs {
		out[id] = true
	}
	return out
}()

// IsDefaultHiddenApp 报告 appid 是否属于默认隐藏的 Steam 运行库 / 工具。
func IsDefaultHiddenApp(appid int) bool {
	return defaultHiddenAppIDs[appid]
}
