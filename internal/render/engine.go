package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// valueKind tags what a templateValue holds.
type valueKind int

const (
	valueNothing valueKind = iota
	valueText
	valueNumber
	valueBool
	valueTexts
	valueMap
	valueLoop
)

// loopState backs loop.index, loop.first, and loop.last.
type loopState struct {
	index  int
	length int
}

type templateValue struct {
	kind   valueKind
	text   string
	number int
	flag   bool
	texts  []string
	dict   map[string]templateValue
	loop   loopState
}

func textValue(text string) templateValue { return templateValue{kind: valueText, text: text} }

func numberValue(number int) templateValue { return templateValue{kind: valueNumber, number: number} }

func boolValue(flag bool) templateValue { return templateValue{kind: valueBool, flag: flag} }

func textsValue(items []string) templateValue { return templateValue{kind: valueTexts, texts: items} }

func mapValue(entries map[string]string) templateValue {
	if len(entries) == 0 {
		return templateValue{kind: valueMap}
	}
	values := make(map[string]templateValue, len(entries))
	for key, value := range entries {
		values[key] = textValue(value)
	}
	return templateValue{kind: valueMap, dict: values}
}

func (value templateValue) print() (string, error) {
	switch value.kind {
	case valueNothing:
		return "", nil
	case valueText:
		return value.text, nil
	case valueNumber:
		return strconv.Itoa(value.number), nil
	case valueBool:
		return strconv.FormatBool(value.flag), nil
	default:
		return "", fmt.Errorf("lists and maps are not printable values")
	}
}

// truthy: empty values are falsy.
func (value templateValue) truthy() bool {
	switch value.kind {
	case valueNothing:
		return false
	case valueText:
		return value.text != ""
	case valueNumber:
		return value.number != 0
	case valueBool:
		return value.flag
	case valueTexts:
		return len(value.texts) > 0
	case valueMap:
		return len(value.dict) > 0
	case valueLoop:
		return value.loop.length > 0
	}
	return false
}

// scope resolves names; loop variables shadow plugin values.
type scope struct {
	plugin    PluginData
	hasPlugin bool
	parent    *scope
	// Loops bind at most two variables, so they live in the struct, not a map.
	names  [2]string
	values [2]templateValue
	count  int
	// Loop position for loop.*; files/hooks are built on first use.
	loopIndex  int
	loopLength int
	hasLoop    bool
	files      templateValue
	filesReady bool
	hooks      templateValue
	hooksReady bool
	// childScope is reused by every loop under this scope.
	childScope *scope
}

func pluginScope(data PluginData) *scope { return &scope{plugin: data, hasPlugin: true} }

func (s *scope) lookup(name string) (templateValue, bool) {
	for current := s; current != nil; current = current.parent {
		for index := 0; index < current.count; index++ {
			if current.names[index] == name {
				return current.values[index], true
			}
		}
		if current.hasPlugin {
			if value, exists := current.pluginValue(name); exists {
				return value, true
			}
		}
		if current.hasLoop && name == "loop" {
			return templateValue{kind: valueLoop, loop: loopState{index: current.loopIndex, length: current.loopLength}}, true
		}
	}
	return templateValue{}, false
}

// pluginValue converts lists and maps lazily, on first use.
func (s *scope) pluginValue(name string) (templateValue, bool) {
	switch name {
	case "name":
		return textValue(s.plugin.Name), true
	case "dir":
		return textValue(s.plugin.Directory), true
	case "file":
		return textValue(s.plugin.File), true
	case "files":
		if !s.filesReady {
			s.files, s.filesReady = textsValue(s.plugin.Files), true
		}
		return s.files, true
	case "hooks":
		if !s.hooksReady {
			s.hooks, s.hooksReady = mapValue(s.plugin.Hooks), true
		}
		return s.hooks, true
	default:
		return templateValue{}, false
	}
}

// child reuses the one child scope so loop iterations allocate nothing.
func (s *scope) child(names []string) *scope {
	child := s.childScope
	if child == nil {
		child = &scope{parent: s}
		s.childScope = child
	}
	child.count = len(names)
	copy(child.names[:], names)
	child.values[0], child.values[1] = templateValue{}, templateValue{}
	child.hasLoop = false
	return child
}

