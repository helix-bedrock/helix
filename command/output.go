package command

import (
	"fmt"
	"slices"
)

// Message is one line of a command's output: a text or one of the game's translation keys
// with its parameters, shown white for success and red for failure.
type Message struct {
	Success bool
	// Text is the text, or the translation key when Translate is set.
	Text      string
	Translate bool
	Params    []string
}

// Output is what a command tells its source. The engine sends it as BDS does.
type Output struct {
	messages  []Message
	successes uint32
}

// Print adds a line of success.
func (o *Output) Print(text string) {
	o.messages = append(o.messages, Message{Success: true, Text: text})
	o.successes++
}

// Printf adds a formatted line of success.
func (o *Output) Printf(format string, args ...any) { o.Print(fmt.Sprintf(format, args...)) }

// Error adds a line of failure.
func (o *Output) Error(text string) {
	o.messages = append(o.messages, Message{Text: text})
}

// Errorf adds a formatted line of failure.
func (o *Output) Errorf(format string, args ...any) { o.Error(fmt.Sprintf(format, args...)) }

// Success adds a line of success in the game's words: a translation key such as
// "commands.give.success" and its parameters, shown in each player's language.
func (o *Output) Success(key string, params ...string) {
	o.messages = append(o.messages, Message{Success: true, Text: key, Translate: true, Params: params})
	o.successes++
}

// Fail adds a line of failure in the game's words.
func (o *Output) Fail(key string, params ...string) {
	o.messages = append(o.messages, Message{Text: key, Translate: true, Params: params})
}

// Messages are the lines so far.
func (o *Output) Messages() []Message { return slices.Clone(o.messages) }

// Successes is how many lines of success there were: BDS's success count.
func (o *Output) Successes() uint32 { return o.successes }

// Failed reports whether the output has a failure and no success.
func (o *Output) Failed() bool { return o.successes == 0 && len(o.messages) > 0 }
