package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"agent-platform/pkg/errors"
)

// SkillLimits 技能包导入限制 (M9, PRD 2.5.1)
type SkillLimits struct {
	MaxPackageSize int64    // 包总大小 (解压后)
	MaxFileCount   int      // 文件数 (含 SKILL.md)
	MaxFileSize    int64    // 单文件大小
	MaxEntrySize   int64    // SKILL.md 指令正文大小
	AllowedExt     []string // 资源文件类型白名单
}

// DefaultSkillLimits 平台默认限制
func DefaultSkillLimits() SkillLimits {
	return SkillLimits{
		MaxPackageSize: 10 * 1024 * 1024,
		MaxFileCount:   500,
		MaxFileSize:    2 * 1024 * 1024,
		MaxEntrySize:   200 * 1024,
		AllowedExt: []string{
			"md", "markdown", "txt", "rst", "json", "yaml", "yml", "toml", "csv", "tsv",
			"html", "css", "py", "js", "ts", "sh", "bat", "ps1", "sql", "xml",
			"png", "jpg", "jpeg", "gif", "svg", "pdf",
		},
	}
}

// ParsedSkillFile 包内解析出的资源文件 (SKILL.md 除外)
type ParsedSkillFile struct {
	Path   string
	Size   int64
	Sha256 string
	Data   []byte
}

// ParsedSkillPackage 解析并校验通过的技能包
type ParsedSkillPackage struct {
	Name          string
	VersionSpec   string
	Description   string
	Author        string
	Tags          []string
	RequiredTools []string
	EntryContent  string
	Files         []ParsedSkillFile
	SizeBytes     int64 // 全部文件 (含 SKILL.md) 总字节
}

// skillNameRe 技能名规则 (PRD 2.5.1)
var skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

const (
	skillManifestName = "SKILL.md"
	maxDescriptionLen = 512
	maxTagCount       = 10
)

type skillRawFile struct {
	path string
	data []byte
}

// parseSkillPackage 解压并校验技能包 (M9-1.2): 路径安全 -> 结构校验 -> frontmatter 解析
func parseSkillPackage(data []byte, limits SkillLimits) (*ParsedSkillPackage, error) {
	if int64(len(data)) > limits.MaxPackageSize {
		return nil, errors.NewValidationError(fmt.Sprintf("技能包超过大小上限 %dMB", limits.MaxPackageSize/1024/1024))
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.NewValidationError("技能包解压失败: 不是有效的 zip 压缩包 (仅支持标准非加密 zip)")
	}

	var files []skillRawFile
	seen := make(map[string]bool)

	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		filePath, ok := safeSkillPath(entry.Name)
		if !ok {
			return nil, errors.NewValidationError("技能包含非法文件路径: " + entry.Name)
		}
		if seen[filePath] {
			return nil, errors.NewValidationError("技能包含重复文件路径: " + filePath)
		}
		seen[filePath] = true

		rc, err := entry.Open()
		if err != nil {
			return nil, errors.NewValidationError("技能包文件读取失败 (" + filePath + "): " + err.Error())
		}
		content, err := io.ReadAll(io.LimitReader(rc, limits.MaxFileSize+1))
		rc.Close()
		if err != nil {
			return nil, errors.NewValidationError("技能包文件读取失败 (" + filePath + "): " + err.Error())
		}
		if int64(len(content)) > limits.MaxFileSize {
			return nil, errors.NewValidationError(fmt.Sprintf("文件 %s 超过单文件大小上限 %dMB", filePath, limits.MaxFileSize/1024/1024))
		}
		files = append(files, skillRawFile{path: filePath, data: content})
	}

	if len(files) > limits.MaxFileCount {
		return nil, errors.NewValidationError(fmt.Sprintf("技能包文件数 %d 超过上限 %d", len(files), limits.MaxFileCount))
	}

	// 接受 "单一顶层目录内含 SKILL.md" 结构: 统一剥离公共顶层目录
	paths := make([]string, len(files))
	for i := range files {
		paths[i] = files[i].path
	}
	if stripPrefix := commonTopLevelDir(paths); stripPrefix != "" {
		for i := range files {
			files[i].path = strings.TrimPrefix(files[i].path, stripPrefix)
		}
	}

	// 定位 SKILL.md
	var manifest *skillRawFile
	for i := range files {
		if files[i].path == skillManifestName {
			manifest = &files[i]
			break
		}
	}
	if manifest == nil {
		return nil, errors.NewValidationError("技能包缺少 SKILL.md (须位于包根目录或单一顶层目录内)")
	}
	if int64(len(manifest.data)) > limits.MaxEntrySize {
		return nil, errors.NewValidationError(fmt.Sprintf("SKILL.md 超过大小上限 %dKB", limits.MaxEntrySize/1024))
	}

	meta, lists, body, err := parseSkillFrontmatter(string(manifest.data))
	if err != nil {
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.NewValidationError("SKILL.md 指令正文为空 (frontmatter 之后须有 Markdown 内容)")
	}

	name := meta["name"]
	if name == "" {
		return nil, errors.NewValidationError("SKILL.md frontmatter 缺少必填字段 name")
	}
	if !skillNameRe.MatchString(name) {
		return nil, errors.NewValidationError("技能名不合法: 仅允许小写字母/数字/中划线, 2-64 位, 如 my-skill")
	}
	description := strings.TrimSpace(meta["description"])
	if description == "" {
		return nil, errors.NewValidationError("SKILL.md frontmatter 缺少必填字段 description")
	}
	if len([]rune(description)) > maxDescriptionLen {
		return nil, errors.NewValidationError(fmt.Sprintf("description 超过 %d 字符上限", maxDescriptionLen))
	}
	versionSpec := strings.TrimSpace(meta["version"])
	if versionSpec == "" {
		versionSpec = "1.0.0"
	}
	if len(versionSpec) > 32 {
		return nil, errors.NewValidationError("version 字段超过 32 字符上限")
	}
	tags := dedupeStrings(lists["tags"])
	if len(tags) > maxTagCount {
		return nil, errors.NewValidationError(fmt.Sprintf("tags 超过 %d 个上限", maxTagCount))
	}
	requiredTools := dedupeStrings(lists["required_tools"])

	parsed := &ParsedSkillPackage{
		Name:          name,
		VersionSpec:   versionSpec,
		Description:   description,
		Author:        strings.TrimSpace(meta["author"]),
		Tags:          tags,
		RequiredTools: requiredTools,
		EntryContent:  body,
	}
	for _, f := range files {
		parsed.SizeBytes += int64(len(f.data))
		if f.path == skillManifestName {
			continue
		}
		if ext := path.Ext(f.path); ext != "" {
			ext = strings.TrimPrefix(ext, ".")
			if !containsString(limits.AllowedExt, ext) {
				return nil, errors.NewValidationError(fmt.Sprintf("文件 %s 类型不在白名单内 (允许: %s)", f.path, strings.Join(limits.AllowedExt, "/")))
			}
		}
		sum := sha256.Sum256(f.data)
		parsed.Files = append(parsed.Files, ParsedSkillFile{
			Path:   f.path,
			Size:   int64(len(f.data)),
			Sha256: hex.EncodeToString(sum[:]),
			Data:   f.data,
		})
	}
	sort.Slice(parsed.Files, func(i, j int) bool { return parsed.Files[i].Path < parsed.Files[j].Path })
	return parsed, nil
}

