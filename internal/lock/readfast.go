package lock

import (
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// The lock file is written by Write and read on every shell start, so it is decoded on the hot
// path. Reading it through a general TOML decoder means building a map of the whole file and then
// copying it into the struct with reflection, which costs more than rendering every plugin. This
// reader understands just the schema Write emits and gives up on anything unfamiliar, leaving the
// general decoder as the fallback.

// lockTable is the table keys are currently being assigned into.
type lockTable int

const (
	tableRoot lockTable = iota
	tableEnv
	tablePlugin
	tableHooks
	tableTemplates
)

// fastLockParser reads lock contents into a LockedConfig, or reports that it cannot.
type fastLockParser struct {
	data   []byte
	pos    int
	locked LockedConfig
	table  lockTable
	// plugin is the index of the plugin the current table belongs to, or -1 before the first one.
	plugin int
	// The bit masks and flags below track what has been defined, because TOML rejects a table or
	// key defined twice and this reader must not accept a file the decoder would reject.
	rootFields      uint8
	pluginFields    uint16
	pluginHooksSeen bool
	envSeen         bool
	templatesSeen   bool
}

// parseLockFast reads a lock file's own schema. The bool is false when the contents use something
// this reader does not handle, which is the caller's cue to decode them as TOML instead.
func parseLockFast(data []byte) (LockedConfig, bool) {
	parser := fastLockParser{data: data, plugin: -1}
	if !parser.parse() {
		return LockedConfig{}, false
	}
	return parser.locked, true
}

func (p *fastLockParser) parse() bool {
	for {
		if !p.skipWhitespace(true) {
			return false
		}
		if p.pos == len(p.data) {
			return true
		}
		// A comment is valid TOML that this reader does not carry, so hand the file to the decoder.
		if p.data[p.pos] == '#' {
			return false
		}
		if p.data[p.pos] == '[' {
			if !p.parseTableHeader() {
				return false
			}
			continue
		}
		if !p.parseAssignment() {
			return false
		}
	}
}

// skipWhitespace advances past spaces and tabs, and past line breaks when wanted. A carriage
// return is only whitespace as part of a line ending, since TOML rejects a bare one.
func (p *fastLockParser) skipWhitespace(lineBreaks bool) bool {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t':
			p.pos++
		case '\n':
			if !lineBreaks {
				return true
			}
			p.pos++
		case '\r':
			if p.pos+1 >= len(p.data) || p.data[p.pos+1] != '\n' {
				return false
			}
			if !lineBreaks {
				return true
			}
			p.pos += 2
		default:
			return true
		}
	}
	return true
}

// endLine consumes the rest of the line, which may hold nothing but spaces.
func (p *fastLockParser) endLine() bool {
	if !p.skipWhitespace(false) {
		return false
	}
	if p.pos == len(p.data) {
		return true
	}
	if p.data[p.pos] != '\n' {
		return false
	}
	p.pos++
	return true
}

// parseTableHeader handles both "[table]" and "[[array.of.tables]]" headers.
func (p *fastLockParser) parseTableHeader() bool {
	p.pos++ // '['
	arrayOfTables := false
	if p.pos < len(p.data) && p.data[p.pos] == '[' {
		arrayOfTables = true
		p.pos++
	}
	name, parts, ok := p.parseKey()
	if !ok || !p.skipWhitespace(false) {
		return false
	}
	if p.pos == len(p.data) || p.data[p.pos] != ']' {
		return false
	}
	p.pos++
	if arrayOfTables {
		if p.pos == len(p.data) || p.data[p.pos] != ']' {
			return false
		}
		p.pos++
	}
	if !p.endLine() {
		return false
	}
	if arrayOfTables {
		if name != "plugins" || parts != 1 {
			return false
		}
		p.locked.Plugins = append(p.locked.Plugins, LockedPlugin{})
		p.plugin = len(p.locked.Plugins) - 1
		p.table = tablePlugin
		p.pluginFields = 0
		p.pluginHooksSeen = false
		return true
	}
	switch name {
	case "env":
		if parts != 1 || p.envSeen {
			return false
		}
		p.table = tableEnv
		p.envSeen = true
		if p.locked.Env == nil {
			p.locked.Env = map[string]string{}
		}
	case "plugins":
		// The encoder writes the array of tables form, so this is not a lock file it wrote.
		return false
	case "plugins.hooks":
		// Two key parts are required: a quoted dot would name one literal key instead of a table.
		if parts != 2 || p.plugin < 0 || p.pluginHooksSeen {
			return false
		}
		p.table = tableHooks
		p.pluginHooksSeen = true
		if p.locked.Plugins[p.plugin].Hooks == nil {
			p.locked.Plugins[p.plugin].Hooks = map[string]string{}
		}
	case "templates":
		if parts != 1 || p.templatesSeen {
			return false
		}
		p.table = tableTemplates
		p.templatesSeen = true
		if p.locked.Templates == nil {
			p.locked.Templates = map[string]string{}
		}
	default:
		return false
	}
	return true
}

