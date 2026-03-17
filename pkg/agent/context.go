package agent

import (
	"context"
	"time"
)

func newTimeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}
