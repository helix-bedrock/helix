package mob

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/helix-bedrock/entity/behavior"
)

// Molang in the behaviour data: the expressions vanilla entities write in experience_reward,
// ambient_sound_interval, heartbeat, eat_block, property defaults and set_property. Numbers,
// strings, ! - * / + -, comparisons, && ||, ?: (and ? without an else, which is 0), and the
// math functions and queries below. Names are case-insensitive and q./v. are short for
// query./variable. as in BDS. Statements, variables and arrow access are not supported: an
// expression using them does not compile and the component keeps its default.
//
// A query about something the engine does not have yet answers as BDS does for a mob without
// it: equipment_count and is_using_item 0 (mobs hold nothing yet), player_level 0.

// playerHitTicks is how long after a player hurt a mob query.last_hit_by_player holds: BDS's
// lastHurtByPlayerTime of 100 ticks.
const playerHitTicks = 100

// molangKind is a node of a parsed expression.
type molangKind uint8

const (
	molangNumber molangKind = iota
	molangString
	molangNeg
	molangNot
	molangBinary
	molangCond
	molangCall
)

// molangCallID is a query or math function.
type molangCallID uint8

const (
	callMathRandom molangCallID = iota
	callMathRandomInt
	callMathDieRoll
	callMathDieRollInt
	callMathClamp
	callMathMin
	callMathMax
	callMathAbs
	callMathFloor
	callMathCeil
	callMathRound
	callMathTrunc
	callMathSqrt
	callMathMod
	callMathPow
	callMathLerp
	callMathPi
	callLastHitByPlayer
	callEquipmentCount
	callIsBaby
	callHasAnyFamily
	callHeadInWater
	callIsInWater
	callIsUsingItem
	callHadComponentGroup
	callIsOnGround
	callIsOnFire
	callProperty
	callPlayerLevel
	callVariant
	callMarkVariant
	callSkinID
	callAngerLevel
	callHealth
	callMaxHealth
)

// molangCalls are the functions by lower-case name, with their argument counts (max -1: any).
var molangCalls = map[string]struct {
	id       molangCallID
	min, max int
}{
	"math.random": {callMathRandom, 2, 2}, "math.random_integer": {callMathRandomInt, 2, 2},
	"math.die_roll": {callMathDieRoll, 3, 3}, "math.die_roll_integer": {callMathDieRollInt, 3, 3},
	"math.clamp": {callMathClamp, 3, 3}, "math.min": {callMathMin, 2, 2}, "math.max": {callMathMax, 2, 2},
	"math.abs": {callMathAbs, 1, 1}, "math.floor": {callMathFloor, 1, 1}, "math.ceil": {callMathCeil, 1, 1},
	"math.round": {callMathRound, 1, 1}, "math.trunc": {callMathTrunc, 1, 1}, "math.sqrt": {callMathSqrt, 1, 1},
	"math.mod": {callMathMod, 2, 2}, "math.pow": {callMathPow, 2, 2}, "math.lerp": {callMathLerp, 3, 3},
	"math.pi": {callMathPi, 0, 0},

	"query.last_hit_by_player":  {callLastHitByPlayer, 0, 0},
	"query.equipment_count":     {callEquipmentCount, 0, 0},
	"query.is_baby":             {callIsBaby, 0, 0},
	"query.has_any_family":      {callHasAnyFamily, 1, -1},
	"query.head_is_in_water":    {callHeadInWater, 0, 0},
	"query.is_in_water":         {callIsInWater, 0, 0},
	"query.is_using_item":       {callIsUsingItem, 0, 0},
	"query.had_component_group": {callHadComponentGroup, 1, 1},
	"query.is_on_ground":        {callIsOnGround, 0, 0},
	"query.is_on_fire":          {callIsOnFire, 0, 0},
	"query.property":            {callProperty, 1, 1},
	"query.player_level":        {callPlayerLevel, 0, 0},
	"query.variant":             {callVariant, 0, 0},
	"query.mark_variant":        {callMarkVariant, 0, 0},
	"query.skin_id":             {callSkinID, 0, 0},
	"query.anger_level":         {callAngerLevel, 0, 1},
	"query.health":              {callHealth, 0, 0},
	"query.max_health":          {callMaxHealth, 0, 0},
}

