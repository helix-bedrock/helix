package command

import (
	"github.com/helix-bedrock/protocol"
	"github.com/helix-bedrock/protocol/packet"
)

// listBuilder puts together one source's AvailableCommands.
type listBuilder struct {
	pk         *packet.AvailableCommands
	valueIndex map[string]uint32
	enumIndex  map[string]uint32
	softIndex  map[string]uint32
}

// value is the index of an enum value, added once.
func (b *listBuilder) value(v string) uint32 {
	if i, ok := b.valueIndex[v]; ok {
		return i
	}
	i := uint32(len(b.pk.EnumValues)) //nolint:gosec // far fewer than 2^32
	b.pk.EnumValues = append(b.pk.EnumValues, v)
	b.valueIndex[v] = i
	return i
}

// enum is the index of an enum, added the first time its type is seen.
func (b *listBuilder) enum(enumType string, values []string) uint32 {
	if i, ok := b.enumIndex[enumType]; ok {
		return i
	}
	e := protocol.CommandEnum{Type: enumType}
	for _, v := range values {
		e.ValueIndices = append(e.ValueIndices, b.value(v))
	}
	i := uint32(len(b.pk.Enums)) //nolint:gosec // few enums
	b.pk.Enums = append(b.pk.Enums, e)
	b.enumIndex[enumType] = i
	return i
}

// soft is the index of a soft enum.
func (b *listBuilder) soft(enumType string, values []string) uint32 {
	if i, ok := b.softIndex[enumType]; ok {
		return i
	}
	i := uint32(len(b.pk.DynamicEnums)) //nolint:gosec // few enums
	b.pk.DynamicEnums = append(b.pk.DynamicEnums, protocol.DynamicEnum{Type: enumType, Values: values})
	b.softIndex[enumType] = i
	return i
}

// argTypes are the protocol's types for the plain kinds.
var argTypes = map[kind]uint32{
	kindInt: protocol.CommandArgTypeInt, kindFloat: protocol.CommandArgTypeFloat, kindString: protocol.CommandArgTypeString,
	kindText: protocol.CommandArgTypeMessage, kindPosition: protocol.CommandArgTypePosition,
	kindBlockPosition: protocol.CommandArgTypeBlockPosition, kindTargets: protocol.CommandArgTypeTarget,
}

// param is the protocol's form of an argument.
func (b *listBuilder) param(m *Manager, t *table, src Source, p param) protocol.CommandParameter {
	out := protocol.CommandParameter{Name: p.name, Optional: p.optional}
	switch p.kind {
	case kindBool:
		out.Type = protocol.CommandArgValid | protocol.CommandArgEnum | b.enum(EnumBoolean, []string{"true", "false"})
	case kindLiteral:
		out.Type = protocol.CommandArgValid | protocol.CommandArgEnum | b.enum(p.literal, []string{p.literal})
	case kindEnum:
		values := m.optionsOf(t, p.enum, src)
		if isDynamic(p.enum) {
			out.Type = protocol.CommandArgValid | protocol.CommandArgSoftEnum | b.soft(p.enum.Type(), values)
		} else {
			out.Type = protocol.CommandArgValid | protocol.CommandArgEnum | b.enum(p.enum.Type(), values)
		}
	default:
		out.Type = protocol.CommandArgValid | argTypes[p.kind]
	}
	return out
}

// Visible reports whether a source may see and run a command.
func Visible(c *Command, src Source) bool {
	return !c.hidden && c.permission != Internal && src.Permission() >= c.permission
}

// Packet is the command list a source is sent: only the commands it may run, with the
// enum options it would be offered.
func (m *Manager) Packet(src Source) *packet.AvailableCommands {
	t := m.state.Load()
	b := &listBuilder{pk: &packet.AvailableCommands{}, valueIndex: map[string]uint32{}, enumIndex: map[string]uint32{}, softIndex: map[string]uint32{}}
	for _, c := range t.commands {
		if !Visible(c, src) {
			continue
		}
		pc := protocol.Command{Name: c.name, Description: c.description, PermissionLevel: byte(c.permission), AliasesOffset: ^uint32(0)}
		if len(c.aliases) > 0 {
			// BDS's alias enum, as registerAlias names it; it holds the aliases.
			pc.AliasesOffset = b.enum(c.name+"CommandAliases", c.aliases)
		}
		for _, o := range c.overloads {
			po := protocol.CommandOverload{}
			for _, p := range o.params {
				po.Parameters = append(po.Parameters, b.param(m, t, src, p))
			}
			pc.Overloads = append(pc.Overloads, po)
		}
		b.pk.Commands = append(b.pk.Commands, pc)
	}
	return b.pk
}

// SoftEnumUpdate is the packet that gives a source a dynamic enum's options now.
func (m *Manager) SoftEnumUpdate(enumType string, src Source) *packet.UpdateSoftEnum {
	return &packet.UpdateSoftEnum{EnumType: enumType, Options: m.EnumOptions(enumType, src), ActionType: packet.SoftEnumActionSet}
}
