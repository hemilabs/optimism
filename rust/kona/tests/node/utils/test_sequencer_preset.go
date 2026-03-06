package node_utils

import (
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/dsl"
	"github.com/ethereum-optimism/optimism/op-devstack/sysgo"
)

type MinimalWithTestSequencersPreset struct {
	*MixedOpKonaPreset

	TestSequencer dsl.TestSequencer
}

func NewMixedOpKonaWithTestSequencer(t devtest.T) *MinimalWithTestSequencersPreset {
	return NewMixedOpKonaWithTestSequencerForConfig(t, ParseL2NodeConfigFromEnv())
}

func NewMixedOpKonaWithTestSequencerForConfig(t devtest.T, l2Config L2NodeConfig) *MinimalWithTestSequencersPreset {
	l2Config = withRequiredOpSequencerForTestSequencer(l2Config)

	runtime := sysgo.NewMixedSingleChainRuntime(t, sysgo.MixedSingleChainPresetConfig{
		NodeSpecs:         mixedOpKonaNodeSpecs(l2Config),
		WithTestSequencer: true,
		TestSequencerName: "test-sequencer",
	})
	mixedPreset, frontends := mixedOpKonaFromRuntime(t, runtime)
	t.Require().NotNil(frontends.TestSequencer, "expected test sequencer frontend")

	return &MinimalWithTestSequencersPreset{
		MixedOpKonaPreset: mixedPreset,
		TestSequencer:     *frontends.TestSequencer,
	}
}

type DefaultMinimalWithTestSequencerIds struct {
	DefaultMixedOpKonaSystemIDs DefaultMixedOpKonaSystemIDs
	TestSequencerId             stack.ComponentID
}

func NewDefaultMinimalWithTestSequencerIds(l2Config L2NodeConfig) DefaultMinimalWithTestSequencerIds {
	return DefaultMinimalWithTestSequencerIds{
		DefaultMixedOpKonaSystemIDs: NewDefaultMixedOpKonaSystemIDs(eth.ChainIDFromUInt64(DefaultL1ID), eth.ChainIDFromUInt64(DefaultL2ID), L2NodeConfig{
			OpSequencerNodesWithGeth: l2Config.OpSequencerNodesWithGeth,
			OpSequencerNodesWithReth: l2Config.OpSequencerNodesWithReth,
			OpNodesWithGeth:          l2Config.OpNodesWithGeth,
			OpNodesWithReth:          l2Config.OpNodesWithReth,
			KonaNodesWithGeth:        l2Config.KonaNodesWithGeth,
			KonaNodesWithReth:        l2Config.KonaNodesWithReth,
		}),
		TestSequencerId: stack.NewTestSequencerID("test-sequencer"),
	}
	return l2Config
}
