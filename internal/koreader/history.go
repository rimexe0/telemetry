package koreader

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// HistoryEntry is the small, non-executable subset of a KOReader history.lua
// entry that Telemetry needs. KOReader writes this file with its dump.lua
// serializer; it is parsed here instead of being executed as Lua.
type HistoryEntry struct {
	Index    int
	TimeUnix int64
	File     string
}

type luaField struct {
	key   string
	value luaValue
}

type luaValue struct {
	kind   byte
	string string
	number int64
	table  []luaField
}

type luaParser struct {
	input       []byte
	pos         int
	depth       int
	elements    int
	maxElements int
}

const (
	maxHistoryBytes = 16 << 20
	maxLuaDepth     = 64
)

func parseHistory(data []byte, maxEntries, maxElements int) ([]HistoryEntry, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("history.lua is empty")
	}
	if len(data) > maxHistoryBytes {
		return nil, fmt.Errorf("history.lua exceeds %d bytes", maxHistoryBytes)
	}
	if maxEntries <= 0 {
		return nil, fmt.Errorf("history.lua entry limit must be positive")
	}
	if maxElements <= 0 {
		return nil, fmt.Errorf("history.lua element limit must be positive")
	}
	p := luaParser{input: data, maxElements: maxElements}
	p.skipSpaceAndComments()
	if p.consumeIdentifier("return") {
		p.skipSpaceAndComments()
	}
	root, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	p.skipSpaceAndComments()
	if p.pos != len(p.input) {
		return nil, fmt.Errorf("history.lua has trailing input at byte %d", p.pos)
	}
	if root.kind != 't' {
		return nil, fmt.Errorf("history.lua root is %q, want table", root.kind)
	}

	entries := make([]HistoryEntry, 0, len(root.table))
	if len(root.table) > maxEntries {
		return nil, fmt.Errorf("history.lua contains %d entries, over limit %d", len(root.table), maxEntries)
	}
	for index, field := range root.table {
		if field.value.kind != 't' {
			return nil, fmt.Errorf("history entry %d is %q, want table", index+1, field.value.kind)
		}
		var (
			timeValue int64
			fileValue string
			hasTime   bool
			hasFile   bool
		)
		for _, entryField := range field.value.table {
			switch entryField.key {
			case "time":
				if entryField.value.kind != 'n' {
					return nil, fmt.Errorf("history entry %d time is %q, want number", index+1, entryField.value.kind)
				}
				timeValue, hasTime = entryField.value.number, true
			case "file":
				if entryField.value.kind != 's' {
					return nil, fmt.Errorf("history entry %d file is %q, want string", index+1, entryField.value.kind)
				}
				fileValue, hasFile = entryField.value.string, true
			}
		}
		if !hasTime || !hasFile {
			return nil, fmt.Errorf("history entry %d lacks time or file", index+1)
		}
		if timeValue <= 0 {
			return nil, fmt.Errorf("history entry %d has invalid Unix time %d", index+1, timeValue)
		}
		if fileValue == "" {
			return nil, fmt.Errorf("history entry %d has an empty file", index+1)
		}
		entries = append(entries, HistoryEntry{Index: index + 1, TimeUnix: timeValue, File: fileValue})
	}
	return entries, nil
}

func (p *luaParser) parseValue() (luaValue, error) {
	p.skipSpaceAndComments()
	if p.pos >= len(p.input) {
		return luaValue{}, fmt.Errorf("history.lua ends while reading a value")
	}
	switch p.input[p.pos] {
	case '{':
		return p.parseTable()
	case '"', '\'':
		value, err := p.parseString()
		if err != nil {
			return luaValue{}, err
		}
		return luaValue{kind: 's', string: value}, nil
	default:
		if isNumberStart(p.input[p.pos]) {
			value, err := p.parseNumber()
			if err != nil {
				return luaValue{}, err
			}
			return luaValue{kind: 'n', number: value}, nil
		}
		identifier := p.parseIdentifier()
		switch identifier {
		case "true", "false", "nil":
			return luaValue{kind: 'b'}, nil
		default:
			if identifier == "" {
				return luaValue{}, fmt.Errorf("history.lua has unexpected byte %q at %d", p.input[p.pos], p.pos)
			}
			return luaValue{}, fmt.Errorf("history.lua contains unsupported identifier %q", identifier)
		}
	}
}

func (p *luaParser) parseTable() (luaValue, error) {
	if p.depth >= maxLuaDepth {
		return luaValue{}, fmt.Errorf("history.lua exceeds nesting depth %d", maxLuaDepth)
	}
	p.pos++ // {
	p.depth++
	defer func() { p.depth-- }()
	fields := make([]luaField, 0)
	for {
		p.skipSpaceAndComments()
		if p.pos >= len(p.input) {
			return luaValue{}, fmt.Errorf("history.lua table is not closed")
		}
		if p.input[p.pos] == '}' {
			p.pos++
			return luaValue{kind: 't', table: fields}, nil
		}
		key, err := p.parseKey()
		if err != nil {
			return luaValue{}, err
		}
		p.skipSpaceAndComments()
		if !p.consume('=') {
			return luaValue{}, fmt.Errorf("history.lua expects '=' after table key at byte %d", p.pos)
		}
		value, err := p.parseValue()
		if err != nil {
			return luaValue{}, err
		}
		fields = append(fields, luaField{key: key, value: value})
		p.elements++
		if p.elements > p.maxElements {
			return luaValue{}, fmt.Errorf("history.lua exceeds %d elements", p.maxElements)
		}
		p.skipSpaceAndComments()
		if p.pos < len(p.input) && (p.input[p.pos] == ',' || p.input[p.pos] == ';') {
			p.pos++
		}
	}
}

