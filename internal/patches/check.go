package patches

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-game/internal/model"
)

// CheckStatus 表示本地补丁文件的校验结果。
type CheckStatus string

const (
	// CheckOK 表示文件存在且哈希与记录一致。
	CheckOK CheckStatus = "ok"
	// CheckMissing 表示记录对应的文件在补丁目录中不存在。
	CheckMissing CheckStatus = "missing"
	// CheckMismatch 表示文件存在，但哈希与记录不一致。
	CheckMismatch CheckStatus = "mismatch"
	// CheckUnverified 表示记录了哈希格式不受支持、文件无法读取等无法校验的情况。
	CheckUnverified CheckStatus = "unverified"
)

// CheckResult 是单条补丁记录的本地校验结果。
type CheckResult struct {
	AppID        int
	Name         string
	Path         string
	ExpectedHash string
	ActualHash   string
	Status       CheckStatus
	Err          string
}

// CheckReport 汇总一次补丁目录校验的结果。
type CheckReport struct {
	Dir        string
	Total      int
	OK         int
	Missing    int
	Mismatch   int
	Unverified int
	Results    []CheckResult
}

// ProgressFunc 在校验每条补丁记录前被调用，index 从 1 开始。
type ProgressFunc func(index, total int, name string)

// CheckDir 在 dir 中递归查找补丁记录对应的文件，并校验文件哈希。
//
// 文件按名称（不区分大小写）与记录的 Name 匹配；同名文件有多个时，
// 只要任意一个的哈希与记录一致就视为通过。dir 不存在时不会报错，
// 有名称的记录都会标记为缺失。
func CheckDir(data *model.PatchesData, dir string) *CheckReport {
	return CheckDirProgress(data, dir, nil)
}

// CheckDirProgress 与 CheckDir 相同，并在校验每条记录前调用 progress。
func CheckDirProgress(data *model.PatchesData, dir string, progress ProgressFunc) *CheckReport {
	report := &CheckReport{Dir: dir}
	if data == nil {
		return report
	}

	index := indexPatchFiles(dir)
	total := len(data.Patches)
	for i, p := range data.Patches {
		if progress != nil {
			progress(i+1, total, p.Name)
		}
		result := checkPatch(p, index)
		report.Results = append(report.Results, result)
		report.Total++
		switch result.Status {
		case CheckOK:
			report.OK++
		case CheckMismatch:
			report.Mismatch++
		case CheckUnverified:
			report.Unverified++
		default:
			report.Missing++
		}
	}
	return report
}

// indexPatchFiles 递归收集目录下的文件，按小写文件名分组。
func indexPatchFiles(dir string) map[string][]string {
	index := make(map[string][]string)
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return index
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		key := strings.ToLower(strings.TrimSpace(d.Name()))
		if key == "" {
			return nil
		}
		index[key] = append(index[key], path)
		return nil
	})
	for key := range index {
		sort.Strings(index[key])
	}
	return index
}

func checkPatch(p model.Patch, index map[string][]string) CheckResult {
	result := CheckResult{
		AppID:        p.AppID,
		Name:         p.Name,
		ExpectedHash: NormalizeHash(p.Hash),
	}
	name := strings.ToLower(strings.TrimSpace(p.Name))
	if name == "" {
		result.Status = CheckUnverified
		result.Err = "补丁记录没有名称"
		return result
	}
	candidates := index[name]
	if len(candidates) == 0 {
		result.Status = CheckMissing
		return result
	}
	candidates = preferAppDir(candidates, p.AppID)

	algorithm := hashAlgorithm(result.ExpectedHash)
	if algorithm == "" {
		result.Path = candidates[0]
		result.Status = CheckUnverified
		if result.ExpectedHash == "" {
			result.Err = "补丁记录没有哈希值"
		} else {
			result.Err = "无法识别哈希算法（支持 MD5 32 位、SHA-256 64 位）"
		}
		return result
	}

	var firstErr string
	for _, path := range candidates {
		actual, err := HashFileWith(path, algorithm)
		if err != nil {
			if firstErr == "" {
				firstErr = err.Error()
				result.Path = path
			}
			continue
		}
		result.ActualHash = actual
		result.Path = path
		if actual == result.ExpectedHash {
			result.Status = CheckOK
			return result
		}
	}
	if firstErr != "" && result.ActualHash == "" {
		result.Status = CheckUnverified
		result.Err = firstErr
		return result
	}
	result.Status = CheckMismatch
	return result
}

func hashAlgorithm(expected string) string {
	switch len(expected) {
	case 64:
		return "sha256"
	case 32:
		return "md5"
	default:
		return ""
	}
}

// preferAppDir 在补丁按 appid 分子目录存放时，只使用该 appid 目录下的文件，
// 避免同一个文件名在不同游戏之间互相干扰。没有 appid 目录时保留全部候选。
func preferAppDir(candidates []string, appid int) []string {
	if appid <= 0 || len(candidates) == 0 {
		return candidates
	}
	id := strconv.Itoa(appid)
	preferred := make([]string, 0, len(candidates))
	for _, path := range candidates {
		dir := filepath.ToSlash(filepath.Dir(path))
		for _, part := range strings.Split(dir, "/") {
			if part == id {
				preferred = append(preferred, path)
				break
			}
		}
	}
	if len(preferred) > 0 {
		return preferred
	}
	return candidates
}

// Label 返回校验结果的中文状态文案。
func (s CheckStatus) Label() string {
	switch s {
	case CheckOK:
		return "已找到"
	case CheckMismatch:
		return "哈希错误"
	case CheckUnverified:
		return "无法校验"
	default:
		return "未找到"
	}
}

// FileIndex 是补丁目录的文件名索引，便于一次扫描后查找多个补丁文件。
type FileIndex struct {
	files map[string][]string
}

// IndexDir 递归扫描 dir 并建立补丁文件名索引。
func IndexDir(dir string) *FileIndex {
	return &FileIndex{files: indexPatchFiles(dir)}
}

// Find 按文件名（不区分大小写）查找补丁文件。appid 大于 0 时优先返回该
// appid 子目录下的文件。找不到时返回空字符串。
func (idx *FileIndex) Find(name string, appid int) string {
	if idx == nil {
		return ""
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	candidates := idx.files[name]
	if len(candidates) == 0 {
		return ""
	}
	candidates = preferAppDir(candidates, appid)
	return candidates[0]
}

// FindFile 在 dir 中按文件名（不区分大小写）查找补丁文件。appid 大于 0 时
// 优先返回该 appid 子目录下的文件。找不到时返回空字符串。
// 需要连续查找多个文件时，先用 IndexDir 建立索引更高效。
func FindFile(dir, name string, appid int) string {
	return IndexDir(dir).Find(name, appid)
}

// FindFiles 返回 dir 中所有同名补丁文件。
func FindFiles(dir, name string, appid int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	candidates := indexPatchFiles(dir)[name]
	if len(candidates) == 0 {
		return nil
	}
	return preferAppDir(candidates, appid)
}