// parseKey reads a key, which may be bare or quoted and may have dotted parts. It returns the name
// with its parts joined, how many parts it had, and whether it read a key at all.
func (p *fastLockParser) parseKey() (string, int, bool) {
	var builder strings.Builder
	parts := 0
	for {
		if !p.skipWhitespace(false) {
			return "", 0, false
		}
		if p.pos == len(p.data) {
			return "", 0, false
		}
		if p.data[p.pos] == '"' {
			part, ok := p.parseString()
			if !ok {
				return "", 0, false
			}
			builder.WriteString(part)
		} else {
			start := p.pos
			for p.pos < len(p.data) && isBareKeyByte(p.data[p.pos]) {
				p.pos++
			}
			if p.pos == start {
				return "", 0, false
			}
			builder.Write(p.data[start:p.pos])
		}
		parts++
		if !p.skipWhitespace(false) {
			return "", 0, false
		}
		if p.pos < len(p.data) && p.data[p.pos] == '.' {
			builder.WriteByte('.')
			p.pos++
			continue
		}
		return builder.String(), parts, true
	}
}

func isBareKeyByte(character byte) bool {
	return character == '_' || character == '-' ||
		(character >= 'A' && character <= 'Z') ||
		(character >= 'a' && character <= 'z') ||
		(character >= '0' && character <= '9')
}

func (p *fastLockParser) parseAssignment() bool {
	key, parts, ok := p.parseKey()
	if !ok || !p.skipWhitespace(false) {
		return false
	}
	if p.pos == len(p.data) || p.data[p.pos] != '=' {
		return false
	}
	p.pos++
	if !p.skipWhitespace(false) || p.pos == len(p.data) {
		return false
	}
	if p.data[p.pos] == '[' {
		items, ok := p.parseArray()
		if !ok || !p.endLine() {
			return false
		}
		return p.assignList(key, parts, items)
	}
	text, ok := p.parseString()
	if !ok || !p.endLine() {
		return false
	}
	return p.assignText(key, parts, text)
}

// assignText puts a string value in the field its table and key name.
func (p *fastLockParser) assignText(key string, parts int, text string) bool {
	switch p.table {
	case tableRoot:
		bit, ok := rootFieldBit(key)
		if !ok || p.rootFields&bit != 0 {
			return false
		}
		p.rootFields |= bit
		switch key {
		case "config_fingerprint":
			p.locked.ConfigFingerprint = text
		case "profile":
			p.locked.Profile = text
		case "profile_match":
			p.locked.ProfileMatch = text
		case "shell":
			p.locked.Shell = text
		}
	case tablePlugin:
		if !p.claimPluginField(key) {
			return false
		}
		plugin := &p.locked.Plugins[p.plugin]
		switch key {
		case "name":
			plugin.Name = text
		case "inline":
			plugin.Inline = text
		case "source":
			plugin.Source = text
		case "url":
			plugin.URL = text
		case "rev":
			plugin.Rev = text
		case "directory":
			plugin.Directory = text
		default:
			// files and apply are lists, so a string here is a different value, not this field.
			return false
		}
	case tableEnv:
		if parts != 1 {
			return false
		}
		if _, exists := p.locked.Env[key]; exists {
			return false
		}
		p.locked.Env[key] = text
	case tableHooks:
		// A dotted key would nest a table where a string is expected, which the decoder rejects.
		if parts != 1 {
			return false
		}
		hooks := p.locked.Plugins[p.plugin].Hooks
		if _, exists := hooks[key]; exists {
			return false
		}
		hooks[key] = text
	case tableTemplates:
		if parts != 1 {
			return false
		}
		if _, exists := p.locked.Templates[key]; exists {
			return false
		}
		p.locked.Templates[key] = text
	default:
		return false
	}
	return true
}

// assignList puts a string list in the field its key names.
func (p *fastLockParser) assignList(key string, parts int, items []string) bool {
	if parts != 1 || p.table != tablePlugin || !p.claimPluginField(key) {
		return false
	}
	plugin := &p.locked.Plugins[p.plugin]
	switch key {
	case "files":
		plugin.Files = items
	case "apply":
		plugin.Apply = items
	default:
		return false
	}
	return true
}

// claimPluginField records that a plugin key has been defined, reporting false for a key that is
// unknown or already defined.
func (p *fastLockParser) claimPluginField(key string) bool {
	bit, ok := pluginFieldBit(key)
	if !ok || p.pluginFields&bit != 0 {
		return false
	}
	p.pluginFields |= bit
	return true
}

// rootFieldBit maps a top-level key to its seen mask, reporting false for an unknown key.
func rootFieldBit(key string) (uint8, bool) {
	switch key {
	case "config_fingerprint":
		return 1 << 0, true
	case "profile":
		return 1 << 1, true
	case "shell":
		return 1 << 2, true
	case "profile_match":
		return 1 << 3, true
	}
	return 0, false
}

