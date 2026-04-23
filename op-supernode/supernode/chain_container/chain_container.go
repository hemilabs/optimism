package chain_container

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	opnodecfg "github.com/ethereum-optimism/optimism/op-node/config"
	rollupNode "github.com/ethereum-optimism/optimism/op-node/node"
	"github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/sources"
	"github.com/ethereum-optimism/optimism/op-supernode/config"
	"github.com/ethereum-optimism/optimism/op-supernode/supernode/chain_container/engine_controller"
	"github.com/ethereum-optimism/optimism/op-supernode/supernode/chain_container/virtual_node"
	gethlog "github.com/ethereum/go-ethereum/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const virtualNodeVersion = "0.1.0"

type ChainContainer interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error

	SafeBlockAtTimestamp(ctx context.Context, ts uint64) (eth.L2BlockRef, error)
	SafeHeadAtL1(ctx context.Context, l1BlockNum uint64) (l1 eth.BlockID, l2 eth.BlockID, err error)
	// L1AtSafeHead returns the earliest L1 block at which the given L2 block became safe.
	L1AtSafeHead(ctx context.Context, l2 eth.BlockID) (eth.BlockID, error)
	CurrentL1(ctx context.Context) (eth.BlockRef, error)
	VerifiedAt(ctx context.Context, ts uint64) (l2, l1 eth.BlockID, err error)
	OptimisticAt(ctx context.Context, ts uint64) (l2, l1 eth.BlockID, err error)
	OutputRootAtL2BlockNumber(ctx context.Context, l2BlockNum uint64) (eth.Bytes32, error)
	OptimisticOutputAtTimestamp(ctx context.Context, ts uint64) (*eth.OutputV0, error)
	// RewindEngine rewinds the engine to the highest block with timestamp less than or equal to the given timestamp.
	// invalidatedBlock is the block that triggered the rewind and is passed to reset callbacks.
	// WARNING: this is a dangerous stateful operation and is intended to be called only
	// by interop transition application. Other callers should not use it until the
	// interface is refactored to make that ownership explicit.
	// TODO(#19561): remove this footgun by moving reorg-triggering operations behind a
	// smaller interop-owned interface.
	RewindEngine(ctx context.Context, timestamp uint64, invalidatedBlock eth.BlockRef) error
	RegisterVerifier(v activity.VerificationActivity)
	// VerifierCurrentL1s returns the CurrentL1 from each registered verifier.
	// This allows callers to determine the minimum L1 block that all verifiers have processed.
	VerifierCurrentL1s() []eth.BlockID
	// FetchReceipts fetches the receipts for a given block by hash.
	// Returns block info and receipts, or an error if the block or receipts cannot be fetched.
	FetchReceipts(ctx context.Context, blockHash eth.BlockID) (eth.BlockInfo, types.Receipts, error)
	// BlockTime returns the block time in seconds for this chain.
	BlockTime() uint64
	// InvalidateBlock adds a block to the deny list and triggers a rewind if the chain
	// currently uses that block at the specified height.
	// output is the marshaled eth.Output preimage for optimistic root computation.
	// WARNING: this is a dangerous stateful operation and is intended to be called only
	// by interop transition application. Other callers should not use it until the
	// interface is refactored to make that ownership explicit.
	// TODO(#19561): remove this footgun by moving reorg-triggering operations behind a
	// smaller interop-owned interface.
	// Returns true if a rewind was triggered, false otherwise.
	InvalidateBlock(ctx context.Context, height uint64, payloadHash common.Hash, decisionTimestamp uint64, stateRoot, messagePasserStorageRoot eth.Bytes32) (bool, error)
	// PruneDeniedAtOrAfterTimestamp removes deny-list entries with DecisionTimestamp >= timestamp.
	// Returns map of removed hashes by height.
	PruneDeniedAtOrAfterTimestamp(timestamp uint64) (map[uint64][]common.Hash, error)
	// PauseAndStopVN pauses the chain container restart loop and stops the virtual node.
	// This is used to freeze a chain's VN before a multi-chain rewind begins, preventing
	// the VN from issuing forkchoice updates that race with the rewind of a peer chain.
	PauseAndStopVN(ctx context.Context) error
	// IsDenied checks if a block hash is on the deny list at the given height.
	IsDenied(height uint64, payloadHash common.Hash) (bool, error)
	// GetDeniedOutput returns the reconstructed OutputV0 for a denied block.
	// Returns nil if the block is not denied at that height.
	GetDeniedOutput(height uint64, payloadHash common.Hash) (*eth.OutputV0, error)
	// OutputV0AtBlockNumber returns the full OutputV0 for the block at the given number.
	OutputV0AtBlockNumber(ctx context.Context, l2BlockNum uint64) (*eth.OutputV0, error)
	// SetResetCallback sets a callback that is invoked when the chain resets.
	// The supernode uses this to notify activities about chain resets.
	SetResetCallback(cb ResetCallback)
}

