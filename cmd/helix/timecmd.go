package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/helix-bedrock/helix"
	"github.com/helix-bedrock/helix/daytime"
)

// timeCommand is the stand-in for BDS's /time until commands are designed.
const timeCommand = "time"

// timeWords are the words /time takes first.
const (
	timeWordSet   = "set"
	timeWordAdd   = "add"
	timeWordQuery = "query"
)

// timeNames are the moments /time set takes, as BDS's command lists them.
var timeNames = []string{"day", "noon", "sunrise", "sunset", "night", "midnight"}

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
