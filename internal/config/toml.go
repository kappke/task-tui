package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type tomlValueKind uint8

const (
	tomlString tomlValueKind = iota + 1
	tomlBoolean
	tomlInteger
	tomlTable
)

type tomlValue struct {
	kind      tomlValueKind
	stringVal string
	boolean   bool
	integer   int64
	table     map[string]tomlValue
}

type tomlEntry struct {
	path  []string
	value tomlValue
}

type tomlDocument struct {
	entries  []tomlEntry
	seenKeys map[string]struct{}
	sections map[string]struct{}
}

func parseTOML(data []byte) (*tomlDocument, error) {
	document := &tomlDocument{
		seenKeys: make(map[string]struct{}),
		sections: make(map[string]struct{}),
	}
	var section []string

	lines := strings.Split(string(data), "\n")
	for lineNumber, line := range lines {
		lineNumber++
		line, err := stripComment(line)
		if err != nil {
			return nil, parseFailure(lineNumber, "invalid quoted text")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			if strings.HasPrefix(line, "[[") || !strings.HasSuffix(line, "]") {
				return nil, parseFailure(lineNumber, "array tables are not supported")
			}
			inside := strings.TrimSpace(line[1 : len(line)-1])
			parsed, err := parseKeyPath(inside)
			if err != nil || len(parsed) == 0 {
				return nil, parseFailure(lineNumber, "invalid table name")
			}
			section = parsed
			key := pathKey(section)
			if _, exists := document.sections[key]; exists {
				return nil, parseFailure(lineNumber, "duplicate table name")
			}
			document.sections[key] = struct{}{}
			continue
		}

		keyText, valueText, ok := splitAssignment(line)
		if !ok {
			return nil, parseFailure(lineNumber, "expected key and value")
		}
		keyParts, err := parseKeyPath(keyText)
		if err != nil || len(keyParts) == 0 {
			return nil, parseFailure(lineNumber, "invalid key")
		}
		fullPath := append(append([]string(nil), section...), keyParts...)
		if isRawSecretPath(fullPath) {
			return nil, fmt.Errorf("%w: line %d: %s", ErrSecretConfig, lineNumber, pathText(fullPath))
		}
		value, err := parseValue(strings.TrimSpace(valueText))
		if err != nil {
			return nil, parseFailure(lineNumber, "invalid value for "+pathText(fullPath))
		}
		key := pathKey(fullPath)
		if _, exists := document.seenKeys[key]; exists {
			return nil, parseFailure(lineNumber, "duplicate key "+pathText(fullPath))
		}
		document.seenKeys[key] = struct{}{}
		document.entries = append(document.entries, tomlEntry{path: fullPath, value: value})
	}

	return document, nil
}

func parseFailure(line int, detail string) error {
	return fmt.Errorf("%w: line %d: %s", ErrConfigParse, line, detail)
}

func pathKey(parts []string) string { return strings.Join(parts, "\x00") }

func pathText(parts []string) string { return strings.Join(parts, ".") }

func isRawSecretPath(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	return isSensitiveConfigKey(parts[len(parts)-1])
}

func stripComment(line string) (string, error) {
	var quote rune
	escaped := false
	for index, r := range line {
		if quote == '"' {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'':
			quote = r
		case '#':
			return line[:index], nil
		}
	}
	if quote != 0 || escaped {
		return "", fmt.Errorf("unterminated string")
	}
	return line, nil
}

func splitAssignment(line string) (string, string, bool) {
	var quote rune
	escaped := false
	depth := 0
	for index, r := range line {
		if quote == '"' {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'':
			quote = r
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '=':
			if depth == 0 {
				return strings.TrimSpace(line[:index]), strings.TrimSpace(line[index+1:]), true
			}
		}
	}
	return "", "", false
}