type virtualNodeFactory func(cfg *opnodecfg.Config, log gethlog.Logger, initOverrides *rollupNode.InitializationOverrides, appVersion string, superAuthority rollup.SuperAuthority) virtual_node.VirtualNode

// ResetCallback is called when the chain container resets due to an invalidated block.
// The supernode uses this to notify activities about the reset.
// invalidatedBlock is the block that was invalidated and triggered the reset.
type ResetCallback func(chainID eth.ChainID, timestamp uint64, invalidatedBlock eth.BlockRef)

type simpleChainContainer struct {
	vn                 virtual_node.VirtualNode
	vncfg              *opnodecfg.Config
	cfg                config.CLIConfig
	engine             engine_controller.EngineController
	pause              atomic.Bool
	stop               atomic.Bool
	stopped            chan struct{}
	log                gethlog.Logger
	chainID            eth.ChainID
	initOverload       *rollupNode.InitializationOverrides  // Base shared resources for all virtual nodes
	rpcHandler         *oprpc.Handler                       // Current per-chain RPC handler instance
	setHandler         func(chainID string, h http.Handler) // Set the RPC handler on the router for the chain
	setMetricsHandler  func(chainID string, h http.Handler) // Set the metrics handler on the router for the chain
	appVersion         string
	virtualNodeFactory virtualNodeFactory    // Factory function to create virtual node (for testing)
	rollupClient       *sources.RollupClient // In-proc rollup RPC client bound to rpcHandler
}

// Interface conformance assertions
var _ ChainContainer = (*simpleChainContainer)(nil)
var _ rollup.SuperAuthority = (*simpleChainContainer)(nil)

func NewChainContainer(
	chainID eth.ChainID,
	vncfg *opnodecfg.Config,
	log gethlog.Logger,
	cfg config.CLIConfig,
	initOverload *rollupNode.InitializationOverrides,
	rpcHandler *oprpc.Handler,
	setHandler func(chainID string, h http.Handler),
	addMetricsRegistry func(key string, g prometheus.Gatherer),
) ChainContainer {
	c := &simpleChainContainer{
		vncfg:              vncfg,
		cfg:                cfg,
		chainID:            chainID,
		log:                log,
		stopped:            make(chan struct{}, 1),
		initOverload:       initOverload,
		rpcHandler:         rpcHandler,
		setHandler:         setHandler,
		setMetricsHandler:  setMetricsHandler,
		appVersion:         virtualNodeVersion,
		virtualNodeFactory: defaultVirtualNodeFactory,
	}
	vncfg.SafeDBPath = c.subPath("safe_db")
	vncfg.RPC = cfg.RPCConfig
	// Attach in-proc rollup client if an initial handler is provided
	if c.rpcHandler != nil {
		if err := c.attachInProcRollupClient(); err != nil {
			log.Warn("failed to attach in-proc rollup client (initial)", "err", err)
		}
	}
	// Initialize engine controller (separate connection, not an op-node override) with a short setup timeout
	if vncfg.L2 != nil {
		setupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Provide contextual logger to engine controller
		engLog := log.New("chain_id", chainID.String(), "component", "engine_controller")
		if eng, err := engine_controller.NewEngineControllerFromConfig(setupCtx, engLog, vncfg); err != nil {
			log.Error("failed to setup engine controller", "err", err)
		} else {
			c.engine = eng
		}
	}
	return c
}

// defaultVirtualNodeFactory is the default factory that creates a real VirtualNode
func defaultVirtualNodeFactory(cfg *opnodecfg.Config, log gethlog.Logger, initOverload *rollupNode.InitializationOverrides, appVersion string) virtual_node.VirtualNode {
	return virtual_node.NewVirtualNode(cfg, log, initOverload, appVersion)
}

func (c *simpleChainContainer) subPath(path string) string {
	return filepath.Join(c.cfg.DataDir, c.chainID.String(), path)
}

