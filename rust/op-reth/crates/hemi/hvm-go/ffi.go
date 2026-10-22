// Copyright (c) 2026 Hemi Labs, Inc.
// Use of this source code is governed by the MIT License,
// which can be found in the LICENSE file.

// The C ABI op-reth uses to drive hVM. Every function returns an HvmStatus code; byte outputs are
// allocated with malloc and must be released with HvmFree.

package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/btcsuite/btcd/wire"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	gethleveldb "github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/hemilabs/heminetwork/cmd/btctool/bdf"
	"github.com/hemilabs/heminetwork/service/tbc"
	"github.com/mitchellh/go-homedir"
	"github.com/syndtr/goleveldb/leveldb"
)

// Status codes, mirrored in the Rust crate (reth-optimism-hemi::ffi).
const (
	statusOK = 0
	// statusDefer: the block cannot be processed yet because the full TBC node is missing Bitcoin
	// data; it may succeed later (op-geth returns SYNCING to op-node in this case).
	statusDefer = 1
	// statusInvalid: the block is invalid with respect to hVM consensus.
	statusInvalid = 2
	// statusError: any other error.
	statusError = 3
	// statusDisabled: hVM is not enabled on this node.
	statusDisabled = 4
	// statusOutOfGas: a precompile was called with too little gas.
	statusOutOfGas = 5
)

type initConfig struct {
	Enabled bool `json:"enabled"`
	// Hvm0Time is the hVM Phase 0 activation timestamp.
	Hvm0Time *uint64 `json:"hvm0Time"`
	// Network is the Bitcoin network (tbc.network).
	Network string `json:"network"`
	// GenesisHeader / GenesisHeight: hVM effective genesis (hvm.genesisheader / hvm.genesisheight).
	GenesisHeader string `json:"genesisHeader"`
	GenesisHeight uint64 `json:"genesisHeight"`
	// HeaderDataDir: lightweight TBC node data directory (hvm.headerdatadir).
	HeaderDataDir string `json:"headerDataDir"`
	// RegistryDir: where the L2 block registry and the keystones are stored.
	RegistryDir string `json:"registryDir"`

	TbcLevelDBHome       string   `json:"tbcLevelDBHome"`
	TbcListenAddress     string   `json:"tbcListenAddress"`
	TbcMaxCachedTxs      int      `json:"tbcMaxCachedTxs"`
	TbcBlockSanity       bool     `json:"tbcBlockSanity"`
	TbcPrometheusAddress string   `json:"tbcPrometheusAddress"`
	TbcSeeds             []string `json:"tbcSeeds"`

	// LogLevel is a geth verbosity (0-5).
	LogLevel int `json:"logLevel"`
}

var (
	bc         *BlockChain
	keystoneDB ethdb.Database
	headerCfg  *tbc.Config
	mainCtx    context.Context

	// session is the block op-reth is currently executing; bc.chainmu is held while it is set.
	session struct {
		active    bool
		build     bool
		tbcHeader *Header
		block     *Block
	}
)

func setOut(out **C.uchar, outLen *C.size_t, b []byte) {
	if out == nil || outLen == nil {
		return
	}
	if len(b) == 0 {
		*out = nil
		*outLen = 0
		return
	}
	p := C.malloc(C.size_t(len(b)))
	copy(unsafe.Slice((*byte)(p), len(b)), b)
	*out = (*C.uchar)(p)
	*outLen = C.size_t(len(b))
}

func setErr(out **C.uchar, outLen *C.size_t, err error) {
	setOut(out, outLen, []byte(err.Error()))
}

func goBytes(p *C.uchar, n C.size_t) []byte {
	if p == nil || n == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(p), C.int(n))
}

func hash32(p *C.uchar) common.Hash {
	return common.BytesToHash(C.GoBytes(unsafe.Pointer(p), 32))
}

//export HvmFree
func HvmFree(p unsafe.Pointer) {
	C.free(p)
}

