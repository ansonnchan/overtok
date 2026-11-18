package doctor

import (
	"context"
	"errors"
	"os/exec"

	"github.com/ansonnchan/overtok/internal/adapters/ccusage"
)

// CCUsageProbe checks whether the pinned optional parser can execute locally.
type CCUsageProbe struct {
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) ([]byte, error)
}

// Check runs only the version command and reduces failures to safe issue codes.
func (p CCUsageProbe) Check(ctx context.Context) ParserStatus {
	lookPath := p.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath("ccusage")
	if err != nil {
		return ParserStatus{}
	}
	run := p.Run
	if run == nil {
		run = runCommand
	}
	output, err := run(ctx, binary, "--version")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ParserStatus{Found: true, Issue: "timeout"}
		}
		return ParserStatus{Found: true, Issue: "execution_failed"}
	}
	return ParserStatus{Found: true, Compatible: ccusage.ValidateVersion(output) == nil}
}

func runCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	output, err := command.Output()
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, ctx.Err()
	}
	return output, err
}
