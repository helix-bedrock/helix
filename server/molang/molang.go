// Package molang evaluates the simple Molang expressions found in BDS's
// entity definitions, such as experience rewards (Helix):
//
//	query.last_hit_by_player ? Math.Random(1,3) : 0
//
// It supports numbers, query.* and variable.* values given by the caller,
// the Math functions random, random_integer, die_roll, die_roll_integer,
// min, max, abs, floor, ceil, round and clamp, the operators ! - * / + -
// < <= > >= == != && || and ?:, and parentheses. It does not support
// statements, assignments, loops or strings. Names are case-insensitive.
//
// Expressions are parsed once and evaluated many times. Evaluating is safe
// from anywhere; it never panics on any input.
package molang

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode"
)

// Env gives an expression its values.
type Env struct {
	// Query returns the value of query.name (or variable.name), without the
	// prefix, in lower case. Unknown names should return 0.
	Query func(name string) float64
	// Rand is used by the random functions. If nil, they return their
	// lowest value.
	Rand *rand.Rand
}

// Expr is a parsed expression.
type Expr struct{ root node }

// Parse parses an expression.
func Parse(src string) (*Expr, error) {
	p := &parser{toks: lex(strings.ToLower(src))}
	n, err := p.ternary()
	if err != nil {
		return nil, fmt.Errorf("molang: %q: %w", src, err)
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("molang: %q: unexpected %q", src, p.toks[p.pos])
	}
	return &Expr{root: n}, nil
}

// Eval evaluates the expression.
func (e *Expr) Eval(env Env) float64 {
	if e == nil || e.root == nil {
		return 0
	}
	return e.root.eval(env)
}

type node interface{ eval(Env) float64 }

type num float64

func (n num) eval(Env) float64 { return float64(n) }

type query string

func (q query) eval(env Env) float64 {
	if env.Query == nil {
		return 0
	}
	return env.Query(string(q))
}

type unary struct {
	op string
	x  node
}

func (u unary) eval(env Env) float64 {
	v := u.x.eval(env)
	if u.op == "!" {
		return b2f(v == 0)
	}
	return -v
}

type binary struct {
	op   string
	l, r node
}

func (b binary) eval(env Env) float64 {
	l := b.l.eval(env)
	switch b.op {
	case "&&":
		return b2f(l != 0 && b.r.eval(env) != 0)
	case "||":
		return b2f(l != 0 || b.r.eval(env) != 0)
	}
	r := b.r.eval(env)
	switch b.op {
	case "+":
		return l + r
	case "-":
		return l - r
	case "*":
		return l * r
	case "/":
		if r == 0 {
			return 0
		}
		return l / r
	case "<":
		return b2f(l < r)
	case "<=":
		return b2f(l <= r)
	case ">":
		return b2f(l > r)
	case ">=":
		return b2f(l >= r)
	case "==":
		return b2f(l == r)
	case "!=":
		return b2f(l != r)
	}
	return 0
}

type ternary struct{ cond, a, b node }

func (t ternary) eval(env Env) float64 {
	if t.cond.eval(env) != 0 {
		return t.a.eval(env)
	}
	if t.b == nil {
		return 0
	}
	return t.b.eval(env)
}

type call struct {
	fn   string
	args []node
}