// scriptBuffer exposes the last byte written so callers can check for a trailing newline.
type scriptBuffer struct{ data []byte }

func (b *scriptBuffer) WriteString(text string) { b.data = append(b.data, text...) }

func (b *scriptBuffer) Grow(size int) { b.data = make([]byte, 0, size) }

func (b *scriptBuffer) Len() int { return len(b.data) }

func (b *scriptBuffer) Last() byte { return b.data[len(b.data)-1] }

func (b *scriptBuffer) String() string { return string(b.data) }

type node interface {
	render(*scope, *scriptBuffer) error
}

type textNode string

type expressionNode struct {
	expression string
	// plan is parsed once at compile time so renders skip the work.
	plan expressionPlan
}

type ifBranch struct {
	condition string
	body      []node
}

type ifNode struct{ branches []ifBranch }

type forNode struct {
	names    []string
	iterable string
	body     []node
	// usesLoop: body reads loop.*, so plain loops can skip it.
	usesLoop bool
}

func renderNodes(nodes []node, current *scope, output *scriptBuffer) error {
	for _, item := range nodes {
		if err := item.render(current, output); err != nil {
			return err
		}
	}
	return nil
}

func (n textNode) render(_ *scope, output *scriptBuffer) error {
	output.WriteString(string(n))
	return nil
}

func (n expressionNode) render(current *scope, output *scriptBuffer) error {
	value, err := n.plan.evaluate(n.expression, current)
	if err != nil {
		return err
	}
	text, err := value.print()
	if err != nil {
		return err
	}
	output.WriteString(text)
	return nil
}

func (n ifNode) render(current *scope, output *scriptBuffer) error {
	for _, branch := range n.branches {
		if branch.condition == "" {
			return renderNodes(branch.body, current, output)
		}
		value, err := evalExpression(branch.condition, current)
		if err != nil {
			return err
		}
		if value.truthy() {
			return renderNodes(branch.body, current, output)
		}
	}
	return nil
}