func (c *simpleChainContainer) Start(ctx context.Context) error {
	defer func() { c.stopped <- struct{}{} }()
	for {
		// Refresh per-start derived fields
		c.vncfg.SafeDBPath = c.subPath("safe_db")
		c.vncfg.RPC = c.cfg.RPCConfig
		// create a fresh handler per (re)start, swap it into the router, and inject into overload
		h := oprpc.NewHandler("", oprpc.WithLogger(c.log.New("chain_id", c.chainID.String())))
		if c.setHandler != nil {
			c.setHandler(c.chainID.String(), h)
		}
		c.initOverload.RPCHandler = h
		c.rpcHandler = h
		// attach in-proc rollup client for this handler
		if err := c.attachInProcRollupClient(); err != nil {
			c.log.Warn("failed to attach in-proc rollup client", "err", err)
		}

		// Disable per-VN metrics server and provide metrics registry hook
		c.vncfg.Metrics.Enabled = false
		if c.initOverload != nil {
			chainID := c.chainID.String()
			c.initOverload.MetricsRegistry = func(reg *prometheus.Registry) {
				if c.setMetricsHandler != nil {
					// Mount per-chain metrics handler at /{chain}/metrics via router
					handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
					c.setMetricsHandler(chainID, handler)
				}
			}
		}
		c.vn = c.virtualNodeFactory(c.vncfg, c.log, c.initOverload, c.appVersion)
		if c.pause.Load() {
			c.log.Info("chain container paused")
			time.Sleep(1 * time.Second)
			continue
		}
		if c.stop.Load() {
			break
		}

		// start the virtual node
		err := c.vn.Start(ctx)
		if err != nil {
			c.log.Warn("virtual node exited with error", "error", err)
		}

		// always stop the virtual node after it exits
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if stopErr := c.vn.Stop(stopCtx); stopErr != nil {
			c.log.Error("error stopping virtual node", "error", stopErr)
		}
		cancel()
		if ctx.Err() != nil {
			c.log.Info("chain container context cancelled, stopping restart loop", "ctx_err", ctx.Err())
			break
		}

		// check if the chain container was stopped
		if c.stop.Load() {
			c.log.Info("chain container stop requested, stopping restart loop")
			break
		}

	}
	c.log.Info("chain container exiting")
	return nil
}

func (c *simpleChainContainer) Stop(ctx context.Context) error {
	c.stop.Store(true)
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Close in-proc rollup RPC resources
	if c.rollupClient != nil {
		c.rollupClient.Close()
	}

	if c.vn != nil {
		if err := c.vn.Stop(stopCtx); err != nil {
			c.log.Error("error stopping virtual node", "error", err)
		}
	}

	// Close engine controller RPC resources
	if c.engine != nil {
		_ = c.engine.Close()
	}

	select {
	case <-c.stopped:
		return nil
	case <-stopCtx.Done():
		return stopCtx.Err()
	}
}

func (c *simpleChainContainer) Pause(ctx context.Context) error {
	c.pause.Store(true)
	return nil
}

func (c *simpleChainContainer) Resume(ctx context.Context) error {
	c.pause.Store(false)
	return nil
}

// SafeBlockAtTimestamp returns the highest SAFE L2 block with timestamp <= ts using the L2 client.
func (c *simpleChainContainer) SafeBlockAtTimestamp(ctx context.Context, ts uint64) (eth.L2BlockRef, error) {
	if c.engine == nil {
		return eth.L2BlockRef{}, engine_controller.ErrNoEngineClient
	}
	return c.engine.SafeBlockAtTimestamp(ctx, ts)
}

// OutputRootAtL2BlockNumber computes the L2 output root for the specified L2 block number.
func (c *simpleChainContainer) OutputRootAtL2BlockNumber(ctx context.Context, l2BlockNum uint64) (eth.Bytes32, error) {
	if c.engine == nil {
		return eth.Bytes32{}, engine_controller.ErrNoEngineClient
	}
	out, err := c.engine.OutputV0AtBlockNumber(ctx, l2BlockNum)
	if err != nil {
		return eth.Bytes32{}, err
	}
	return eth.OutputRoot(out), nil
}

