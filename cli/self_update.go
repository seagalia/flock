package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/updater"
)

type SelfUpdate struct {
	SkipSSL bool `short:"k" name:"skip-ssl-validation" help:"Skip ssl validation for downloading"`
}

func (c *SelfUpdate) Run(ctx context.Context) error {
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Failed to get self path: %s", err.Error())
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(selfPath), ".exe") {
		selfPath += ".exe"
	}
	// No cache path: the user asked for an update, so GitHub is asked now.
	needUpdate, nextVersion, warn := updater.IsNeedUpdate(ctx, binInfoGlobal, "")
	if warn != nil {
		msg.Warningf("When getting version: %s", warn.Error())
		return nil
	}
	if !needUpdate {
		msg.Success("You are up to date.")
		return nil
	}
	msg.Printfln("You are updating flock to version %s", msg.Yellow(nextVersion.String()))

	spinner := msg.GetSpinner()
	spinner.Message("Updating flock ...")
	spinner.Start()
	err = updater.Update(ctx, binInfoGlobal, selfPath, c.SkipSSL)
	spinner.Stop()
	if err != nil {
		return err
	}
	msg.Println("Finished updating flock.")
	return nil
}

func (c *SelfUpdate) NoCheckUpdate() bool {
	return true
}
