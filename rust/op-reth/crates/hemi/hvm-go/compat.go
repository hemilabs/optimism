// Copyright (c) 2026 Hemi Labs, Inc.
// Use of this source code is governed by the MIT License,
// which can be found in the LICENSE file.

// This file is the adaptation layer that lets the hVM code ported verbatim from hemilabs/op-geth
// (ported_blockchain.go) run inside op-reth. op-geth keeps the hVM state on its core.BlockChain;
// here BlockChain only holds the hVM state, and the L2 chain it needs to walk is a registry of
// block metadata that op-reth feeds through the exported functions in ffi.go.

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/wire"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/hemilabs/heminetwork/service/tbc"
	"github.com/syndtr/goleveldb/leveldb"
)

// Values from hemilabs/op-geth core/blockchain.go.
var (
	// The upstream ID used when TBC is in its genesis configuration for Hemi
	hVMGenesisUpstreamId = [32]byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
		0x48, 0x56, 0x4D, 0x47, 0x45, 0x4E, 0x45, 0x53, 0x49, 0x53, // HVMGENESIS
		0x48, 0x56, 0x4D, 0x47, 0x45, 0x4E, 0x45, 0x53, 0x49, 0x53, // HVMGENESIS
		0x06, 0x05, 0x04, 0x03, 0x02, 0x01}

	// Temporary dummy ID used when TBC is testing application of headers that will go into a new block
	hVMDummyUpstreamId = [32]byte{
		0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C,
		0x44, 0x55, 0x4D, 0x4D, 0x59, 0x42, 0x4C, 0x4F, 0x43, 0x4B, // DUMMYBLOCK
		0x44, 0x55, 0x4D, 0x4D, 0x59, 0x42, 0x4C, 0x4F, 0x43, 0x4B, // DUMMYBLOCK
		0x0C, 0x0B, 0x0A, 0x09, 0x08, 0x07}

	emptyArray = [32]byte{}

	errChainStopped = errors.New("blockchain is stopped")

	ErrExternalHeaderTBCInvalidState = errors.New("external header TBC instance is in an invalid state")

	hvmBtcAttrFailMeter      = metrics.NewRegisteredMeter("chain/hvm/btcattr/fail", nil)
	hvmBtcAttrFailingGauge   = metrics.NewRegisteredGauge("chain/hvm/btcattr/failing", nil)
	hvmFullTBCBehindGauge    = metrics.NewRegisteredGauge("chain/hvm/fulltbc/behind", nil)
	hvmBtcAttrDiffTruncMeter = metrics.NewRegisteredMeter("chain/hvm/btcattr/diff_trunc", nil)
	hvmReapplyRestoreMeter   = metrics.NewRegisteredMeter("chain/hvm/reapply/restore", nil)
)

const (
	// Number of blocks behind the lightweight TBC canonical tip that the full TBC node is indexed to.
	hVMIndexerTipLag = 2

	// 0x1a03fffc = difficulty of 4194304
	testnet3LowDiffThresholdForTipLag = 436469756
)

// migrationCrit: op-geth's legacy testnet3->mainnet header-store migration never runs in op-reth
// (a fresh op-reth node has no legacy store), so its crit is a plain crit.
func migrationCrit(msg string, ctx ...interface{}) {
	log.Crit(msg, ctx...)
}

// chainConfig is the subset of op-geth's params.ChainConfig the hVM code reads.
type chainConfig struct {
	Hvm0Time *uint64
}

// IsHvm0 returns whether time is either equal to the hVM Phase 0 fork time or greater.
func (c *chainConfig) IsHvm0(time uint64) bool {
	return c.Hvm0Time != nil && *c.Hvm0Time <= time
}

// Header is the subset of an L2 header the hVM code reads.
type Header struct {
	hash       common.Hash
	ParentHash common.Hash
	Number     *big.Int
	Time       uint64
}

// Hash returns the L2 block hash, computed by op-reth.
func (h *Header) Hash() common.Hash { return h.hash }

// Block is an L2 block as far as hVM is concerned: its header and the data of its Bitcoin
// Attributes Deposited transaction(s).
type Block struct {
	header *Header
	// btcAttrData holds the data of each Bitcoin Attributes Deposited transaction in the block, in
	// order. A valid block has at most one.
	btcAttrData [][]byte
}

func (b *Block) Hash() common.Hash       { return b.header.hash }
func (b *Block) ParentHash() common.Hash { return b.header.ParentHash }
func (b *Block) NumberU64() uint64       { return b.header.Number.Uint64() }
func (b *Block) Number() *big.Int        { return new(big.Int).Set(b.header.Number) }
func (b *Block) Time() uint64            { return b.header.Time }
func (b *Block) Header() *Header         { return b.header }

