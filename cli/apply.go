package cli

import (
	"context"
	"fmt"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/resfile"
	"github.com/seagalia/flock/sdk/clients"
)

type ApplyCmd struct {
	Files []string `short:"f" name:"file" required:"" help:"Path to a yml file or to a folder of yml files with the resources to apply, can be repeated" type:"path" predictor:"file"`

	DryRun bool `name:"dry-run" help:"Show the changes without applying them"`
	Yes    bool `short:"y" name:"yes" help:"Apply without confirmation"`

	client *clients.Client
}

func (c *ApplyCmd) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ApplyCmd) Run(ctx context.Context) error {
	docs, problems := resfile.Read(c.Files...)
	problems = append(problems, resfile.Check(docs)...)
	if len(problems) > 0 {
		for _, problem := range problems {
			msg.Errorf("%s", problem)
		}
		return fmt.Errorf("%d problems in the files", len(problems))
	}
	// a folder without yml files or an empty file would otherwise look like a run with nothing to change
	if len(docs) == 0 {
		return fmt.Errorf("no resource in %s", strings.Join(c.Files, ", "))
	}

	plan, err := planApply(ctx, c.client, docs)
	if err != nil {
		return err
	}
	if len(plan.Changes) == 0 {
		msg.Successf("Nothing to change, %d resources are up to date.", len(plan.Unchanged))
		return nil
	}

	if err := printChanges(plan.Changes); err != nil {
		return err
	}
	if len(plan.Unchanged) > 0 {
		msg.Infof("%d resources are up to date.", len(plan.Unchanged))
	}
	if c.DryRun {
		return nil
	}

	if !c.Yes {
		confirm := false
		err := confirmChanges("Do you confirm these changes?", &confirm)
		if err != nil {
			return err
		}
		if !confirm {
			msg.Info("Nothing applied.")
			return nil
		}
	}
	return applyChanges(ctx, c.client, plan.Changes, printApplied)
}