func (c call) eval(env Env) float64 {
	a := make([]float64, len(c.args))
	for i, x := range c.args {
		a[i] = x.eval(env)
	}
	arg := func(i int) float64 {
		if i < len(a) {
			return a[i]
		}
		return 0
	}
	randf := func(lo, hi float64) float64 {
		if env.Rand == nil || hi <= lo {
			return lo
		}
		return lo + env.Rand.Float64()*(hi-lo)
	}
	randi := func(lo, hi float64) float64 {
		l, h := math.Round(lo), math.Round(hi)
		if env.Rand == nil || h <= l {
			return l
		}
		return l + float64(env.Rand.IntN(int(h-l)+1))
	}
	switch c.fn {
	case "random":
		return randf(arg(0), arg(1))
	case "random_integer":
		return randi(arg(0), arg(1))
	case "die_roll", "die_roll_integer":
		var sum float64
		for range int(math.Max(0, math.Min(arg(0), 1000))) {
			if c.fn == "die_roll" {
				sum += randf(arg(1), arg(2))
			} else {
				sum += randi(arg(1), arg(2))
			}
		}
		return sum
	case "min":
		return math.Min(arg(0), arg(1))
	case "max":
		return math.Max(arg(0), arg(1))
	case "abs":
		return math.Abs(arg(0))
	case "floor":
		return math.Floor(arg(0))
	case "ceil":
		return math.Ceil(arg(0))
	case "round":
		return math.Round(arg(0))
	case "clamp":
		return math.Max(arg(1), math.Min(arg(2), arg(0)))
	}
	return 0
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func lex(s string) []string {
	var toks []string
	for i := 0; i < len(s); {
		c := rune(s[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case unicode.IsDigit(c) || c == '.' && i+1 < len(s) && unicode.IsDigit(rune(s[i+1])):
			j := i
			for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
				j++
			}
			if j < len(s) && s[j] == 'f' {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_' || s[j] == '.') {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		default:
			if i+1 < len(s) {
				two := s[i : i+2]
				if two == "&&" || two == "||" || two == "<=" || two == ">=" || two == "==" || two == "!=" {
					toks = append(toks, two)
					i += 2
					continue
				}
			}
			toks = append(toks, string(c))
			i++
		}
	}
	return toks
}

type parser struct {
	toks []string
	pos  int
}

func (p *parser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}

func (p *parser) next() string {
	t := p.peek()
	p.pos++
	return t
}

var errEnd = errors.New("unexpected end")

func (p *parser) ternary() (node, error) {
	c, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	if p.peek() != "?" {
		return c, nil
	}
	p.next()
	a, err := p.ternary()
	if err != nil {
		return nil, err
	}
	if p.peek() != ":" {
		return ternary{cond: c, a: a}, nil
	}
	p.next()
	b, err := p.ternary()
	if err != nil {
		return nil, err
	}
	return ternary{cond: c, a: a, b: b}, nil
}

var levels = [][]string{{"||"}, {"&&"}, {"==", "!="}, {"<", "<=", ">", ">="}, {"+", "-"}, {"*", "/"}}

func (p *parser) binary(level int) (node, error) {
	if level == len(levels) {
		return p.unary()
	}
	l, err := p.binary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		op := p.peek()
		found := false
		for _, o := range levels[level] {
			found = found || op == o
		}
		if !found {
			return l, nil
		}
		p.next()
		r, err := p.binary(level + 1)
		if err != nil {
			return nil, err
		}
		l = binary{op: op, l: l, r: r}
	}
}

func (p *parser) unary() (node, error) {
	if op := p.peek(); op == "!" || op == "-" {
		p.next()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return unary{op: op, x: x}, nil
	}
	return p.primary()
}

func (p *parser) primary() (node, error) {
	t := p.next()
	switch {
	case t == "":
		return nil, errEnd
	case t == "(":
		n, err := p.ternary()
		if err != nil {
			return nil, err
		}
		if p.next() != ")" {
			return nil, errors.New("missing )")
		}
		return n, nil
	case t[0] >= '0' && t[0] <= '9' || t[0] == '.':
		f, err := strconv.ParseFloat(strings.TrimSuffix(t, "f"), 64)
		if err != nil {
			return nil, err
		}
		return num(f), nil
	case strings.HasPrefix(t, "math."):
		fn := strings.TrimPrefix(t, "math.")
		if p.next() != "(" {
			return nil, fmt.Errorf("%s: missing (", t)
		}
		var args []node
		for p.peek() != ")" {
			a, err := p.ternary()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.peek() == "," {
				p.next()
			}
		}
		p.next()
		return call{fn: fn, args: args}, nil
	}
	for _, prefix := range []string{"query.", "q.", "variable.", "v."} {
		if name, ok := strings.CutPrefix(t, prefix); ok {
			if p.peek() == "(" {
				// Queries with arguments are not supported: skip them.
				depth := 0
				for t := p.next(); t != ""; t = p.next() {
					if t == "(" {
						depth++
					} else if t == ")" {
						if depth--; depth == 0 {
							break
						}
					}
				}
			}
			return query(name), nil
		}
	}
	return nil, fmt.Errorf("unknown name %q", t)
}
