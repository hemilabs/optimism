package divergence

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/depreqres/common"
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-devstack/sysgo"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/eth"

	safety "github.com/ethereum-optimism/optimism/op-service/eth/safety"
	"github.com/ethereum/go-ethereum"
)

// TestCLELDivergence tests that the CL and EL diverge when the CL advances the unsafe head, due to accepting SYNCING response from the EL, but the EL cannot validate the block (yet), does not canonicalize it, and doesn't serve it.
func TestCLELDivergence(gt *testing.T) {
	t := devtest.ParallelT(gt)
	// Example error with kona-node:
	//
	// assertions.go:387:             ERROR[03-31|10:19:50.846]
	// assertions.go:387:             	Error Trace:	/Users/josh/repos/optimism/op-acceptance-tests/tests/depreqres/reqressyncdisabled/divergence/divergence_test.go:34
	// assertions.go:387:             	Error:      	Not equal:
	// assertions.go:387:             	            	expected: 0x1
	// assertions.go:387:             	            	actual  : 0x0
	// assertions.go:387:             	Test:       	TestCLELDivergence
	sysgo.SkipOnKonaNode(t, "not supported")
	sys := presets.NewSingleChainMultiNodeWithoutP2PWithoutCheck(t, common.ReqRespSyncDisabledOpts(sync.ELSync)...)
	require := t.Require()
	l := t.Logger()

	startNum := sys.L2CLB.HeadBlockRef(safety.LocalUnsafe).Number

	// Wait for the sequencer to produce the next block so the verifier initial EL sync can complete.
	sys.L2CL.Reached(safety.LocalUnsafe, startNum+1, 30)

	// Complete initial EL sync by providing the first missing block.
	// At this point, the EL has sufficient state to validate block startNum+1.
	sys.L2CLB.SignalTarget(sys.L2EL, startNum+1)
	require.Equal(startNum+1, sys.L2ELB.BlockRefByLabel(eth.Unsafe).Number)

	for _, delta := range []uint64{3, 4, 5} {
		targetNumber := startNum + delta
		targetBlock := sys.L2EL.BlockRefByNumber(targetNumber)

	// Choose a future EL sync target for which the EL lacks state to validate.
	delta := uint64(5)
	targetNumber := startNum + delta
	sys.L2CL.Advanced(safety.LocalUnsafe, targetNumber, 30)
	targetBlock := sys.L2EL.BlockRefByNumber(targetNumber)

	// The CL advances its unsafe head to the target block, even though there is a gap.
	var ss *eth.SyncStatus
	require.Eventually(func() bool {
		l.Info("Sending payload", "target", targetNumber, "startNum", startNum)
		sys.L2CLB.SignalTarget(sys.L2EL, targetNumber)

		// Canonical unsafe head never advances because of the gap
		require.Equal(startNum+1, sys.L2ELB.BlockRefByLabel(eth.Unsafe).Number)

		// EL-sync can quickly reset the status tracker after exposing the posted
		// unsafe head, so poll tightly and without extra RPCs between the post and
		// the SyncStatus check.
		var ss *eth.SyncStatus
		require.Eventually(func() bool {
			ss = sys.L2CLB.SyncStatus()
			return ss.UnsafeL2.Number == targetNumber && ss.UnsafeL2.Hash == targetBlock.Hash
		}, 2*time.Second, 10*time.Millisecond, "L2CLB unsafe head did not expose target block")

		// Confirm that L2ELB cannot fetch the block by hash yet, because the block is not canonicalized, even though the CL reference is set to it.
		_, err := sys.L2ELB.Escape().L2EthClient().L2BlockRefByHash(t.Ctx(), ss.UnsafeL2.Hash)
		require.Error(err, ethereum.NotFound)
	}
}