func gethLogLevel(v int) slog.Level {
	switch {
	case v <= 0:
		return log.LevelCrit
	case v == 1:
		return log.LevelError
	case v == 2:
		return log.LevelWarn
	case v == 3:
		return log.LevelInfo
	case v == 4:
		return log.LevelDebug
	default:
		return log.LevelTrace
	}
}

// HvmInit opens the registry and, when hVM is enabled, starts the full TBC node and waits until it
// is indexed to the hVM effective genesis, like op-geth does at startup.
//
//export HvmInit
func HvmInit(cfgJSON *C.uchar, cfgLen C.size_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	var cfg initConfig
	if err := json.Unmarshal(goBytes(cfgJSON, cfgLen), &cfg); err != nil {
		setErr(out, outLen, fmt.Errorf("invalid hVM config: %w", err))
		return statusError
	}

	level := cfg.LogLevel
	if level == 0 {
		level = 3
	}
	log.SetDefault(log.NewLogger(log.NewTerminalHandlerWithLevel(os.Stderr,
		gethLogLevel(level), false)))

	mainCtx = context.Background()
	bc = &BlockChain{
		ctx:         mainCtx,
		chainConfig: &chainConfig{Hvm0Time: cfg.Hvm0Time},
	}

	registryDir, err := homedir.Expand(cfg.RegistryDir)
	if err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	bc.registry, err = leveldb.OpenFile(registryDir+"/registry", nil)
	if err != nil {
		setErr(out, outLen, fmt.Errorf("open hVM registry: %w", err))
		return statusError
	}
	kv, err := gethleveldb.New(registryDir+"/keystones", 16, 16, "hvm/keystones", false)
	if err != nil {
		setErr(out, outLen, fmt.Errorf("open keystone db: %w", err))
		return statusError
	}
	keystoneDB = rawdb.NewDatabase(kv)

	if v, err := bc.registry.Get(registryHeadKey, nil); err == nil {
		if h := bc.GetHeaderByHash(common.BytesToHash(v)); h != nil {
			bc.currentBlock.Store(h)
		}
	}

	if !cfg.Enabled {
		return statusOK
	}

	// Full TBC node (cmd/geth/config.go in hemilabs/op-geth).
	fullCfg := tbc.NewDefaultConfig()
	if cfg.TbcListenAddress != "" {
		fullCfg.ListenAddress = cfg.TbcListenAddress
	}
	if cfg.TbcMaxCachedTxs != 0 {
		fullCfg.MaxCachedTxs = cfg.TbcMaxCachedTxs
	}
	if cfg.TbcLevelDBHome != "" {
		fullCfg.LevelDBHome = cfg.TbcLevelDBHome
	}
	fullCfg.BlockSanity = cfg.TbcBlockSanity
	fullCfg.Network = cfg.Network
	if cfg.TbcPrometheusAddress != "" {
		fullCfg.PrometheusListenAddress = cfg.TbcPrometheusAddress
	}
	if len(cfg.TbcSeeds) > 0 {
		fullCfg.Seeds = cfg.TbcSeeds
	}
	fullCfg.PeersWanted = 16
	fullCfg.MempoolEnabled = false
	tbcLevel := "INFO"
	if level == 4 {
		tbcLevel = "DEBUG"
	} else if level >= 5 {
		tbcLevel = "TRACE"
	}
	fullCfg.LogLevel = "tbcd=" + tbcLevel + ";tbc=" + tbcLevel + ";level=" + tbcLevel
	if fullCfg.AutoIndex {
		setErr(out, outLen, errors.New("refusing to start TBC full node with AutoIndex=true"))
		return statusError
	}

	genesisHeader, err := bdf.Hex2Header(cfg.GenesisHeader)
	if err != nil {
		setErr(out, outLen, fmt.Errorf("unable to parse hVM effective-genesis header: %w", err))
		return statusError
	}
	if IsMismatchedHvmGenesisPairing(fullCfg.Network, cfg.GenesisHeight, genesisHeader.BlockHash().String()) {
		setErr(out, outLen, fmt.Errorf("hVM effective-genesis (height,header) pair is desynced from the "+
			"canonical checkpoint for network %q", fullCfg.Network))
		return statusError
	}

	if err := vm.SetupTBCFullNode(mainCtx, fullCfg); err != nil {
		setErr(out, outLen, fmt.Errorf("unable to set up TBC full node: %w", err))
		return statusError
	}
	for !vm.TBCFullNode.Running() {
		time.Sleep(100 * time.Millisecond)
	}
	if err := waitFullNodeAtGenesis(genesisHeader, cfg.GenesisHeight); err != nil {
		setErr(out, outLen, err)
		return statusError
	}

	// Lightweight (header-only) TBC node configuration (eth/backend.go buildHvmHeaderNodeConfig).
	headerDir, err := homedir.Expand(cfg.HeaderDataDir)
	if err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	hc := tbc.NewDefaultConfig()
	hc.ExternalHeaderMode = true
	hc.EffectiveGenesisBlock = genesisHeader
	hc.GenesisHeightOffset = cfg.GenesisHeight
	hc.LevelDBHome = headerDir
	hc.BlockheaderCacheSize = "0"
	hc.BlockCacheSize = "0"
	hc.AutoIndex = false
	hc.BlockSanity = true
	hc.MaxCachedTxs = 0
	hc.MempoolEnabled = false
	hc.Network = cfg.Network
	headerCfg = hc

	return statusOK
}

