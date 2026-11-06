package command

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Selector is a target as typed: @a, @p, @r, @e, @s or @initiator with its arguments, or
// a player's name.
type Selector struct {
	// Kind is the letter after @ ("a", "p", "r", "e", "s", "initiator"), or "" for a name.
	Kind string
	// Name is the name typed when Kind is "".
	Name string
	// Args are the arguments in brackets, in order.
	Args []SelectorArg
}

// SelectorArg is one key=value in a selector's brackets; Negated is a value starting "!".
type SelectorArg struct {
	Key, Value string
	Negated    bool
}

// Arg is the first argument with a key.
func (s Selector) Arg(key string) (SelectorArg, bool) {
	for _, a := range s.Args {
		if a.Key == key {
			return a, true
		}
	}
	return SelectorArg{}, false
}

// String is the selector as typed.
func (s Selector) String() string {
	if s.Kind == "" {
		return s.Name
	}
	var b strings.Builder
	b.WriteString("@" + s.Kind)
	if len(s.Args) > 0 {
		b.WriteByte('[')
		for i, a := range s.Args {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(a.Key + "=")
			if a.Negated {
				b.WriteByte('!')
			}
			b.WriteString(a.Value)
		}
		b.WriteByte(']')
	}
	return b.String()
}

// selectorKinds are the selectors BDS knows.
var selectorKinds = []string{"a", "p", "r", "e", "s", "initiator"}

// parseError is why an overload did not fit, at a place in the line.
type parseError struct {
	full       string
	start, end int
	// syntax errors show where the line went wrong; others show key and params.
	syntax bool
	key    string
	params []string
}

// report writes the error as BDS words it.
func (e *parseError) report(out *Output) {
	if e.syntax {
		end := min(max(e.end, e.start), len(e.full))
		out.Fail(keySyntax, e.full[:e.start], e.full[e.start:end], e.full[end:])
		return
	}
	out.Fail(e.key, e.params...)
}

// scanner reads arguments off a line.
type scanner struct {
	s string
	i int
	// base is where s starts in the full line, for errors.
	base int
	full string
}

func (sc *scanner) skipSpace() {
	for sc.i < len(sc.s) && sc.s[sc.i] == ' ' {
		sc.i++
	}
}

func (sc *scanner) done() bool {
	sc.skipSpace()
	return sc.i >= len(sc.s)
}

// syntaxAt is a syntax error at the token from start.
func (sc *scanner) syntaxAt(start int) *parseError {
	end := start
	for end < len(sc.s) && sc.s[end] != ' ' {
		end++
	}
	return &parseError{full: sc.full, start: sc.base + start, end: sc.base + end, syntax: true}
}

// failAt is a value error at start.
func (sc *scanner) failAt(start int, key string, params ...string) *parseError {
	return &parseError{full: sc.full, start: sc.base + start, end: sc.base + start, key: key, params: params}
}

// word reads one word, or a quoted string with \" and \\ escapes.
func (sc *scanner) word() (string, int, bool) {
	sc.skipSpace()
	start := sc.i
	if sc.i >= len(sc.s) {
		return "", start, false
	}
	if sc.s[sc.i] != '"' {
		for sc.i < len(sc.s) && sc.s[sc.i] != ' ' {
			sc.i++
		}
		return sc.s[start:sc.i], start, true
	}
	var b strings.Builder
	for sc.i++; sc.i < len(sc.s); sc.i++ {
		c := sc.s[sc.i]
		switch {
		case c == '\\' && sc.i+1 < len(sc.s):
			sc.i++
			b.WriteByte(sc.s[sc.i])
		case c == '"':
			sc.i++
			return b.String(), start, true
		default:
			b.WriteByte(c)
		}
	}
	sc.i = start
	return "", start, false
}

// coord reads one coordinate. Coordinates may touch: "~~1~".
func (sc *scanner) coord() (Coord, int, bool) {
	if sc.i >= len(sc.s) || sc.s[sc.i] != '~' && sc.s[sc.i] != '^' {
		sc.skipSpace()
	}
	start := sc.i
	if sc.i >= len(sc.s) {
		return Coord{}, start, false
	}
	var c Coord
	switch sc.s[sc.i] {
	case '~':
		c.Relative = true
		sc.i++
	case '^':
		c.Local = true
		sc.i++
	}
	numStart := sc.i
	for sc.i < len(sc.s) && strings.IndexByte("0123456789.-+", sc.s[sc.i]) >= 0 {
		sc.i++
	}
	if num := sc.s[numStart:sc.i]; num != "" {
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			sc.i = start
			return Coord{}, start, false
		}
		c.Value = v
	} else if !c.Relative && !c.Local {
		return Coord{}, start, false
	}
	if sc.i < len(sc.s) && sc.s[sc.i] != ' ' && sc.s[sc.i] != '~' && sc.s[sc.i] != '^' {
		sc.i = start
		return Coord{}, start, false
	}
	return c, start, true
}

// position reads three coordinates; ^ may not mix with the others.
func (sc *scanner) position() (Position, *parseError) {
	var p Position
	for i, at := range []*Coord{&p.X, &p.Y, &p.Z} {
		c, start, ok := sc.coord()
		if !ok {
			return p, sc.syntaxAt(start)
		}
		if i > 0 && c.Local != p.X.Local {
			return p, sc.syntaxAt(start)
		}
		*at = c
	}
	return p, nil
}