// Transactions returns a view of the block transactions that can extract the Bitcoin Attributes
// Deposited data, like op-geth's types.Transactions.
func (b *Block) Transactions() blockTransactions { return blockTransactions{b} }

type blockTransactions struct{ b *Block }

// ExtractBtcAttrData mirrors op-geth's types.Transactions.ExtractBtcAttrData.
func (t blockTransactions) ExtractBtcAttrData() (*types.BtcAttributesDepositData, error) {
	var extracted *types.BtcAttributesDepositData
	for _, data := range t.b.btcAttrData {
		if extracted != nil {
			return nil, fmt.Errorf("transactions contain more than one Bitcoin Attributes Deposited transaction")
		}
		var parsed types.BtcAttributesDepositData
		if err := parsed.UnmarshalBinary(data); err != nil {
			return nil, err
		}
		extracted = &parsed
	}
	return extracted, nil
}

// btcBlockReporter is satisfied by op-geth's reportBlock arguments; op-reth reports bad blocks
// itself, so this only logs.
type processResult interface{}

// BlockChain holds the hVM state that op-geth keeps on its core.BlockChain.
type BlockChain struct {
	ctx context.Context

	chainConfig *chainConfig

	hvmEnabled          bool
	tbcHeaderNode       *tbc.Server
	tbcHeaderNodeConfig *tbc.Config
	tbcHeaderNodeMu     sync.RWMutex

	hvmDiffEnforceable     atomic.Bool
	hvmMigrationInProgress bool

	// chainmu serializes every change of the lightweight TBC node, like op-geth's chain mutex.
	chainmu sync.Mutex

	btcAttributesDepCacheKey   btcAttrCacheKey
	btcAttributesDepCacheEntry *types.BtcAttributesDepositedTx

	missingProgressionBlocks *wire.MsgHeaders

	// currentBlock is the canonical L2 head, as reported by op-reth.
	currentBlock atomic.Pointer[Header]

	// buildParent, when set, overrides CurrentBlock while generating a Bitcoin Attributes
	// Deposited transaction for a block built on top of it.
	buildParent *Header

	// registry stores the metadata of every L2 block op-reth processed and the canonical index.
	registry *leveldb.DB

	// pending holds blocks op-reth is executing before they are persisted, op-geth's holding pen.
	pendingMu sync.RWMutex
	pending   map[common.Hash]*Block
}

var (
	registryBlockPrefix     = []byte("b")
	registryCanonicalPrefix = []byte("c")
	registryHeadKey         = []byte("head")
)

func canonicalKey(number uint64) []byte {
	k := make([]byte, 0, len(registryCanonicalPrefix)+8)
	k = append(k, registryCanonicalPrefix...)
	return binary.BigEndian.AppendUint64(k, number)
}

func blockKey(hash common.Hash) []byte {
	return append(append([]byte{}, registryBlockPrefix...), hash[:]...)
}

// encodeBlock: parent(32) || number(8) || time(8) || count(4) || (len(4) || data)*
func encodeBlock(b *Block) []byte {
	var buf bytes.Buffer
	buf.Write(b.header.ParentHash[:])
	_ = binary.Write(&buf, binary.BigEndian, b.header.Number.Uint64())
	_ = binary.Write(&buf, binary.BigEndian, b.header.Time)
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(b.btcAttrData)))
	for _, d := range b.btcAttrData {
		_ = binary.Write(&buf, binary.BigEndian, uint32(len(d)))
		buf.Write(d)
	}
	return buf.Bytes()
}

func decodeBlock(hash common.Hash, v []byte) (*Block, error) {
	if len(v) < 52 {
		return nil, fmt.Errorf("short hVM registry entry for %s", hash)
	}
	h := &Header{hash: hash}
	copy(h.ParentHash[:], v[:32])
	h.Number = new(big.Int).SetUint64(binary.BigEndian.Uint64(v[32:40]))
	h.Time = binary.BigEndian.Uint64(v[40:48])
	n := binary.BigEndian.Uint32(v[48:52])
	rest := v[52:]
	b := &Block{header: h}
	for i := uint32(0); i < n; i++ {
		if len(rest) < 4 {
			return nil, fmt.Errorf("corrupt hVM registry entry for %s", hash)
		}
		l := binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		if uint32(len(rest)) < l {
			return nil, fmt.Errorf("corrupt hVM registry entry for %s", hash)
		}
		b.btcAttrData = append(b.btcAttrData, common.CopyBytes(rest[:l]))
		rest = rest[l:]
	}
	return b, nil
}

// registerBlock records the hVM-relevant metadata of an L2 block.
func (bc *BlockChain) registerBlock(b *Block) error {
	return bc.registry.Put(blockKey(b.Hash()), encodeBlock(b), nil)
}

