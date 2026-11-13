package command

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl64"
	"github.com/helix-bedrock/protocol"
)

// testSource is a source at 10 64 -5 that finds one player, "Steve".
type testSource struct {
	perm Permission
	got  []Selector
}

type namedTarget string

func (n namedTarget) Name() string { return string(n) }

func (s *testSource) Name() string                   { return "tester" }
func (s *testSource) Permission() Permission         { return s.perm }
func (s *testSource) Position() mgl64.Vec3           { return mgl64.Vec3{10, 64, -5} }
func (s *testSource) Rotation() (yaw, pitch float32) { return 0, 0 }
func (s *testSource) Targets(sel Selector) []Target {
	s.got = append(s.got, sel)
	if sel.Kind != "" || strings.EqualFold(sel.Name, "steve") {
		return []Target{namedTarget("Steve")}
	}
	return nil
}

// mode is a plugin's enum.
type mode string

func (mode) Type() string            { return "TestMode" }
func (mode) Options(Source) []string { return []string{"easy", "hard"} }

// warp is a dynamic enum.
type warp string

func (warp) Type() string { return "Warp" }
func (warp) Dynamic()     {}

type setTime struct {
	Set    Literal `cmd:"set"`
	Amount int
}

func (c setTime) Run(_ Source, out *Output) { out.Printf("set %d", c.Amount) }

type giveCmd struct {
	Player Targets
	Item   Item `cmd:"itemName"`
	Amount Optional[int32]
}

func (c giveCmd) Run(_ Source, out *Output) {
	out.Printf("%s %s %d %v", c.Player.List[0].Name(), c.Item, c.Amount.Value, c.Amount.Set)
}

type tpCmd struct {
	To Position `cmd:"destination"`
}

func (c tpCmd) Run(src Source, out *Output) { out.Printf("%v", c.To.Resolve(src)) }

type modeCmd struct{ Mode mode }

func (c modeCmd) Run(_ Source, out *Output) { out.Print(string(c.Mode)) }

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(nil)
	m.SetEnumOptions("Item", []string{"apple", "minecraft:apple"})
	for _, s := range []Spec{
		{Name: "time", Overloads: []Runnable{setTime{}}, Permission: GameDirectors},
		{Name: "give", Overloads: []Runnable{giveCmd{}}},
		{Name: "tp", Aliases: []string{"teleport"}, Overloads: []Runnable{tpCmd{}}},
		{Name: "mode", Overloads: []Runnable{modeCmd{}}},
	} {
		if err := m.Add("test", s); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// first is the first line of an output.
func first(o *Output) Message {
	if m := o.Messages(); len(m) > 0 {
		return m[0]
	}
	return Message{}
}

func TestExecute(t *testing.T) {
	m := testManager(t)
	op := &testSource{perm: Owner}
	cases := map[string]string{
		"/time set 100":                "set 100",
		"give Steve apple":             "Steve apple 0 false",
		"give @a[r=5] apple 12":        "Steve apple 12 true",
		"tp 1 2 3":                     "[1 2 3]",
		"teleport ~1 ~ ~-1":            "[11 64 -6]",
		"tp ~~1~":                      "[10 65 -5]",
		"mode HARD":                    "hard",
		`give "Steve" minecraft:apple`: "Steve minecraft:apple 0 false",
	}
	for line, want := range cases {
		if got := first(m.Execute(op, line)); !got.Success || got.Text != want {
			t.Errorf("%q: %+v, want %q", line, got, want)
		}
	}
	found := false
	for _, sel := range op.got {
		found = found || sel.Kind == "a" && len(sel.Args) == 1 && sel.Args[0] == (SelectorArg{Key: "r", Value: "5"})
	}
	if !found {
		t.Errorf("selectors %+v", op.got)
	}
}

func TestExecuteErrors(t *testing.T) {
	m := testManager(t)
	cases := []struct {
		perm   Permission
		line   string
		key    string
		params []string
	}{
		{Any, "time set 1", keyUnknown, []string{"time"}},
		{Owner, "nope", keyUnknown, []string{"nope"}},
		{Owner, "time set x", keyNum, []string{"x"}},
		{Owner, "time sett 1", keySyntax, []string{"/time ", "sett", " 1"}},
		{Owner, "time set 1 2", keySyntax, []string{"/time set 1 ", "2", ""}},
		{Owner, "give Alex apple", keyNoPlayer, nil},
		{Owner, "give Steve pear", keySyntax, []string{"/give Steve ", "pear", ""}},
		{Owner, "tp 1 2", keySyntax, []string{"/tp 1 2", "", ""}},
		{Owner, "tp ^1 2 3", keySyntax, []string{"/tp ^1 ", "2", " 3"}},
	}
	for _, c := range cases {
		got := first(m.Execute(&testSource{perm: c.perm}, c.line))
		if got.Success || got.Text != c.key || strings.Join(got.Params, "|") != strings.Join(c.params, "|") {
			t.Errorf("%q: %+v, want %s %q", c.line, got, c.key, c.params)
		}
	}
}

func TestBuilderAndReplace(t *testing.T) {
	m := testManager(t)
	c, err := NewBuilder("warp", "Goes to a warp.").Alias("w").Overload(func(_ Source, a Args, out *Output) {
		out.Printf("%s %d %v", a.String("name"), a.Int("times"), a.Has("times"))
	}, Lit("go"), Param("name", warp("")), Opt("times", 0)).Build()
	if err != nil {
		t.Fatal(err)
	}
	m.Register("plugin", c)
	m.SetEnumOptions("Warp", []string{"spawn", "shop"})
	if got := first(m.Execute(&testSource{}, "w go shop")); got.Text != "shop 0 false" {
		t.Fatalf("warp: %+v", got)
	}
	// Replacing tp also takes its alias away.
	if err := m.Add("plugin", Spec{Name: "tp", Overloads: []Runnable{modeCmd{}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Command("teleport"); ok {
		t.Error("old alias kept")
	}
	if !slicesEqual(m.DynamicEnums(), []string{"Warp"}) {
		t.Errorf("dynamic %v", m.DynamicEnums())
	}
}

func TestPacket(t *testing.T) {
	m := testManager(t)
	pk := m.Packet(&testSource{perm: Any})
	names := map[string]protocol.Command{}
	for _, c := range pk.Commands {
		names[c.Name] = c
	}
	if _, ok := names["time"]; ok {
		t.Error("an operator command sent to a visitor")
	}
	give := names["give"].Overloads[0].Parameters
	item := give[1]
	if item.Name != "itemName" || item.Type&protocol.CommandArgEnum == 0 || pk.Enums[item.Type&0xffff].Type != "Item" {
		t.Fatalf("item param %+v", item)
	}
	if !give[2].Optional || give[0].Type != protocol.CommandArgValid|protocol.CommandArgTypeTarget {
		t.Errorf("give params %+v", give)
	}
	tp := names["tp"]
	if tp.AliasesOffset == ^uint32(0) || pk.Enums[tp.AliasesOffset].Type != "tpCommandAliases" {
		t.Errorf("aliases %+v", tp)
	}
}

func TestPanicRecovered(t *testing.T) {
	m := NewManager(nil)
	c, _ := NewBuilder("boom", "").Overload(func(Source, Args, *Output) { panic("x") }).Build()
	m.Register("plugin", c)
	if got := first(m.Execute(&testSource{}, "boom")); got.Text != keyException {
		t.Fatalf("%+v", got)
	}
}

func slicesEqual(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }
