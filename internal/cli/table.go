package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ansiReset = "\x1b[0m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
)

// isTerminal 判断输出目标是终端。
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// colorEnabled 判断输出是否适合使用 ANSI 颜色：仅在终端输出且未设置
// NO_COLOR、TERM=dumb 时启用，管道和文件重定向保持纯文本。
func colorEnabled(w io.Writer) bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	return isTerminal(w)
}

// colorize 在启用颜色时用指定 ANSI 码包裹文本。
func colorize(enabled bool, code, s string) string {
	if !enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}

func writeJSONOutput(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderTable 按终端显示宽度对齐输出表格（兼容中英文混排）。
func renderTable(w io.Writer, headers []string, right []bool, rows [][]string) {
	widths := tableWidths(headers, rows)
	var b strings.Builder
	b.WriteString(tableRow(headers, right, widths, headers))
	b.WriteString(tableRow(headers, right, widths, tableSeparator(widths)))
	for _, row := range rows {
		b.WriteString(tableRow(headers, right, widths, row))
	}
	fmt.Fprint(w, b.String())
}

// tableGroup 是分组表格中的一组：Label 是组标题，Rows 是该组的数据行。
type tableGroup struct {
	Label string
	Rows  [][]string
}

// renderGroupedTable 输出带分组标题的表格：列宽跨所有分组统一计算，
// 每组数据行前输出一行红色组标题。输出不是终端时标题自动使用纯文本。
func renderGroupedTable(w io.Writer, headers []string, right []bool, groups []tableGroup) {
	var all [][]string
	for _, g := range groups {
		all = append(all, g.Rows...)
	}
	widths := tableWidths(headers, all)

	var b strings.Builder
	b.WriteString(tableRow(headers, right, widths, headers))
	b.WriteString(tableRow(headers, right, widths, tableSeparator(widths)))
	colored := colorEnabled(w)
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(colorize(colored, ansiRed, g.Label))
		b.WriteString("\n")
		for _, row := range g.Rows {
			b.WriteString(tableRow(headers, right, widths, row))
		}
	}
	fmt.Fprint(w, b.String())
}

func tableWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = displayWidth(h)
	}
	for _, row := range rows {
		for i := 0; i < len(headers) && i < len(row); i++ {
			if w := displayWidth(row[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

func tableSeparator(widths []int) []string {
	sep := make([]string, len(widths))
	for i := range sep {
		sep[i] = strings.Repeat("-", widths[i])
	}
	return sep
}

func tableRow(headers []string, right []bool, widths []int, cells []string) string {
	var b strings.Builder
	for i := range headers {
		if i > 0 {
			b.WriteString("  ")
		}
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		pad := widths[i] - displayWidth(cell)
		if pad < 0 {
			pad = 0
		}
		if right[i] {
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString(cell)
		} else {
			b.WriteString(cell)
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// displayWidth 返回终端显示宽度；ANSI 颜色序列不占宽度。
func displayWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && !(s[i] >= 0x40 && s[i] <= 0x7e) {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w += runeWidth(r)
		i += size
	}
	return w
}

func runeWidth(r rune) int {
	if r == 0 {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == '\u200b' {
		return 0
	}
	if isWide(r) {
		return 2
	}
	return 1
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

func truncateDisplay(s string, max int) string {
	if max <= 0 || displayWidth(s) <= max {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > max-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

func humanSize(n int64) string {
	if n <= 0 {
		return "-"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
