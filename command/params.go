package command

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-gl/mathgl/mgl32"
)

// Text is the rest of the line, spaces and all: a chat message, say.
type Text string

// Literal is a fixed word, such as "set" in /time set. Its word is the field's cmd tag.
type Literal struct{}

// Coord is one coordinate as typed: a number, ~ (from the source) or ^ (along where the
// source looks), with an offset.
type Coord struct {
	Value float64
	// Relative is ~, Local is ^.
	Relative, Local bool
}

// Position is three coordinates, such as "~ ~1 ~" or "10 64 -3".
type Position struct{ X, Y, Z Coord }

// Resolve is the position as seen from a source.
func (p Position) Resolve(src Source) mgl32.Vec3 {
	at := src.Position()
	if p.X.Local {
		yaw, pitch := src.Rotation()
		return localOffset(at, yaw, pitch, p.X.Value, p.Y.Value, p.Z.Value)
	}
	return mgl32.Vec3{coordAt(p.X, at[0]), coordAt(p.Y, at[1]), coordAt(p.Z, at[2])}
}

// coordAt is one coordinate from where the source is.
func coordAt(c Coord, from float32) float32 {
	if c.Relative {
		return from + float32(c.Value)
	}
	return float32(c.Value)
}

// localOffset moves left, up and forward from a point by where it looks, as ^ does.
func localOffset(at mgl32.Vec3, yaw, pitch float32, left, up, forward float64) mgl32.Vec3 {
	y, p := float64(yaw)*math.Pi/180, float64(pitch)*math.Pi/180
	fwd := mgl32.Vec3{float32(-math.Sin(y) * math.Cos(p)), float32(-math.Sin(p)), float32(math.Cos(y) * math.Cos(p))}
	upv := mgl32.Vec3{float32(-math.Sin(y) * -math.Sin(p)), float32(math.Cos(p)), float32(math.Cos(y) * -math.Sin(p))}
	lft := upv.Cross(fwd)
	return at.Add(lft.Mul(float32(left))).Add(upv.Mul(float32(up))).Add(fwd.Mul(float32(forward)))
}

// RelativeFloat is one number that may be relative with ~, such as a rotation: BDS's
// RelativeFloat, sent as its "value" argument type.
type RelativeFloat struct {
	Value    float64
	Relative bool
}

// Resolve is the number from a starting value: itself, or from plus it for ~.
func (f RelativeFloat) Resolve(from float32) float32 {
	if f.Relative {
		return from + float32(f.Value)
	}
	return float32(f.Value)
}

// BlockPosition is three whole coordinates, a block.
type BlockPosition struct{ X, Y, Z Coord }

// Resolve is the block as seen from a source.
func (p BlockPosition) Resolve(src Source) [3]int {
	v := Position(p).Resolve(src)
	return [3]int{int(math.Floor(float64(v[0]))), int(math.Floor(float64(v[1]))), int(math.Floor(float64(v[2])))}
}

// Targets are what a selector (@a, @p[r=5]) or a name found. The engine resolves them, so
// List holds its players and entities.
type Targets struct {
	Selector Selector
	List     []Target
}

// Enum is a string type whose values come from a list, shown as suggestions. Type names
// the list in the packet; types with the same name share it.
type Enum interface {
	Type() string
}

// EnumOptions is an Enum that gives its own options, per source if it wants: only a
// player's own homes, say. Without it the options are those set on the Manager.
type EnumOptions interface {
	Enum
	Options(src Source) []string
}

// DynamicEnum is an Enum whose options change while running (players online, warps): it
// is sent as BDS's soft enum, and Manager.RefreshEnum sends new options at once.
type DynamicEnum interface {
	Enum
	Dynamic()
}

// isDynamic reports whether an enum is a soft one.
func isDynamic(e Enum) bool {
	_, ok := e.(DynamicEnum)
	return ok
}

// Item is an item's name, from the game's Item list, which clients show with icons.
type Item string

// Type is BDS's name of the item list.
func (Item) Type() string { return EnumItem }

// Block is a block's name, from the game's Block list.
type Block string

// Type is BDS's name of the block list.
func (Block) Type() string { return EnumBlock }

// Optional is an argument that may be left out; Set says whether it was typed. Optional
// arguments come after all the others.
type Optional[T any] struct {
	Value T
	Set   bool
}

// optionalValue lets the reader find Optional's value type.
func (Optional[T]) optionalValue() reflect.Type { return reflect.TypeFor[T]() }

// optionalField is what Optional has.
type optionalField interface{ optionalValue() reflect.Type }

// kind is what an argument is, which decides how it is typed and sent.
type kind uint8

const (
	kindInt kind = iota
	kindFloat
	kindBool
	kindString
	kindText
	kindPosition
	kindBlockPosition
	kindRelativeFloat
	kindTargets
	kindLiteral
	kindEnum
)