// waitFullNodeAtGenesis is the startup loop of hemilabs/op-geth cmd/geth/config.go: wait until the
// full TBC node knows the hVM effective genesis block and index it there.
func waitFullNodeAtGenesis(genesisHeader *wire.BlockHeader, genesisHeight uint64) error {
	genesisHash := genesisHeader.BlockHash()
	for {
		time.Sleep(time.Second)

		bh, bhb, err := vm.TBCFullNode.BlockHeaderBest(mainCtx)
		if err != nil {
			return fmt.Errorf("could not get best block header from TBC full node: %w", err)
		}
		syncInfo := *vm.GetTBCFullNodeSyncStatus()
		vm.FixMismatchedIndexesIfRequired(mainCtx)

		if bh < genesisHeight {
			log.Info(fmt.Sprintf("TBC Full Node is performing initial sync, current tip: %s @ %d",
				bhb.BlockHash().String(), bh))
			continue
		}

		available, err := vm.TBCFullNode.FullBlockAvailable(mainCtx, genesisHash)
		if err != nil {
			return fmt.Errorf("unable to check whether the hVM genesis block is available: %w", err)
		}
		if !available {
			continue
		}
		if syncInfo.Utxo.Height < genesisHeight {
			available, missing, missingHeader, err := vm.TBCBlocksAvailableToHeader(mainCtx, genesisHeader)
			if err != nil {
				return fmt.Errorf("checking blocks available to hVM genesis: %w", err)
			}
			if !available {
				if missingHeader != nil {
					return fmt.Errorf("TBC full node is missing header %s below the hVM genesis", missingHeader)
				}
				if missing != nil && len(*missing) < 16 {
					for i := range *missing {
						vm.TBCAttemptBlockRefetch(mainCtx, &(*missing)[i])
					}
				}
				continue
			}
			if err := vm.TBCIndexToHeader(genesisHeader, genesisHeader); err != nil {
				return fmt.Errorf("unable to index the TBC full node to the hVM genesis: %w", err)
			}
		}
		log.Info(fmt.Sprintf("TBC full node indexed to block %s @ %d which is at or beyond hVM effective genesis "+
			"block %s @ %d", bhb.BlockHash().String(), bh, genesisHash.String(), genesisHeight))
		return nil
	}
}

