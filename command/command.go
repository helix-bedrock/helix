// Package command is Helix's command system: commands are declared once, as Go structs or
// with a Builder, and the package turns them into what the client shows (the names,
// overloads, argument types and suggestions of BDS's AvailableCommands packet), parses
// what a player types against them with BDS's error messages, checks permissions and
// runs them. Vanilla commands and plugin commands use it the same way, with no namespace
// prefix: a plugin's "home" is /home.
//
// The engine gives each command a Source (who runs it) and sends the Output back. The
// package knows nothing about players or the world, so it can be used and tested alone.
package command

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
)

// Permission is the command permission level a source needs, BDS's levels.
type Permission uint8

// BDS's command permission levels, lowest first.
const (
	// Any lets everyone run the command.
	Any Permission = iota
	// GameDirectors are operators in game.
	GameDirectors
	// Admin is for commands such as kick and op.
	Admin
	// Host is the world's host.
	Host
	// Owner is the server owner: the console.
	Owner
	// Internal commands are never shown or run from a player.
	Internal
)

// Source is who runs a command: a player, the console, a command block or a plugin. The
// engine implements it.
type Source interface {
	// Name is the source's name in messages.
	Name() string
	// Permission is the source's command permission level.
	Permission() Permission
	// Position is where the source is, for relative coordinates and selectors.
	Position() mgl32.Vec3
	// Rotation is which way the source looks, yaw and pitch in degrees, for ^ coordinates.
	Rotation() (yaw, pitch float32)
	// Targets resolves a selector, or a name, as seen from the source.
	Targets(s Selector) []Target
}

// Target is something a selector found. The engine's players and entities implement it;
// commands type-assert to the kind they want.
type Target interface {
	// Name is the target's name in messages.
	Name() string
}

// Runnable is one way to call a command, an overload: a struct whose exported fields are
// its arguments, in order. Run is called on a copy filled with what was typed.
//
// Field types: int kinds, float32, float64, bool, string (one word or quoted), Text (the
// rest of the line), Position, BlockPosition, Targets, Literal (a fixed word, given in the
// field's cmd tag), Item, Block, any string type implementing Enum, and Optional of any
// of these. The tag `cmd:"name"` names the argument; without it the field's name is used,
// starting lower case.
type Runnable interface {
	Run(src Source, out *Output)
}

// Spec declares a command made of struct overloads.
type Spec struct {
	// Name is what players type after the slash.
	Name string
	// Description is shown in the command list and /help.
	Description string
	// Aliases are other names for the command.
	Aliases []string
	// Permission is the level a source needs to see and run the command.
	Permission Permission
	// Hidden commands run but are never sent in the command list.
	Hidden bool
	// Overloads are the ways to call it, tried in order.
	Overloads []Runnable
}

// Command is a command ready to register, from a Spec (Define) or a Builder (Build).
type Command struct {
	name, description string
	aliases           []string
	permission        Permission
	hidden            bool
	overloads         []overload
	owner             string
}

// Name is the command's name.
func (c *Command) Name() string { return c.name }

// Description is the command's description.
func (c *Command) Description() string { return c.description }

// Aliases are the command's other names.
func (c *Command) Aliases() []string { return slices.Clone(c.aliases) }

// Permission is the level the command needs.
func (c *Command) Permission() Permission { return c.permission }

// Owner is the plugin that registered the command, "minecraft" for vanilla.
func (c *Command) Owner() string { return c.owner }

// Define checks a Spec and makes it a Command.
func Define(spec Spec) (*Command, error) {
	c, err := newCommand(spec.Name, spec.Description, spec.Aliases, spec.Permission, spec.Hidden)
	if err != nil {
		return nil, err
	}
	if len(spec.Overloads) == 0 {
		return nil, fmt.Errorf("command: %s: no overloads", spec.Name)
	}
	for i, r := range spec.Overloads {
		o, err := structOverload(r)
		if err != nil {
			return nil, fmt.Errorf("command: %s: overload %d: %w", spec.Name, i, err)
		}
		c.overloads = append(c.overloads, o)
	}
	return c, nil
}

