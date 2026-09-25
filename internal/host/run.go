package host

import (
	"context"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
)

func run(ctx context.Context, r process.Runner, name string, args []string, stdin []byte) (string, error) {
	res, err := r.Run(ctx, process.Spec{Name: name, Args: args, Stdin: stdin})
	if err != nil {
		return "", observability.WrapError(observability.CodeRuntimeUnavailable, name, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(res.Stdout))
		}
		if msg == "" {
			msg = fmt.Sprintf("%s exit %d", name, res.ExitCode)
		}
		return "", observability.NewError(observability.CodeRuntimeUnavailable, msg)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
