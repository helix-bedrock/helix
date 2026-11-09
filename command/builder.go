package command

import (
	"fmt"
	"reflect"
)

// Builder makes a command in code, for commands only known while running: ones a server
// owner writes in a config file, say. It makes the same Command a Spec does.
type Builder struct {
	name, description string
	aliases           []string
	permission        Permission
	hidden            bool
	overloads         []builtOverload
}

// builtOverload is an overload as the Builder was given it.
type builtOverload struct {
	args []Arg
	run  func(src Source, args Args, out *Output)
}

// Arg is one argument for a Builder overload: Param, Opt or Lit.
type Arg struct {
	name     string
	example  any
	optional bool
	literal  bool
}

// Param is a required argument; example is a value of its type (0, "", Position{},
// Targets{}, Item(""), a plugin's enum type...), as a struct field would have.
func Param(name string, example any) Arg { return Arg{name: name, example: example} }

// Opt is an argument that may be left out. Optional arguments come last.
func Opt(name string, example any) Arg { return Arg{name: name, example: example, optional: true} }

// Lit is a fixed word, such as "set".
func Lit(word string) Arg { return Arg{name: word, literal: true} }

// Args are a Builder overload's values by name. A left out optional argument is missing.
type Args map[string]any

// Has reports whether an argument was given.
func (a Args) Has(name string) bool { _, ok := a[name]; return ok }

// Int is an int argument, 0 if missing.
func (a Args) Int(name string) int { v, _ := a[name].(int64); return int(v) }

// Float is a float argument, 0 if missing.
func (a Args) Float(name string) float64 { v, _ := a[name].(float64); return v }

// Bool is a bool argument, false if missing.
func (a Args) Bool(name string) bool { v, _ := a[name].(bool); return v }

// String is a string, Text or enum argument, "" if missing.
func (a Args) String(name string) string {
	switch v := a[name].(type) {
	case string:
		return v
	case Text:
		return string(v)
	}
	return ""
}

// Position is a Position argument.
func (a Args) Position(name string) Position { v, _ := a[name].(Position); return v }

// RelativeFloat is a RelativeFloat argument.
func (a Args) RelativeFloat(name string) RelativeFloat { v, _ := a[name].(RelativeFloat); return v }

// BlockPosition is a BlockPosition argument.
func (a Args) BlockPosition(name string) BlockPosition {
	v, _ := a[name].(BlockPosition)
	return v
}

// Targets is a Targets argument.
func (a Args) Targets(name string) Targets { v, _ := a[name].(Targets); return v }

// NewBuilder starts a command.
func NewBuilder(name, description string) *Builder {
	return &Builder{name: name, description: description}
}

// Alias adds other names.
func (b *Builder) Alias(names ...string) *Builder {
	b.aliases = append(b.aliases, names...)
	return b
}

// Permission sets the level the command needs.
func (b *Builder) Permission(p Permission) *Builder {
	b.permission = p
	return b
}

// Hide keeps the command out of the command list.
func (b *Builder) Hide() *Builder {
	b.hidden = true
	return b
}

// Overload adds a way to call the command.
func (b *Builder) Overload(run func(src Source, args Args, out *Output), args ...Arg) *Builder {
	b.overloads = append(b.overloads, builtOverload{args: args, run: run})
	return b
}

// Build checks the command and makes it.
func (b *Builder) Build() (*Command, error) {
	c, err := newCommand(b.name, b.description, b.aliases, b.permission, b.hidden)
	if err != nil {
		return nil, err
	}
	if len(b.overloads) == 0 {
		return nil, fmt.Errorf("command: %s: no overloads", b.name)
	}
	for i, bo := range b.overloads {
		if bo.run == nil {
			return nil, fmt.Errorf("command: %s: overload %d: nothing to run", b.name, i)
		}
		params := make([]param, len(bo.args))
		for j, a := range bo.args {
			t, literal := typeLiteral, a.name
			if !a.literal {
				if a.example == nil {
					return nil, fmt.Errorf("command: %s: overload %d: argument %s has no example", b.name, i, a.name)
				}
				t, literal = reflect.TypeOf(a.example), ""
			}
			p, err := paramOf(a.name, t, literal)
			if err != nil {
				return nil, fmt.Errorf("command: %s: overload %d: %w", b.name, i, err)
			}
			p.optional = p.optional || a.optional
			params[j] = p
		}
		if err := checkParams(params); err != nil {
			return nil, fmt.Errorf("command: %s: overload %d: %w", b.name, i, err)
		}
		run := bo.run
		c.overloads = append(c.overloads, overload{params: params, run: func(src Source, values []reflect.Value, out *Output) {
			args := Args{}
			for k, v := range values {
				if v.IsValid() && params[k].kind != kindLiteral {
					args[params[k].name] = v.Interface()
				}
			}
			run(src, args, out)
		}})
	}
	return c, nil
}