func (p *luaParser) parseKey() (string, error) {
	p.skipSpaceAndComments()
	if p.consume('[') {
		p.skipSpaceAndComments()
		var key string
		if p.pos < len(p.input) && (p.input[p.pos] == '"' || p.input[p.pos] == '\'') {
			value, err := p.parseString()
			if err != nil {
				return "", err
			}
			key = value
		} else {
			value, err := p.parseNumber()
			if err != nil {
				return "", err
			}
			key = strconv.FormatInt(value, 10)
		}
		p.skipSpaceAndComments()
		if !p.consume(']') {
			return "", fmt.Errorf("history.lua expects ']' after table key at byte %d", p.pos)
		}
		return key, nil
	}
	key := p.parseIdentifier()
	if key == "" {
		return "", fmt.Errorf("history.lua has invalid table key at byte %d", p.pos)
	}
	return key, nil
}

func (p *luaParser) parseNumber() (int64, error) {
	start := p.pos
	if p.pos < len(p.input) && (p.input[p.pos] == '-' || p.input[p.pos] == '+') {
		p.pos++
	}
	digits := p.pos
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
	}
	if digits == p.pos {
		return 0, fmt.Errorf("history.lua expects an integer at byte %d", start)
	}
	value, err := strconv.ParseInt(string(p.input[start:p.pos]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("history.lua number at byte %d: %w", start, err)
	}
	return value, nil
}

func (p *luaParser) parseString() (string, error) {
	quote := p.input[p.pos]
	p.pos++
	var out []byte
	for p.pos < len(p.input) {
		value := p.input[p.pos]
		p.pos++
		if value == quote {
			if !utf8.Valid(out) {
				return "", fmt.Errorf("history.lua string is not valid UTF-8")
			}
			return string(out), nil
		}
		if value != '\\' {
			out = append(out, value)
			continue
		}
		if p.pos >= len(p.input) {
			return "", fmt.Errorf("history.lua string ends after escape")
		}
		escaped := p.input[p.pos]
		p.pos++
		switch escaped {
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '\\', '"', '\'':
			out = append(out, escaped)
		case '\n':
			// Lua permits a backslash followed by a physical newline.
			if p.pos < len(p.input) && p.input[p.pos] == '\r' {
				p.pos++
			}
		case 'z':
			for p.pos < len(p.input) && strings.ContainsRune(" \t\r\n\f\v", rune(p.input[p.pos])) {
				p.pos++
			}
		case 'x':
			if p.pos+2 > len(p.input) {
				return "", fmt.Errorf("history.lua hex escape is incomplete")
			}
			parsed, err := strconv.ParseUint(string(p.input[p.pos:p.pos+2]), 16, 8)
			if err != nil {
				return "", fmt.Errorf("history.lua hex escape: %w", err)
			}
			out = append(out, byte(parsed))
			p.pos += 2
		default:
			if escaped >= '0' && escaped <= '9' {
				digits := []byte{escaped}
				for len(digits) < 3 && p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
					digits = append(digits, p.input[p.pos])
					p.pos++
				}
				parsed, err := strconv.ParseUint(string(digits), 10, 8)
				if err != nil {
					return "", fmt.Errorf("history.lua decimal escape: %w", err)
				}
				out = append(out, byte(parsed))
				continue
			}
			return "", fmt.Errorf("history.lua contains unsupported string escape \\%c", escaped)
		}
	}
	return "", fmt.Errorf("history.lua string is not closed")
}

func (p *luaParser) parseIdentifier() string {
	start := p.pos
	for p.pos < len(p.input) {
		value := p.input[p.pos]
		if !((value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || value == '_') {
			break
		}
		p.pos++
	}
	return string(p.input[start:p.pos])
}

func (p *luaParser) consumeIdentifier(expected string) bool {
	start := p.pos
	if p.parseIdentifier() == expected {
		return true
	}
	p.pos = start
	return false
}

func (p *luaParser) consume(expected byte) bool {
	if p.pos < len(p.input) && p.input[p.pos] == expected {
		p.pos++
		return true
	}
	return false
}

func (p *luaParser) skipSpaceAndComments() {
	for p.pos < len(p.input) {
		if strings.ContainsRune(" \t\r\n\f\v", rune(p.input[p.pos])) {
			p.pos++
			continue
		}
		if p.pos+1 >= len(p.input) || p.input[p.pos] != '-' || p.input[p.pos+1] != '-' {
			return
		}
		p.pos += 2
		if p.pos+1 < len(p.input) && p.input[p.pos] == '[' && p.input[p.pos+1] == '[' {
			p.pos += 2
			for p.pos+1 < len(p.input) && !(p.input[p.pos] == ']' && p.input[p.pos+1] == ']') {
				p.pos++
			}
			if p.pos+1 >= len(p.input) {
				p.pos = len(p.input)
				return
			}
			p.pos += 2
			continue
		}
		for p.pos < len(p.input) && p.input[p.pos] != '\n' {
			p.pos++
		}
	}
}

func isNumberStart(value byte) bool {
	return (value >= '0' && value <= '9') || value == '-' || value == '+'
}
