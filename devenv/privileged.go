package devenv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const capEffPrefix = "CapEff:"

func confirmPrivileged(ctx context.Context, in io.Reader, inIsTerminal bool, out io.Writer, yes bool) error {
	fmt.Fprintln(out, "--privileged changes this machine, and removes these changes when flock dev stops:")
	fmt.Fprintln(out, "  - production sysctls are set")
	fmt.Fprintln(out, "  - the nftables table inet flock is created")
	fmt.Fprintln(out, "  - the interface flock0 is created")
	fmt.Fprintln(out, "  - IPVS services are created")
	fmt.Fprintln(out, "  - the main IP of the machine is served as a VIP")
	if yes {
		return nil
	}
	if !inIsTerminal {
		return errors.New("--privileged changes this machine, pass --yes to agree with no terminal")
	}

	fmt.Fprint(out, "Continue? [y/N] ")

	// Buffered so the goroutine never blocks on a send once ctx is cancelled: a read on stdin can't be interrupted
	// and may outlive this call.
	answers := make(chan string, 1)
	go func() {
		defer close(answers)
		line, _ := bufio.NewReader(in).ReadString('\n')
		answers <- line
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case answer := <-answers:
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "y" || answer == "yes" {
			return nil
		}
		return errors.New("privileged mode declined")
	}
}

func parseCapEff(status io.Reader) (uint64, error) {
	scanner := bufio.NewScanner(status)
	for scanner.Scan() {
		value, found := strings.CutPrefix(scanner.Text(), capEffPrefix)
		if !found {
			continue
		}
		caps, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return 0, fmt.Errorf("parse CapEff: %w", err)
		}
		return caps, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read status: %w", err)
	}
	return 0, errors.New("no CapEff line in status")
}
