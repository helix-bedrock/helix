package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/daytime"
	"github.com/helix-bedrock/protocol"
)

// timeCommand is the stand-in for BDS's /time until commands are designed.
const timeCommand = "time"

// The enums /time lists, by the order timeList gives them.
const (
	timeEnumSet = iota
	timeEnumName
	timeEnumAdd
	timeEnumQuery
	timeEnumCount
)

// timeWords are the words /time takes first.
const (
	timeWordSet   = "set"
	timeWordAdd   = "add"
	timeWordQuery = "query"
)

// timeNames are the moments /time set takes, as BDS's command lists them.
var timeNames = []string{"day", "noon", "sunrise", "sunset", "night", "midnight"}

// timeList is what makes the client show and complete /time. firstValue and firstEnum are where
// its enum values and enums start in the AvailableCommands packet.
func timeList(firstValue, firstEnum uint32) (protocol.Command, []string, []protocol.CommandEnum) {
	names := make([]uint32, len(timeNames))
	for i := range names {
		names[i] = firstValue + uint32(i) //nolint:gosec // few names
	}
	after := firstValue + uint32(len(timeNames)) //nolint:gosec // few names
	values := append(append([]string{}, timeNames...), timeWordSet, timeWordAdd, timeWordQuery)
	enums := make([]protocol.CommandEnum, timeEnumCount)
	enums[timeEnumSet] = protocol.CommandEnum{Type: "TimeSet", ValueIndices: []uint32{after}}
	enums[timeEnumName] = protocol.CommandEnum{Type: "TimeSpec", ValueIndices: names}
	enums[timeEnumAdd] = protocol.CommandEnum{Type: "TimeAdd", ValueIndices: []uint32{after + 1}}
	enums[timeEnumQuery] = protocol.CommandEnum{Type: "TimeQuery", ValueIndices: []uint32{after + 2}}
	word := func(name string, enum uint32) protocol.CommandParameter {
		return protocol.CommandParameter{Name: name, Type: protocol.CommandArgValid | protocol.CommandArgEnum | (firstEnum + enum)}
	}
	amount := protocol.CommandParameter{Name: "amount", Type: protocol.CommandArgValid | protocol.CommandArgTypeInt}
	return protocol.Command{
		Name: timeCommand, Description: "Changes or shows the time: set day, set 6000, add 1000, query.",
		PermissionLevel: protocol.CommandPermissionLevelAny, AliasesOffset: ^uint32(0),
		Overloads: []protocol.CommandOverload{
			{Parameters: []protocol.CommandParameter{word("set", timeEnumSet), word("time", timeEnumName)}},
			{Parameters: []protocol.CommandParameter{word("set", timeEnumSet), amount}},
			{Parameters: []protocol.CommandParameter{word("add", timeEnumAdd), amount}},
			{Parameters: []protocol.CommandParameter{word("query", timeEnumQuery)}},
		},
	}, values, enums
}

// timeRun runs /time: set <name or number>, add <number>, query.
func timeRun(s *helix.Server, args []string) string {
	clock := s.Clock()
	if clock == nil {
		return "The world is not open."
	}
	const usage = "Use /time set <day, noon, sunrise, sunset, night, midnight or a number>, /time add <number> or /time query."
	if len(args) == 0 {
		return usage
	}
	switch strings.ToLower(args[0]) {
	case timeWordQuery:
		now := clock.Time()
		return fmt.Sprintf("Time of day: %d, day %d, clock %d.", now%daytime.DayLength, now/daytime.DayLength, now)
	case timeWordSet:
		if len(args) != 2 {
			return usage
		}
		if at, ok := daytime.MarkerTime(strings.ToLower(args[1])); ok {
			s.SetTime(daytime.NextOccurrence(clock.Time(), at))
			return fmt.Sprintf("Set the time to %s.", strings.ToLower(args[1]))
		}
		n, err := strconv.ParseInt(args[1], 10, 32)
		if err != nil || n < 0 {
			return usage
		}
		s.SetTime(int32(n))
		return fmt.Sprintf("Set the time to %d.", n)
	case timeWordAdd:
		if len(args) != 2 {
			return usage
		}
		n, err := strconv.ParseInt(args[1], 10, 32)
		if err != nil || n < 0 {
			return usage
		}
		s.SetTime(daytime.Added(clock.Time(), int32(n)))
		return fmt.Sprintf("Added %d to the time.", n)
	}
	return usage
}