func (n forNode) render(current *scope, output *scriptBuffer) error {
	value, err := evalExpression(n.iterable, current)
	if err != nil {
		return err
	}
	switch value.kind {
	case valueTexts:
		if len(n.names) != 1 {
			return fmt.Errorf("a list loop takes one variable, got %d", len(n.names))
		}
		loop := current.child(n.names)
		loop.hasLoop, loop.loopLength = n.usesLoop, len(value.texts)
		for index, item := range value.texts {
			loop.loopIndex = index
			loop.values[0] = textValue(item)
			if err := renderNodes(n.body, loop, output); err != nil {
				return err
			}
		}
		return nil
	case valueMap:
		if len(n.names) != 2 {
			return fmt.Errorf("a map loop takes two variables, got %d", len(n.names))
		}
		keys := make([]string, 0, len(value.dict))
		for key := range value.dict {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		loop := current.child(n.names)
		loop.hasLoop, loop.loopLength = n.usesLoop, len(keys)
		for index, key := range keys {
			loop.loopIndex = index
			loop.values[0], loop.values[1] = textValue(key), value.dict[key]
			if err := renderNodes(n.body, loop, output); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("cannot loop over this value")
	}
}

type tokenKind int

const (
	tokenText tokenKind = iota
	tokenExpression
	tokenBlock
)

type token struct {
	kind tokenKind
	text string
}

func tokenize(text string) ([]token, error) {
	var tokens []token
	for {
		start := strings.Index(text, "{{")
		if block := strings.Index(text, "{%"); block >= 0 && (start < 0 || block < start) {
			start = block
		}
		if start < 0 {
			break
		}
		if start > 0 {
			tokens = append(tokens, token{kind: tokenText, text: text[:start]})
		}
		kind := tokenExpression
		closing := "}}"
		if text[start+1] == '%' {
			kind, closing = tokenBlock, "%}"
		}
		end := findTagEnd(text, start+2, closing)
		if end < 0 {
			return nil, fmt.Errorf("unclosed tag %q", text[start:])
		}
		tokens = append(tokens, token{kind: kind, text: strings.TrimSpace(text[start+2 : end])})
		text = text[end+len(closing):]
	}
	if text != "" {
		tokens = append(tokens, token{kind: tokenText, text: text})
	}
	return tokens, nil
}

// findTagEnd scans forward from the tag body and returns the closing delimiter's index,
// skipping string literals so a "}}" or "%}" inside a quoted value does not end the tag.
func findTagEnd(text string, index int, closing string) int {
	var quote byte
	for index < len(text) {
		switch {
		case quote != 0:
			switch text[index] {
			case '\\':
				index++
			case quote:
				quote = 0
			}
		case text[index] == '"' || text[index] == '\'':
			quote = text[index]
		case strings.HasPrefix(text[index:], closing):
			return index
		}
		index++
	}
	return -1
}

type parser struct {
	tokens   []token
	position int
}

func parseTemplate(text string) ([]node, error) {
	tokens, err := tokenize(text)
	if err != nil {
		return nil, err
	}
	p := parser{tokens: tokens}
	nodes, terminator, err := p.parseNodes()
	if err != nil {
		return nil, err
	}
	if terminator != "" {
		return nil, fmt.Errorf("unexpected %q", terminator)
	}
	return nodes, nil
}

// parseNodes stops at the end of the template or a closing block, which it returns.
func (p *parser) parseNodes() ([]node, string, error) {
	var nodes []node
	for p.position < len(p.tokens) {
		current := p.tokens[p.position]
		p.position++
		switch current.kind {
		case tokenText:
			nodes = append(nodes, textNode(current.text))
		case tokenExpression:
			nodes = append(nodes, expressionNode{expression: current.text, plan: planExpression(current.text)})
		default:
			keyword, rest := splitTag(current.text)
			switch keyword {
			case "if":
				if rest == "" {
					return nil, "", fmt.Errorf("if block needs a condition")
				}
				branch, err := p.parseIf(rest)
				if err != nil {
					return nil, "", err
				}
				nodes = append(nodes, branch)
			case "for":
				loop, err := p.parseFor(strings.TrimSpace(strings.TrimPrefix(current.text, "for")))
				if err != nil {
					return nil, "", err
				}
				nodes = append(nodes, loop)
			case "endif", "endfor", "else":
				return nodes, current.text, nil
			default:
				return nil, "", fmt.Errorf("unsupported template block %q", keyword)
			}
		}
	}
	return nodes, "", nil
}

func (p *parser) parseIf(condition string) (node, error) {
	var branches []ifBranch
	current := condition
	for {
		body, terminator, err := p.parseNodes()
		if err != nil {
			return nil, err
		}
		branches = append(branches, ifBranch{condition: current, body: body})
		switch {
		case terminator == "endif":
			return ifNode{branches: branches}, nil
		case terminator == "else":
			current = ""
		case strings.HasPrefix(terminator, "else if"):
			_, rest := splitTag(terminator)
			next := strings.TrimSpace(strings.TrimPrefix(rest, "if"))
			if next == "" {
				return nil, fmt.Errorf("if block needs a condition")
			}
			current = next
		default:
			return nil, fmt.Errorf("unclosed if block")
		}
	}
}

func (p *parser) parseFor(clause string) (node, error) {
	names, iterable, found := strings.Cut(clause, " in ")
	names, iterable = strings.TrimSpace(names), strings.TrimSpace(iterable)
	if !found || names == "" || iterable == "" {
		return nil, fmt.Errorf("invalid for block %q", clause)
	}
	variables := []string{}
	for _, name := range strings.Split(names, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("invalid for block %q", clause)
		}
		variables = append(variables, name)
	}
	body, terminator, err := p.parseNodes()
	if err != nil {
		return nil, err
	}
	if terminator != "endfor" {
		return nil, fmt.Errorf("unclosed for block")
	}
	return forNode{names: variables, iterable: iterable, body: body, usesLoop: nodesUseLoop(body) || referencesName(iterable, "loop")}, nil
}

// nodesUseLoop reports whether any node reads loop.*.
func nodesUseLoop(nodes []node) bool {
	for _, item := range nodes {
		switch current := item.(type) {
		case expressionNode:
			if referencesName(current.expression, "loop") {
				return true
			}
		case ifNode:
			for _, branch := range current.branches {
				if referencesName(branch.condition, "loop") || nodesUseLoop(branch.body) {
					return true
				}
			}
		case forNode:
			if current.usesLoop {
				return true
			}
		}
	}
	return false
}

func referencesName(text, name string) bool {
	for _, field := range strings.FieldsFunc(text, func(character rune) bool {
		return character != '_' && !unicode.IsLetter(character) && !unicode.IsDigit(character)
	}) {
		if field == name {
			return true
		}
	}
	return false
}

// splitTag cuts at the first space so clauses like "x in files" survive.
func splitTag(text string) (string, string) {
	keyword, rest, _ := strings.Cut(strings.TrimSpace(text), " ")
	return keyword, strings.TrimSpace(rest)
}

// evalExpression handles literals, paths, calls, filters, and `not`.
func evalExpression(text string, current *scope) (templateValue, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return templateValue{}, fmt.Errorf("empty template expression")
	}
	if rest, found := strings.CutPrefix(text, "not "); found {
		value, err := evalExpression(rest, current)
		if err != nil {
			return templateValue{}, err
		}
		return boolValue(!value.truthy()), nil
	}
	if index := findFilter(text); index >= 0 {
		value, err := evalExpression(text[:index], current)
		if err != nil {
			return templateValue{}, err
		}
		return applyFilter(strings.TrimSpace(text[index+1:]), value)
	}
	if len(text) > 1 && text[0] == '(' && strings.HasSuffix(text, ")") {
		return evalExpression(text[1:len(text)-1], current)
	}
	if len(text) > 1 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		unquoted, err := strconv.Unquote(text)
		if err != nil {
			return templateValue{}, err
		}
		return textValue(unquoted), nil
	}
	// Only numeric-looking text is parsed, so names never allocate parse errors.
	if isNumeric(text) {
		if number, err := strconv.Atoi(text); err == nil {
			return numberValue(number), nil
		}
	}
	switch text {
	case "true":
		return boolValue(true), nil
	case "false":
		return boolValue(false), nil
	}
	// Fast path for bare names, the common case.
	if isPlainName(text) {
		value, exists := current.lookup(text)
		if !exists {
			return templateValue{}, fmt.Errorf("unknown template value %q", text)
		}
		return value, nil
	}
	if name, arguments, found := parseCall(text); found {
		return callFunction(name, arguments, current)
	}
	return lookupPath(text, current)
}