func parseKeyPath(text string) ([]string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty key")
	}
	var parts []string
	for index := 0; index < len(text); {
		for index < len(text) && unicode.IsSpace(rune(text[index])) {
			index++
		}
		if index >= len(text) {
			return nil, fmt.Errorf("missing key")
		}

		var part string
		var next int
		var err error
		switch text[index] {
		case '"', '\'':
			start := index
			part, next, err = parseQuoted(text[start:])
			next += start
		default:
			start := index
			for index < len(text) && text[index] != '.' && !unicode.IsSpace(rune(text[index])) {
				index++
			}
			part = text[start:index]
			next = index
			if part == "" {
				err = fmt.Errorf("empty key")
			}
		}
		if err != nil || strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("invalid key")
		}
		parts = append(parts, part)
		index = next
		for index < len(text) && unicode.IsSpace(rune(text[index])) {
			index++
		}
		if index == len(text) {
			break
		}
		if text[index] != '.' {
			return nil, fmt.Errorf("expected dot")
		}
		index++
		if index == len(text) {
			return nil, fmt.Errorf("missing key after dot")
		}
	}
	return parts, nil
}

func parseQuoted(text string) (string, int, error) {
	if len(text) < 2 {
		return "", 0, fmt.Errorf("unterminated string")
	}
	quote := text[0]
	if quote == '\'' {
		for index := 1; index < len(text); index++ {
			if text[index] == quote {
				return text[1:index], index + 1, nil
			}
		}
		return "", 0, fmt.Errorf("unterminated string")
	}

	escaped := false
	for index := 1; index < len(text); index++ {
		if escaped {
			escaped = false
			continue
		}
		if text[index] == '\\' {
			escaped = true
			continue
		}
		if text[index] == quote {
			value, err := strconv.Unquote(text[:index+1])
			return value, index + 1, err
		}
	}
	return "", 0, fmt.Errorf("unterminated string")
}

func parseValue(text string) (tomlValue, error) {
	if text == "" {
		return tomlValue{}, fmt.Errorf("empty value")
	}
	if text[0] == '"' || text[0] == '\'' {
		value, next, err := parseQuoted(text)
		if err != nil || strings.TrimSpace(text[next:]) != "" {
			return tomlValue{}, fmt.Errorf("invalid string")
		}
		return tomlValue{kind: tomlString, stringVal: value}, nil
	}
	if text == "true" || text == "false" {
		return tomlValue{kind: tomlBoolean, boolean: text == "true"}, nil
	}
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		table, err := parseInlineTable(strings.TrimSpace(text[1 : len(text)-1]))
		if err != nil {
			return tomlValue{}, err
		}
		return tomlValue{kind: tomlTable, table: table}, nil
	}
	integerText := strings.ReplaceAll(text, "_", "")
	if integer, err := strconv.ParseInt(integerText, 10, 64); err == nil {
		return tomlValue{kind: tomlInteger, integer: integer}, nil
	}
	return tomlValue{}, fmt.Errorf("unsupported value")
}

func parseInlineTable(text string) (map[string]tomlValue, error) {
	result := make(map[string]tomlValue)
	if strings.TrimSpace(text) == "" {
		return result, nil
	}
	for _, item := range splitComma(text) {
		key, valueText, ok := splitAssignment(strings.TrimSpace(item))
		if !ok {
			return nil, fmt.Errorf("invalid inline table")
		}
		parts, err := parseKeyPath(key)
		if err != nil || len(parts) != 1 {
			return nil, fmt.Errorf("invalid inline table key")
		}
		if isSensitiveConfigKey(parts[0]) {
			return nil, ErrSecretConfig
		}
		value, err := parseValue(strings.TrimSpace(valueText))
		if err != nil {
			return nil, err
		}
		if _, exists := result[parts[0]]; exists {
			return nil, fmt.Errorf("duplicate inline table key")
		}
		result[parts[0]] = value
	}
	return result, nil
}

func splitComma(text string) []string {
	var result []string
	start := 0
	var quote rune
	escaped := false
	depth := 0
	for index, r := range text {
		if quote == '"' {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'':
			quote = r
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				result = append(result, text[start:index])
				start = index + 1
			}
		}
	}
	result = append(result, text[start:])
	return result
}
