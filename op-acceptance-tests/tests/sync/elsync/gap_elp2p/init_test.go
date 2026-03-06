package gap_elp2p

import (
	bss "github.com/ethereum-optimism/optimism/op-batcher/batcher"
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
	"github.com/ethereum-optimism/optimism/op-devstack/sysgo"
)

func TestMain(m *testing.M) {
	// No ELP2P, CLP2P to control the supply of unsafe payload to the CL
	presets.DoMain(m, presets.WithSingleChainMultiNodeWithoutP2P(),
		presets.WithCompatibleTypes(compat.SysGo),
		stack.MakeCommon(sysgo.WithBatcherOption(func(id stack.ComponentID, cfg *bss.CLIConfig) {
			// For stopping derivation, not to advance safe heads
			cfg.Stopped = true
		}),
	}
}

func newGapELP2PSystem(t devtest.T) *presets.SingleChainMultiNode {
	return presets.NewSingleChainMultiNodeWithoutP2PWithoutCheck(t, gapELP2POpts()...)
}
