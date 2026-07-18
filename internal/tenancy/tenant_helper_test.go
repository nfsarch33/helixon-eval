// tenant_helper_test.go — exposes the idempotency.ErrIdempotentReplay
// sentinel for cross-package tests.
package tenancy

import "github.com/nfsarch33/helixon-eval/internal/idempotency"

func idempotencyErr() error {
	return idempotency.ErrIdempotentReplay
}