// newCommand checks the parts every command has.
func newCommand(name, description string, aliases []string, perm Permission, hidden bool) (*Command, error) {
	if !validName(name) {
		return nil, fmt.Errorf("command: %q is not a valid name", name)
	}
	for _, a := range aliases {
		if !validName(a) {
			return nil, fmt.Errorf("command: %s: alias %q is not a valid name", name, a)
		}
	}
	if perm > Internal {
		return nil, fmt.Errorf("command: %s: no permission level %d", name, perm)
	}
	return &Command{name: strings.ToLower(name), description: description, aliases: lowerAll(aliases), permission: perm, hidden: hidden}, nil
}

// validName reports whether a name can be typed: one word, no namespace.
func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t\n:/\"")
}

// lowerAll is the strings in lower case.
func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// Call is a command being run, as middleware sees it.
type Call struct {
	// Command is what runs.
	Command *Command
	// Source is who runs it.
	Source Source
	// Line is what was typed, without the slash.
	Line string
	// Output is what the source is told.
	Output *Output
}

// Middleware runs around every command: next runs the command (or the next middleware).
// Not calling next stops the command.
type Middleware func(c *Call, next func())

// ErrNotFound is returned for a name no command has.
var ErrNotFound = errors.New("command: not found")

// Manager holds the commands. Reads are lock free; Register, Remove, Wrap and the enum
// setters may be called from anywhere, at boot or while running, and tell the engine
// through OnChange so online players get the new list.
type Manager struct {
	log   *slog.Logger
	mu    sync.Mutex
	state atomic.Pointer[table]
	// changed is told when commands change, enum when an enum's options do.
	changed atomic.Pointer[func()]
	enum    atomic.Pointer[func(enumType string)]
}

// table is one version of the commands, never changed once published.
type table struct {
	byName     map[string]*Command
	commands   []*Command
	middleware []Middleware
	options    map[string][]string
}

// NewManager returns an empty Manager. log may be nil.
func NewManager(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	m := &Manager{log: log}
	options := make(map[string][]string, len(vanillaEnums))
	for k, v := range vanillaEnums {
		options[k] = v
	}
	m.state.Store(&table{byName: map[string]*Command{}, options: options})
	return m
}

// OnChange sets what is told when commands change. The engine resends the command list.
func (m *Manager) OnChange(fn func()) { m.changed.Store(&fn) }

// OnEnumChange sets what is told when a dynamic enum's options change. The engine sends
// the new options to online players.
func (m *Manager) OnEnumChange(fn func(enumType string)) { m.enum.Store(&fn) }

// edit publishes a changed copy of the table.
func (m *Manager) edit(fn func(t *table)) {
	m.mu.Lock()
	old := m.state.Load()
	t := &table{byName: make(map[string]*Command, len(old.byName)), commands: slices.Clone(old.commands), middleware: slices.Clone(old.middleware), options: make(map[string][]string, len(old.options))}
	for k, v := range old.byName {
		t.byName[k] = v
	}
	for k, v := range old.options {
		t.options[k] = v
	}
	fn(t)
	m.state.Store(t)
	m.mu.Unlock()
}

// notify tells the engine the commands changed.
func (m *Manager) notify() {
	if fn := m.changed.Load(); fn != nil {
		(*fn)()
	}
}

// Register adds a command for owner. A command of the same name, or one of its aliases,
// is replaced, and the log says which owner replaced which.
func (m *Manager) Register(owner string, c *Command) {
	if c == nil {
		return
	}
	cp := *c
	cp.owner = owner
	m.edit(func(t *table) {
		for _, name := range append([]string{cp.name}, cp.aliases...) {
			if old, ok := t.byName[name]; ok {
				m.log.Info("command replaced", "command", name, "by", owner, "was", old.owner)
				removeFrom(t, old)
			}
		}
		t.commands = append(t.commands, &cp)
		for _, name := range append([]string{cp.name}, cp.aliases...) {
			t.byName[name] = &cp
		}
	})
	m.notify()
}

// Add defines and registers a Spec for owner.
func (m *Manager) Add(owner string, spec Spec) error {
	c, err := Define(spec)
	if err != nil {
		return err
	}
	m.Register(owner, c)
	return nil
}

// removeFrom takes a command and all its names out of a table.
func removeFrom(t *table, c *Command) {
	t.commands = slices.DeleteFunc(t.commands, func(x *Command) bool { return x == c })
	for k, v := range t.byName {
		if v == c {
			delete(t.byName, k)
		}
	}
}

// Remove takes away the command a name or alias belongs to.
func (m *Manager) Remove(name string) error {
	found := false
	m.edit(func(t *table) {
		if c, ok := t.byName[strings.ToLower(name)]; ok {
			removeFrom(t, c)
			found = true
		}
	})
	if !found {
		return ErrNotFound
	}
	m.notify()
	return nil
}

