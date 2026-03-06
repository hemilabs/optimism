package node_utils

import (
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
)

type MinimalWithConductors struct {
	*MixedOpKonaPreset

	ConductorSets map[stack.ComponentID]dsl.ConductorSet
}

func NewMixedOpKonaWithConductors(t devtest.T) *MinimalWithConductors {
	system := shim.NewSystem(t)
	orch := presets.Orchestrator()
	orch.Hydrate(system)
	chains := system.L2Networks()
	conductorSets := make(map[stack.ComponentID]dsl.ConductorSet)
	for _, chain := range chains {
		chainMatcher := match.L2ChainById(chain.ID())
		l2 := system.L2Network(match.Assume(t, chainMatcher))

func NewMixedOpKonaWithConductorsForConfig(t devtest.T, _ L2NodeConfig, opts ...presets.Option) *MinimalWithConductors {
	return presets.NewMinimalWithConductors(t, opts...)
}