// planKind is the shape of a pre-parsed expression.
type planKind int

const (
	// planGeneric is the zero value: anything the planner leaves to evalExpression.
	planGeneric planKind = iota
	planName
	planOptionalName
	planMember
	planFiltered
)

// expressionPlan is an expression's shape, resolved at compile time.
type expressionPlan struct {
	kind           planKind
	text           string
	name           string
	member         string
	optional       bool
	memberOptional bool
	filter         string
	inner          *expressionPlan
}

// planExpression returns the zero plan for shapes evalExpression must handle.
func planExpression(text string) expressionPlan {
	trimmed := strings.TrimSpace(text)
	if index := findFilter(trimmed); index >= 0 {
		inner := planExpression(trimmed[:index])
		if filter := strings.TrimSpace(trimmed[index+1:]); filter == "nl" && inner.kind != planGeneric {
			return expressionPlan{kind: planFiltered, text: trimmed, filter: filter, inner: &inner}
		}
		return expressionPlan{}
	}
	segment, rest := nextSegment(trimmed)
	if !isIdentifier(segment.name) {
		return expressionPlan{}
	}
	if rest == "" {
		if segment.optional {
			return expressionPlan{kind: planOptionalName, text: trimmed, name: segment.name}
		}
		return expressionPlan{kind: planName, text: trimmed, name: segment.name}
	}
	member, tail := nextSegment(rest)
	if tail != "" || !isIdentifierOrNumber(member.name) {
		return expressionPlan{}
	}
	return expressionPlan{
		kind:           planMember,
		text:           trimmed,
		name:           segment.name,
		member:         member.name,
		optional:       segment.optional,
		memberOptional: member.optional,
	}
}