// safeSkillPath 路径安全校验 (防 zip-slip): 拒绝绝对路径 / .. / 空字节, 返回清理后的相对路径
func safeSkillPath(raw string) (string, bool) {
	if strings.ContainsRune(raw, 0) {
		return "", false
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if normalized == "" || strings.HasPrefix(normalized, "/") {
		return "", false
	}
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// commonTopLevelDir 全部文件位于同一顶层目录下时返回该目录前缀 (含尾 /), 否则返回空
func commonTopLevelDir(paths []string) string {
	prefix := ""
	first := true
	for _, p := range paths {
		idx := strings.Index(p, "/")
		if idx < 0 {
			return ""
		}
		top := p[:idx]
		if first {
			prefix = top + "/"
			first = false
		} else if top != prefix[:len(prefix)-1] {
			return ""
		}
	}
	if first {
		return ""
	}
	return prefix
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	return out
}

func containsString(items []string, target string) bool {
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
}

// parseSkillFrontmatter 解析 SKILL.md 头部 YAML frontmatter (扁平键值 + 简单列表子集, 不引第三方依赖)。
// 支持: key: value / key: [a, b] / key: 换行后 "- item" 列表 / 注释与空行。返回 (标量, 列表, 正文)
func parseSkillFrontmatter(raw string) (map[string]string, map[string][]string, string, error) {
	if strings.HasPrefix(raw, "\ufeff") {
		raw = raw[len("\ufeff"):]
	}
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \r") != "---" {
		return nil, nil, "", errors.NewValidationError("SKILL.md 须以 YAML frontmatter 开头 (--- ... ---)")
	}
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \r"); t == "---" || t == "..." {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return nil, nil, "", errors.NewValidationError("SKILL.md frontmatter 未闭合 (缺少结尾 ---)")
	}
	body := strings.Join(lines[closeIdx+1:], "\n")

	scalars := make(map[string]string)
	lists := make(map[string][]string)
	var currentListKey string
	for i := 1; i < closeIdx; i++ {
		line := strings.TrimRight(lines[i], " \r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			if currentListKey == "" {
				return nil, nil, "", errors.NewValidationError(fmt.Sprintf("SKILL.md frontmatter 格式错误 (第 %d 行): 列表项前须有键", i+1))
			}
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if item != "" {
				lists[currentListKey] = append(lists[currentListKey], unquoteYAMLValue(item))
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, nil, "", errors.NewValidationError(fmt.Sprintf("SKILL.md frontmatter 格式错误 (第 %d 行): %s", i+1, trimmed))
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, nil, "", errors.NewValidationError(fmt.Sprintf("SKILL.md frontmatter 格式错误 (第 %d 行): 缺少键名", i+1))
		}
		value = strings.TrimSpace(value)
		currentListKey = ""
		if value == "" {
			// 块列表起始
			currentListKey = key
			lists[key] = nil
			continue
		}
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			// 行内列表 [a, b]
			inner := strings.TrimSpace(value[1 : len(value)-1])
			if inner != "" {
				for _, part := range strings.Split(inner, ",") {
					part = unquoteYAMLValue(strings.TrimSpace(part))
					if part != "" {
						lists[key] = append(lists[key], part)
					}
				}
			}
			continue
		}
		// 标量: 去掉行内注释 (空白后的 #)
		if idx := strings.Index(value, " #"); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		scalars[key] = unquoteYAMLValue(value)
	}
	return scalars, lists, body, nil
}

// unquoteYAMLValue 去除 YAML 值的成对引号
func unquoteYAMLValue(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