// HvmStart sets up the lightweight TBC node. op-reth must have registered the canonical head and
// called HvmSetHead before.
//
//export HvmStart
func HvmStart(out **C.uchar, outLen *C.size_t) C.int32_t {
	if headerCfg == nil {
		return statusDisabled
	}
	if bc.currentBlock.Load() == nil {
		setErr(out, outLen, errors.New("HvmStart called before the canonical head was set"))
		return statusError
	}
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()
	bc.SetupHvmHeaderNode(headerCfg)
	return statusOK
}

func blockFromArgs(hash, parent *C.uchar, number, timestamp C.uint64_t, attrs *C.uchar, attrsLen C.size_t) (*Block, error) {
	b := &Block{header: &Header{
		hash:       hash32(hash),
		ParentHash: hash32(parent),
		Number:     new(big.Int).SetUint64(uint64(number)),
		Time:       uint64(timestamp),
	}}
	// attrs: (len(4 BE) || data)*
	rest := goBytes(attrs, attrsLen)
	for len(rest) > 0 {
		if len(rest) < 4 {
			return nil, errors.New("malformed Bitcoin Attributes Deposited data")
		}
		l := uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3])
		rest = rest[4:]
		if uint32(len(rest)) < l {
			return nil, errors.New("malformed Bitcoin Attributes Deposited data")
		}
		b.btcAttrData = append(b.btcAttrData, common.CopyBytes(rest[:l]))
		rest = rest[l:]
	}
	return b, nil
}

// HvmRegisterBlock records the hVM metadata of an L2 block: its hash, parent, number, timestamp and
// the data of its Bitcoin Attributes Deposited transactions, encoded as (len(4 BE) || data)*.
//
//export HvmRegisterBlock
func HvmRegisterBlock(hash, parent *C.uchar, number, timestamp C.uint64_t, attrs *C.uchar, attrsLen C.size_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if bc == nil {
		return statusDisabled
	}
	b, err := blockFromArgs(hash, parent, number, timestamp, attrs, attrsLen)
	if err != nil {
		setErr(out, outLen, err)
		return statusInvalid
	}
	if err := bc.registerBlock(b); err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	return statusOK
}

// HvmSetHead makes hash the canonical head and moves the hVM state to it, like op-geth does when
// it writes a new head block.
//
//export HvmSetHead
func HvmSetHead(hash *C.uchar, out **C.uchar, outLen *C.size_t) C.int32_t {
	if bc == nil {
		return statusDisabled
	}
	h := bc.GetHeaderByHash(hash32(hash))
	if h == nil {
		setErr(out, outLen, fmt.Errorf("unknown head %s", hash32(hash)))
		return statusError
	}
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()
	if err := bc.setCanonicalHead(h); err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	if !bc.hvmEnabled {
		return statusOK
	}
	if err := bc.updateHvmHeaderConsensus(h, true); err != nil {
		if isHvmFullNodeBehind(err) {
			log.Warn("Full TBC node is behind the new head; its indexers catch up on a later import",
				"head", h.Hash(), "err", err)
			return statusOK
		}
		if isHvmReapplyRecoverableError(err) {
			bc.recoverReapplyHvmState(fmt.Sprintf("head %s @ %d", h.Hash(), h.Number.Uint64()), err)
			return statusOK
		}
		setErr(out, outLen, err)
		return statusError
	}
	return statusOK
}

func statusForErr(err error) C.int32_t {
	switch {
	case err == nil:
		return statusOK
	case isHvmFullNodeBehind(err), errors.Is(err, consensus.ErrFullTBCMissingFullBTCBlock),
		errors.Is(err, consensus.ErrFullTBCMissingBTCHeader), errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState):
		return statusDefer
	case errors.Is(err, consensus.ErrInvalidHVMHeaders), errors.Is(err, consensus.ErrInvalidHVMBlockFormat),
		errors.Is(err, consensus.ErrBadTraversalGeometry):
		return statusInvalid
	default:
		return statusError
	}
}