// evaluate falls back to evalExpression for unplanned shapes.
func (plan expressionPlan) evaluate(text string, current *scope) (templateValue, error) {
	switch plan.kind {
	case planName, planOptionalName:
		value, exists := current.lookup(plan.name)
		if !exists {
			if plan.kind == planOptionalName {
				return templateValue{}, nil
			}
			return templateValue{}, fmt.Errorf("unknown template value %q", plan.name)
		}
		return value, nil
	case planMember:
		value, exists := current.lookup(plan.name)
		if !exists {
			if plan.optional {
				return templateValue{}, nil
			}
			return templateValue{}, fmt.Errorf("unknown template value %q", plan.name)
		}
		member, exists := memberValue(value, plan.member)
		if !exists {
			if plan.memberOptional {
				return templateValue{}, nil
			}
			return templateValue{}, fmt.Errorf("template value %q has no field %q", plan.text, plan.member)
		}
		return member, nil
	case planFiltered:
		value, err := plan.inner.evaluate(text, current)
		if err != nil {
			return templateValue{}, err
		}
		return newlineValue(value), nil
	default:
		return evalExpression(text, current)
	}
}

func isIdentifier(text string) bool {
	if text == "" || text == "true" || text == "false" {
		return false
	}
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case character == '_' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z'):
		case index > 0 && character >= '0' && character <= '9':
		default:
			return false
		}
	}
	return true
}

func isIdentifierOrNumber(text string) bool {
	if text == "" {
		return false
	}
	for index := 0; index < len(text); index++ {
		if character := text[index]; character < '0' || character > '9' {
			return isIdentifier(text)
		}
	}
	return true
}

func isNumeric(text string) bool {
	return text != "" && (text[0] == '-' || (text[0] >= '0' && text[0] <= '9'))
}

func isPlainName(text string) bool {
	if text == "" {
		return false
	}
	for _, character := range text {
		switch character {
		case '.', '?', '(', ')', '|', ':', ' ', '\t':
			return false
		}
	}
	return true
}

func findFilter(text string) int {
	depth := 0
	var quote byte
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case quote != 0:
			switch character {
			case '\\':
				index++
			case quote:
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == '(':
			depth++
		case character == ')':
			depth--
		case character == '|' && depth == 0:
			return index
		}
	}
	return -1
}

func parseCall(text string) (string, []string, bool) {
	open := strings.Index(text, "(")
	if open <= 0 || !strings.HasSuffix(text, ")") {
		return "", nil, false
	}
	name := strings.TrimSpace(text[:open])
	if name == "" || strings.ContainsAny(name, " .") {
		return "", nil, false
	}
	return name, splitArguments(text[open+1 : len(text)-1]), true
}

func splitArguments(text string) []string {
	var arguments []string
	depth := 0
	var quote byte
	start := 0
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case quote != 0:
			switch character {
			case '\\':
				index++
			case quote:
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == '(':
			depth++
		case character == ')':
			depth--
		case character == ',' && depth == 0:
			arguments = append(arguments, strings.TrimSpace(text[start:index]))
			start = index + 1
		}
	}
	if trimmed := strings.TrimSpace(text[start:]); trimmed != "" || len(arguments) > 0 {
		arguments = append(arguments, trimmed)
	}
	return arguments
}

// callFunction handles the built-ins: nl and get.
func callFunction(name string, arguments []string, current *scope) (templateValue, error) {
	switch name {
	case "nl":
		if len(arguments) != 1 {
			return templateValue{}, fmt.Errorf("nl takes one argument")
		}
		value, err := evalExpression(arguments[0], current)
		if err != nil {
			return templateValue{}, err
		}
		return newlineValue(value), nil
	case "get":
		if len(arguments) != 2 {
			return templateValue{}, fmt.Errorf("get takes two arguments")
		}
		container, err := evalExpression(arguments[0], current)
		if err != nil {
			return templateValue{}, err
		}
		key, err := evalExpression(arguments[1], current)
		if err != nil {
			return templateValue{}, err
		}
		if container.kind != valueMap {
			return templateValue{}, nil
		}
		return container.dict[key.text], nil
	default:
		return templateValue{}, fmt.Errorf("unknown template function %q", name)
	}
}

func applyFilter(text string, value templateValue) (templateValue, error) {
	name, _, _ := strings.Cut(text, ":")
	switch strings.TrimSpace(name) {
	case "nl":
		return newlineValue(value), nil
	case "dquote":
		printed, err := value.print()
		if err != nil {
			return templateValue{}, err
		}
		return textValue(escapeDQuotes(printed)), nil
	default:
		return templateValue{}, fmt.Errorf("unknown template filter %q", strings.TrimSpace(name))
	}
}

