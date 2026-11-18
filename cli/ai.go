package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	msg "github.com/ArthurHlt/messages"
)

// skillContent is the Claude skill teaching the flock workflow, embedded so it matches the commands of this binary.
//
//go:embed skill/SKILL.md
var skillContent string

type AiCmd struct {
	InstallSkill InstallSkill `cmd:"" name:"install-skill" help:"Install the flock skill for Claude Code, so it can write and check flock manifests and configs."`
}

type InstallSkill struct {
	Project bool `name:"project" help:"Install in ./.claude/skills of the current project instead of ~/.claude/skills"`
	Stdout  bool `name:"stdout" help:"Print the skill instead of installing it"`
}

func (c *InstallSkill) Run() error {
	if c.Stdout {
		fmt.Print(skillContent)
		return nil
	}
	skillsDir := "~/.claude/skills"
	if c.Project {
		skillsDir = ".claude/skills"
	}
	skillsDir, err := ExpandHome(skillsDir)
	if err != nil {
		return err
	}
	skillPath := filepath.Join(skillsDir, "flock", "SKILL.md")
	err = os.MkdirAll(filepath.Dir(skillPath), 0755)
	if err != nil {
		return err
	}
	err = os.WriteFile(skillPath, []byte(skillContent), 0644)
	if err != nil {
		return err
	}
	msg.Successf("flock skill installed in %s, run it again after a self-update to follow the new commands", msg.Cyan(skillPath))
	return nil
}
