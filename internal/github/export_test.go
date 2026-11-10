package github

import (
	"context"
	"time"
)

// WithSleep replaces the waits of the rate limit pauses, so tests do not
// sleep.
func WithSleep(sleep func(ctx context.Context, d time.Duration) error) ClientOption {
	return func(c *Client) { c.sleep = sleep }
}
