package recall

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ApplyMarkdownPatch applies the model-facing recall patch operations in the
// same order as AgentDock: literal replacement, section replacement, append.
func ApplyMarkdownPatch(content, old, replacement, section, sectionContent, appendText string) (string, int, error) {
	current := content
	changes := 0
	operations := 0
	if old != "" {
		operations++
		count := strings.Count(current, old)
		if count == 0 {
			return "", 0, errors.New("patch old text was not found")
		}
		current = strings.ReplaceAll(current, old, replacement)
		changes += count
	}
	if strings.TrimSpace(section) != "" {
		operations++
		updated, err := replaceMarkdownSection(current, section, sectionContent)
		if err != nil {
			return "", 0, err
		}
		current = updated
		changes++
	}
	if appendText != "" {
		operations++
		current = ensureTrailingNewline(current) + appendText
		changes++
	}
	if operations == 0 {
		return "", 0, errors.New("provide old/new, section/section_content, or append")
	}
	return current, changes, nil
}

func replaceMarkdownSection(content, heading, sectionContent string) (string, error) {
	heading = strings.TrimSpace(heading)
	lines := strings.Split(content, "\n")
	headingRE := regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	start, end, level := -1, len(lines), 0
	for i, line := range lines {
		match := headingRE.FindStringSubmatch(line)
		if len(match) == 3 && strings.TrimSpace(match[2]) == heading {
			start = i
			level = len(match[1])
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("section heading was not found: %s", heading)
	}
	for i := start + 1; i < len(lines); i++ {
		match := headingRE.FindStringSubmatch(lines[i])
		if len(match) == 3 && len(match[1]) <= level {
			end = i
			break
		}
	}
	sectionLines := []string{lines[start]}
	if strings.TrimSpace(sectionContent) != "" {
		sectionLines = append(sectionLines, strings.Split(strings.Trim(sectionContent, "\n"), "\n")...)
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, sectionLines...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

// UpdateMarkdownFacts updates facts only inside the requested Markdown section
// when section is set. Missing facts are optionally inserted in that section.
func UpdateMarkdownFacts(content, section string, facts map[string]string, appendIfMissing bool) (string, []map[string]any, error) {
	if len(facts) == 0 {
		return "", nil, errors.New("provide key/value or facts")
	}
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	updated := content
	updates := make([]map[string]any, 0, len(keys))
	missing := make([]string, 0)
	for _, key := range keys {
		var found, changed bool
		updated, found, changed = updateMarkdownFactLine(updated, section, key, facts[key], appendIfMissing)
		updates = append(updates, map[string]any{"key": key, "value": facts[key], "found": found, "changed": changed})
		if !found {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 && !appendIfMissing {
		return "", nil, fmt.Errorf("facts not found: %s", strings.Join(missing, ", "))
	}
	return updated, updates, nil
}

func updateMarkdownFactLine(content, section, key, value string, appendIfMissing bool) (string, bool, bool) {
	lines := strings.Split(content, "\n")
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	headingRE := regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	inSection := section == ""
	sectionLevel := 0
	insertAfter := -1
	for i, line := range lines {
		if match := headingRE.FindStringSubmatch(line); len(match) == 3 {
			level := len(match[1])
			name := strings.TrimSpace(match[2])
			if section != "" && name == section {
				inSection = true
				sectionLevel = level
				insertAfter = i
				continue
			}
			if section != "" && inSection && level <= sectionLevel {
				inSection = false
			}
		}
		if !inSection || !lineLooksLikeFact(line, key) {
			continue
		}
		updated := replaceFactValue(line, key, value)
		lines[i] = updated
		return strings.Join(lines, "\n"), true, updated != line
	}
	if !appendIfMissing {
		return content, false, false
	}
	newLine := key + "：" + value
	if section != "" && insertAfter >= 0 {
		out := append([]string{}, lines[:insertAfter+1]...)
		out = append(out, newLine)
		out = append(out, lines[insertAfter+1:]...)
		return strings.Join(out, "\n"), true, true
	}
	return ensureTrailingNewline(content) + newLine + "\n", true, true
}

func lineLooksLikeFact(line, key string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	return strings.HasPrefix(trimmed, key+"：") || strings.HasPrefix(trimmed, key+":") || strings.HasPrefix(trimmed, key+" =") || strings.HasPrefix(trimmed, key+"=")
}

func replaceFactValue(line, key, value string) string {
	index := strings.Index(line, key)
	if index < 0 {
		return line
	}
	rest := line[index+len(key):]
	separator := "："
	if strings.HasPrefix(rest, ":") {
		separator = ":"
	} else if strings.HasPrefix(rest, " =") || strings.HasPrefix(rest, "=") {
		separator = " ="
	}
	prefix := line[:index]
	if separator == " =" {
		return prefix + key + " = " + value
	}
	return prefix + key + separator + value
}

func ensureTrailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

// UnifiedDiff returns the same compact one-hunk diff shape used by AgentDock.
func UnifiedDiff(path, oldText, newText string, maxBytes int) string {
	if oldText == newText {
		return ""
	}
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	const contextLines = 3
	start := prefix - contextLines
	if start < 0 {
		start = 0
	}
	oldEnd := min(len(oldLines), len(oldLines)-suffix+contextLines)
	newEnd := min(len(newLines), len(newLines)-suffix+contextLines)
	var builder strings.Builder
	builder.WriteString("--- " + path + "\n")
	builder.WriteString("+++ " + path + "\n")
	builder.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", start+1, oldEnd-start, start+1, newEnd-start))
	for i := start; i < prefix && i < len(oldLines); i++ {
		builder.WriteString(" " + oldLines[i] + "\n")
	}
	for i := prefix; i < len(oldLines)-suffix; i++ {
		builder.WriteString("-" + oldLines[i] + "\n")
		if maxBytes > 0 && builder.Len() >= maxBytes {
			return truncateUTF8(builder.String(), maxBytes)
		}
	}
	for i := prefix; i < len(newLines)-suffix; i++ {
		builder.WriteString("+" + newLines[i] + "\n")
		if maxBytes > 0 && builder.Len() >= maxBytes {
			return truncateUTF8(builder.String(), maxBytes)
		}
	}
	for i := len(oldLines) - suffix; i < oldEnd && i < len(oldLines); i++ {
		builder.WriteString(" " + oldLines[i] + "\n")
	}
	out := builder.String()
	if maxBytes > 0 && len(out) > maxBytes {
		return truncateUTF8(out, maxBytes)
	}
	return out
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}
