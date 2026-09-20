package priv

import "context"

// Runner abstracts running privileged or system commands.
// Implementations may elevate as needed per OS.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

var defaultRunner Runner = platformRunner()

// SetRunner swaps the global runner implementation.
func SetRunner(r Runner) {
	if r != nil {
		defaultRunner = r
	}
}

// Run executes the provided command using the global runner.
func Run(ctx context.Context, name string, args ...string) (string, error) {
	return defaultRunner.Run(ctx, name, args...)
}