// Command is the command a name or alias belongs to.
func (m *Manager) Command(name string) (*Command, bool) {
	c, ok := m.state.Load().byName[strings.ToLower(name)]
	return c, ok
}

// Commands are all registered commands, in the order registered.
func (m *Manager) Commands() []*Command { return slices.Clone(m.state.Load().commands) }

// Wrap adds middleware around every command; the last added runs first.
func (m *Manager) Wrap(mw Middleware) {
	if mw == nil {
		return
	}
	m.edit(func(t *table) { t.middleware = append(t.middleware, mw) })
}

// SetEnumOptions sets the options of an enum type for everyone, for enums whose type
// does not give its own (EnumOptions). Item and Block are filled by the engine. A dynamic
// enum's new options are sent to online players at once; any other's with the next list.
func (m *Manager) SetEnumOptions(enumType string, options []string) {
	m.edit(func(t *table) { t.options[enumType] = slices.Clone(options) })
	m.RefreshEnum(enumType)
}

// RefreshEnum sends the options of a dynamic enum to online players again, after what
// its Options method answers changed.
func (m *Manager) RefreshEnum(enumType string) {
	if fn := m.enum.Load(); fn != nil {
		(*fn)(enumType)
	}
}

// EnumOptions are an enum type's options for a source: its own Options if it has them,
// else those set with SetEnumOptions.
func (m *Manager) EnumOptions(enumType string, src Source) []string {
	t := m.state.Load()
	for _, c := range t.commands {
		for _, o := range c.overloads {
			for _, p := range o.params {
				if p.enum != nil && p.enum.Type() == enumType {
					return m.optionsOf(t, p.enum, src)
				}
			}
		}
	}
	return slices.Clone(t.options[enumType])
}

// optionsOf is an enum's options for a source.
func (m *Manager) optionsOf(t *table, e Enum, src Source) []string {
	if o, ok := e.(EnumOptions); ok {
		return o.Options(src)
	}
	return t.options[e.Type()]
}

// DynamicEnums are the dynamic enum types any command uses.
func (m *Manager) DynamicEnums() []string {
	var out []string
	for _, c := range m.state.Load().commands {
		for _, o := range c.overloads {
			for _, p := range o.params {
				if p.enum != nil && isDynamic(p.enum) && !slices.Contains(out, p.enum.Type()) {
					out = append(out, p.enum.Type())
				}
			}
		}
	}
	return out
}

// Execute runs a line a source typed, without or with the slash, and returns what the
// source is told. It never panics on a command's behalf: a panic in Run is recovered,
// logged with the command's owner, and reported to the source as BDS's unknown error.
func (m *Manager) Execute(src Source, line string) *Output {
	out := &Output{}
	line = strings.TrimPrefix(strings.TrimSpace(line), "/")
	name, rest, _ := strings.Cut(line, " ")
	t := m.state.Load()
	c, ok := t.byName[strings.ToLower(name)]
	if !ok || src.Permission() < c.permission || c.permission == Internal && src.Permission() != Internal {
		out.Fail(keyUnknown, name)
		return out
	}
	run, err := c.match(&matcher{m: m, t: t, src: src}, name, rest)
	if err != nil {
		err.report(out)
		return out
	}
	call := &Call{Command: c, Source: src, Line: line, Output: out}
	next := func() { m.safeRun(c, func() { run(src, out) }, out) }
	for _, mw := range t.middleware {
		inner, mw := next, mw
		next = func() { m.safeRun(c, func() { mw(call, inner) }, out) }
	}
	next()
	return out
}

// safeRun runs fn, turning a panic into a logged error.
func (m *Manager) safeRun(c *Command, fn func(), out *Output) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("command panicked", "command", c.name, "owner", c.owner, "panic", r)
			out.Fail(keyException)
		}
	}()
	fn()
}

// BDS's message keys, from its en_US.lang.
const (
	keyUnknown   = "commands.generic.unknown"
	keySyntax    = "commands.generic.syntax"
	keyException = "commands.generic.exception"
	keyNum       = "commands.generic.num.invalid"
	keyBool      = "commands.generic.boolean.invalid"
	keyNoTarget  = "commands.generic.noTargetMatch"
	keyNoPlayer  = "commands.generic.player.notFound"
)
