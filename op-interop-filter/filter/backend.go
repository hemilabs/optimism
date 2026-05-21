package filter

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-interop-filter/metrics"
	"github.com/ethereum-optimism/optimism/op-supervisor/supervisor/types"

	messages "github.com/ethereum-optimism/optimism/op-core/interop/messages"
	safety "github.com/ethereum-optimism/optimism/op-service/eth/safety"
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
	return b.manualFailsafe.Load() || len(b.GetChainErrors()) > 0 || b.crossValidator.Error() != nil
}

// SetFailsafeEnabled sets the manual failsafe override.
func (b *Backend) SetFailsafeEnabled(enabled bool) {
	b.manualFailsafe.Store(enabled)
	b.metrics.RecordFailsafeEnabled(b.FailsafeEnabled())
}

// GetChainErrors returns all chains that are in an error state
func (b *Backend) GetChainErrors() map[eth.ChainID]*IngesterError {
	errs := make(map[eth.ChainID]*IngesterError)
	for chainID, ingester := range b.chains {
		if err := ingester.Error(); err != nil {
			errs[chainID] = err
		}
	}
	return errs
}

// Ready returns true if all chains have completed backfill
func (b *Backend) Ready() bool {
	for _, ingester := range b.chains {
		if !ingester.Ready() {
			return false
		}
	}

	return len(b.chains) > 0
}

// supportedSafetyLevel returns true if the safety level is supported for access list checks.
func supportedSafetyLevel(level safety.Level) bool {
	return level == safety.LocalUnsafe || level == safety.CrossUnsafe
}

// classifyRejectionReason categorizes an error from CheckAccessList into a rejection reason label.
func classifyRejectionReason(err error) string {
	switch {
	case errors.Is(err, types.ErrFailsafeEnabled):
		return "failsafe"
	case errors.Is(err, types.ErrUnknownChain):
		return "unknown_chain"
	case errors.Is(err, types.ErrConflict):
		return "expired_message"
	default:
		return "invalid_executing_message"
	}
}

// CheckAccessList validates the given access list entries.
// This is a stub implementation that always returns ErrUninitialized.
func (b *Backend) CheckAccessList(ctx context.Context, inboxEntries []common.Hash,
	minSafety safety.Level, execDescriptor messages.ExecutingDescriptor) error {

	start := time.Now()
	defer func() {
		b.metrics.RecordCheckAccessListDuration(time.Since(start).Seconds())
	}()

	if b.passthrough {
		b.metrics.RecordCheckAccessList(true)
		return nil
	}

	if b.FailsafeEnabled() {
		b.metrics.RecordCheckAccessList(false)
		b.metrics.RecordCheckAccessListRejection("failsafe")
		return types.ErrFailsafeEnabled
	}

	if !b.Ready() {
		b.metrics.RecordCheckAccessList(false)
		b.metrics.RecordCheckAccessListRejection("failsafe")
		b.log.Debug("Backend not ready; rejecting access list check")
		return types.ErrUninitialized
	}

	if !supportedSafetyLevel(minSafety) {
		b.metrics.RecordCheckAccessList(false)
		b.metrics.RecordCheckAccessListRejection("invalid_executing_message")
		return fmt.Errorf("unsupported safety level %s: only %s and %s are supported",
			minSafety, safety.LocalUnsafe, safety.CrossUnsafe)
	}

	if _, ok := b.chains[execDescriptor.ChainID]; !ok {
		if !b.legacyCheckAccessListFormat {
			b.metrics.RecordCheckAccessList(false)
			b.metrics.RecordCheckAccessListRejection("unknown_chain")
			return fmt.Errorf("executing chain %s: %w", execDescriptor.ChainID, types.ErrUnknownChain)
		}
		b.log.Debug("Supporting legacy check access list format", "executing_chain", execDescriptor.ChainID)
	}

	remaining := inboxEntries
	for len(remaining) > 0 {
		var access messages.Access
		var err error
		remaining, access, err = messages.ParseAccess(remaining)
		if err != nil {
			b.metrics.RecordCheckAccessList(false)
			b.metrics.RecordCheckAccessListRejection("parse_error")
			return fmt.Errorf("failed to parse access entry: %w", err)
		}

		if err := b.crossValidator.ValidateAccessEntry(access, minSafety, execDescriptor); err != nil {
			b.metrics.RecordCheckAccessList(false)
			b.metrics.RecordCheckAccessListRejection(classifyRejectionReason(err))
			return err
		}
	}

	b.metrics.RecordCheckAccessList(true)
	return nil
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