// SafeHeadAtL1 queries the embedded op-node RPC handler for the SafeDB mapping at/preceding the given L1 block number.
func (c *simpleChainContainer) SafeHeadAtL1(ctx context.Context, l1BlockNum uint64) (eth.BlockID, eth.BlockID, error) {
	if c.vn == nil {
		return eth.BlockID{}, eth.BlockID{}, fmt.Errorf("virtual node not initialized")
	}
	return c.vn.SafeHeadAtL1(ctx, l1BlockNum)
}

// L1AtSafeHead delegates to the virtual node to resolve the earliest L1 at which the L2 became safe.
func (c *simpleChainContainer) L1AtSafeHead(ctx context.Context, l2 eth.BlockID) (eth.BlockID, error) {
	if c.vn == nil {
		return eth.BlockID{}, fmt.Errorf("virtual node not initialized")
	}
	return c.vn.L1AtSafeHead(ctx, l2)
}

// CurrentL1 returns the most recent processed L1 block reference based on the derivation pipeline sync status.
func (c *simpleChainContainer) CurrentL1(ctx context.Context) (eth.BlockRef, error) {
	if c.vn == nil {
		if c.log != nil {
			c.log.Warn("CurrentL1: virtual node not initialized")
		}
		return eth.BlockRef{}, nil
	}
	return c.vn.CurrentL1(ctx)
}

// VerifiedAt returns the verified L2 and L1 blocks for the given L2 timestamp.
func (c *simpleChainContainer) VerifiedAt(ctx context.Context, ts uint64) (l2, l1 eth.BlockID, err error) {
	l2Block, err := c.SafeBlockAtTimestamp(ctx, ts)
	if err != nil {
		c.log.Error("error determining l2 block at given timestamp", "error", err)
		return eth.BlockID{}, eth.BlockID{}, err
	}
	l1Block, err := c.L1AtSafeHead(ctx, l2Block.ID())
	if err != nil {
		c.log.Error("error determining l1 block number at which l2 block became safe", "error", err)
		return eth.BlockID{}, eth.BlockID{}, err
	}

	// if there were Verification Activities, we would check if the data could be *verified* at this L1, or would use its L1 block number
	// but there are currently no verification activities, so we just return the l2 and l1 blocks
	return l2Block.ID(), l1Block, nil
}

// OptimisticAt returns the optimistic (pre-verified) L2 and L1 blocks for the given L2 timestamp.
func (c *simpleChainContainer) OptimisticAt(ctx context.Context, ts uint64) (l2, l1 eth.BlockID, err error) {
	l2Block, err := c.SafeBlockAtTimestamp(ctx, ts)
	if err != nil {
		c.log.Error("error determining l2 block at given timestamp", "error", err)
		return eth.BlockID{}, eth.BlockID{}, err
	}
	l1Block, err := c.L1AtSafeHead(ctx, l2Block.ID())
	if err != nil {
		c.log.Error("error determining l1 block number at which l2 block became safe", "error", err)
		return eth.BlockID{}, eth.BlockID{}, err
	}

	// if there were Verification Activities, we could check if there was a pre-verified block which was added to the denylist
	// but there are currently no verification activities, so we just return the l2 and l1 blocks
	return l2Block.ID(), l1Block, nil
}

// OptimisticOutputAtTimestamp returns the full Output for the optimistic L2 block at the given timestamp.
// For now this simply calls the op-node's normal OutputAtBlock for the block number computed from the timestamp.
func (c *simpleChainContainer) OptimisticOutputAtTimestamp(ctx context.Context, ts uint64) (*eth.OutputResponse, error) {
	if c.rollupClient == nil {
		return nil, fmt.Errorf("rollup client not initialized")
	}
	// Determine the optimistic L2 block at timestamp (currently same as safe block at ts)
	l2Block, err := c.SafeBlockAtTimestamp(ctx, ts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve L2 block at timestamp: %w", err)
	}
	// Call the standard OutputAtBlock RPC
	out, err := c.rollupClient.OutputAtBlock(ctx, l2Block.Number)
	if err != nil {
		return nil, fmt.Errorf("failed to get output at block %d: %w", l2Block.Number, err)
	}
	return out, nil
}

// attachInProcRollupClient creates a new in-proc rollup RPC client bound to the current rpcHandler.
// It will close any existing client before replacing it.
func (c *simpleChainContainer) attachInProcRollupClient() error {
	if c.rpcHandler == nil {
		return fmt.Errorf("rpc handler not initialized")
	}
	inproc, err := c.rpcHandler.DialInProc()
	if err != nil {
		return err
	}
	// Close previous rollup client if present
	if c.rollupClient != nil {
		c.rollupClient.Close()
	}
	c.rollupClient = sources.NewRollupClient(client.NewBaseRPCClient(inproc))
	return nil
}

