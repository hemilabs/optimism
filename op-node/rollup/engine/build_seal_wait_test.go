package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"

	opmetrics "github.com/ethereum-optimism/optimism/op-node/metrics"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/event"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
)

// sealWaitEngine only answers engine_getPayload, with a payload whose
// timestamp is still in the future.
type sealWaitEngine struct {
	ExecEngine
	envelope *eth.ExecutionPayloadEnvelope
}

func (e *sealWaitEngine) GetPayload(ctx context.Context, info eth.PayloadInfo) (*eth.ExecutionPayloadEnvelope, error) {
	return e.envelope, nil
}

type sealWaitEmitter struct {
	sealedAt time.Time
	sealed   *BuildSealedEvent
	other    []event.Event
}

func (c *sealWaitEmitter) Emit(ctx context.Context, ev event.Event) {
	if x, ok := ev.(BuildSealedEvent); ok {
		c.sealedAt = time.Now()
		c.sealed = &x
		return
	}
	c.other = append(c.other, ev)
}

// TestBuildSealWaitsForBlockTimestamp ensures that a block that is sealed
// before its timestamp (the sequencer does that after a temporary error) is
// not handed out, and so not published or inserted, before its timestamp.
func TestBuildSealWaitsForBlockTimestamp(t *testing.T) {
	const ahead = 2 * time.Second

	blockHash := common.HexToHash("0xabc1")
	cfg := &rollup.Config{BlockTime: 12}
	cfg.Genesis.L2 = eth.BlockID{Hash: blockHash, Number: 100}
	cfg.Genesis.L1 = eth.BlockID{Hash: common.HexToHash("0x11"), Number: 7}

	// a whole-second timestamp between 1 and 2 seconds in the future
	timestamp := uint64(time.Now().Add(ahead).Unix())
	engine := &sealWaitEngine{envelope: &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		BlockNumber:  100,
		BlockHash:    blockHash,
		Timestamp:    eth.Uint64Quantity(timestamp),
		Transactions: []eth.Data{{types.DepositTxType}},
	}}}
	emitter := &sealWaitEmitter{}
	controller := &EngineController{
		engine:    engine,
		log:       testlog.Logger(t, log.LevelDebug),
		metrics:   opmetrics.NoopMetrics,
		rollupCfg: cfg,
		ctx:       context.Background(),
		emitter:   emitter,
	}

	start := time.Now()
	controller.onBuildSeal(context.Background(), BuildSealEvent{
		Info:         eth.PayloadInfo{ID: eth.PayloadID{1}, Timestamp: timestamp},
		BuildStarted: start,
	})
	require.NotNil(t, emitter.sealed, "no BuildSealedEvent, other events: %v", emitter.other)
	require.Empty(t, emitter.other)
	require.False(t, emitter.sealedAt.Before(time.Unix(int64(timestamp), 0)),
		"the block was handed out at %s, before its timestamp %d", emitter.sealedAt, timestamp)
	require.Less(t, emitter.sealedAt.Sub(start), ahead+time.Second, "the sealer waited longer than the block timestamp")
}

// TestBuildSealDoesNotWaitForPastTimestamp ensures that the wait only
// applies to blocks ahead of the clock.
func TestBuildSealDoesNotWaitForPastTimestamp(t *testing.T) {
	blockHash := common.HexToHash("0xabc2")
	cfg := &rollup.Config{BlockTime: 12}
	cfg.Genesis.L2 = eth.BlockID{Hash: blockHash, Number: 100}
	cfg.Genesis.L1 = eth.BlockID{Hash: common.HexToHash("0x11"), Number: 7}

	timestamp := uint64(time.Now().Add(-10 * time.Second).Unix())
	engine := &sealWaitEngine{envelope: &eth.ExecutionPayloadEnvelope{ExecutionPayload: &eth.ExecutionPayload{
		BlockNumber:  100,
		BlockHash:    blockHash,
		Timestamp:    eth.Uint64Quantity(timestamp),
		Transactions: []eth.Data{{types.DepositTxType}},
	}}}
	emitter := &sealWaitEmitter{}
	controller := &EngineController{
		engine:    engine,
		log:       testlog.Logger(t, log.LevelDebug),
		metrics:   opmetrics.NoopMetrics,
		rollupCfg: cfg,
		ctx:       context.Background(),
		emitter:   emitter,
	}

	start := time.Now()
	controller.onBuildSeal(context.Background(), BuildSealEvent{
		Info:         eth.PayloadInfo{ID: eth.PayloadID{1}, Timestamp: timestamp},
		BuildStarted: start,
	})
	require.NotNil(t, emitter.sealed)
	require.Less(t, emitter.sealedAt.Sub(start), 500*time.Millisecond)
}
