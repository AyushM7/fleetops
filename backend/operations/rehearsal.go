//go:build fleetops_rehearsal

package operations

import (
	"context"
	"os/exec"

	"github.com/AyushM7/fleetops/backend/db"
)

// OpenRecoveryRehearsal uses an in-process, offline transport with the normal broker.
func OpenRecoveryRehearsal(ctx context.Context, pool *db.Pool, entrypoint string, send func(context.Context, []byte) ([]byte, error)) (*Manager, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, err
	}
	ledger, err := openLedger(ctx, pool, pricing{minInputRate, minOutputRate})
	if err != nil {
		return nil, err
	}
	return &Manager{ledger: ledger, slots: make(chan struct{}, 1), recovery: &recoveryWorker{
		node: node, entrypoint: entrypoint, send: send, dayRequests: 12, dayMicros: 3_524_400,
	}}, nil
}