// HvmPrepareBlock moves the hVM state to the given (registered) block before op-reth executes it:
// the lightweight TBC node to the block's parent and then the block, and the full TBC node to the
// lightweight tip minus the indexer lag (core.BlockChain.ProcessBlock in hemilabs/op-geth).
//
// On statusOK the hVM lock stays held until HvmFinishBlock; on any other status it is released.
//
//export HvmPrepareBlock
func HvmPrepareBlock(hash *C.uchar, out **C.uchar, outLen *C.size_t) C.int32_t {
	if bc == nil || !bc.hvmEnabled {
		return statusDisabled
	}
	block := bc.GetBlockByHash(hash32(hash))
	if block == nil {
		setErr(out, outLen, fmt.Errorf("block %s is not registered", hash32(hash)))
		return statusError
	}

	bc.chainmu.Lock()
	status, err := bc.prepareBlock(block)
	if status != statusOK {
		bc.chainmu.Unlock()
		if err != nil {
			setErr(out, outLen, err)
		}
		return status
	}
	return statusOK
}

func (bc *BlockChain) prepareBlock(block *Block) (C.int32_t, error) {
	session.active, session.build, session.tbcHeader, session.block = true, false, nil, block

	if !bc.chainConfig.IsHvm0(block.Time()) {
		return statusOK, nil
	}

	isFirstHvmBlock := false
	var parent *Header
	if block.NumberU64() != 0 {
		parent = bc.GetHeaderByHash(block.ParentHash())
		if parent == nil {
			session.active = false
			return statusError, fmt.Errorf("parent %s of hVM block %s @ %d is not registered",
				block.ParentHash(), block.Hash(), block.NumberU64())
		}
		if !bc.chainConfig.IsHvm0(parent.Time) {
			isFirstHvmBlock = true
		}
	} else {
		isFirstHvmBlock = true
	}

	var err error
	if !isFirstHvmBlock {
		session.tbcHeader, err = bc.getHeaderModeTBCEVMHeader()
		if err != nil {
			session.active = false
			return statusError, fmt.Errorf("unable to get the EVM block the lightweight TBC node represents: %w", err)
		}
		if session.tbcHeader == nil || session.tbcHeader.Hash() != parent.Hash() {
			if err := bc.updateHvmHeaderConsensus(parent, false); err != nil {
				if isHvmReapplyRecoverableError(err) {
					bc.recoverReapplyHvmState(fmt.Sprintf("parent-move to %s @ %d",
						parent.Hash(), parent.Number.Uint64()), err)
					err = bc.updateHvmHeaderConsensus(parent, false)
				}
				if err != nil {
					session.active = false
					return statusForErr(err), err
				}
			}
		}
		represented, err := bc.getHeaderModeTBCEVMHeader()
		if err != nil || represented == nil || represented.Hash() != parent.Hash() {
			session.active = false
			return statusError, fmt.Errorf("lightweight TBC does not represent parent %s after moving it there", parent.Hash())
		}
	}

	if err := bc.updateHvmHeaderConsensus(block.Header(), false); err != nil {
		bc.reportBlock(block, nil, err)
		session.active = false
		return statusForErr(err), err
	}

	if err := bc.updateFullTBCToLightweight(); err != nil {
		if session.tbcHeader != nil {
			if rerr := bc.updateHvmHeaderConsensus(session.tbcHeader, false); rerr != nil {
				if isHvmReapplyRecoverableError(rerr) {
					bc.recoverReapplyHvmState("updateFullTBCToLightweight-failure revert", rerr)
				} else {
					log.Crit("unable to restore lightweight TBC node to previous state", "err", rerr)
				}
			}
		}
		session.active = false
		if isHvmFullNodeBehind(err) {
			return statusDefer, err
		}
		return statusError, err
	}
	return statusOK, nil
}