// molangExpr is a parsed expression node.
type molangExpr struct {
	kind molangKind
	num  float32
	str  string
	op   string
	call molangCallID
	args []*molangExpr
}

// molangFunc evaluates an expression for one mob, as a component's compiled Molang.
type molangFunc struct {
	e *molangExpr
	m *Mob
}

func (f molangFunc) eval(behavior.Mob) float32 { return f.e.eval(f.m) }

// parseMolang parses an expression.
func parseMolang(src string) (*molangExpr, error) {
	p := molangParser{src: src}
	e, err := p.ternary()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i < len(p.src) {
		return nil, fmt.Errorf("molang: unexpected %q at %d", p.src[p.i:], p.i)
	}
	return e, nil
}

type molangParser struct {
	src string
	i   int
}

var errMolangEnd = errors.New("molang: unexpected end")

func (p *molangParser) space() {
	for p.i < len(p.src) && strings.IndexByte(" \t\r\n", p.src[p.i]) >= 0 {
		p.i++
	}
}

// eat consumes tok when it comes next.
func (p *molangParser) eat(tok string) bool {
	p.space()
	if strings.HasPrefix(p.src[p.i:], tok) {
		p.i += len(tok)
		return true
	}
	return false
}

func (p *molangParser) ternary() (*molangExpr, error) {
	c, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if !strings.HasPrefix(p.src[p.i:], "?") || strings.HasPrefix(p.src[p.i:], "??") {
		return c, nil
	}
	p.i++
	a, err := p.ternary()
	if err != nil {
		return nil, err
	}
	b := &molangExpr{kind: molangNumber}
	if p.eat(":") {
		if b, err = p.ternary(); err != nil {
			return nil, err
		}
	}
	return &molangExpr{kind: molangCond, args: []*molangExpr{c, a, b}}, nil
}

// molangLevels are the binary operators by precedence, loosest first. Longer operators come
// first in a level so <= is not read as <.
var molangLevels = [][]string{
	{"||"}, {"&&"}, {"==", "!="}, {"<=", ">=", "<", ">"}, {"+", "-"}, {"*", "/"},
}

func (p *molangParser) binary(level int) (*molangExpr, error) {
	if level == len(molangLevels) {
		return p.unary()
	}
	l, err := p.binary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		op := ""
		for _, o := range molangLevels[level] {
			if p.eat(o) {
				op = o
				break
			}
		}
		if op == "" {
			return l, nil
		}
		r, err := p.binary(level + 1)
		if err != nil {
			return nil, err
		}
		l = &molangExpr{kind: molangBinary, op: op, args: []*molangExpr{l, r}}
	}
}

func (p *molangParser) unary() (*molangExpr, error) {
	switch {
	case p.eat("!"):
		e, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &molangExpr{kind: molangNot, args: []*molangExpr{e}}, nil
	case p.eat("-"):
		e, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &molangExpr{kind: molangNeg, args: []*molangExpr{e}}, nil
	}
	return p.primary()
}

func (p *molangParser) primary() (*molangExpr, error) {
	p.space()
	if p.i >= len(p.src) {
		return nil, errMolangEnd
	}
	c := p.src[p.i]
	switch {
	case c == '(':
		p.i++
		e, err := p.ternary()
		if err != nil {
			return nil, err
		}
		if !p.eat(")") {
			return nil, fmt.Errorf("molang: missing ) at %d", p.i)
		}
		return e, nil
	case c == '\'':
		end := strings.IndexByte(p.src[p.i+1:], '\'')
		if end < 0 {
			return nil, errMolangEnd
		}
		s := p.src[p.i+1 : p.i+1+end]
		p.i += end + 2
		return &molangExpr{kind: molangString, str: s}, nil
	case c >= '0' && c <= '9' || c == '.':
		j := p.i
		for j < len(p.src) && (p.src[j] >= '0' && p.src[j] <= '9' || p.src[j] == '.') {
			j++
		}
		v, err := strconv.ParseFloat(p.src[p.i:j], 32)
		if err != nil {
			return nil, fmt.Errorf("molang: number %q: %w", p.src[p.i:j], err)
		}
		p.i = j
		if j < len(p.src) && (p.src[j] == 'f' || p.src[j] == 'F') {
			p.i++
		}
		return &molangExpr{kind: molangNumber, num: float32(v)}, nil
	case isMolangName(c):
		return p.name()
	}
	return nil, fmt.Errorf("molang: unexpected %q at %d", c, p.i)
}

