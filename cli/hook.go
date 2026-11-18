package cli

import (
	"fmt"
	"os"
	"strings"

	msg "github.com/ArthurHlt/messages"
)

type Hook struct {
	Completion string `short:"c" name:"completion" help:"Give your shell name (zsh or bash) for adding automatic completion"`
}

func (c *Hook) Run() error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find flock binary path: %w", err)
	}
	msg.Print(c.makeCompletion(c.Completion, binary))
	return nil
}

func (c *Hook) NoCheckUpdate() bool {
	return true
}

// makeCompletion gives the script registering flock as its own bash completer:
// the shell then runs the binary with COMP_LINE set and kongplete answers.
func (c *Hook) makeCompletion(shellName, binary string) string {
	completeCmd := fmt.Sprintf("complete -o default -C %q flock\n", binary)
	shellName = strings.TrimSpace(strings.ToLower(shellName))
	if shellName == "zsh" {
		return "autoload -U +X bashcompinit && bashcompinit\n" + completeCmd
	}
	return completeCmd
}