// HvmFinishBlock ends the execution started by HvmPrepareBlock or HvmPrepareBuild and releases the
// hVM lock. When the block was invalid (ok == 0) the hVM state is reverted to where it was. When
// the block is valid but does not extend the canonical head, the hVM state is moved back to the
// canonical head, like op-geth does for blocks inserted without setting the head.
//
//export HvmFinishBlock
func HvmFinishBlock(ok C.int32_t) {
	if bc == nil || !session.active {
		return
	}
	defer func() {
		session.active = false
		session.tbcHeader, session.block = nil, nil
		bc.chainmu.Unlock()
	}()
	if session.build || session.block == nil {
		return
	}
	block := session.block
	if !bc.chainConfig.IsHvm0(block.Time()) {
		return
	}
	if ok == 0 {
		bc.revertHvmStateAfterInvalidBlock(session.tbcHeader, block)
		return
	}
	current := bc.currentBlock.Load()
	if current == nil || block.ParentHash() == current.Hash() || block.Hash() == current.Hash() {
		return
	}
	if session.tbcHeader != nil {
		if err := bc.updateHvmHeaderConsensus(session.tbcHeader, true); err != nil {
			if isHvmFullNodeBehind(err) {
				log.Warn("Full TBC node is behind reverted state", "block", session.tbcHeader.Hash(), "err", err)
			} else if isHvmReapplyRecoverableError(err) {
				bc.recoverReapplyHvmState("revert lightweight view after side block", err)
			} else {
				log.Crit("Unable to revert lightweight TBC node", "err", err)
			}
		}
	}
}

// HvmPrepareBuild moves the hVM state to the parent of a block op-reth is about to build and takes
// the hVM lock until HvmFinishBlock.
//
//export HvmPrepareBuild
func HvmPrepareBuild(parentHash *C.uchar, timestamp C.uint64_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if bc == nil || !bc.hvmEnabled {
		return statusDisabled
	}
	parent := bc.GetHeaderByHash(hash32(parentHash))
	bc.chainmu.Lock()
	session.active, session.build, session.block, session.tbcHeader = true, true, nil, nil
	if parent == nil || !bc.chainConfig.IsHvm0(uint64(timestamp)) || !bc.chainConfig.IsHvm0(parent.Time) {
		return statusOK
	}
	current, err := bc.getHeaderModeTBCEVMHeader()
	if err == nil && current != nil && current.Hash() == parent.Hash() {
		return statusOK
	}
	if err := bc.updateHvmHeaderConsensus(parent, true); err != nil {
		log.Warn("Unable to move hVM state to the parent of the block being built", "parent", parent.Hash(), "err", err)
	}
	return statusOK
}

