package depreqres

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-acceptance-tests/tests/depreqres/common"
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/dsl"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/eth"

	safety "github.com/ethereum-optimism/optimism/op-service/eth/safety"
)

const disabledReqRespSyncFlakyReason = "known flaky in the default acceptance run"

func TestUnsafeChainNotStalling_DisabledReqRespSync(gt *testing.T) {
	t := devtest.ParallelT(gt)
	t.MarkFlaky(disabledReqRespSyncFlakyReason)
	sys := presets.NewSingleChainMultiNodeWithoutCheck(t, common.ReqRespSyncDisabledOpts(sync.ELSync)...)
	// We don't want the safe head to move, as this can also progress the unsafe head
	sys.L2Batcher.Stop()
	l := t.Logger()

	l.Info("Confirm that the CL nodes are progressing the unsafe chain")
	delta := uint64(3)
	dsl.CheckAll(t,
		sys.L2CL.AdvancedFn(safety.LocalUnsafe, delta, 30),
		sys.L2CLB.AdvancedFn(safety.LocalUnsafe, delta, 30),
	)

	l.Info("Disconnect L2CL from L2CLB, and vice versa")
	sys.L2CLB.DisconnectPeer(sys.L2CL)
	sys.L2CL.DisconnectPeer(sys.L2CLB)

	ssA_before := sys.L2CL.SyncStatus()
	ssB_before := sys.L2CLB.SyncStatus()

	l.Info("Wait for L2CLB unsafe head to stall after disconnect")
	sys.L2CLB.WaitForStall(safety.LocalUnsafe)

	time.Sleep(20 * time.Second)

	ssA_after := sys.L2CL.SyncStatus()
	ssB_after := sys.L2CLB.SyncStatus()

	l.Info("L2CL status after delay", "unsafeL2", ssA_after.UnsafeL2.ID(), "safeL2", ssA_after.SafeL2.ID())
	l.Info("L2CLB status after delay", "unsafeL2", ssB_after.UnsafeL2.ID(), "safeL2", ssB_after.SafeL2.ID())

	require.Greater(ssA_after.UnsafeL2.Number, ssA_before.UnsafeL2.Number, "unsafe chain for L2CL should have advanced")
	require.Equal(ssB_after.UnsafeL2.Number, ssB_before.UnsafeL2.Number, "unsafe chain for L2CLB should have stalled")

	l.Info("Re-connect L2CL to L2CLB")
	sys.L2CLB.ConnectPeer(sys.L2CL)
	sys.L2CL.ConnectPeer(sys.L2CLB)

	l.Info("Confirm that the unsafe chain for L2CLB can advance")
	sys.L2CLB.Reached(safety.LocalUnsafe, ssA.UnsafeL2.Number, 30)
	sys.L2ELB.Reached(eth.Unsafe, ssA.UnsafeL2.Number, 30)
}