// escapeDQuotes escapes the characters the shell expands inside double quotes, so a value
// holding $, `, \, or " still yields the exact same literal text when it reaches the shell.
func escapeDQuotes(text string) string {
	if !strings.ContainsAny(text, "\\$`\"") {
		return text
	}
	var output strings.Builder
	output.Grow(len(text) + 8)
	for _, character := range text {
		switch character {
		case '\\', '$', '`', '"':
			output.WriteByte('\\')
		}
		output.WriteRune(character)
	}
	return output.String()
}

// newlineValue adds a newline only if one is missing.
func newlineValue(value templateValue) templateValue {
	if value.kind != valueText || strings.HasSuffix(value.text, "\n") {
		return value
	}
	value.text += "\n"
	return value
}

type pathSegment struct {
	name     string
	optional bool
}

// The next separator is a dot, or the "?" that starts a "?." member.
func nextSegmentEnd(text string) int {
	for index := 1; index < len(text); index++ {
		if text[index] == '?' && index+1 < len(text) && text[index+1] == '.' {
			return index
		}
		if text[index] == '.' && text[index-1] != '?' {
			return index
		}
	}
	return -1
}

// nextSegment splits one member off a path like hooks?.pre or files.0.
func nextSegment(path string) (segment pathSegment, rest string) {
	remaining := path
	switch {
	case strings.HasPrefix(remaining, "?."):
		segment.optional, remaining = true, remaining[2:]
	case strings.HasPrefix(remaining, "."):
		remaining = remaining[1:]
	}
	end := nextSegmentEnd(remaining)
	if end >= 0 {
		return pathSegment{name: remaining[:end], optional: segment.optional}, remaining[end:]
	}
	return pathSegment{name: remaining, optional: segment.optional}, ""
}

// lookupPath walks a dotted path; `?.` returns nothing instead of failing.
func lookupPath(path string, current *scope) (templateValue, error) {
	segment, rest := nextSegment(strings.TrimSpace(path))
	if segment.name == "" {
		return templateValue{}, fmt.Errorf("invalid template expression %q", path)
	}
	value, exists := current.lookup(segment.name)
	if !exists {
		if segment.optional {
			return templateValue{}, nil
		}
		return templateValue{}, fmt.Errorf("unknown template value %q", segment.name)
	}
	for rest != "" {
		segment, rest = nextSegment(rest)
		if segment.name == "" {
			break
		}
		value, exists = memberValue(value, segment.name)
		if !exists {
			if segment.optional {
				return templateValue{}, nil
			}
			return templateValue{}, fmt.Errorf("template value %q has no field %q", path, segment.name)
		}
	}
	return value, nil
}

// memberValue: map key, list index, or loop field.
func memberValue(value templateValue, member string) (templateValue, bool) {
	switch value.kind {
	case valueMap:
		found, exists := value.dict[member]
		return found, exists
	case valueTexts:
		index, err := strconv.Atoi(member)
		if err != nil || index < 0 || index >= len(value.texts) {
			return templateValue{}, false
		}
		return textValue(value.texts[index]), true
	case valueLoop:
		switch member {
		case "index":
			return numberValue(value.loop.index), true
		case "first":
			return boolValue(value.loop.index == 0), true
		case "last":
			return boolValue(value.loop.index == value.loop.length-1), true
		}
		return templateValue{}, false
	default:
		return templateValue{}, false
	}
}

// compiledTemplate caches a parse so repeated renders skip it.
type compiledTemplate struct {
	nodes []node
	err   error
}

var (
	templateCacheMu sync.RWMutex
	templateCache   = map[string]compiledTemplate{}
)

// compileTemplate parses once; the same text re-renders for every plugin.
func compileTemplate(text string) ([]node, error) {
	templateCacheMu.RLock()
	entry, ok := templateCache[text]
	templateCacheMu.RUnlock()
	if ok {
		return entry.nodes, entry.err
	}
	nodes, err := parseTemplate(text)
	templateCacheMu.Lock()
	templateCache[text] = compiledTemplate{nodes: nodes, err: err}
	templateCacheMu.Unlock()
	return nodes, err
}