// param is one argument of an overload.
type param struct {
	name     string
	kind     kind
	optional bool
	literal  string
	enum     Enum
	// typ is the value's Go type.
	typ reflect.Type
}

// overload is one way to call a command.
type overload struct {
	params []param
	// run is called with the parsed values, in params order; a left out optional is the
	// zero reflect.Value.
	run func(src Source, values []reflect.Value, out *Output)
}

var (
	typeText          = reflect.TypeFor[Text]()
	typeLiteral       = reflect.TypeFor[Literal]()
	typePosition      = reflect.TypeFor[Position]()
	typeBlockPosition = reflect.TypeFor[BlockPosition]()
	typeTargets       = reflect.TypeFor[Targets]()
	typeRelativeFloat = reflect.TypeFor[RelativeFloat]()
	typeEnum          = reflect.TypeFor[Enum]()
	typeOptional      = reflect.TypeFor[optionalField]()
)

// paramOf works out the argument a Go type is.
func paramOf(name string, t reflect.Type, literal string) (param, error) {
	p := param{name: name, typ: t}
	if t.Implements(typeOptional) {
		inner, _ := reflect.Zero(t).Interface().(optionalField)
		v, err := paramOf(name, inner.optionalValue(), literal)
		if err != nil {
			return param{}, err
		}
		v.optional, v.typ = true, inner.optionalValue()
		return v, nil
	}
	switch {
	case t == typeText:
		p.kind = kindText
	case t == typeLiteral:
		if literal == "" {
			return param{}, fmt.Errorf("argument %s: a Literal needs its word in the cmd tag", name)
		}
		p.kind, p.literal = kindLiteral, strings.ToLower(literal)
	case t == typePosition:
		p.kind = kindPosition
	case t == typeBlockPosition:
		p.kind = kindBlockPosition
	case t == typeTargets:
		p.kind = kindTargets
	case t == typeRelativeFloat:
		p.kind = kindRelativeFloat
	case t.Kind() == reflect.String && t.Implements(typeEnum):
		e, _ := reflect.Zero(t).Interface().(Enum)
		if e.Type() == "" {
			return param{}, fmt.Errorf("argument %s: enum %s has no type name", name, t)
		}
		p.kind, p.enum = kindEnum, e
	default:
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			p.kind = kindInt
		case reflect.Float32, reflect.Float64:
			p.kind = kindFloat
		case reflect.Bool:
			p.kind = kindBool
		case reflect.String:
			p.kind = kindString
		default:
			return param{}, fmt.Errorf("argument %s: %s is not an argument type", name, t)
		}
	}
	return p, nil
}

// checkParams checks an overload's arguments: optional ones last, and Text at the end.
func checkParams(params []param) error {
	optional := false
	for i, p := range params {
		if p.optional {
			optional = true
		} else if optional {
			return fmt.Errorf("argument %s: required after an optional one", p.name)
		}
		if p.kind == kindText && i != len(params)-1 {
			return fmt.Errorf("argument %s: Text takes the rest of the line, so it comes last", p.name)
		}
	}
	return nil
}

// structOverload reads a Runnable's fields as an overload.
func structOverload(r Runnable) (overload, error) {
	t := reflect.TypeOf(r)
	ptr := t.Kind() == reflect.Pointer
	if ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return overload{}, fmt.Errorf("%s is not a struct", t)
	}
	var params []param
	var fields []int
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, tag := argName(f.Name), f.Tag.Get("cmd")
		literal := ""
		if f.Type == typeLiteral {
			literal, name = tag, tag
		} else if tag != "" {
			name = tag
		}
		p, err := paramOf(name, f.Type, literal)
		if err != nil {
			return overload{}, err
		}
		params = append(params, p)
		fields = append(fields, i)
	}
	if err := checkParams(params); err != nil {
		return overload{}, err
	}
	return overload{params: params, run: func(src Source, values []reflect.Value, out *Output) {
		v := reflect.New(t).Elem()
		for i, idx := range fields {
			if !values[i].IsValid() {
				continue
			}
			f := v.Field(idx)
			if params[i].optional {
				f.FieldByName("Value").Set(values[i].Convert(params[i].typ))
				f.FieldByName("Set").SetBool(true)
				continue
			}
			f.Set(values[i].Convert(f.Type()))
		}
		run := v.Interface()
		if ptr {
			run = v.Addr().Interface()
		}
		if r, ok := run.(Runnable); ok {
			r.Run(src, out)
		}
	}}, nil
}

// argName is a field's name starting lower case.
func argName(field string) string {
	r, size := utf8.DecodeRuneInString(field)
	return string(unicode.ToLower(r)) + field[size:]
}
