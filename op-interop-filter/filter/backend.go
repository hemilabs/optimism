package filter

import (
	"context"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/ethereum-optimism/optimism/op-interop-filter/metrics"
	"github.com/ethereum-optimism/optimism/op-supervisor/supervisor/types"
)

// Backend coordinates chain ingesters and handles the failsafe state.
// This is a stub implementation - the actual logic will be added in a follow-up PR.
type Backend struct {
	log     log.Logger
	metrics metrics.Metricer
	cfg     *Config
}

// NewBackend creates a new Backend instance
func NewBackend(ctx context.Context, logger log.Logger, m metrics.Metricer, cfg *Config) (*Backend, error) {
	b := &Backend{
		log:     logger,
		metrics: m,
		cfg:     cfg,
	}
	logger.Info("Created backend", "chains", len(cfg.L2RPCs))
	return b, nil
}

// Start starts the backend
func (b *Backend) Start(ctx context.Context) error {
	b.log.Info("Starting backend (stub)")
	return nil
}

// Stop stops the backend
func (b *Backend) Stop(ctx context.Context) error {
	b.log.Info("Stopping backend (stub)")
	return nil
}

// FailsafeEnabled returns whether failsafe is enabled
func (b *Backend) FailsafeEnabled() bool {
	return false
}

// CheckAccessList validates the given access list entries.
// This is a stub implementation that always returns ErrUninitialized.
func (b *Backend) CheckAccessList(ctx context.Context, inboxEntries []common.Hash,
	minSafety types.SafetyLevel, execDescriptor types.ExecutingDescriptor) error {

	b.metrics.RecordCheckAccessList(false)
	return types.ErrUninitialized
}

// GetBlockHashByNumber returns the latest block hash or the block hash at a specific height for the given chain.
// Accepts rpc.BlockNumber: "latest" or a numeric block number. Other named tags are not supported.
func (b *Backend) GetBlockHashByNumber(chainID eth.ChainID, blockNum rpc.BlockNumber) (common.Hash, error) {
	ingester, ok := b.chains[chainID]
	if !ok {
		return common.Hash{}, fmt.Errorf("chain %s: %w", chainID, types.ErrUnknownChain)
	}

	if blockNum == rpc.LatestBlockNumber {
		block, ok := ingester.LatestBlock()
		if !ok {
			return common.Hash{}, fmt.Errorf("latest block for chain %s: %w", chainID, ethereum.NotFound)
		}
		return block.Hash, nil
	}
	if blockNum < 0 {
		return common.Hash{}, fmt.Errorf("unsupported block tag %q: only \"latest\" and block numbers are supported", blockNum)
	}

	blockHash, ok := ingester.BlockHashByNumber(uint64(blockNum))
	if !ok {
		return common.Hash{}, fmt.Errorf("block %d for chain %s: %w", blockNum, chainID, ethereum.NotFound)
	}
	return blockHash, nil
}
