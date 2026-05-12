package superroot

import (
	"context"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-service/eth"
	cc "github.com/ethereum-optimism/optimism/op-supernode/supernode/chain_container"
	"github.com/ethereum/go-ethereum"
	gethlog "github.com/ethereum/go-ethereum/log"
)

// Superroot satisfies the RPC Activity interface
// it provides the superroot at a given timestamp for all chains
// along with the current L1s and the verified and optimistic L1:L2 pairs
type Superroot struct {
	log    gethlog.Logger
	chains map[eth.ChainID]cc.ChainContainer
}

func New(log gethlog.Logger, chains map[eth.ChainID]cc.ChainContainer) *Superroot {
	return &Superroot{
		log:    log,
		chains: chains,
	}
}

func (s *Superroot) ActivityName() string { return "superroot" }

func (s *Superroot) RPCNamespace() string    { return "superroot" }
func (s *Superroot) RPCService() interface{} { return &superrootAPI{s: s} }

type superrootAPI struct{ s *Superroot }

// OutputWithSource is the full Output and its source L1 block
type OutputWithSource struct {
	Output   *eth.OutputResponse
	SourceL1 eth.BlockID
}

func (s *Superroot) atTimestamp(ctx context.Context, timestamp uint64) (eth.SuperRootAtTimestampResponse, error) {
	aggregate, err := syncstatus.Aggregate(ctx, s.log, s.chains)
	if err != nil {
		return eth.SuperRootAtTimestampResponse{}, err
	}

	var (
		optimistic         = make(map[eth.ChainID]eth.OutputWithRequiredL1, len(s.chains))
		verifiedRequiredL1 eth.BlockID
		chainOutputs       = make([]eth.ChainIDAndOutput, 0, len(s.chains))
	)

	notFound := false
	// Collect verified L2 and L1 blocks at the given timestamp
	for chainID, chain := range s.chains {
		// verifiedAt returns the L2 block which is fully verified at the given timestamp, and the minimum L1 block at which verification is possible
		verifiedL2, verifiedL1, err := chain.VerifiedAt(ctx, timestamp)
		if err != nil {
			s.log.Warn("failed to get verified L1", "chain_id", chainID.String(), "err", err)
			return atTimestampResponse{}, fmt.Errorf("%w: %w", ethereum.NotFound, err)
		}
		verified[chainID] = L2WithRequiredL1{
			L2:            verifiedL2,
			MinRequiredL1: verifiedL1,
		}
		// Verified data is available: track the L1 block that includes the data
		// for every chain — i.e. the MAX of per-chain minimum-required L1s.
		if verifiedL1.Number > verifiedRequiredL1.Number {
			verifiedRequiredL1 = verifiedL1
		}
		// Compute output root at or before timestamp using the verified L2 block number
		outRoot, err := chain.OutputRootAtL2BlockNumber(ctx, verifiedL2.Number)
		if err != nil {
			s.log.Warn("failed to compute output root at L2 block", "chain_id", chainID.String(), "l2_number", verifiedL2.Number, "err", err)
			return atTimestampResponse{}, fmt.Errorf("%w: %w", ethereum.NotFound, err)
		}
		chainOutputs = append(chainOutputs, eth.ChainIDAndOutput{ChainID: chainID, Output: outRoot})
		// Optimistic output is the full output at the optimistic L2 block for the timestamp
		optimisticOut, err := chain.OptimisticOutputAtTimestamp(ctx, timestamp)
		if err != nil {
			s.log.Warn("failed to get optimistic L1", "chain_id", chainID.String(), "err", err)
			return atTimestampResponse{}, fmt.Errorf("%w: %w", ethereum.NotFound, err)
		}
		// Also include the source L1 for context
		_, optimisticL1, err := chain.OptimisticAt(ctx, timestamp)
		if err != nil {
			s.log.Warn("failed to get optimistic source L1", "chain_id", chainID.String(), "err", err)
			return atTimestampResponse{}, fmt.Errorf("%w: %w", ethereum.NotFound, err)
		}
		optimistic[chainID] = eth.OutputWithRequiredL1{
			Output:     optimisticOut,
			OutputRoot: eth.OutputRoot(optimisticOut),
			RequiredL1: optimisticL1,
		}
	}

	response := eth.SuperRootAtTimestampResponse{
		CurrentL1:                 aggregate.CurrentL1,
		CurrentSafeTimestamp:      aggregate.SafeTimestamp,
		CurrentLocalSafeTimestamp: aggregate.LocalSafeTimestamp,
		CurrentFinalizedTimestamp: aggregate.FinalizedTimestamp,
		OptimisticAtTimestamp:     optimistic,
		ChainIDs:                  aggregate.ChainIDs,
	}
	if !notFound {
		// Build super root from collected outputs
		superV1 := eth.NewSuperV1(timestamp, chainOutputs...)
		superRoot := eth.SuperRoot(superV1)
		response.Data = &eth.SuperRootResponseData{
			VerifiedRequiredL1: verifiedRequiredL1,
			Super:              superV1,
			SuperRoot:          superRoot,
		}
	}
	return response, nil
}