// isCriticalRewindError returns true if the error is a critical configuration error
// that should not be retried.
func isCriticalRewindError(err error) bool {
	return errors.Is(err, engine_controller.ErrNoEngineClient) ||
		errors.Is(err, engine_controller.ErrNoRollupConfig) ||
		errors.Is(err, engine_controller.ErrRewindComputeTargetsFailed) ||
		errors.Is(err, engine_controller.ErrRewindTimestampToBlockConversion) ||
		errors.Is(err, engine_controller.ErrRewindOverFinalizedHead)
}

// WARNING: this should only be called by the interop activity.
// Other callers risk triggering chain rewinds outside the interop WAL model.
// TODO(#19561): remove this footgun by moving reorg-triggering operations behind a
// smaller interop-owned interface.
func (c *simpleChainContainer) RewindEngine(ctx context.Context, timestamp uint64, invalidatedBlock eth.BlockRef) error {
	if !c.resetting.CompareAndSwap(false, true) {
		return fmt.Errorf("reset already in progress")
	}
	defer c.resetting.Store(false)

	if c.vn == nil {
		return fmt.Errorf("virtual node not initialized")
	}
	if c.engine == nil {
		return fmt.Errorf("engine not initialized")
	}

	// Pause the container to stop it restarting the vn when we kill it
	err := c.Pause(ctx)
	if err != nil {
		return err
	}
	// Always resume the container on return, even if we exit early due to context cancellation
	// or an error mid-rewind. Without this, a cancelled ctx leaves pause=true permanently,
	// causing the Start() loop to spin forever and block Supernode.Stop()'s wg.Wait().
	defer c.Resume(context.Background()) //nolint:errcheck
	c.log.Info("chain_container/RewindEngine: paused container")

	// stop the vn
	err = c.vn.Stop(ctx)
	if err != nil {
		return err
	}
	c.log.Info("chain_container/RewindEngine: stopped vn")

retryLoop:
	for {
		err = c.engine.RewindToTimestamp(ctx, timestamp)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			c.log.Error("chain_container/RewindEngine: timeout exceeded")
			return err
		case isCriticalRewindError(err):
			c.log.Error("chain_container/RewindEngine: critical error", "err", err)
			return err
		case err == nil:
			c.log.Info("chain_container/RewindEngine: executed engine rewind")
			break retryLoop
		default:
			c.log.Error("chain_container/RewindEngine: temporary error", "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}

	// Notify activities about the reset
	if c.onReset != nil {
		c.onReset(c.chainID, timestamp, invalidatedBlock)
	}

	// resume the chain container to trigger a new vn to be started
	err = c.Resume(ctx)
	if err != nil {
		return err
	}
	c.log.Info("chain_container/RewindEngine: resumed container")

	return nil
}

// PauseAndStopVN pauses the container restart loop and stops the running virtual node.
// This must be called before a multi-chain rewind to prevent a peer chain's VN from
// issuing forkchoice updates that race with the rewind operation.
// RewindEngine's own Pause+Stop calls are idempotent when called after this.
func (c *simpleChainContainer) PauseAndStopVN(ctx context.Context) error {
	if err := c.Pause(ctx); err != nil {
		return err
	}
	if c.vn == nil {
		return nil
	}
	return c.vn.Stop(ctx)
}

// SetResetCallback sets a callback that is invoked when the chain resets.
// This must only be called during initialization, before the chain container starts processing.
// Calling this while InvalidateBlock may be running is unsafe.
func (c *simpleChainContainer) SetResetCallback(cb ResetCallback) {
	c.onReset = cb
}

// blockNumberToTimestamp converts a block number to its timestamp using rollup config.
func (c *simpleChainContainer) blockNumberToTimestamp(blockNum uint64) (uint64, error) {
	if c.vncfg == nil {
		return 0, fmt.Errorf("rollup config not available")
	}
	if blockNum < c.vncfg.Rollup.Genesis.L2.Number {
		return 0, fmt.Errorf("block number %d before genesis %d", blockNum, c.vncfg.Rollup.Genesis.L2.Number)
	}
	return c.vncfg.Rollup.TimestampForBlock(blockNum), nil
}