// GetBlockByHash returns a registered block.
func (bc *BlockChain) GetBlockByHash(hash common.Hash) *Block {
	bc.pendingMu.RLock()
	if b, ok := bc.pending[hash]; ok {
		bc.pendingMu.RUnlock()
		return b
	}
	bc.pendingMu.RUnlock()
	v, err := bc.registry.Get(blockKey(hash), nil)
	if err != nil {
		return nil
	}
	b, err := decodeBlock(hash, v)
	if err != nil {
		log.Error("unable to decode hVM registry entry", "hash", hash, "err", err)
		return nil
	}
	return b
}

// GetHeaderByHash returns the header of a registered block.
func (bc *BlockChain) GetHeaderByHash(hash common.Hash) *Header {
	if b := bc.GetBlockByHash(hash); b != nil {
		return b.header
	}
	return nil
}

// GetHeader returns the header of a registered block with the given hash and number.
func (bc *BlockChain) GetHeader(hash common.Hash, number uint64) *Header {
	h := bc.GetHeaderByHash(hash)
	if h == nil || h.Number.Uint64() != number {
		return nil
	}
	return h
}

// GetHeaderByNumber returns the canonical header at number.
func (bc *BlockChain) GetHeaderByNumber(number uint64) *Header {
	v, err := bc.registry.Get(canonicalKey(number), nil)
	if err != nil {
		return nil
	}
	return bc.GetHeaderByHash(common.BytesToHash(v))
}

// GetBlockByNumber returns the canonical block at number.
func (bc *BlockChain) GetBlockByNumber(number uint64) *Block {
	h := bc.GetHeaderByNumber(number)
	if h == nil {
		return nil
	}
	return bc.GetBlockByHash(h.hash)
}

// CurrentBlock returns the canonical head (or the parent of the block being built).
func (bc *BlockChain) CurrentBlock() *Header {
	if bc.buildParent != nil {
		return bc.buildParent
	}
	return bc.currentBlock.Load()
}

// CurrentHeader is CurrentBlock.
func (bc *BlockChain) CurrentHeader() *Header { return bc.CurrentBlock() }

// setCanonicalHead updates the canonical index so that it ends at head.
func (bc *BlockChain) setCanonicalHead(head *Header) error {
	batch := new(leveldb.Batch)

	// drop canonical entries above the new head
	for n := head.Number.Uint64() + 1; ; n++ {
		if ok, _ := bc.registry.Has(canonicalKey(n), nil); !ok {
			break
		}
		batch.Delete(canonicalKey(n))
	}

	// walk back until the index agrees
	cursor := head
	for {
		n := cursor.Number.Uint64()
		v, err := bc.registry.Get(canonicalKey(n), nil)
		if err == nil && common.BytesToHash(v) == cursor.hash {
			break
		}
		batch.Put(canonicalKey(n), cursor.hash[:])
		if n == 0 {
			break
		}
		parent := bc.GetHeaderByHash(cursor.ParentHash)
		if parent == nil {
			// the registry does not know older blocks (op-reth registered the chain from here)
			break
		}
		cursor = parent
	}
	batch.Put(registryHeadKey, head.hash[:])
	if err := bc.registry.Write(batch, nil); err != nil {
		return err
	}
	bc.currentBlock.Store(head)
	return nil
}

func (bc *BlockChain) getBlockFromDiskOrHoldingPen(hash common.Hash) *Block {
	return bc.GetBlockByHash(hash) // Upstream must check if nil
}

func (bc *BlockChain) getHeaderFromDiskOrHoldingPen(hash common.Hash) *Header {
	return bc.GetHeaderByHash(hash) // Upstream must check if nil
}

// reportBlock: op-reth reports invalid blocks itself.
func (bc *BlockChain) reportBlock(block *Block, res processResult, err error) {
	if block != nil {
		log.Error("hVM rejected block", "hash", block.Hash(), "number", block.NumberU64(), "err", err)
	}
}

// isAwaitingHvmSnapSync: op-reth never snap syncs, it executes every block.
func (bc *BlockChain) isAwaitingHvmSnapSync() bool { return false }

// maybeMigrateHvmHeaderNode: op-geth's legacy header-store migration does not apply to op-reth.
func (bc *BlockChain) maybeMigrateHvmHeaderNode(config *tbc.Config) bool { return false }

// Config returns the chain configuration.
func (bc *BlockChain) Config() *chainConfig { return bc.chainConfig }

var _ = time.Second

// hvmHeaderStoreDir is the network-scoped store directory of the lightweight TBC node (from
// hemilabs/op-geth core/blockchain_hvm_migration.go).
func hvmHeaderStoreDir(levelDBHome, network string) string {
	return filepath.Join(levelDBHome, canonicalBTCNetwork(network))
}