// pluginFieldBit maps a plugin key to its seen mask, reporting false for an unknown key.
func pluginFieldBit(key string) (uint16, bool) {
	switch key {
	case "name":
		return 1 << 0, true
	case "inline":
		return 1 << 1, true
	case "source":
		return 1 << 2, true
	case "url":
		return 1 << 3, true
	case "rev":
		return 1 << 4, true
	case "directory":
		return 1 << 5, true
	case "files":
		return 1 << 6, true
	case "apply":
		return 1 << 7, true
	}
	return 0, false
}

// parseArray reads an array of strings, which may be broken across lines.
func (p *fastLockParser) parseArray() ([]string, bool) {
	p.pos++ // '['
	items := []string{}
	for {
		if !p.skipWhitespace(true) || p.pos == len(p.data) {
			return nil, false
		}
		if p.data[p.pos] == '#' {
			return nil, false
		}
		if p.data[p.pos] == ']' {
			p.pos++
			return items, true
		}
		item, ok := p.parseString()
		if !ok {
			return nil, false
		}
		items = append(items, item)
		if !p.skipWhitespace(true) || p.pos == len(p.data) {
			return nil, false
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return items, true
		default:
			return nil, false
		}
	}
}

// parseString reads one basic string, unescaping the escapes the encoder writes.
func (p *fastLockParser) parseString() (string, bool) {
	// A multiline string is valid TOML that the encoder never writes.
	if strings.HasPrefix(string(p.data[p.pos:min(p.pos+3, len(p.data))]), `"""`) {
		return "", false
	}
	if p.data[p.pos] != '"' {
		return "", false
	}
	p.pos++
	var builder strings.Builder
	for {
		if p.pos == len(p.data) {
			return "", false
		}
		switch p.data[p.pos] {
		case '"':
			p.pos++
			return builder.String(), true
		case '\\':
			p.pos++
			if p.pos == len(p.data) || !p.writeEscape(&builder) {
				return "", false
			}
		case '\n':
			return "", false
		default:
			start := p.pos
			for p.pos < len(p.data) && p.data[p.pos] != '"' && p.data[p.pos] != '\\' && p.data[p.pos] != '\n' {
				p.pos++
			}
			run := p.data[start:p.pos]
			// TOML rejects raw control characters and text that is not valid UTF-8.
			if !isPlainStringRun(run) {
				return "", false
			}
			builder.Write(run)
		}
	}
}

// isPlainStringRun reports whether a run of bytes is legal inside a basic string.
func isPlainStringRun(run []byte) bool {
	ascii := true
	for _, character := range run {
		// Tab is the one control character a basic string may hold unescaped.
		if (character < 0x20 && character != '\t') || character == 0x7f {
			return false
		}
		if character >= 0x80 {
			ascii = false
		}
	}
	return ascii || utf8.Valid(run)
}

// writeEscape appends the byte or rune an escape sequence stands for.
func (p *fastLockParser) writeEscape(builder *strings.Builder) bool {
	switch p.data[p.pos] {
	case 'b':
		builder.WriteByte('\b')
	case 't':
		builder.WriteByte('\t')
	case 'n':
		builder.WriteByte('\n')
	case 'f':
		builder.WriteByte('\f')
	case 'r':
		builder.WriteByte('\r')
	case '"':
		builder.WriteByte('"')
	case '\\':
		builder.WriteByte('\\')
	case 'u':
		return p.writeUnicodeEscape(builder, 4)
	case 'U':
		return p.writeUnicodeEscape(builder, 8)
	default:
		return false
	}
	p.pos++
	return true
}

func (p *fastLockParser) writeUnicodeEscape(builder *strings.Builder, digits int) bool {
	start := p.pos + 1
	if start+digits > len(p.data) {
		return false
	}
	code := p.data[start : start+digits]
	for _, character := range code {
		if !isHexByte(character) {
			return false
		}
	}
	// The digits are known to be hex here, so the only error left is a value above the max rune.
	value, err := strconv.ParseUint(string(code), 16, 32)
	if err != nil || (value >= 0xD800 && value <= 0xDFFF) {
		return false
	}
	builder.WriteRune(rune(value))
	p.pos = start + digits
	return true
}

func isHexByte(character byte) bool {
	return (character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'f') ||
		(character >= 'A' && character <= 'F')
}

// readLock decodes lock contents, preferring the schema-specific reader over the general decoder.
func readLock(contents []byte) (LockedConfig, error) {
	if locked, ok := parseLockFast(contents); ok {
		return locked, nil
	}
	var locked LockedConfig
	if err := toml.Unmarshal(contents, &locked); err != nil {
		return LockedConfig{}, err
	}
	return locked, nil
}

// readLockFile reads and decodes a lock file.
func readLockFile(path string) (LockedConfig, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return LockedConfig{}, err
	}
	return readLock(contents)
}
