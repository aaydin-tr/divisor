package run

import (
	"context"
	"strings"
	"time"

	"github.com/aaydin-tr/divisor/benchmark/oha"
)

const readinessRetryDelay = 250 * time.Millisecond

// waitUntilReady sends from the load generator's side of the host network; err is set only
// when the benchmark itself cannot go on.
func (r *runner) waitUntilReady(ctx context.Context, url string) (notReadyReason string, err error) {
	deadline := time.Now().Add(r.plan.Schedule.ReadinessTimeout())
	for {
		output, err := r.runLoadGenerator(ctx, oha.ReadinessArguments(url))
		if err != nil {
			return "", err
		}
		result, err := oha.ParseResult(output)
		if err != nil {
			return "", err
		}
		problems := result.Problems()
		if len(problems) == 0 {
			return "", nil
		}
		if time.Now().After(deadline) {
			return strings.Join(problems, "; "), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(readinessRetryDelay):
		}
	}
}