func isMolangName(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// molangPrefixes are BDS's short forms.
var molangPrefixes = map[string]string{"q": "query", "v": "variable", "t": "temp", "c": "context"}

func (p *molangParser) name() (*molangExpr, error) {
	j := p.i
	for j < len(p.src) && (isMolangName(p.src[j]) || p.src[j] == '.' || p.src[j] >= '0' && p.src[j] <= '9') {
		j++
	}
	name := strings.ToLower(p.src[p.i:j])
	p.i = j
	switch name {
	case "true":
		return &molangExpr{kind: molangNumber, num: 1}, nil
	case "false":
		return &molangExpr{kind: molangNumber, num: 0}, nil
	case "this":
		// The current value in BDS; in the data it is only query.anger_level(this).
		return &molangExpr{kind: molangNumber}, nil
	}
	if head, rest, ok := strings.Cut(name, "."); ok {
		if long, ok := molangPrefixes[head]; ok {
			name = long + "." + rest
		}
	}
	fn, ok := molangCalls[name]
	if !ok {
		return nil, fmt.Errorf("molang: %s not supported", name)
	}
	e := &molangExpr{kind: molangCall, call: fn.id}
	if p.eat("(") && !p.eat(")") {
		for {
			a, err := p.ternary()
			if err != nil {
				return nil, err
			}
			e.args = append(e.args, a)
			if p.eat(")") {
				break
			}
			if !p.eat(",") {
				return nil, fmt.Errorf("molang: %s: missing , or )", name)
			}
		}
	}
	if len(e.args) < fn.min || fn.max >= 0 && len(e.args) > fn.max {
		return nil, fmt.Errorf("molang: %s takes %d to %d arguments, not %d", name, fn.min, fn.max, len(e.args))
	}
	return e, nil
}

func molangBool(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// eval evaluates the expression for m. Strings are 0 outside function arguments.
func (e *molangExpr) eval(m *Mob) float32 {
	switch e.kind {
	case molangNumber:
		return e.num
	case molangNeg:
		return -e.args[0].eval(m)
	case molangNot:
		return molangBool(e.args[0].eval(m) == 0)
	case molangCond:
		if e.args[0].eval(m) != 0 {
			return e.args[1].eval(m)
		}
		return e.args[2].eval(m)
	case molangBinary:
		return e.binary(m)
	case molangCall:
		return e.callEval(m)
	}
	return 0
}

func (e *molangExpr) binary(m *Mob) float32 {
	l := e.args[0].eval(m)
	switch e.op {
	case "&&":
		return molangBool(l != 0 && e.args[1].eval(m) != 0)
	case "||":
		return molangBool(l != 0 || e.args[1].eval(m) != 0)
	}
	r := e.args[1].eval(m)
	switch e.op {
	case "==":
		return molangBool(l == r)
	case "!=":
		return molangBool(l != r)
	case "<":
		return molangBool(l < r)
	case "<=":
		return molangBool(l <= r)
	case ">":
		return molangBool(l > r)
	case ">=":
		return molangBool(l >= r)
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
	}
	return 0
}

func (e *molangExpr) arg(m *Mob, i int) float32 { return e.args[i].eval(m) }

// random is Molang's math.random: a float from lo to hi.
func molangRandom(m *Mob, lo, hi float32) float32 { return lo + m.NextFloat()*(hi-lo) }

// randomInt is math.random_integer: an integer from lo to hi, both included.
func molangRandomInt(m *Mob, lo, hi float32) float32 {
	a, b := math.Round(float64(lo)), math.Round(float64(hi))
	if b < a {
		a, b = b, a
	}
	return float32(a + math.Floor(float64(m.NextFloat())*(b-a+1)))
}

func (e *molangExpr) callEval(m *Mob) float32 {
	switch e.call {
	case callMathRandom:
		return molangRandom(m, e.arg(m, 0), e.arg(m, 1))
	case callMathRandomInt:
		return molangRandomInt(m, e.arg(m, 0), e.arg(m, 1))
	case callMathDieRoll, callMathDieRollInt:
		n, lo, hi := int(e.arg(m, 0)), e.arg(m, 1), e.arg(m, 2)
		var sum float32
		for range max(n, 0) {
			if e.call == callMathDieRoll {
				sum += molangRandom(m, lo, hi)
			} else {
				sum += molangRandomInt(m, lo, hi)
			}
		}
		return sum
	case callMathClamp:
		return min(max(e.arg(m, 0), e.arg(m, 1)), e.arg(m, 2))
	case callMathMin:
		return min(e.arg(m, 0), e.arg(m, 1))
	case callMathMax:
		return max(e.arg(m, 0), e.arg(m, 1))
	case callMathAbs:
		return float32(math.Abs(float64(e.arg(m, 0))))
	case callMathFloor:
		return float32(math.Floor(float64(e.arg(m, 0))))
	case callMathCeil:
		return float32(math.Ceil(float64(e.arg(m, 0))))
	case callMathRound:
		return float32(math.Round(float64(e.arg(m, 0))))
	case callMathTrunc:
		return float32(math.Trunc(float64(e.arg(m, 0))))
	case callMathSqrt:
		return float32(math.Sqrt(float64(e.arg(m, 0))))
	case callMathMod:
		return float32(math.Mod(float64(e.arg(m, 0)), float64(e.arg(m, 1))))
	case callMathPow:
		return float32(math.Pow(float64(e.arg(m, 0)), float64(e.arg(m, 1))))
	case callMathLerp:
		a, b, t := e.arg(m, 0), e.arg(m, 1), e.arg(m, 2)
		return a + (b-a)*t
	case callMathPi:
		return math.Pi
	}
	return e.query(m)
}

func (e *molangExpr) query(m *Mob) float32 {
	switch e.call {
	case callLastHitByPlayer:
		return molangBool(m.playerHitTime > 0)
	case callIsBaby:
		return molangBool(m.Flag(behavior.FlagBaby))
	case callHasAnyFamily:
		for _, a := range e.args {
			if slices.Contains(m.families, a.str) {
				return 1
			}
		}
		return 0
	case callHeadInWater:
		return molangBool(m.UnderWater())
	case callIsInWater:
		return molangBool(m.InWater())
	case callHadComponentGroup:
		return molangBool(m.hasGroup(e.args[0].str))
	case callIsOnGround:
		return molangBool(m.OnGround())
	case callIsOnFire:
		return molangBool(m.Flag(flagOnFire))
	case callProperty:
		if v, ok := m.inst.Property(e.args[0].str); ok && !v.IsString {
			return v.Number
		}
		return 0
	case callVariant, callMarkVariant, callSkinID:
		comp := "minecraft:variant"
		switch e.call {
		case callMarkVariant:
			comp = "minecraft:mark_variant"
		case callSkinID:
			comp = "minecraft:skin_id"
		}
		v, _ := intComponent(m.inst.Components(), comp)
		return float32(v)
	case callAngerLevel:
		// With or without an actor (the data passes this), the anger at the most annoying
		// nuisance; not checked against BDS's query.
		if m.states != nil && m.states.AngerLevel != nil {
			return float32(m.states.AngerLevel.Anger())
		}
		return 0
	case callHealth:
		return m.health
	case callMaxHealth:
		return m.maxHealth
	}
	// equipment_count, is_using_item, player_level: nothing a mob has yet.
	return 0
}

// hasGroup reports a component group added and not removed since.
func (m *Mob) hasGroup(name string) bool {
	has := false
	for _, s := range m.inst.Stack() {
		if s.Group == name {
			has = s.Added
		}
	}
	return has
}
