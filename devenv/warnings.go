package devenv

import (
	"context"
	"fmt"
	"io"
	"time"
)

var notForProductionWait = 3 * time.Second

func warnNotForProduction(ctx context.Context, out io.Writer) error {
	fmt.Fprintln(out, "flock dev is a dev mode, not for production.")
	fmt.Fprintln(out, "In production run one container per component (controller, agent), each with its own config:")
	fmt.Fprintln(out, "https://flock.seagalia.io/start/containers/")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(notForProductionWait):
		return nil
	}
}
