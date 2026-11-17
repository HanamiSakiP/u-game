package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

func writeJSONOutput(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// renderTable 按终端显示宽度对齐输出表格（兼容中英文混排）。
func renderTable(w io.Writer, headers []string, right []bool, rows [][]string) {
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

	var b strings.Builder
	writeRow := func(cells []string) {
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
	}

	writeRow(headers)
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = strings.Repeat("-", widths[i])
	}
	writeRow(sep)
	for _, row := range rows {
		writeRow(row)
	}
	fmt.Fprint(w, b.String())
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
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
