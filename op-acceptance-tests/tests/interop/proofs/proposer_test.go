package proofs

import (
	"testing"

	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/presets"
)

func TestProposer(gt *testing.T) {
	t := devtest.ParallelT(gt)
	sys := presets.NewSimpleInteropSupernodeProofs(t, presets.WithChallengerCannonKonaEnabled())

	dgf := sys.DisputeGameFactory()

	newGame := dgf.WaitForGame()
	rootClaim := newGame.RootClaim().Value()
	l2SequenceNumber := newGame.L2SequenceNumber()

	superRoot := sys.Supervisor.FetchSuperRootAtTimestamp(l2SequenceNumber)
	t.Require().Equal(superRoot.SuperRoot[:], rootClaim[:])
}