// HvmNextBtcAttr builds the Bitcoin Attributes Deposited transaction for the block op-reth builds
// on top of parentHash at timestamp. The output is to(20) || gas(8 BE) || data, or empty when the
// block should not carry one.
//
//export HvmNextBtcAttr
func HvmNextBtcAttr(parentHash *C.uchar, timestamp C.uint64_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if bc == nil || !bc.hvmEnabled {
		return statusDisabled
	}
	parent := bc.GetHeaderByHash(hash32(parentHash))
	if parent == nil {
		setErr(out, outLen, fmt.Errorf("parent %s is not registered", hash32(parentHash)))
		return statusError
	}

	// getBitcoinAttributesForNextBlock only tries the chain lock; give a concurrent block import a
	// moment to finish.
	var tx interface{ toBytes() []byte }
	for i := 0; i < 20; i++ {
		buildParentMu.Lock()
		bc.buildParent = parent
		t, err := bc.GetBitcoinAttributesForNextBlock(uint64(timestamp))
		bc.buildParent = nil
		buildParentMu.Unlock()
		if err != nil {
			setErr(out, outLen, err)
			return statusError
		}
		if t != nil {
			tx = btcAttrTx{t.To, t.Gas, t.Data}
			break
		}
		if bc.chainmu.TryLock() {
			// not busy: there is genuinely nothing to include
			bc.chainmu.Unlock()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tx == nil {
		return statusOK
	}
	setOut(out, outLen, tx.toBytes())
	return statusOK
}

var buildParentMu sync.Mutex

type btcAttrTx struct {
	to   *common.Address
	gas  uint64
	data []byte
}

func (t btcAttrTx) toBytes() []byte {
	b := make([]byte, 0, 28+len(t.data))
	var to common.Address
	if t.to != nil {
		to = *t.to
	}
	b = append(b, to[:]...)
	b = append(b, byte(t.gas>>56), byte(t.gas>>48), byte(t.gas>>40), byte(t.gas>>32),
		byte(t.gas>>24), byte(t.gas>>16), byte(t.gas>>8), byte(t.gas))
	return append(b, t.data...)
}

// HvmPrecompile runs the hVM precompile at address 0x00..addr (0x40-0x49) exactly like
// hemilabs/op-geth's EVM.runPrecompile. On statusOK, out holds the return data and gasUsed the
// gas consumed; on statusError the call failed and consumes all gas.
//
//export HvmPrecompile
func HvmPrecompile(addr C.uint8_t, input *C.uchar, inputLen C.size_t, gas C.uint64_t, gasUsed *C.uint64_t, out **C.uchar, outLen *C.size_t) (status C.int32_t) {
	p, ok := vm.PrecompiledContractsHvm0[common.BytesToAddress([]byte{byte(addr)})]
	if !ok {
		setErr(out, outLen, fmt.Errorf("no hVM precompile at 0x%x", byte(addr)))
		return statusError
	}
	in := goBytes(input, inputLen)
	cost := p.RequiredGas(in)
	if uint64(gas) < cost {
		*gasUsed = C.uint64_t(gas)
		return statusOutOfGas
	}
	*gasUsed = C.uint64_t(cost)

	defer func() {
		if r := recover(); r != nil {
			// runPrecompileGuarded: a fault inside an hVM precompile is a deterministic no-op.
			log.Error("invalid Bitcoin data in hVM precompile; treating as a no-op", "precompile", p.Name())
			setOut(out, outLen, nil)
			status = statusOK
		}
	}()
	ret, err := p.Run(in, common.Hash{})
	if errors.Is(err, vm.ErrHVMInvalidPrecompileInput) {
		setOut(out, outLen, nil)
		return statusOK
	}
	if err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	setOut(out, outLen, ret)
	return statusOK
}

// HvmBtcBlockAvailable reports whether the full TBC node has the given Bitcoin block (hash in
// internal byte order); if not it returns statusDefer.
//
//export HvmBtcBlockAvailable
func HvmBtcBlockAvailable(hash *C.uchar) C.int32_t {
	if vm.TBCFullNode == nil {
		return statusDisabled
	}
	if vm.BlockAvailableByCommonHash(hash32(hash)) {
		return statusOK
	}
	return statusDefer
}

// HvmMissingBtcBlocks returns the Bitcoin blocks hVM progression is waiting for, as 32-byte
// hashes, to request from L2 peers.
//
//export HvmMissingBtcBlocks
func HvmMissingBtcBlocks(out **C.uchar, outLen *C.size_t) C.int32_t {
	if bc == nil || !bc.hvmEnabled {
		return statusDisabled
	}
	var b []byte
	for _, h := range bc.GetMissingBtcBlocks() {
		b = append(b, h[:]...)
	}
	setOut(out, outLen, b)
	return statusOK
}

func strip0x(s string) string { return strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X") }

func main() {}

// HvmHasBlock reports whether a block is registered (statusOK) or not (statusDefer).
//
//export HvmHasBlock
func HvmHasBlock(hash *C.uchar) C.int32_t {
	if bc == nil {
		return statusDisabled
	}
	if ok, _ := bc.registry.Has(blockKey(hash32(hash)), nil); ok {
		return statusOK
	}
	return statusDefer
}