// selector reads @x[...] or a name.
func (sc *scanner) selector() (Selector, int, *parseError) {
	sc.skipSpace()
	start := sc.i
	if sc.i >= len(sc.s) {
		return Selector{}, start, sc.syntaxAt(start)
	}
	if sc.s[sc.i] != '@' {
		name, at, ok := sc.word()
		if !ok {
			return Selector{}, at, sc.syntaxAt(at)
		}
		return Selector{Name: name}, at, nil
	}
	sc.i++
	kindStart := sc.i
	for sc.i < len(sc.s) && sc.s[sc.i] != ' ' && sc.s[sc.i] != '[' {
		sc.i++
	}
	sel := Selector{Kind: strings.ToLower(sc.s[kindStart:sc.i])}
	if !slices.Contains(selectorKinds, sel.Kind) {
		return Selector{}, start, sc.syntaxAt(start)
	}
	if sc.i >= len(sc.s) || sc.s[sc.i] != '[' {
		return sel, start, nil
	}
	end := strings.IndexByte(sc.s[sc.i:], ']')
	if end < 0 {
		return Selector{}, start, sc.syntaxAt(start)
	}
	body := sc.s[sc.i+1 : sc.i+end]
	sc.i += end + 1
	for part := range strings.SplitSeq(body, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return Selector{}, start, sc.syntaxAt(start)
		}
		a := SelectorArg{Key: strings.TrimSpace(key), Value: strings.TrimSpace(value)}
		if strings.HasPrefix(a.Value, "!") {
			a.Negated, a.Value = true, strings.TrimSpace(a.Value[1:])
		}
		a.Value = strings.Trim(a.Value, "\"")
		sel.Args = append(sel.Args, a)
	}
	return sel, start, nil
}

// read reads one argument's value.
func (m *matcher) read(sc *scanner, p param) (reflect.Value, *parseError) {
	switch p.kind {
	case kindText:
		sc.skipSpace()
		text := sc.s[sc.i:]
		sc.i = len(sc.s)
		return reflect.ValueOf(Text(text)), nil
	case kindPosition, kindBlockPosition:
		pos, err := sc.position()
		if err != nil {
			return reflect.Value{}, err
		}
		if p.kind == kindBlockPosition {
			return reflect.ValueOf(BlockPosition(pos)), nil
		}
		return reflect.ValueOf(pos), nil
	case kindTargets:
		sel, start, err := sc.selector()
		if err != nil {
			return reflect.Value{}, err
		}
		found := m.src.Targets(sel)
		if len(found) == 0 {
			if sel.Kind == "" {
				return reflect.Value{}, sc.failAt(start, keyNoPlayer)
			}
			return reflect.Value{}, sc.failAt(start, keyNoTarget)
		}
		return reflect.ValueOf(Targets{Selector: sel, List: found}), nil
	}
	w, start, ok := sc.word()
	if !ok {
		return reflect.Value{}, sc.syntaxAt(start)
	}
	switch p.kind {
	case kindLiteral:
		if !strings.EqualFold(w, p.literal) {
			return reflect.Value{}, sc.syntaxAt(start)
		}
		return reflect.ValueOf(Literal{}), nil
	case kindInt:
		v, err := strconv.ParseInt(w, 10, 32)
		if err != nil {
			return reflect.Value{}, sc.failAt(start, keyNum, w)
		}
		return reflect.ValueOf(v), nil
	case kindFloat:
		v, err := strconv.ParseFloat(w, 64)
		if err != nil {
			return reflect.Value{}, sc.failAt(start, keyNum, w)
		}
		return reflect.ValueOf(v), nil
	case kindBool:
		switch strings.ToLower(w) {
		case "true":
			return reflect.ValueOf(true), nil
		case "false":
			return reflect.ValueOf(false), nil
		}
		return reflect.Value{}, sc.failAt(start, keyBool, w)
	case kindEnum:
		for _, o := range m.m.optionsOf(m.t, p.enum, m.src) {
			if strings.EqualFold(o, w) {
				return reflect.ValueOf(o), nil
			}
		}
		return reflect.Value{}, sc.syntaxAt(start)
	}
	return reflect.ValueOf(w), nil
}

// matcher matches a line against a command's overloads for one source.
type matcher struct {
	m   *Manager
	t   *table
	src Source
}

// try parses rest against one overload.
func (m *matcher) try(o overload, sc *scanner) ([]reflect.Value, *parseError) {
	values := make([]reflect.Value, len(o.params))
	for i, p := range o.params {
		if p.optional && sc.done() {
			break
		}
		v, err := m.read(sc, p)
		if err != nil {
			return nil, err
		}
		values[i] = v
	}
	if !sc.done() {
		return nil, sc.syntaxAt(sc.i)
	}
	return values, nil
}

// match finds the overload a line fits and returns how to run it. Without one, the error
// is the one that got furthest into the line, as BDS reports.
func (c *Command) match(mt *matcher, name, rest string) (func(Source, *Output), *parseError) {
	full := "/" + name + " " + rest
	base := len(full) - len(rest)
	var best *parseError
	for _, o := range c.overloads {
		sc := &scanner{s: rest, base: base, full: full}
		values, err := mt.try(o, sc)
		if err == nil {
			o := o
			return func(src Source, out *Output) { o.run(src, values, out) }, nil
		}
		if best == nil || err.start > best.start {
			best = err
		}
	}
	return nil, best
}
