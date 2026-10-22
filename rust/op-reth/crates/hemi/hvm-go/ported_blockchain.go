// Code ported from hemilabs/op-geth core/blockchain.go (commit c1771608d7ed). DO NOT EDIT
// by hand without keeping it in sync with op-geth; see compat.go for the adaptation layer.

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/hemilabs/heminetwork/database"
	"github.com/hemilabs/heminetwork/service/tbc"
)

func (bc *BlockChain) getHeaderModeTBCEVMHeader() (*Header, error) {
	if !bc.hvmEnabled {
		return nil, fmt.Errorf("getHeaderModeTBCEVMHeader() called but hVM is not enabled")
	}

	stateId, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil {
		return nil, err
	}

	// We are at genesis configuration, no error but no header represented yet
	if bytes.Equal(stateId[:], hVMGenesisUpstreamId[:]) {
		return nil, nil
	}

	stateBlockHash := common.BytesToHash(stateId[:])
	header := bc.getHeaderFromDiskOrHoldingPen(stateBlockHash)

	if header != nil {
		return header, nil
	}
	return nil, fmt.Errorf("unable to find EVM header corresponding to hash %x", stateBlockHash[:])
}

// getHvmPhase0ActivationBlock descends the blockchain until it
// finds the first block which is after the hVM Phase 0 activation timestamp.
// TODO: cache this somewhere after calculating and make sure reorgs are considered to update cache
func (bc *BlockChain) getHvmPhase0ActivationBlock() (*Header, error) {
	if !bc.hvmEnabled {
		log.Warn("getHvmPhase0ActivationBlock called when hVM is disabled")
		return nil, fmt.Errorf("hVM is disabled")
	}

	cursor := bc.CurrentBlock()

	// Find the block where hVM Phase 0 activation occurs
	// TODO: Make this more efficient with intelligent indexing based on timestamp
	// instead of this simple descent.
	// Note: genesis block cannot contain a Bitcoin Attributes Deposited tx.
	for cursor.Number.Uint64() > 1000 {
		header := bc.GetHeaderByNumber(cursor.Number.Uint64() - 1000)
		if !bc.chainConfig.IsHvm0(header.Time) {
			// Our tip is now less than 1000 blocks above activation height, descend individually
			break
		}

		cursor = header
	}

	// Walk back until we are either at genesis or we pass behind the hVM Phase 0 activation timestamp
	for {
		// we are at genesis, no ParentHash should exist
		if cursor.Number.Uint64() == 0 {
			break
		}

		header := bc.GetHeaderByHash(cursor.ParentHash)
		if bc.chainConfig.IsHvm0(header.Time) && header.Number.Uint64() > 0 {
			cursor = header
			continue
		}
		break
	}

	return cursor, nil
}

// performFullHvmHeaderStateRestore is used to clear and completely regenerate
// the embedded header-only TBC node from genesis state, applying all
// hVM header state transitions in all blocks up to the current configured
// EVM tip.
func (bc *BlockChain) performFullHvmHeaderStateRestore() {
	if !bc.hvmEnabled {
		log.Warn("performFullHvmHeaderStateRestore called but hVM is disabled")
		return
	}

	log.Info("*****************************************************************")
	log.Info("Performing full hVM header state restore, this could take awhile.")

	bc.resetHvmHeaderNodeToGenesis()

	tip := bc.CurrentBlock()

	cursor, err := bc.getHvmPhase0ActivationBlock()
	if err != nil {
		log.Crit("Unable to get hVM Phase 0 activation block", "err", err)
	}

	// Walk cursor forward until we get to our tip, assumes GetBlockByNumber correctly returns
	// blocks on the canonical chain which will eventually reach the tip returned by bc.CurrentBlock() above
	log.Info(fmt.Sprintf("Performing full hVM header state restore starting at block %s @ %d",
		cursor.Hash().String(), cursor.Number.Uint64()))

	for {
		// Print out progress so we know restore is progressing
		if cursor.Number.Uint64()%1000 == 0 {
			log.Info(fmt.Sprintf("Processing hVM header state changes for block %s @ %d",
				cursor.Hash().String(), cursor.Number.Uint64()))
		}
		err := bc.applyHvmHeaderConsensusUpdate(cursor, false, false)
		if err != nil {
			log.Crit(fmt.Sprintf("Failed to fully restore hVM state, encountered an error processing hVM "+
				"state updates for block %s @ %d", cursor.Hash().String(), cursor.Number.Uint64()), "err", err)
		}
		if cursor.Number.Uint64() < tip.Number.Uint64() {
			next := bc.GetHeaderByNumber(cursor.Number.Uint64() + 1)
			if next != nil {
				cursor = next
			} else {
				// next should never be nil because we are below tip
				log.Crit(fmt.Sprintf("Reached unexpected end of chain while restoring hVM header state, "+
					"last header applied: %s @ %d", cursor.Hash().String(), cursor.Number.Uint64()))
			}
		} else {
			break
		}
	}
	log.Info(fmt.Sprintf("Done performing full hVM header state restore. Tip: %s @ %d", cursor.Hash().String(),
		cursor.Number.Uint64()))
}

// resetHvmHeaderNodeToGenesis is used in the event that chain corruption
// occurs either in the header-only TBC node specifically or in geth in general.
// This method deletes the entire header-only TBC node's data directory,
// and configures it with the effective genesis block defined in the config.
// If this is called to fix a header mode TBC corruption (rather than as part of
// a broader overall EVM reset to genesis), caller must also process all of
// the header state transitions defined by Bitcoin Attributes Deposited
// transactions in the current chain since the activation of hVM Phase 0.
// If this function fails to delete the data directory and restart external
// header mode TBC correctly, it fails with a critical error as we will be
// unable to properly process Hemi state transitions.
func (bc *BlockChain) resetHvmHeaderNodeToGenesis() {
	// Exclude the lock-free GetMissingBtcBlocks reader for the whole teardown+reassign: between
	// ExternalHeaderTearDown (which closes the old node's leveldb) and initHvmHeaderNode (which reassigns
	// bc.tbcHeaderNode) the node is unusable, so a concurrent read would hit a torn pointer / use-after-
	// teardown. See tbcHeaderNodeMu. initHvmHeaderNode is also called directly at startup via
	// SetupHvmHeaderNode, before the broadcast goroutine exists, so that path needs no lock; locking only
	// here also avoids a reentrant Lock when reset calls initHvmHeaderNode.
	bc.tbcHeaderNodeMu.Lock()
	defer bc.tbcHeaderNodeMu.Unlock()

	log.Info("Resetting hVM header TBC node to genesis")
	if bc.tbcHeaderNode != nil {
		log.Info("Header-only TBC instance running, tearing down...")
		err := bc.tbcHeaderNode.ExternalHeaderTearDown()
		if err != nil {
			log.Crit("resetHvmHeaderNodeToGenesis failed when calling ExternalHeaderTearDown on TBC", "err", err)
		}
	} else {
		log.Info("Header-only TBC instance is not running, nothing to tear down. Continuing with genesis reset.")
	}

	// Network-scoped delete: remove ONLY this node's own <LevelDBHome>/<canonicalNet> store, NEVER the
	// parent LevelDBHome. The parent can also hold a sibling network's store — e.g. a migrated <…>/mainnet/
	// alongside the retired <…>/testnet3.migrated-*/ rollback backup — that a parent-wipe would destroy, and
	// every steady-state recovery path (recoverReapplyHvmState, the writeBlockAndSetHead inline restore, the
	// snap-completion reset) routes through here. canonicalBTCNetwork is load-bearing, not cosmetic: TBC writes
	// "upgradetest" under <…>/testnet3/, so a raw-name Join would delete a nonexistent dir and leave the real
	// store, after which the genesis-state assertion below would crit on the stale reopen.
	dataDir := hvmHeaderStoreDir(bc.tbcHeaderNodeConfig.LevelDBHome, bc.tbcHeaderNodeConfig.Network)

	path, _ := filepath.Abs(dataDir)
	log.Info(fmt.Sprintf("Deleting TBC external header mode instance data directory: %s", path))

	if err := os.RemoveAll(dataDir); err != nil {
		log.Crit(fmt.Sprintf("ResetHvmHeaderNodeToGenesis unable to delete external header mode TBC "+
			"data directory %s", dataDir))
	}

	if _, err := os.Open(dataDir); os.IsNotExist(err) {
		log.Info(fmt.Sprintf("Successfully deleted external header mode TBC data directory %s", dataDir))
	} else {
		log.Crit(fmt.Sprintf("The data directory %s still exists after attempting to delete", dataDir))
	}

	log.Info("Deleted hVM header TBC node data directory", "dataDir", dataDir)

	bc.initHvmHeaderNode(bc.tbcHeaderNodeConfig)

	// Make sure after initializing, the stateId is set to the hVMGenesisUpstreamId as expected
	stateId, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil {
		log.Crit("Unable to reset external header mode TBC to genesis configuration, after reset unable to "+
			"query upstream state id", "err", err)
	}
	if !bytes.Equal(stateId[:], hVMGenesisUpstreamId[:]) {
		log.Crit(fmt.Sprintf("Unable to reset external header mode TBC to genesis configuration, after reset "+
			"the TBC instance reports an unexpected upstream state id of %x when the default of %x was expected",
			stateId[:], hVMGenesisUpstreamId[:]))
	}
}

// btcGenesisCheckpoint pins a canonical (effective-genesis height, Bitcoin block hash) pair.
type btcGenesisCheckpoint struct {
	height uint64
	hash   string // EffectiveGenesisBlock.BlockHash().String()
}

// hvmGenesisCheckpoints maps a TBC network to its canonical effective-genesis checkpoint(s). The
// testnet3 entry is op-geth's compiled default (ethconfig.Defaults.HvmGenesisHeader/Height); the
// mainnet and testnet deployments set no override and rely on the binary default, so the
// default is the canonical pairing. upgradetest == testnet3 (TBC lockstep). A Hemi-mainnet
// op-geth build compiles in its own Bitcoin-mainnet default; add its checkpoint here when this build
// supports mainnet hVM (today eth/backend.go defaults the consensus node to testnet3 via
// config.TBCNetwork → ethconfig.DefaultTBCNetwork).
// TestHvmGenesisCheckpointMatchesCanonicalHeader pins these to the canonical header so they cannot drift.
var hvmGenesisCheckpoints = map[string][]btcGenesisCheckpoint{
	// testnet3 pins two accepted effective-genesis pairs:
	//
	//   [0] The compiled default (ethconfig.Defaults.HvmGenesisHeader/Height). This MUST stay element
	//       [0]: the lockstep tests in eth/backend_hvm_genesis_test.go pin the default to this pair, and
	//       the in-core inspection test reads testnet3[0].
	//
	//   [1] Backwards-compatibility pin for the deployed fleet. These nodes have run this Bitcoin-mainnet
	//       effective-genesis pair (set via --hvm.genesisheight / --hvm.genesisheader) since before this
	//       genesis-pairing guard existed. The TBC consensus network defaults to "testnet3" in
	//       eth/backend.go (buildHvmHeaderNodeConfig sets it from config.TBCNetwork → ethconfig.DefaultTBCNetwork)
	//       even though this pair is a Bitcoin-mainnet
	//       (height, header); because the ENTIRE fleet shares the same override the pairing is internally
	//       consistent and does not split the network — it is a legacy mislabel, not a desync. The guard
	//       is config-only and runs at every startup for every node (snap-synced or full), so without this
	//       entry the running fleet would log.Crit on boot. Keep it pinned until the network is properly
	//       migrated off the override (i.e. the TBC network is wired from chain config and the data dir is
	//       relocated); removing it bricks the live fleet.
	"testnet3": {
		{height: 3522419, hash: "000000000000000096c98151accc5ee217d7cc4ff1e59a3d91e4c9365c4ea144"},
		// [1] DEFER-state pin: a legacy node (or the migration's defer fallback) runs Network=testnet3 over
		//     the Bitcoin-mainnet pair below. Sourced from the ONE shared constant (core/vm/hvm_genesis.go),
		//     dual-pinned identically under "mainnet" so BOTH the deferred and migrated states pass the guard.
		{height: vm.MainnetHvmGenesisHeight, hash: vm.MainnetHvmGenesisHash},
	},
	// MIGRATED-state pin: a migrated mainnet node runs Network=mainnet over the SAME {883092,…eda8} pair.
	// Keep this dual-pin (with testnet3[1]) until a verifiable fleet-wide "migration complete" signal confirms
	// zero deferred nodes remain; removing testnet3[1] on a timer would crit any still-deferred node.
	"mainnet":     {{height: vm.MainnetHvmGenesisHeight, hash: vm.MainnetHvmGenesisHash}},
	"upgradetest": {{height: 3522419, hash: "000000000000000096c98151accc5ee217d7cc4ff1e59a3d91e4c9365c4ea144"}},
}

// canonicalBTCNetwork maps a configured TBC network name to its on-disk / chaincfg canonical form, mirroring
// TBC's own network-name rewrite in the heminetwork database/tbcd/level package: "upgradetest" is stored as
// "testnet3"; everything else is identity. The on-disk store path (<HvmHeaderDataDir>/<net>), the
// network-scoped reset, and the migration detection must all use this canonical form so they agree with
// where TBC actually writes — a raw-name Join would target a nonexistent dir and silently miss the real store.
func canonicalBTCNetwork(network string) string {
	if network == "upgradetest" {
		return "testnet3"
	}
	return network
}

type hvmGenesisPairing int

const (
	hvmGenesisPairingCanonical hvmGenesisPairing = iota // matches a known checkpoint exactly
	hvmGenesisPairingCustom                             // touches no checkpoint -> fully custom (allowed)
	hvmGenesisPairingMismatch                           // height XOR hash matches a checkpoint -> desynced pair
)

// classifyHvmGenesisPairing detects a desynced hVM effective-genesis pair: the height
// (GenesisHeightOffset) and the header (EffectiveGenesisBlock) are two independently-overridable
// knobs, and the btcd absolute-height retarget math is correct only if the height is the true Bitcoin
// height of the header. For the network's canonical checkpoints, if exactly one of (height, hash)
// matches while the other diverges the knobs are out of sync (mismatch); a full match is canonical;
// touching neither is a fully-custom genesis (allowed, e.g. localnet or the height-0 test harness).
// Pure, so it is unit-testable without log.Crit.
func classifyHvmGenesisPairing(network string, height uint64, hash string) hvmGenesisPairing {
	mismatch := false
	for _, cp := range hvmGenesisCheckpoints[network] {
		hEq, sEq := cp.height == height, cp.hash == hash
		if hEq && sEq {
			return hvmGenesisPairingCanonical
		}
		if hEq != sEq {
			mismatch = true
		}
	}
	if mismatch {
		return hvmGenesisPairingMismatch
	}
	return hvmGenesisPairingCustom
}

// IsCanonicalHvmGenesisPairing reports whether (network, height, headerHash) exactly matches a pinned
// canonical hVM effective-genesis checkpoint. Exported so a package that imports both core and
// ethconfig (e.g. package eth) can assert that ethconfig.Defaults.HvmGenesisHeader/Height stays in
// lockstep with hvmGenesisCheckpoints: ethconfig imports core, so the checkpoint map cannot be
// compared against the defaults from within core itself without an import cycle. Without that external
// assertion, re-pinning ethconfig.Defaults without updating the checkpoint would brick every enforced
// node at startup (initHvmHeaderNode crits) while the in-core binding test stays green.
func IsCanonicalHvmGenesisPairing(network string, height uint64, hash string) bool {
	return classifyHvmGenesisPairing(network, height, hash) == hvmGenesisPairingCanonical
}

// IsMismatchedHvmGenesisPairing reports whether (network, height, headerHash) is a DESYNCED pair: exactly one
// of the height or the header matches a canonical checkpoint while the other diverges. A fully-custom pair
// (matching no checkpoint) is NOT a mismatch. Exported for the non-consensus full TBC node, which legitimately
// runs a custom start pair and so cannot use IsCanonicalHvmGenesisPairing as a gate (that would reject custom),
// but should still fail early on the clearly-misconfigured mismatch case.
func IsMismatchedHvmGenesisPairing(network string, height uint64, hash string) bool {
	return classifyHvmGenesisPairing(network, height, hash) == hvmGenesisPairingMismatch
}

// hvmMigrationAwareCrit fatally fails a hVM operation. During an in-progress migration rebuild
// (bc.hvmMigrationInProgress) it routes through migrationCrit so the "failed" meter is marked and the
// in-progress gauge cleared before log.Crit's os.Exit (the deferred gauge-clear in
// migrateHvmHeaderNode never runs across os.Exit). On the normal boot / reset / steady-state apply paths it is a
// plain fatal crit. Used by initHvmHeaderNode's I/O crits and by applyHvmHeaderConsensusUpdate's crits, both of
// which are reachable inside the migration rebuild window (init during the rebuild, applyHvmHeaderConsensusUpdate during the
// forward catch-up).
func (bc *BlockChain) hvmMigrationAwareCrit(msg string, ctx ...interface{}) {
	if bc.hvmMigrationInProgress {
		migrationCrit(msg, ctx...)
		return
	}
	log.Crit(msg, ctx...)
}

func (bc *BlockChain) initHvmHeaderNode(config *tbc.Config) {
	if config.ExternalHeaderMode != true {
		log.Crit("initHvmHeaderNode called with a TBC config that does not have ExternalHeaderMode set")
	}

	// Contextual-difficulty: refuse to start on a desynced hVM effective-genesis pair. A wrong GenesisHeightOffset
	// for the configured EffectiveGenesisBlock mis-aligns the contextual-difficulty retarget boundary
	// (%BlocksPerRetarget) and, under contextual-difficulty enforcement, can false-reject every honest boundary header
	// — a network-wide split. The node's own ExternalHeaderSetup trusts the pair, so this is the one
	// place to catch the misconfiguration.
	if config.EffectiveGenesisBlock != nil {
		switch classifyHvmGenesisPairing(config.Network, config.GenesisHeightOffset, config.EffectiveGenesisBlock.BlockHash().String()) {
		case hvmGenesisPairingMismatch:
			log.Crit("Refusing to start: hVM effective-genesis height/header pair (from --hvm.genesisheight / "+
				"--hvm.genesisheader, or the compiled defaults) is DESYNCED from the canonical checkpoint for this "+
				"network (the height must be the true Bitcoin height of the genesis header; a mismatched pair "+
				"mis-aligns the retarget boundary and can split the network — revert to the canonical pair, or if "+
				"ethconfig.Defaults was re-pinned update core.hvmGenesisCheckpoints to match)",
				"network", config.Network, "height", config.GenesisHeightOffset,
				"header", config.EffectiveGenesisBlock.BlockHash().String())
		case hvmGenesisPairingCustom:
			// GenesisHeightOffset feeds the absolute-height retarget boundary (%BlocksPerRetarget), so
			// on a difficulty-enforced network it is a consensus parameter. A pair that matches no canonical
			// checkpoint cannot be trusted as the true Bitcoin (height,header) and would split from
			// canonical nodes whose offset differs — so refuse to start (fail closed) on any real network.
			// Only the localnet/regtest dev network (single-operator, intentionally without a pinned
			// genesis) is allowed through with a warning; any other network (including a mainnet build
			// that has not yet added its checkpoint here) must be pinned before it can run enforced.
			if config.Network != "localnet" {
				// Include the canonical remediation values for this network so the operator can
				// recover (mirrors the migration's defer-fallback crit). For mainnet that is the shared constant pair.
				canonHint := "<none pinned for this network>"
				if cps := hvmGenesisCheckpoints[config.Network]; len(cps) > 0 {
					canonHint = fmt.Sprintf("height=%d hash=%s", cps[0].height, cps[0].hash)
				}
				wantHeader := ""
				if canonicalBTCNetwork(config.Network) == "mainnet" {
					wantHeader = vm.MainnetHvmGenesisHeader // the exact --hvm.genesisheader bytes the flag needs
				}
				log.Crit("Refusing to start: hVM effective-genesis (height,header) pair (from --hvm.genesisheight / "+
					"--hvm.genesisheader, or the compiled defaults) is NOT a pinned canonical pair for this "+
					"difficulty-enforced network; the height is a consensus parameter (it positions the retarget "+
					"boundary) and a non-canonical offset splits from canonical nodes — set --hvm.genesisheight / "+
					"--hvm.genesisheader to the canonical pair below (or add the canonical checkpoint to core.hvmGenesisCheckpoints)",
					"network", config.Network, "gotHeight", config.GenesisHeightOffset,
					"gotHash", config.EffectiveGenesisBlock.BlockHash().String(),
					"wantCanonical", canonHint, "wantHeader", wantHeader)
			}
			log.Warn("hVM effective-genesis pair has no canonical checkpoint for the localnet dev network; cannot "+
				"verify the height/header pairing — ensure GenesisHeightOffset is the true Bitcoin height of the header",
				"network", config.Network, "height", config.GenesisHeightOffset,
				"header", config.EffectiveGenesisBlock.BlockHash().String())
		case hvmGenesisPairingCanonical:
			// matches the canonical checkpoint; nothing to verify
		default:
			// Defensive: the switch is exhaustive over today's three classifications, but a future
			// classification value must fail closed, never silently boot a node on an unclassified pairing.
			// The zero value is hvmGenesisPairingCanonical (routes to the Canonical arm), so this guards
			// only a hypothetical added enum value.
			log.Crit("Refusing to start: hVM effective-genesis pairing returned an unknown classification",
				"network", config.Network, "height", config.GenesisHeightOffset,
				"header", config.EffectiveGenesisBlock.BlockHash().String())
		}
	}

	// Contextual-difficulty: chaincfg <-> genesis-pairing lockstep. The consensus node's Network is used in two
	// independent places — the genesis-pairing classifier above (network -> hvmGenesisCheckpoints) and
	// the contextual-difficulty + PoW validators (network -> btcd chaincfg.Params, via vm.paramsForNetwork).
	// If a node booted on a Network the classifier accepts (or a localnet custom pair) but that has no
	// chaincfg params, every contextual-difficulty validation would return the unavailable/skip sentinel — mapping to
	// ErrCorruptHVMHeaderOnlyModeState on the apply path and wedging the node in a per-block restore loop.
	// Cross-check the two maps are in lockstep here (fail fast at startup). vm.SupportsBTCNetwork is the
	// dedicated chaincfg-membership probe (true iff vm.paramsForNetwork resolves the network).
	if !vm.SupportsBTCNetwork(config.Network) {
		log.Crit("Refusing to start: the hVM consensus node's Network has no btcd chaincfg params, so "+
			"contextual-difficulty / proof-of-work validation cannot be parameterized (the genesis-pairing "+
			"network and the validator-params network must be in lockstep) — add the network to "+
			"core/vm.paramsForNetwork or fix the configured network",
			"network", config.Network)
	}

	tbcHeaderNode, err := tbc.NewServer(config)
	if err != nil {
		bc.hvmMigrationAwareCrit("initHvmHeaderNode unable to create new TBC server", "err", err)
	}

	// Pass in the hVMGenesisUpstreamId, which will only be set by ExternalHeaderSetup if the TBC instance
	// has not been initialized with any headers yet.
	err = tbcHeaderNode.ExternalHeaderSetup(bc.ctx, hVMGenesisUpstreamId[:])
	if err != nil {
		// A re-open failure here is most often the store being held by another process (exactly one op-geth
		// process per HvmHeaderDataDir is required), or a still-held goleveldb directory lock leaked by an
		// upstream level.New error path on a prior deferred migration, or corrupt store
		// metadata. Name the likely causes so the failure is diagnosable instead of an opaque fatal.
		bc.hvmMigrationAwareCrit("initHvmHeaderNode unable to run ExternalHeaderSetup on TBC — the hVM header store could "+
			"not be opened; ensure exactly ONE op-geth process uses this HvmHeaderDataDir and restart. If it "+
			"recurs, a prior boot may have leaked the goleveldb directory lock or the store metadata is corrupt "+
			"and must be restored from backup", "err", err)
	}

	height, header, err := tbcHeaderNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		bc.hvmMigrationAwareCrit("initHvmHeaderNode unable to get best block header after initialization", "err", err)
	}

	log.Info(fmt.Sprintf("After hVM external header node initialization, best header = %s @ %d",
		header.BlockHash().String(), height))

	bc.tbcHeaderNode = tbcHeaderNode
	bc.tbcHeaderNodeConfig = config
	bc.hvmEnabled = true
	// A node is difficulty-enforceable unless it is in the DEFER state — running testnet3
	// params over the Bitcoin-mainnet effective-genesis pair (the legacy mislabel / the migration's defer
	// fallback). A genuine testnet3 node (height 3522419), a localnet node, and a MIGRATED mainnet node
	// (Network=mainnet) are all enforceable. Keyed on the (network, height) pair, NOT the word "migrated", so
	// the genuine testnet3 fleet and the post-migration mainnet steady state both enforce correctly.
	bc.hvmDiffEnforceable.Store(!isLegacyDeferredPairing(config.Network, config.GenesisHeightOffset))
	if !bc.hvmDiffEnforceable.Load() {
		log.Warn("hVM difficulty enforcement DISABLED this boot: node is in the legacy DEFER state (testnet3 "+
			"params over the Bitcoin-mainnet genesis pair); it enforces only after migrating to network=mainnet. "+
			"Do NOT run this node as the active op-stack sequencer until it has migrated (it can package a "+
			"Bitcoin-Attributes header that enforced validators reject -> chain split)",
			"network", config.Network, "genesisHeight", config.GenesisHeightOffset)
	}
}

// isLegacyDeferredPairing reports the DEFER state: a node running on the RAW "testnet3" network over the
// Bitcoin-MAINNET effective-genesis pair (height 883092) — the legacy "mainnet-as-testnet3" mislabel and the
// migration's defer fallback (which sets config.Network = "testnet3"). Such a node must NOT enforce difficulty:
// it would validate real mainnet headers under TestNet3Params (the ReduceMinDifficulty 20-minute rule),
// splitting from a correctly-migrated fleet.
//
// Matched on the RAW network "testnet3", NOT canonicalBTCNetwork — to stay CONSISTENT with the
// genesis-pairing guard, whose dual-pin {883092,…eda8} lives under the "testnet3" (and "mainnet") checkpoint
// keys but NOT under "upgradetest". An upgradetest node over the mainnet pair is rejected (Custom -> crit) by
// classifyHvmGenesisPairing before it ever runs, so it is not a valid defer state; canonicalizing here would
// have this function disagree (call it deferred) with a guard that refuses to boot it. The genuine fleet and
// the defer path both use the raw "testnet3" network, so this narrowing does not affect any real node.

func isLegacyDeferredPairing(network string, genesisHeight uint64) bool {
	return network == "testnet3" && genesisHeight == vm.MainnetHvmGenesisHeight
}

func (bc *BlockChain) SetupHvmHeaderNode(config *tbc.Config) {
	// Automatic legacy "mainnet-as-testnet3" migration, at the TOP before any dir is opened. When
	// it fully rebuilt a migrated mainnet store it returns true and bc.tbcHeaderNode is already initialized at
	// the EVM tip — skip the normal init/restore. When it deferred it mutated config.Network back to "testnet3"
	// and returns false, so the normal boot below runs on the untouched legacy store. When no migration was
	// needed it returns false unchanged (the common path: genuine testnet3, or an already-migrated mainnet).
	if bc.maybeMigrateHvmHeaderNode(config) {
		return
	}

	bc.initHvmHeaderNode(config)

	// Get the current state ID
	stateId, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil {
		log.Crit("Unable to get upstream state ID from TBC header node", "err", err)
	}

	current := bc.currentBlock.Load()
	currentHash := current.Hash()

	log.Info(fmt.Sprintf("hVM node initiated, stateId=%x, current EVM tip=%s", stateId[:], currentHash.String()))

	tnFix, _ := hex.DecodeString("2decc762c95d7c392b5e852fc861aab2044b5e5748d1696a0cb00de70014d0f4")
	// Special case for testnet — network-gated so this testnet3-specific bad-BTC-header surgery
	// can NEVER run on a migrated mainnet node (which reaches this block on its 2nd+ boot, when the migration
	// is already complete and maybeMigrateHvmHeaderNode returns false). The stateId is testnet3-specific, but
	// the guard makes the intent structural rather than relying on hash non-collision.
	if canonicalBTCNetwork(config.Network) == "testnet3" && bytes.Equal(stateId[:], tnFix[:]) {
		correctPrevStateId, _ := hex.DecodeString("4d1bafde31ffe9d02b81131333340c762a639865361b9429cdf21181e78d8bff")
		badBTCBlock, _ := hex.DecodeString("aef45566e1303e620317eb7073aff8eca8d58834d58fa4cceabd010000000000")

		var badBlock chainhash.Hash
		_ = badBlock.SetBytes(badBTCBlock[:])
		badBlockHeader, _, err := bc.tbcHeaderNode.BlockHeaderByHash(context.Background(), badBlock)
		if err != nil {
			log.Error("Unable to get bad BTC block", "block", badBTCBlock, "err", err)
			// Backup: get tip which will be same bad BTC block
			_, badBlockHeader, err = bc.tbcHeaderNode.BlockHeaderBest(context.Background())
			if err != nil {
				log.Crit("Unable to get tip")
			}

		}

		headersToRemove := make([]*wire.BlockHeader, 1)
		headersToRemove[0] = badBlockHeader

		msgHeaders := &wire.MsgHeaders{
			Headers: headersToRemove,
		}

		correctHead, _ := hex.DecodeString("d870eebda743ce0b264480ffbaec8686f1baadb9634a2eb4c511a80000000000")

		var ch chainhash.Hash
		_ = ch.SetBytes(correctHead[:])
		prevHeader, _, err := bc.tbcHeaderNode.BlockHeaderByHash(context.Background(), ch)
		if err != nil {
			log.Error("Unable to get correct previous block", "block", badBTCBlock, "err", err)
			// Backup: get previous block
			prevHeader, _, err = bc.tbcHeaderNode.BlockHeaderByHash(context.Background(), badBlockHeader.PrevBlock)
			if err != nil {
				log.Crit("Unable to get previous block")
			}
		}

		// Remove partial hVM state transition and set state back to hVM state at 4d1bafde31ffe9d02b81131333340c762a639865361b9429cdf21181e78d8bff
		_, _, err = bc.tbcHeaderNode.RemoveExternalHeaders(context.Background(), msgHeaders, prevHeader, correctPrevStateId)
		if err != nil {
			log.Crit("Unable to remove external headers", "err", err)
		}

		// Get updated state ID after fix to continue initialization
		stateId, err = bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
		if err != nil {
			log.Crit("Unable to get upstream state ID from TBC header node", "err", err)
		}
	}

	if bytes.Equal(stateId[:], hVMGenesisUpstreamId[:]) {
		// TBC claims to be in its genesis configuration, check to ensure its best header is the hVM genesis header
		_, bestHeader, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
		if err != nil {
			log.Crit("SetupHvmHeaderNode unable to get best block header from TBC which claims to be in genesis"+
				" initialization state", "err", err)
		}

		bestHeaderHash := bestHeader.BlockHash()
		genesisHash := config.EffectiveGenesisBlock.BlockHash()

		if bc.chainConfig.IsHvm0(current.Time) {
			// Current tip is after hVM Phase 0 activation time, so header-only mode TBC node should not be
			// at genesis state ID. Attempt recovery to align header-only mode TBC with current EVM chain tip.
			bc.performFullHvmHeaderStateRestore()

			// Check to make sure state restore was successful
			postRestoreStateId, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
			if err != nil {
				log.Crit("Unable to get upstream state ID from lightweight TBC node after attempting restore.")
			}

			if bytes.Equal(postRestoreStateId[:], currentHash[:]) {
				log.Info(fmt.Sprintf("Successfully regenerated lightweight TBC state to match EVM tip %s", currentHash.String()))
			} else {
				// Failed to restore, exit
				log.Crit(fmt.Sprintf("Attempted to regenerate lightweight tBC state to match EVM tip %s "+
					"but after restore upstream state ID is %x", currentHash.String(), postRestoreStateId[:]))
			}
		} else {
			if bytes.Equal(bestHeaderHash[:], genesisHash[:]) {
				log.Info("SetupHvmHeaderNode has determined that the external header mode TBC is in its genesis state" +
					" and initialized correctly.")
			}
		}

	} else {
		potentialBlockHash := common.BytesToHash(stateId[:])
		potentialHeader := bc.GetHeaderByHash(potentialBlockHash)

		if potentialHeader != nil {
			// TBC has already been progressed with EVM blocks prior, as its upstream state ID corresponds
			// to a known block from the Hemi chain
			log.Info(fmt.Sprintf("SetupHvmHeaderNode had determined that the external header mode TBC currently"+
				" represents block %s @ %d", potentialHeader.Hash().String(), potentialHeader.Number.Uint64()))
		} else {
			// TBC is in an invalid state, attempt to recover it
			log.Info(fmt.Sprintf("The hVM header-only TBC node has an invaid state on startup; stateId=%x,"+
				" attempting full restore from hVM activation height", stateId))
			bc.performFullHvmHeaderStateRestore()
		}
	}
}

// NewBlockChain returns a fully initialised block chain using information
// available in the database. It initialises the default Ethereum Validator
// and Processor.

func (bc *BlockChain) findCommonAncestor(a *Header, b *Header) (*Header, error) {
	// Set cursor to the higher of the two headers
	highCursor := a
	lowCursor := b
	if b.Number.Uint64() > a.Number.Uint64() {
		highCursor = b
		lowCursor = a
	}

	lowHeight := lowCursor.Number.Uint64()

	// Cursor is the higher header, walk it back to lowHeight
	for i := highCursor.Number.Uint64(); i > lowHeight; i-- {
		highCursor = bc.GetHeader(highCursor.ParentHash, i-1)
	}

	if highCursor.Hash().Cmp(lowCursor.Hash()) == 0 {
		// If they are equal, then lowCursor is the ancestor
		return lowCursor, nil
	}

	if lowCursor.Number.Uint64() != highCursor.Number.Uint64() {
		// Sanity check, should be impossible
		log.Crit(fmt.Sprintf("when looking for common ancestor between %s @ %d and %s @ %d, "+
			"highCursor was walked back to height %d which doesn't match lowCursor height %d",
			a.Hash().String(), a.Number.Uint64(), b.Hash().String(), b.Number.Uint64(),
			highCursor.Number.Uint64(), lowCursor.Number.Uint64()))
	}

	// While high and low cursors are not the same block, walk each back together block-by-block
	for highCursor.Hash().Cmp(lowCursor.Hash()) != 0 {
		// Walk each cursor back to their parent
		highCursor = bc.GetHeader(highCursor.ParentHash, highCursor.Number.Uint64()-1)
		lowCursor = bc.GetHeader(lowCursor.ParentHash, lowCursor.Number.Uint64()-1)

		if highCursor.Number.Uint64() == 0 {
			return nil, fmt.Errorf("when looking for common ancestor between %s @ %d and %s @ %d, "+
				"we walked backwards to the genesis block without finding a common ancestor",
				a.Hash().String(), a.Number.Uint64(), b.Hash().String(), b.Number.Uint64())
		}
	}

	// high and low cursors match, found common ancestor
	return highCursor, nil
}

func btcAttrDepIsHeaderless(btcAttrDep *types.BtcAttributesDepositData) bool {
	return btcAttrDep == nil || len(btcAttrDep.Headers) == 0
}

// unapplyHvmHeaderConsensusUpdate retrieves the block corresponding to
// the provided block header, extracts its Bitcoin Attributes Deposited
// transaction and, if it exists, removes the header information contained
// in it from the protocol's lightweight view of Bitcoin and verifies that
// TBC has been correctly returned to the canonical tip claimed by the
// previous block which contains a Bitcoin Attributes Deposited tx.
func (bc *BlockChain) unapplyHvmHeaderConsensusUpdate(header *Header) error {
	block := bc.getBlockFromDiskOrHoldingPen(header.Hash())
	if block == nil {
		// Symmetric to the prevBlock / walk-cursor guards below: the block being unapplied is absent from
		// disk + holding pen (a deep reorg/rewind orphaned its body). Since this is an already-applied block,
		// an absent body is a torn-store condition, not a bad block — return the recoverable corrupt-state
		// sentinel so the walkHvmHeaderConsensusBack caller rebuilds the lightweight view from genesis rather
		// than escalating to a crit.
		log.Error(fmt.Sprintf("block %s @ %d to unapply hVM consensus updates is nil; treating as corrupt hVM state",
			header.Hash().String(), header.Number.Uint64()))
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}

	// When we unapply the current block, TBC's state will reflect that of the
	// previous block
	prevBlock := bc.getHeaderFromDiskOrHoldingPen(header.ParentHash)
	if prevBlock == nil {
		// Symmetric to the apply-side currentHead==nil guards in updateHvmHeaderConsensus: the parent of the
		// block being unapplied is absent from both disk and the holding pen (a deep reorg/rewind orphaned it,
		// or recoverReapplyHvmState reset us to genesis with the parent already gone). Dereferencing
		// prevBlock.Time / prevBlock.Hash() below would nil-panic and crash the process; return the recoverable
		// corrupt-state sentinel so the caller rebuilds the lightweight view from genesis instead (the
		// walkHvmHeaderConsensusBack caller routes ErrCorruptHVMHeaderOnlyModeState through recovery, not crit).
		log.Error(fmt.Sprintf("prevBlock (parent %x of block %s @ %d being unapplied) is nil; treating as corrupt hVM state",
			header.ParentHash[:], header.Hash().String(), header.Number.Uint64()))
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}
	stateTransitionTargetHash := [32]byte{}

	if bc.chainConfig.IsHvm0(header.Time) && !bc.chainConfig.IsHvm0(prevBlock.Time) {
		// Special case, we are unapplying the hVM state transition for the activation block,
		// so set the state transition target hash back to the genesis default
		copy(stateTransitionTargetHash[0:32], hVMGenesisUpstreamId[0:32])
	} else {
		// Previous block had hVM active, so we will set its hash as the upstream
		// state id of the external header TBC node after reverting hVM state transition
		// from the block to unapply
		copy(stateTransitionTargetHash[0:32], prevBlock.Hash().Bytes()[0:32])
	}

	btcAttrDep, err := block.Transactions().ExtractBtcAttrData()
	if err != nil {
		// Error implies that state of Bitcoin Attributes Deposited tx in the transaction list is invalid.
		// This should be impossible because any block which is being unapplied would have undergone the
		// same check previously and passed when it was originally applied.
		// TODO: Bubble this error up and invalidate this block and restore external header TBC node from genesis to prev tip?
		log.Crit(fmt.Sprintf("Error while extracting Bitcoin Attributes Deposited transaction to unwind "+
			"hVM state application for block %s @ %d", header.Hash().String(), header.Number.Uint64()),
			"err", err)
	}

	if btcAttrDepIsHeaderless(btcAttrDep) {
		// No Bitcoin headers to unapply: the block either has no Bitcoin Attributes Deposited tx, or has
		// one carrying zero headers (an empty-but-present tx). In both cases forward-apply made no TBC
		// header change (it only advanced the upstream state id), so the inverse is to roll the upstream
		// state id back — there is nothing to remove. Treating empty-but-present as a no-op here mirrors
		// the forward path so apply/unapply remain exact inverses (see btcAttrDepIsHeaderless).
		log.Info(fmt.Sprintf("No Bitcoin headers to unapply in hVM state for block %s @ %d (no Bitcoin "+
			"Attributes Deposited transaction, or one carrying zero headers)", header.Hash().String(),
			header.Number.Uint64()))

		// Even with no header changes, explicitly update TBC's state id so it is correct for the previous
		// block after removing this one. stateTransitionTargetHash is already set to the previous block or
		// the genesis upstream state ID depending on whether the previous parent had hVM Phase 0 active.
		if bc.chainConfig.IsHvm0(header.Time) {
			err := bc.tbcHeaderNode.SetUpstreamStateId(bc.ctx, stateTransitionTargetHash)
			if err != nil {
				// TODO: Recovery mode that resets TBC header mode to genesis configuration and rebuilds it from hVM activation block
				log.Crit(fmt.Sprintf("Error while updating the upstream state id in TBC with no corresponding "+
					"consensus state modifications for unapplying block %s @ %d", header.Hash().String(),
					header.Number.Uint64()), "err", err)
			}
		}
		return nil
	}

	if !bc.chainConfig.IsHvm0(header.Time) {
		// This should never happen, because the block shouldn't have a Bitcoin Attributes Deposited tx before this
		// activation timestamp and already would have failed validation in the forward direction when originally
		// applied
		// TODO: Bubble this error up and invalidate this previous block?
		log.Crit(fmt.Sprintf("block %s @ %d has a Bitcoin Attributes Deposited transaction but its timestamp "+
			"%d is before the hVM Phase 0 activation height %d", header.Hash().String(), header.Number.Uint64(),
			header.Time, *bc.chainConfig.Hvm0Time))
	}

	currentTipHeight, currentTip, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		// This is a critical TBC failure, not related to block validity
		// TODO: Recovery mode that resets TBC header mode to genesis configuration and rebuilds it from hVM activation block
		log.Crit(fmt.Sprintf("when unapplying hVM changes for block %s @ %d, unable to retrieve tip "+
			"from lightweight TBC!", header.Hash().String(), header.Number.Uint64()), "err", err)
	}
	currentTipHash := currentTip.BlockHash()

	// Descend the Hemi chain from this height until either we find a block with a Bitcoin Attributes Deposited
	// transaction or we get to before the hVM Phase 0 activation height to determine the correct previous
	// tip.
	// TODO: Get this state more efficiently?
	var expectedPreviousTipHash [32]byte
	cursor := bc.getBlockFromDiskOrHoldingPen(header.ParentHash)
	if cursor == nil {
		// Symmetric to the prevBlock guard above, but for the BLOCK store: the parent header can resolve while
		// its full block is absent from disk + holding pen (a deep reorg/rewind orphaned the body). The loop
		// condition cursor.Time() below would nil-panic; return the recoverable sentinel so the caller rebuilds
		// the lightweight view from genesis instead of crashing the process.
		log.Error(fmt.Sprintf("parent block %x of %s @ %d is nil while walking back to the previous BtcAttr tip; treating as corrupt hVM state",
			header.ParentHash[:], header.Hash().String(), header.Number.Uint64()))
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}
	for bc.chainConfig.IsHvm0(cursor.Time()) {
		oldBtcAttrDep, err := cursor.Transactions().ExtractBtcAttrData()
		if err != nil {
			// Error implies that state of Bitcoin Attributes Deposited tx in the transaction list is invalid.
			// This should be impossible because any block which is being unapplied would have undergone the
			// same check previously in the forward direction and passed.
			// TODO: Bubble this error up to invalidate the old block?
			log.Crit(fmt.Sprintf("Error while extracting Bitcoin Attributes Deposited transaction from "+
				"prior block %s @ %d when attempting to unwind hVM state application for block %s @ %d",
				cursor.Hash().String(), cursor.NumberU64(), header.Hash().String(), header.Number.Uint64()), "err", err)
		}
		if oldBtcAttrDep != nil {
			// Found previous state
			expectedPreviousTipHash = oldBtcAttrDep.CanonicalTip
			break
		}
		cursor = bc.getBlockFromDiskOrHoldingPen(cursor.ParentHash())
		if cursor == nil {
			// Same orphaned-ancestor recovery: an ancestor block on the walk-back is absent from disk +
			// holding pen. The next loop-condition cursor.Time() would nil-panic; recover instead of crashing.
			log.Error(fmt.Sprintf("ancestor block on the unapply walk-back for %s @ %d is nil; treating as corrupt hVM state",
				header.Hash().String(), header.Number.Uint64()))
			return consensus.ErrCorruptHVMHeaderOnlyModeState
		}
	}
	if bytes.Equal(expectedPreviousTipHash[:], emptyArray[:]) {
		// Walked back the chain to the hVM Phase 0 activation height and did not find any previous BTC Attr Dep
		// transactions, so the previous state to the change we are unapplying is the genesis state
		genHash := bc.tbcHeaderNodeConfig.EffectiveGenesisBlock.BlockHash()
		copy(expectedPreviousTipHash[0:32], genHash[0:32])
		log.Info(fmt.Sprintf("when unapplying hVM changes for block %s @ %d, got to block %s @ %d with timestamp "+
			"%d which is before the hVM Phase 0 activation timestamp %d, so previous canonical tip should be "+
			"the genesis block %x", header.Hash().String(), header.Number.Uint64(), cursor.Hash().String(),
			cursor.NumberU64(), cursor.Time(), bc.chainConfig.Hvm0Time, genHash[:]))
	} else {
		log.Info(fmt.Sprintf("expectedPreviousTipHash=%x is not zeroed, so a non-genesis previous canonical "+
			"BTC tip was found", expectedPreviousTipHash[:]))
	}

	// Convert the expected previous BTC tip hash to a chainhash
	expectedPreviousTipHashParsed, err := chainhash.NewHash(expectedPreviousTipHash[:])
	if err != nil {
		log.Warn(fmt.Sprintf("Unable to create blockhash from %x", expectedPreviousTipHash[:]), "err", err)
	}

	if expectedPreviousTipHashParsed == nil {
		log.Crit("expectedPreviousTipHashParsed is nil")
	}

	// Get the actual header represented by the previous canonical tip hash
	expectedPreviousTip, expectedPreviousTipHeight, err :=
		bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, *expectedPreviousTipHashParsed)

	if err != nil {
		// This should never happen, it means TBC doesn't have a header which either:
		// 1. Should have already been added to it when this older block was originally processed, or
		// 2. Is the genesis block TBC is configured with
		// TODO: TBC recovery from genesis?
		log.Crit(fmt.Sprintf("when unapplying hVM changes for block %s @ %d, previous canonical tip "+
			"should be %x but TBC encountered an error when fetching that header", header.Hash().String(),
			header.Number.Uint64(), expectedPreviousTipHash[:]), "err", err)
	}

	// TODO: Better header to slice
	var expectedPreviousTipBuf bytes.Buffer
	err = expectedPreviousTip.Serialize(&expectedPreviousTipBuf)
	if err != nil {
		// This is a critical failure, not related to block validity
		// TODO: TBC recovery from genesis
		log.Crit(fmt.Sprintf("when unapplying hVM changes from block %s @ %d, unable to serialize "+
			"tip from lightweight TBC!", header.Hash().String(), header.Number.Uint64()), "err", err)
	}

	// Unflatten the BTC headers stored in the BTC Attr Dep transaction to unapply into wire.MsgHeaders
	reconstitutedHeaders, err := unflattenBTCHeaders(btcAttrDep.Headers)
	if err != nil {
		// This is a critical failure as the headers should be valid if the hVM consensus update we are
		// now unapplying was able to be applied in the first place in the forward direction
		log.Crit(fmt.Sprintf("when unapplying hVM changes for block %s @ %d, unable to unflatten "+
			"one of the BTC headers from the block", header.Hash().String(), header.Number.Uint64()),
			"err", err)
	}

	log.Info(fmt.Sprintf("[Unapply HVM Header Consensus Update] *REMOVING* external BTC headers:"))
	for i := 0; i < len(reconstitutedHeaders.Headers); i++ {
		log.Info(fmt.Sprintf("\t %s", reconstitutedHeaders.Headers[i].BlockHash().String()))
	}

	rt, lastHeader, err := bc.tbcHeaderNode.RemoveExternalHeaders(
		bc.ctx, reconstitutedHeaders, expectedPreviousTip, stateTransitionTargetHash[:])
	if err != nil {
		// This is a critical failure, not related to block validity
		// TODO: TBC recovery from genesis
		log.Crit(fmt.Sprintf("when unapplying hVM changes from block %s @ %d, unable to remove "+
			"%d headers and change the canonical tip from %s @ %d to %s @ %d", header.Hash().String(),
			header.Number.Uint64(), len(btcAttrDep.Headers), currentTipHash.String(), currentTipHeight,
			expectedPreviousTip.BlockHash().String(), expectedPreviousTipHeight), "err", err)
	}
	lastHeaderHash := lastHeader.BlockHash()

	newHeight, newTip, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		// TODO: TBC recovery from genesis
		log.Crit(fmt.Sprintf("when unapplying hVM changes from block %s @ %d, attempted to remove "+
			"%d headers and change the canonical tip from %s @ %d to %s @ %d, but TBC reports an error "+
			"getting the canonical tip after state transition", header.Hash().String(),
			header.Number.Uint64(), len(btcAttrDep.Headers), currentTipHash.String(), currentTipHeight,
			expectedPreviousTipHash[:], expectedPreviousTipHeight), "err", err)
	}

	newTipHash := newTip.BlockHash()

	if !bytes.Equal(newTipHash[:], expectedPreviousTipHash[:]) {
		// TODO: TBC recovery from genesis
		log.Crit(fmt.Sprintf("when unapplying hVM changes from block %s @ %d, attempted to remove "+
			"%d headers and change the canonical tip from %s @ %d to %s @ %d, but TBC reports that the "+
			"canonical tip after state transition is %s @ %d which is incorrect", header.Hash().String(),
			header.Number.Uint64(), len(btcAttrDep.Headers), currentTipHash.String(), currentTipHeight,
			expectedPreviousTip.BlockHash().String(), expectedPreviousTipHeight, newTipHash.String(), newHeight),
			"err", err)
	}

	log.Info(fmt.Sprintf("successfully unapplied hVM changes from block %s @ %d, removed %d headers "+
		"and changed the canonical tip from %s @ %d to %s @ %d, last header before removed chunk is %x, rt=%d",
		header.Hash().String(), header.Number.Uint64(), len(btcAttrDep.Headers), currentTipHash.String(),
		currentTipHeight, expectedPreviousTip.BlockHash().String(), expectedPreviousTipHeight, lastHeaderHash[:], rt))

	return nil
}

// addExternalHeadersOutcome is the classification of a non-nil error returned by
// tbc.AddExternalHeaders on the hVM apply path. The mapping to a consensus error is consensus-binding:
// a wrong classification either false-rejects a canonical block (treating a torn store as a bad block)
// or fails to reject an invalid one. See classifyAddExternalHeadersError.
type addExternalHeadersOutcome int

const (
	// addHeadersDuplicate: every header in the batch is already present (database.DuplicateError) — an
	// idempotent re-application (post-restore retry / reorg re-apply). Never a bad block.
	addHeadersDuplicate addExternalHeadersOutcome = iota
	// addHeadersCorrupt: a typed database.NotFoundError after connectivity-validation confirmed the
	// parents were present — a header just proven present is now missing, a torn store. Recoverable via
	// restore, never a bad block. (A non-typed leveldb/IO fault is not classified here — it maps to
	// addHeadersBadBlock; see consensusErrorForAddHeadersOutcome.)
	addHeadersCorrupt
	// addHeadersBadBlock: the batch genuinely does not connect to committed state, or is malformed —
	// the block is invalid.
	addHeadersBadBlock
)

// classifyAddExternalHeadersError maps a non-nil AddExternalHeaders error to a consensus outcome. It is
// pure (no receiver, no IO) so the consensus-binding mapping is unit-testable without a torn leveldb.
// The discriminator for a database.NotFoundError is connectivityConfirmed: the floor-aware validator's
// anchor loop resolves every header's parent (returning Unconnected/IO first) before the floor gate, so
// a nil or below-floor verdict both prove every parent was resolvable (against the committed view or an
// earlier header in the same batch). The only parent AddExternalHeaders looks up in the committed store
// is the first new header's parent, which the anchor loop confirmed. Hence a NotFound when connectivity
// was confirmed can only be a torn store (corrupt -> self-heal); a NotFound when connectivity was not
// confirmed (Unconnected, or the validator did not run) is the genuine non-connecting signal (bad block,
// preserving pre-difficulty-enforcement behavior). database.DuplicateError is always idempotent. Any other (non-typed)
// error — intra-batch non-contiguity (malformed) or a leveldb fault — conservatively maps to bad block.
func classifyAddExternalHeadersError(err error, connectivityConfirmed bool) addExternalHeadersOutcome {
	var dupErr database.DuplicateError
	if errors.As(err, &dupErr) {
		return addHeadersDuplicate
	}
	var notFoundErr database.NotFoundError
	if errors.As(err, &notFoundErr) {
		if connectivityConfirmed {
			return addHeadersCorrupt
		}
		return addHeadersBadBlock
	}
	return addHeadersBadBlock
}

const (
	// btcAddHeadersMaxRetries / btcAddHeadersRetryDelay bound an in-place retry of AddExternalHeaders on a
	// transient (non-typed) IO/leveldb fault before the error is classified. 3 retries x 20ms = 60ms
	// worst-case added latency, only on the rare transient fault — far cheaper than the alternatives it
	// avoids (a node-local false-reject that costs a re-derive, or a full from-genesis restore).
	btcAddHeadersMaxRetries = 3
	btcAddHeadersRetryDelay = 20 * time.Millisecond
)

// isTransientAddHeadersError reports whether an AddExternalHeaders error is the transient IO/leveldb class
// an in-place retry can ride out — i.e. not a semantic outcome. A database.DuplicateError (idempotent —
// the headers are already present) and a typed database.NotFoundError (a genuine missing parent / torn
// store, handled deterministically by classifyAddExternalHeadersError) are semantic and must not be
// retried: retrying changes nothing and only delays the correct handling. Everything else is the
// non-typed leveldb/IO fault that today maps straight to addHeadersBadBlock -> consensus.ErrInvalidHVMHeaders,
// a node-local false-reject of a possibly-valid block — exactly what a retry should ride out.
// Consensus-neutral: a retry only changes whether a transient local fault rejects; the accept/reject
// verdict on healthy IO is unchanged, so it never introduces divergence — it only narrows the window of
// an already-existing node-local IO-induced reject (recoverable by re-derive/restart).
func isTransientAddHeadersError(err error) bool {
	if err == nil {
		return false
	}
	var dupErr database.DuplicateError
	var notFoundErr database.NotFoundError
	return !errors.As(err, &dupErr) && !errors.As(err, &notFoundErr)
}

// shouldRetryAddHeadersIO reports whether a non-typed AddExternalHeaders error at the apply path can only
// be a transient IO fault (retry-worthy), as opposed to a deterministic malformed-batch reject (the TBC node's
// pre-IO intra-batch contiguity check returns a plain, non-typed error that retry can never clear). It is
// retry-worthy iff op-geth's own validator already confirmed the batch connects (connectivityConfirmed —
// the accept / below-floor-defer arms) or this is restore-replay of already-committed, known-contiguous
// history (!enforceBTCDiff, where the contextual-difficulty validation block is skipped). When enforcing a batch the
// validator did not confirm (ErrBTCBatchUnconnected -> connectivityConfirmed stays false), a non-typed error
// is the deterministic contiguity reject — do not retry (avoids a redundant ~60ms-under-chainmu retry +
// per-attempt Warn spam on a bad block). Residual: a rare batch shape that op-geth's overlay accepts but
// the TBC node's stricter contiguity rejects would still be retried — bounded (one retry, then a bad block);
// the common non-connecting case is excluded.
func shouldRetryAddHeadersIO(connectivityConfirmed, enforceBTCDiff bool) bool {
	return connectivityConfirmed || !enforceBTCDiff
}

// retryWhileTransient retries `call` while its error is transient (per isTransient) up to maxRetries,
// sleeping `delay` between attempts, aborting (returning the last error) on ctx cancellation. `firstErr`
// is the error from the initial, already-made attempt; `call` re-invokes the operation. `onRetry` (if
// non-nil) is invoked before each retry's sleep with the 1-based attempt number and the error, for
// logging. It is a free function (no *BlockChain, no tbcd types) so the loop control is unit-testable
// with stub closures. Returns the final error (nil once an attempt succeeds, the last transient error
// after the bound/cancellation, or firstErr immediately when it is nil/semantic).
func retryWhileTransient(ctx context.Context, maxRetries int, delay time.Duration, isTransient func(error) bool,
	firstErr error, call func() error, onRetry func(attempt int, err error)) error {
	err := firstErr
	for attempt := 1; err != nil && isTransient(err) && attempt <= maxRetries; attempt++ {
		if onRetry != nil {
			onRetry(attempt, err)
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
		err = call()
	}
	return err
}

// consensusErrorForAddHeadersOutcome maps the two terminal AddExternalHeaders outcomes to their consensus
// error. Pure, so each arm's consensus-binding return is unit-pinned without a torn leveldb.
// addHeadersDuplicate is not mapped here: it performs IO (verify the canonical-tip claim + advance the
// state id) and returns nil on success, so it is handled inline at the call site.
func consensusErrorForAddHeadersOutcome(o addExternalHeadersOutcome) error {
	switch o {
	case addHeadersCorrupt:
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	case addHeadersBadBlock:
		return consensus.ErrInvalidHVMHeaders
	default:
		// addHeadersDuplicate (handled inline) must never reach here; fail closed to an invalid block
		// (a reject), never a silent accept.
		return consensus.ErrInvalidHVMHeaders
	}
}

// applyHvmHeaderConsensusUpdate retrieves the block corresponding to
// the provided block header, extracts its Bitcoin Attributes Deposited
// transaction and, if it exists, applies the headers contained in it
// to the protocol's lightweight view of Bitcoin and verifies that the
// claimed canonical tip is correct.
// enforceBTCDiff gates contextual-difficulty enforcement. It is true on the live forward-apply
// and reorg paths and false during a full state restore/replay of already-accepted canonical blocks
// (performFullHvmHeaderStateRestore log.Crits on any apply error, so re-judging a historical block under
// the new rule must not turn recovery into a crash).
func (bc *BlockChain) applyHvmHeaderConsensusUpdate(header *Header, attemptPrefetch bool, enforceBTCDiff bool) error {
	// Migrate-before-enforce: a node in the DEFER state (testnet3 params over the mainnet pair
	// this boot) must NOT enforce contextual difficulty on the import path — it would judge mainnet headers
	// under TestNet3Params and split. Fold the per-boot enforceability gate in here so every caller (forward
	// apply, reorg) is covered at one point; when not enforceable the path behaves like restore/replay (the
	// already-accepted canonical headers are re-applied without re-judging difficulty).
	enforceBTCDiff = enforceBTCDiff && bc.hvmDiffEnforceable.Load()
	block := bc.getBlockFromDiskOrHoldingPen(header.Hash())
	if block == nil {
		// Block not on disk or in holding pen
		return fmt.Errorf("unable to get block %s @ %d to apply hVM consensus updates",
			header.Hash().String(), header.Number.Uint64())
	}

	stateTransitionTargetHash := [32]byte{}
	copy(stateTransitionTargetHash[0:32], header.Hash().Bytes()[0:32])

	// Store the current TBC state hash so we can put it back if we revert our changes here
	previousStateTransitionHash, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil {
		// Migration-aware: this apply path is reachable from the forward catch-up inside the
		// migration rebuild window, where a plain log.Crit's os.Exit would leak the in-progress gauge and skip
		// the "failed" meter; route through migrationCrit when a migration is in progress.
		bc.hvmMigrationAwareCrit("Unable to get upstream state id from TBC", "err", err)
	} else {
		log.Info(fmt.Sprintf("Applying hVM header update: adding block %s @ %d, previous state id is %x",
			header.Hash().String(), header.Number.Uint64(), previousStateTransitionHash[:]))
	}

	prevHashSanity := common.BytesToHash(previousStateTransitionHash[:])
	if bytes.Equal(prevHashSanity[:], hVMGenesisUpstreamId[:]) {
		log.Info(fmt.Sprintf("Applying first hVM header update on block %s @ %d",
			header.Hash().String(), header.Number.Uint64()))
	} else {
		check := bc.getBlockFromDiskOrHoldingPen(prevHashSanity)
		if check == nil {
			// Symmetric to the currentHead==nil / unapply orphaned-store guards: the upstream-state-id's
			// block body is absent from disk + holding pen (a deep reorg/rewind orphaned it; its header may
			// still resolve, which is why the header-store currentHead guard upstream did not catch it).
			// check.Hash() below would nil-panic; return the recoverable corrupt-state sentinel so the caller
			// rebuilds the lightweight view from genesis instead of crashing.
			log.Error(fmt.Sprintf("prior-state block %x is nil while applying hVM update for %s @ %d; treating as corrupt hVM state",
				prevHashSanity[:], header.Hash().String(), header.Number.Uint64()))
			return consensus.ErrCorruptHVMHeaderOnlyModeState
		}
		checkHash := check.Hash()
		if !bytes.Equal(checkHash[:], header.ParentHash[:]) {
			// This implies a code bug as upstream calls of this function should be guarded to
			// only occur when the new block is the direct child of the current state. Migration-aware:
			// also reachable from the forward catch-up window — route through migrationCrit so the
			// failed meter/gauge are cleared before os.Exit.
			bc.hvmMigrationAwareCrit(fmt.Sprintf("Applying hVM header update for block %s @ %d failed, "+
				"previous state id is %x but parent of updated block is %s @ %d",
				header.Hash().String(), header.Number.Uint64(), previousStateTransitionHash[:],
				header.ParentHash[:], header.Number.Uint64()-1))
		}
	}

	btcAttrDep, err := block.Transactions().ExtractBtcAttrData()
	if err != nil {
		// Error implies that state of Bitcoin Attributes Deposited tx in the transaction list is invalid
		log.Error(fmt.Sprintf("Error while extracting Bitcoin Attributes Deposited transaction to process hVM state "+
			"application for applying block %s @ %d", header.Hash().String(), header.Number.Uint64()), "err", err)
		return consensus.ErrInvalidHVMBlockFormat // Block will never be valid, error was extracting BTC Attr. Dep. tx
	}

	if btcAttrDep == nil {
		log.Info(fmt.Sprintf("Nothing to apply in hVM state for block %s @ %d; doesn't contain a Bitcoin "+
			"Attributes Deposited transaction", header.Hash().String(), header.Number.Uint64()))

		// Even though we didn't make any changes, explicitly update TBC's state id to indicate that
		// TBC's current state is correct after processing this block if hVM Phase 0 is active at
		// this block's timestamp
		if bc.chainConfig.IsHvm0(header.Time) {
			err := bc.tbcHeaderNode.SetUpstreamStateId(bc.ctx, stateTransitionTargetHash)
			if err != nil {
				// Being unable to set the upstream state id implies possible data corruption
				log.Error(fmt.Sprintf("Error while updating the upstream state id in TBC with no corresponding "+
					"consensus state modifications for block %s @ %d", header.Hash().String(), header.Number.Uint64()), "err", err)
				return consensus.ErrCorruptHVMHeaderOnlyModeState
			}
		}
		return nil
	}

	if !bc.chainConfig.IsHvm0(header.Time) { // && btcAttrDep != nil per above check
		log.Error(fmt.Sprintf("block %s @ %d has a Bitcoin Attributes Deposited transaction but its timestamp "+
			"%d is before the hVM Phase 0 activation height %d", header.Hash().String(), header.Number.Uint64(),
			header.Time, *bc.chainConfig.Hvm0Time))
		return consensus.ErrInvalidHVMBlockFormat // Block will never be valid
	}

	prevHeight, prevTip, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		// Being unable to get the best block header implies possible data corruption
		log.Error(fmt.Sprintf("when processing block %s @ %d, unable to retrieve tip from lightweight TBC!",
			header.Hash().String(), header.Number.Uint64()), "err", err)
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}
	log.Debug(fmt.Sprintf("before processing BTC headers from block %s @ %d, the lightweight TBC node's tip "+
		"is %s @ %d", header.Hash().String(), header.Number.Uint64(), prevTip.BlockHash().String(), prevHeight))

	prevTipHash := prevTip.BlockHash()

	headersToAdd := len(btcAttrDep.Headers)
	var lastHeader *[80]byte
	if headersToAdd > 0 {
		// BTC Attributes Deposited transaction communicates at least one new header, store the last one for reference
		lastHeader = &btcAttrDep.Headers[headersToAdd-1]

		reconstitutedHeaders, err := unflattenBTCHeaders(btcAttrDep.Headers)
		if err != nil {
			// Being unable to parse the BTC headers in the Bitcoin Attributes Deposited transaction means the
			// transaction (and thus block) is invalid
			log.Error(fmt.Sprintf("when applying hVM changes for block %s @ %d, unable to unflatten "+
				"one of the BTC headers from the block", header.Hash().String(), header.Number.Uint64()),
				"err", err)
			return consensus.ErrInvalidHVMBlockFormat
		}

		// Contextual-difficulty (enforce on the consensus path — floor-aware). Validate the batch's contextual Bitcoin
		// difficulty against the lightweight consensus view before committing it. This is the
		// consensus-binding ingress, so it is the authoritative enforcement point. The other two writers
		// into bc.tbcHeaderNode are also covered: the
		// sequencer build path validates+truncates and snap-sync validates the enforceable suffix before
		// bulk-loading (see getBitcoinAttributesForNextBlock and SnapSyncHvm). Outcomes:
		//   - accept (nil): proceed to commit.
		//   - below floor: the lightweight node is seeded from an effective-genesis floor
		//     (GenesisHeightOffset), so near-floor headers' required walks cross below the floor and are
		//     unverifiable here; defer (fall through to AddExternalHeaders, whose cumulative-work + the
		//     canonical-tip check below still apply). Bounded near-floor band, but not one-time: the floor
		//     gate is stateless, so a reorg back below floor+clearance re-enters the defer band (see the
		//     vm.ErrBTCBatchBelowFloor doc).
		//   - unconnected: the batch does not connect to committed state; fall through so the existing
		//     AddExternalHeaders no-orphan path reports it as ErrInvalidHVMHeaders.
		//   - genuine RuleError: a real contextual violation -> ErrInvalidHVMHeaders (returns before any
		//     AddExternalHeaders commit; the insertChain caller walks consensus state back).
		//   - IO/corrupt read: ErrCorruptHVMHeaderOnlyModeState (recoverable; never a silent accept).
		// Gated on the hVM fork and on enforceBTCDiff (skipped during restore/replay).
		// batchConnectivityConfirmed records whether the floor-aware validator confirmed the batch connects
		// to committed state. The validator's anchor loop resolves every header's parent (returning
		// Unconnected / IO-corrupt first) before the floor gate, so both a nil and a below-floor verdict
		// prove the parents were present. The AddExternalHeaders error classifier below uses this to tell a
		// torn store (connectivity confirmed, yet a header now missing -> corrupt) apart from a genuinely
		// non-connecting batch (-> invalid block). Left false when the validator reported Unconnected or did
		// not run (replay / pre-fork).
		batchConnectivityConfirmed := false
		if enforceBTCDiff && bc.chainConfig.IsHvm0(header.Time) {
			// Proof-of-work gate (context-free; runs on every header, not subject to the
			// effective-genesis floor defer the contextual check below uses, because hash<=target needs no
			// ancestry). This verifies the header's hash meets its claimed target (real work), independent
			// of the difficulty field the contextual check validates. Gated on
			// the same enforceBTCDiff as the contextual check. A btcd RuleError here is an invalid block; an
			// unknown-network/nil fail-closes to recoverable corrupt state (never a silent accept).
			switch err := vm.CheckBTCHeaderBatchPoWForNetwork(bc.tbcHeaderNodeConfig.Network,
				reconstitutedHeaders.Headers); {
			case err == nil:
				// every header meets its claimed PoW target; fall through to the contextual check
			case errors.Is(err, vm.ErrBTCHeaderContextUnavailable):
				log.Error(fmt.Sprintf("block %s @ %d: BTC-header PoW validation could not be parameterized "+
					"(unknown network / nil) — treating as recoverable corrupt state",
					header.Hash().String(), header.Number.Uint64()), "err", err)
				return consensus.ErrCorruptHVMHeaderOnlyModeState
			default:
				log.Error(fmt.Sprintf("block %s @ %d: REJECTED — its Bitcoin Attributes Deposited tx carries a "+
					"BTC header whose hash does not meet its claimed proof-of-work target",
					header.Hash().String(), header.Number.Uint64()), "err", err)
				return consensus.ErrInvalidHVMHeaders
			}

			switch err := vm.ValidateBTCHeaderBatchForNetwork(bc.ctx, bc.tbcHeaderNode,
				bc.tbcHeaderNodeConfig.Network, bc.tbcHeaderNodeConfig.GenesisHeightOffset,
				reconstitutedHeaders.Headers); {
			case err == nil:
				// every header is contextually valid AND connects; proceed to commit
				batchConnectivityConfirmed = true
			case errors.Is(err, vm.ErrBTCBatchBelowFloor):
				// near-floor: enforcement deferred, but the anchor loop still confirmed connectivity
				batchConnectivityConfirmed = true
				log.Debug(fmt.Sprintf("block %s @ %d: BTC header batch is within the effective-genesis-floor "+
					"clearance; deferring contextual-difficulty enforcement to AddExternalHeaders for this batch",
					header.Hash().String(), header.Number.Uint64()))
			case errors.Is(err, vm.ErrBTCBatchUnconnected):
				log.Warn(fmt.Sprintf("block %s @ %d: BTC header batch does not connect to the committed "+
					"consensus view; letting AddExternalHeaders reject it", header.Hash().String(), header.Number.Uint64()))
			case errors.Is(err, vm.ErrBTCHeaderContextUnavailable):
				log.Error(fmt.Sprintf("block %s @ %d: contextual BTC-difficulty validation hit an IO/unreadable "+
					"lightweight-TBC error — treating as recoverable corrupt state",
					header.Hash().String(), header.Number.Uint64()), "err", err)
				return consensus.ErrCorruptHVMHeaderOnlyModeState
			default:
				log.Error(fmt.Sprintf("block %s @ %d: REJECTED — its Bitcoin Attributes Deposited tx carries a "+
					"contextually-invalid BTC header (wrong difficulty / median-time-past / version)",
					header.Hash().String(), header.Number.Uint64()), "err", err)
				return consensus.ErrInvalidHVMHeaders
			}
		}

		log.Info(fmt.Sprintf("[Apply HVM Header Consensus Update] *ADDING* external BTC headers:"))
		for i := 0; i < len(reconstitutedHeaders.Headers); i++ {
			log.Info(fmt.Sprintf("\t %s", reconstitutedHeaders.Headers[i].BlockHash().String()))
		}

		it, cbh, lbh, _, err := bc.tbcHeaderNode.AddExternalHeaders(
			bc.ctx, reconstitutedHeaders, stateTransitionTargetHash[:])
		// Retry in place on a transient (non-typed) IO/leveldb fault before classifying — rides out a
		// momentary blip that would otherwise be classified addHeadersBadBlock -> ErrInvalidHVMHeaders (a
		// node-local false-reject) or, on the restore-replay path, log.Crit the rebuild.
		//
		// Why retry is idempotent (the underlying store write is not atomic, so a fault mid-write can leave
		// a partial commit): (1) a re-insert is duplicate-skipped (keyed on the header hash) — cumulative
		// work is recomputed deterministically from Bits, never double-counted; and (2) the one partial-commit
		// window that matters — headers committed but the upstream-state-id not — re-surfaces on retry as
		// DuplicateError, which the addHeadersDuplicate arm below repairs idempotently (re-verifies the
		// canonical tip and re-issues SetUpstreamStateId).
		// That duplicate-arm SetUpstreamStateId advance is load-bearing for this case; do not remove it.
		// See isTransientAddHeadersError. Bounded; aborts on ctx cancellation. Gated by shouldRetryAddHeadersIO
		// so a deterministic malformed-batch reject (which retry can never clear) is classified immediately
		// rather than burning the retry budget + Warn spam.
		if shouldRetryAddHeadersIO(batchConnectivityConfirmed, enforceBTCDiff) {
			err = retryWhileTransient(bc.ctx, btcAddHeadersMaxRetries, btcAddHeadersRetryDelay, isTransientAddHeadersError, err,
				func() error {
					it, cbh, lbh, _, err = bc.tbcHeaderNode.AddExternalHeaders(
						bc.ctx, reconstitutedHeaders, stateTransitionTargetHash[:])
					return err
				},
				func(attempt int, e error) {
					log.Warn(fmt.Sprintf("block %s @ %d: transient IO fault adding BtcAttr BTC headers to the "+
						"lightweight view; retrying in place (attempt %d/%d) before classifying",
						header.Hash().String(), header.Number.Uint64(), attempt, btcAddHeadersMaxRetries), "err", e)
				})
		}
		// This retry is deliberately scoped to the consensus apply funnel (all three consensus callers —
		// forward-apply, reorg, and restore-replay — route through applyHvmHeaderConsensusUpdate). The
		// other AddExternalHeaders sites (snap reconstruction; sequencer build-path dry-run) keep their
		// fail-stop crits: those are node-local and restart-recoverable, and the sequencer dry-run's
		// Add-then-Remove would need separate idempotency reasoning to retry safely.
		if err != nil {
			addOutcome := classifyAddExternalHeadersError(err, batchConnectivityConfirmed)
			switch addOutcome {
			case addHeadersDuplicate:
				// Idempotent: every header in the batch is already present (a post-restore retry or a reorg
				// re-apply), never a bad block. AddExternalHeaders returned before running its state-id batch
				// hook, so — like the empty-but-present path — verify the claimed canonical tip against the
				// (already-updated) view and advance the upstream state id to this block ourselves. On any
				// inconsistency, self-heal (corrupt) rather than false-reject a possibly-canonical block.
				_, curTip, e := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
				if e != nil {
					log.Error(fmt.Sprintf("block %s @ %d: BtcAttr headers already present, but reading the current "+
						"canonical tip failed", header.Hash().String(), header.Number.Uint64()), "err", e)
					return consensus.ErrCorruptHVMHeaderOnlyModeState
				}
				curTipHash := curTip.BlockHash()
				if !bytes.Equal(curTipHash[:], btcAttrDep.CanonicalTip[:]) {
					log.Error(fmt.Sprintf("block %s @ %d: BtcAttr headers already present, but the canonical tip "+
						"%x does not match the claimed %x — treating as recoverable corrupt state",
						header.Hash().String(), header.Number.Uint64(), curTipHash[:], btcAttrDep.CanonicalTip[:]))
					return consensus.ErrCorruptHVMHeaderOnlyModeState
				}
				if e := bc.tbcHeaderNode.SetUpstreamStateId(bc.ctx, stateTransitionTargetHash); e != nil {
					log.Error(fmt.Sprintf("block %s @ %d: BtcAttr headers already present, but advancing the upstream "+
						"state id failed", header.Hash().String(), header.Number.Uint64()), "err", e)
					return consensus.ErrCorruptHVMHeaderOnlyModeState
				}
				log.Info(fmt.Sprintf("block %s @ %d: BtcAttr BTC headers already present in the lightweight view "+
					"(duplicate); treated as idempotent, advanced upstream state id, canonical tip unchanged",
					header.Hash().String(), header.Number.Uint64()))
				return nil
			case addHeadersCorrupt:
				// Connectivity was just confirmed by the validator, yet AddExternalHeaders reports a typed
				// NotFound -> a header just proven present is missing -> a torn lightweight view, not a bad
				// block. Recoverable via restore.
				log.Error(fmt.Sprintf("block %s @ %d: adding BtcAttr BTC headers failed although connectivity was "+
					"confirmed — treating as recoverable corrupt state", header.Hash().String(), header.Number.Uint64()),
					"err", err)
				return consensusErrorForAddHeadersOutcome(addOutcome)
			default: // addHeadersBadBlock
				// The batch genuinely does not connect to committed state (no validator connectivity
				// confirmation), is malformed (intra-batch non-contiguity), or hit a non-typed leveldb/IO
				// fault -> invalid block (preserves pre-difficulty-enforcement AddExternalHeaders no-orphan behavior). An IO
				// fault here is a node-local false-reject (a known residual; healthy peers still accept) — it
				// is never a silent accept and never a consensus split.
				log.Error(fmt.Sprintf("block %s @ %d has a Bitcoin Attributes Deposited transaction which contains "+
					"%d Bitcoin headers, and adding these headers to hVM's lightweight BTC consensus view caused an "+
					"error", header.Hash().String(), header.Number.Uint64(), len(btcAttrDep.Headers)), "err", err)
				// Pass the classified outcome (not a literal) so the test-pinned mapper is the single source
				// of the consensus return: a deleted case label or a swapped literal cannot change it.
				return consensusErrorForAddHeadersOutcome(addOutcome)
			}
		}

		// Proactively check for any missing full blocks between full TBC's current indexed height and the
		// new canonical tip that resulted from adding the new headers, and trigger a fetch from peers for
		// any blocks that we will need in the future when the full TBC node's indexers are advanced.

		// Convert tbcd.BlockHeader (contains position and cumulative diff. info) to wire.BlockHeader
		cbhWire, err := cbh.Wire()
		if err != nil {
			// Although this failure does not immediately prevent chain progression, the inability to convert
			// the canonical block hash returned from TBC by adding headers to a wire indicates something has
			// gone very wrong so exit. Also reachable from the migration forward catch-up window — route through
			// migrationCrit so the failed meter/gauge are cleared before os.Exit.
			bc.hvmMigrationAwareCrit(fmt.Sprintf("after applying Bitcoin consensus information from block %s @ %d to "+
				"hVM's lightweight BTC consensus view, the canonical header %s returned could not be converted "+
				"to a wire message", header.Hash().String(), header.Number.Uint64(), cbh.Hash.String()),
				"err", err)
		}

		if attemptPrefetch {
			_, blocksMissing, _, err := vm.TBCBlocksAvailableToHeader(bc.ctx, cbhWire)
			if err != nil {
				log.Error(fmt.Sprintf("unable to proactively check for full TBC node containing blocks to tip %s",
					cbh.Hash.String()), "err", err)
			}

			if blocksMissing != nil {
				if len(*blocksMissing) > 0 {
					for _, blockMissing := range *blocksMissing {
						// Note that it's possible the canonical tip returned by lightweight consensus is not canonical
						// on the actual Bitcoin network and one or more blocks cannot be acquired, but as long as
						// the reorg is smaller than the hVM indexing delay/lag it will be fine.
						log.Info(fmt.Sprintf("Proactively attempting to fetch missing full BTC block %s from TBC peers "+
							"so it will be available when needed for indexing", blockMissing.BlockHash().String()))
						vm.TBCAttemptBlockRefetch(bc.ctx, &blockMissing)
					}
				}
			}
		}

		cbHash := cbh.Hash[:]
		// Check that the Bitcoin Attributes Deposited transaction claims the correct canonical tip
		if !bytes.Equal(cbHash, btcAttrDep.CanonicalTip[:]) {
			// Canonical tip determined by TBC based on the new headers does not match canonical tip claimed by
			// Bitcoin Attributes Deposited transaction

			// Print out error, then remove the bad headers to return TBC to the correct state
			log.Error(fmt.Sprintf("block %s @ %d has a Bitcoin Attributes Deposited transaction which "+
				"claims that after adding %d headers ending with %x, the canonical tip should be %x, but after "+
				"adding the headers to TBC the canonical tip is %x", header.Hash().String(), header.Number.Uint64(),
				headersToAdd, lastHeader[:], btcAttrDep.CanonicalTip[:], cbHash[:]))

			log.Info(fmt.Sprintf("[Apply HVM Header Consensus Update] *REMOVING* external BTC headers:"))
			for i := 0; i < len(reconstitutedHeaders.Headers); i++ {
				log.Info(fmt.Sprintf("\t %s", reconstitutedHeaders.Headers[i].BlockHash().String()))
			}

			// Remove the added headers and set the canonical tip and previous upstream state id back to
			// what it was prior to the invalid addition
			rt, removalParent, err := bc.tbcHeaderNode.RemoveExternalHeaders(
				bc.ctx, reconstitutedHeaders, prevTip, previousStateTransitionHash[:])

			if err != nil {
				log.Error(fmt.Sprintf("after adding headers ending with %x from the Bitcoin Attributes "+
					" Deposited transaction in block %s @ %d, unable to remove those headers from TBC's view",
					lastHeader[:], header.Hash().String(), header.Number), "err", err)

				// Unable to unapply our undesired changes, TBC lightweight node could be recovered
				return consensus.ErrCorruptHVMHeaderOnlyModeState
			}

			removalParentHash := removalParent.BlockHash()

			log.Error(fmt.Sprintf("successfully removed headers applied from invalid block %s @ %d, last header "+
				"before removed section is %x. Removal type: %d", header.Hash().String(), header.Number.Uint64(),
				removalParentHash[:], rt))

			// Headers this block adds to lightweight view don't result in the claimed new canonical tip
			return consensus.ErrInvalidHVMHeaders
		}

		lbHash := lbh.Hash[:]
		if !bytes.Equal(lbh.Header[:], lastHeader[:]) {
			// Indicates a bug in TBC, as TBC didn't add all the headers we passed in.
			// Unlikely this would be due to data corruption, so assume bug and exit. Also reachable from the
			// migration forward catch-up window — route through migrationCrit so the failed meter/gauge are
			// cleared before os.Exit.
			bc.hvmMigrationAwareCrit(fmt.Sprintf("block %s @ %d has a Bitcoin Attributes Deposited transaction which "+
				"contains %d headers ending in %x, but after adding those headers to lightweight TBC, TBC's last "+
				"added block was %x", header.Hash().String(), header.Number.Uint64(), headersToAdd, lastHeader[:],
				lbHash[:]))
		}

		log.Info(fmt.Sprintf("Successfully added %d bitcoin headers from the Bitcoin Attributes Deposited tx "+
			"from block %s @ %d, current canonical tip is %x, former tip was %x @ %d, insertType=%d", headersToAdd,
			header.Hash().String(), header.Number.Uint64(), lbHash[:], prevTipHash[:], prevHeight, it))
		return nil
	} else {
		// Empty-but-present case (btcAttrDep != nil, zero headers): the forward counterpart of the unapply
		// btcAttrDepIsHeaderless(...) no-op branch; the two must stay exact inverses, so keep this
		// structural classification consistent with that helper if refactored.
		//
		// No headers to add, make sure that claimed canonical in BTC Attributes Deposited matches TBC's current
		if !bytes.Equal(prevTipHash[:], btcAttrDep.CanonicalTip[:]) {
			log.Error(fmt.Sprintf("block %s @ %d contains a Bitcoin Attributes Deposited transaction which "+
				"does not contain any headers, but claims the canonical tip should be %x when light TBC's tip "+
				"is %x", header.Hash().String(), header.Number.Uint64(), btcAttrDep.CanonicalTip[:], prevTipHash[:]))
			// Block contains a BTC Attr. Dep. transaction which claims an incorrect canonical claim, reject
			return consensus.ErrInvalidHVMHeaders
		}
		// An empty-but-present BtcAttr tx makes no TBC header change, but — like the no-BtcAttr-tx path
		// above and the headers-added path (which advances the id via AddExternalHeaders) — we must still
		// advance the TBC upstream state id to represent this block. Omitting it would leave the id at the
		// parent, which then trips the parent-mismatch check when the next block is applied (and during a
		// full state restore). The upstream state id is TBC-internal metadata (never part of the EVM state
		// root / block hash), so advancing it here is consensus-safe and changes no block's acceptance —
		// the CanonicalTip check above is the only acceptance decision and is preserved. The matching
		// unapply rolls it back.
		if bc.chainConfig.IsHvm0(header.Time) {
			err := bc.tbcHeaderNode.SetUpstreamStateId(bc.ctx, stateTransitionTargetHash)
			if err != nil {
				// Being unable to set the upstream state id implies possible data corruption
				log.Error(fmt.Sprintf("Error while updating the upstream state id in TBC for a Bitcoin "+
					"Attributes Deposited transaction with no headers for block %s @ %d", header.Hash().String(),
					header.Number.Uint64()), "err", err)
				return consensus.ErrCorruptHVMHeaderOnlyModeState
			}
		}
		return nil
	}
}

// setMissingProgressionBlocks updates the missing-BTC-progression cache under tbcHeaderNodeMu so the
// lock-free GetMissingBtcBlocks reader (peer-broadcast goroutine) cannot race this write. tbcHeaderNodeMu
// — not chainmu — serializes this write against that reader, so the guard is correct regardless of
// caller: the writers are reached from both the chainmu-held apply path and the non-chainmu SnapSyncHvm
// completion path (both via updateFullTBCToLightweight). For chainmu-held callers the lock ordering is
// chainmu -> tbcHeaderNodeMu; reset never holds the mutex while reaching here, so there is no reentrant Lock.
func (bc *BlockChain) setMissingProgressionBlocks(m *wire.MsgHeaders) {
	bc.tbcHeaderNodeMu.Lock()
	bc.missingProgressionBlocks = m
	bc.tbcHeaderNodeMu.Unlock()
}

func (bc *BlockChain) GetMissingBtcBlocks() []common.Hash {
	if bc == nil {
		return nil
	}
	// This runs on the per-peer broadcast goroutine (prefetchBTCBlocks), outside chainmu, so it can race
	// resetHvmHeaderNodeToGenesis tearing down + reassigning bc.tbcHeaderNode (and the missingProgressionBlocks
	// writes). Hold the read side of tbcHeaderNodeMu only around the lifecycle-sensitive accesses — the
	// tbcHeaderNode nil-check, the missingProgressionBlocks read, and BlockHeaderBest (which produces `tip`) —
	// and release it before the unbounded full-node walk below. `tip` is a freshly-decoded *wire.BlockHeader
	// that does not alias the torn-down node, and TBCBlocksAvailableToHeader touches only the independent
	// global vm.TBCFullNode, so it is safe to run unlocked; releasing first means a from-genesis restore
	// (which takes tbcHeaderNodeMu.Lock while holding chainmu on the re-apply path) is never stalled behind
	// this broadcast read, avoiding a priority inversion that would freeze block insertion. TryRLock (not
	// RLock) so we never even briefly block on an in-progress reset — a missed 5s prefetch round is benign.
	tip, early, done := func() (*wire.BlockHeader, []common.Hash, bool) {
		if !bc.tbcHeaderNodeMu.TryRLock() {
			log.Debug("GetMissingBtcBlocks() skipped: lightweight TBC node reset in progress")
			return nil, nil, true
		}
		defer bc.tbcHeaderNodeMu.RUnlock()

		if bc.tbcHeaderNode == nil {
			log.Warn("GetMissingBtcBlocks() does not have tbcHeaderNode active yet")
			return nil, nil, true
		}

		if bc.missingProgressionBlocks != nil {
			headers := bc.missingProgressionBlocks.Headers
			missingHashArr := make([]common.Hash, len(headers))
			for i := 0; i < len(headers); i++ {
				blockhash := headers[i].BlockHash()
				bhBytes := blockhash.CloneBytes()
				var hash common.Hash
				hash.SetBytes(bhBytes)
				missingHashArr[i] = hash
			}
			return nil, missingHashArr, true
		}

		_, t, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
		if err != nil {
			log.Debug("Unable to get best block header from TBC node in GetMissingBtcBlocks()", "err", err)
			return nil, nil, true
		}
		return t, nil, false
	}()
	if done {
		return early
	}

	// tbcHeaderNodeMu is released here: the rest operates on the independent global vm.TBCFullNode using the
	// already-captured `tip`, so it runs unlocked and cannot stall a concurrent lightweight-node reset.
	_, blocksMissing, missingHeaderHash, err := vm.TBCBlocksAvailableToHeader(bc.ctx, tip)
	if err != nil {
		log.Error(fmt.Sprintf("unable to proactively check for full TBC node containing blocks to tip %s",
			tip.BlockHash().String()), "err", err)
	}

	if missingHeaderHash != nil {
		// Create an array with just the hash of the one known missing header
		missingHashArr := make([]common.Hash, 0)
		bhBytes := missingHeaderHash.CloneBytes()
		var hash common.Hash
		hash.SetBytes(bhBytes)
		missingHashArr = append(missingHashArr, hash)
		return missingHashArr
	}

	if blocksMissing != nil {
		if len(*blocksMissing) > 0 {
			missingFullBlocks := make([]common.Hash, 0)
			for _, blockMissing := range *blocksMissing {
				bh := blockMissing.BlockHash()
				bhBytes := bh.CloneBytes()
				var hash common.Hash
				hash.SetBytes(bhBytes)
				missingFullBlocks = append(missingFullBlocks, hash)
			}
			return missingFullBlocks
		}
	}

	return nil
}

func (bc *BlockChain) IsHvmEnabled() bool {
	return bc.hvmEnabled
}

// btcAttrCacheKey identifies the inputs the cached Bitcoin Attributes Deposited tx was built from. The
// tx is a pure function of these three: the EVM tip it is built on top of, and the lightweight and
// full-node BTC consensus tips (which together drive the walk-back and the set of headers the tx
// carries), so the cached entry is valid only while all three are unchanged. Keying on the EVM tip alone
// would let a BTC-view change (the full node syncing new headers or a Bitcoin reorg) while the EVM tip is
// unchanged keep re-serving a stale tx; if that stale tx is then rejected, the sequencer re-serves it
// forever — a permanent self-inflicted halt. Keying on all three (a comparable struct, so no dimension
// can be silently dropped) invalidates the entry on any BTC-view change. See TestBtcAttrCacheKey.
type btcAttrCacheKey struct {
	evmTip   common.Hash    // the EVM block the next block is built on top of
	lightTip chainhash.Hash // lightweight TBC node's best BTC header at build time
	fullTip  chainhash.Hash // full TBC node's best BTC header at build time
}

// cachedBtcAttrFor returns the cached Bitcoin Attributes Deposited tx iff a non-nil entry was built for
// exactly this (evmTip, lightTip, fullTip) view; otherwise nil. Pure over the receiver's cache fields, so
// the hit/miss decision (the all-three-must-match key and the non-nil-entry guard) is unit-testable
// without a live full node. Callers must hold bc.chainmu.
func (bc *BlockChain) cachedBtcAttrFor(key btcAttrCacheKey) *types.BtcAttributesDepositedTx {
	if bc.btcAttributesDepCacheEntry != nil && bc.btcAttributesDepCacheKey == key {
		return bc.btcAttributesDepCacheEntry
	}
	return nil
}

// storeBtcAttrCache records the freshly-built tx under its full (evmTip, lightTip, fullTip) key. Callers
// must hold bc.chainmu. Paired with cachedBtcAttrFor so the inline check and write are pinned together by
// TestBtcAttrCacheRoundTrip.
func (bc *BlockChain) storeBtcAttrCache(key btcAttrCacheKey, tx *types.BtcAttributesDepositedTx) {
	bc.btcAttributesDepCacheKey = key
	bc.btcAttributesDepCacheEntry = tx
}

// errHvmBtcAttrPendingBlocked is an internal sentinel returned by getBitcoinAttributesForNextBlock when
// the TBC full node has BTC consensus headers the lightweight view lacks (so the hVM Bitcoin view is
// supposed to advance) but they could not be turned into a BtcAttr tx this round — the full block for the
// next header is not yet available, or a should-never-happen header inconsistency was hit. Not a hard
// error (the caller still builds a valid block without the tx), but it must not be reported as a healthy
// idle cycle or a persistent stall would be invisible to operators. The public wrapper maps it to the
// stuck gauge and hides it from the caller.
var errHvmBtcAttrPendingBlocked = errors.New("hVM BtcAttr: pending BTC headers could not be communicated this build")

// GetBitcoinAttributesForNextBlock computes the optional Bitcoin Attributes Deposited tx for
// the next block on the sequencer build path. It wraps getBitcoinAttributesForNextBlock to
// record an alertable failure metric: on the build path a returned error makes the caller skip
// this block's (optional) BtcAttr tx and retry, so a persistent failure is otherwise only
// visible as a recurring per-build log.Error.
func (bc *BlockChain) GetBitcoinAttributesForNextBlock(timestamp uint64) (*types.BtcAttributesDepositedTx, error) {
	return finalizeHvmBtcAttrResult(bc.getBitcoinAttributesForNextBlock(timestamp))
}

// finalizeHvmBtcAttrResult records the observability metrics (via recordHvmBtcAttrResult) and
// produces the public (tx, err) pair. On any error surfaced to the caller it never leaks a
// partial tx (returns nil, rerr); otherwise it returns tx unchanged — nil for an idle cycle or a
// hidden "pending blocked" sentinel, the built tx on success. Extracted as a pure function so the
// "no partial tx alongside an error" guarantee can be unit-tested without a TBC node.
func finalizeHvmBtcAttrResult(tx *types.BtcAttributesDepositedTx, err error) (*types.BtcAttributesDepositedTx, error) {
	if rerr := recordHvmBtcAttrResult(err); rerr != nil {
		return nil, rerr
	}
	return tx, nil
}

// recordHvmBtcAttrResult records the observability metrics for a BtcAttr generation outcome and
// returns the error the public method should expose to the caller. It is the single place that
// classifies the inner result, so it can be unit-tested without a TBC node:
//   - nil (success or a legitimately idle cycle): clear the stuck gauge.
//   - errHvmBtcAttrPendingBlocked: pending BTC work that did not advance this round (e.g. the
//     next full block is not yet downloaded). Raise the stuck gauge but hide the sentinel from
//     the caller (return nil) so it surfaces as a plain (nil, nil) idle return — no error log spam.
//   - shutdown (errChainStopped, or context.Canceled): not an hVM fault — leave metrics untouched
//     to avoid a spurious alert during shutdown.
//   - any other error: a genuine failure; mark the fail meter and raise the stuck gauge.
func recordHvmBtcAttrResult(err error) error {
	switch {
	case err == nil:
		hvmBtcAttrFailingGauge.Update(0)
	case errors.Is(err, errHvmBtcAttrPendingBlocked):
		hvmBtcAttrFailingGauge.Update(1)
		return nil
	case errors.Is(err, errChainStopped) || errors.Is(err, context.Canceled):
		// Shutdown teardown only. bc.ctx is cancel-only (context.WithCancel, no deadline), so a
		// context.Canceled on this path is the chain context being torn down at Stop(), not a
		// fault — leave metrics untouched to avoid a spurious alert.
		//
		// Load-bearing: we deliberately do not fold context.DeadlineExceeded here. No shutdown path
		// produces it (bc.ctx has no deadline), so a DeadlineExceeded could only originate from a
		// downstream per-call timeout — a genuine backend stall that must raise the alert, so it falls
		// through to the default branch below. If a future change gives bc.ctx a deadline, revisit this.
	default:
		hvmBtcAttrFailMeter.Mark(1)
		hvmBtcAttrFailingGauge.Update(1)
	}
	return err
}

// btcAttrFutureSkewWindow is how far a block timestamp may lead wall-clock (seconds) before the sequencer
// skips attaching a Bitcoin Attributes Deposited tx to it.
const btcAttrFutureSkewWindow = 60 * 60

// btcAttrFutureSkewExceeded reports whether a candidate block timestamp is too far in the FUTURE (more than
// btcAttrFutureSkewWindow ahead of wall-clock `now`) for the sequencer to attach a BtcAttr tx. The ordered
// compare (timestamp > now first) avoids the uint64 underflow that `timestamp - now` would hit for a
// past-timestamped catch-up block — which must still get the tx — so it returns false for every timestamp
// at or before now. Extracted as a pure predicate so this build-path decision is unit-testable.
func btcAttrFutureSkewExceeded(timestamp, now uint64) bool {
	return timestamp > now && timestamp-now > btcAttrFutureSkewWindow
}

// enforceableBTCBatch is the build-path difficulty-enforceability classifier used by longestEnforceableBTCHeaderPrefix to
// truncate the sequencer's proposed BTC-header prefix at the first non-enforceable header. A DEFER-state node
// (hvmDiffEnforceable=false: testnet3 params over the mainnet pair) must NOT truncate — it would judge real
// mainnet headers under TestNet3Params (the 20-min rule) and drop honest headers a correctly-migrated sequencer
// keeps, diverging the build path; it returns nil (accept the full prefix; the apply path is the enforcement
// point and is also gated). When enforceable it checks proof-of-work first (a PoW RuleError truncates like a
// contextual one, so the sequencer never proposes a header whose hash does not meet its target), then contextual
// difficulty. Extracted so the deferred-vs-enforced gate is unit-testable on the real logic.
func (bc *BlockChain) enforceableBTCBatch(batch []*wire.BlockHeader) error {
	if !bc.hvmDiffEnforceable.Load() {
		return nil
	}
	if e := vm.CheckBTCHeaderBatchPoWForNetwork(bc.tbcHeaderNodeConfig.Network, batch); e != nil {
		return e
	}
	return vm.ValidateBTCHeaderBatchForNetwork(bc.ctx, bc.tbcHeaderNode,
		bc.tbcHeaderNodeConfig.Network, bc.tbcHeaderNodeConfig.GenesisHeightOffset, batch)
}

func (bc *BlockChain) getBitcoinAttributesForNextBlock(timestamp uint64) (*types.BtcAttributesDepositedTx, error) {
	// Lock the chain mutex - all other code that modifies lightweight TBC node respects this mutex
	// and locking this resource ensures that we can safely modify the lightweight TBC node to ensure
	// the new Bitcoin Attributes Deposited transaction we generate can be successfully applied
	// when it occurs in a block for real, and also to ensure the canonical tip we report matches what
	// canonical tip lightweight TBC will report after the specified headers are added.
	if !bc.chainmu.TryLock() {
		return nil, errChainStopped
	}
	defer bc.chainmu.Unlock()

	// Don't generate a Bitcoin Attributes deposited transaction unless we're building for a recent block.
	// Only FUTURE skew gates: a past-timestamped block (e.g. sequencer catch-up after downtime, where L2
	// timestamps lag wall clock) must still generate the BtcAttr tx. The ordered compare avoids the uint64
	// underflow that `timestamp - now` would hit when timestamp < now (which would wrongly drop the tx and
	// stall the BTC view for the whole catch-up window). XXX: Move this upstream?
	now := uint64(time.Now().Unix())
	if btcAttrFutureSkewExceeded(timestamp, now) {
		// No error, but no BTC Attributes Dep tx
		return nil, nil
	}

	if !bc.hvmEnabled {
		// hVM not enabled, nothing to do
		return nil, nil
	}

	if !bc.chainConfig.IsHvm0(timestamp) {
		// hVM enabled but not yet at activation time, nothing to do
		return nil, nil
	}

	if bc.isAwaitingHvmSnapSync() {
		// During an hVM snap sync the lightweight TBC view is owned and rebuilt by SnapSyncHvm (via
		// AddExternalHeaders), so reading its BTC tip here would race that rebuild and could emit a BtcAttr
		// tx bound to a half-restored view. Skip the (optional) BtcAttr tx for this block and retry on the
		// next; the post-snap catch-up re-syncs the view. Mirrors the same latch gate in ProcessBlock and
		// updateHvmHeaderConsensus. Safe under chainmu: chainmu->hvmSnapMu is the established lock order
		// (ProcessBlock takes this latch under chainmu too), and hvmSnapMu is a leaf.
		return nil, nil
	}

	lastTip := bc.CurrentBlock()
	if lastTip == nil {
		// Build path: returning an error makes the sequencer skip the (optional) Bitcoin
		// Attributes Deposited tx for this block and retry on the next one, rather than
		// crashing the process. No lightweight-TBC state has been mutated at this point.
		return nil, errors.New("unable to generate the Bitcoin Attributes Deposited transaction, " +
			"as the current EVM tip is unknown")
	}
	lastTipHash := lastTip.Hash()

	log.Info(fmt.Sprintf("Generating Bitcoin Attributes Deposited transaction for a new block with timestamp "+
		"%d on top of prior block %s @ %d", timestamp, lastTip.Hash().String(), lastTip.Number.Uint64()))

	// NOTE: the build cache is checked BELOW, after the lightweight + full-node BTC tips are read, because
	// the cache key includes both (a BTC-view change must invalidate the entry — see btcAttrCacheKey).

	// Sanity check: lightweight TBC node's state should always reflect lastTip when this is called.
	// If it doesn't, log the error and manually move the lightweight node to represent the current
	// tip so we can return valid data.
	currentTbcEvmTip, err := bc.getHeaderModeTBCEVMHeader()
	if err != nil {
		// Read-only lookup; no lightweight-TBC mutation yet. Skip this block's BtcAttr tx
		// and retry on the next, rather than crashing the sequencer.
		return nil, fmt.Errorf("unable to get the EVM block that lightweight TBC's state represents "+
			"while trying to generate a Bitcoin Attributes Deposited transaction for the next block after "+
			"%s @ %d: %w", lastTip.Hash().String(), lastTip.Number.Uint64(), err)
	}
	if currentTbcEvmTip != nil {
		if currentTbcEvmTip.Hash().Cmp(lastTip.Hash()) != 0 {
			log.Error(fmt.Sprintf("When attempting to generate a Bitcoin Attributes Deposited transaction "+
				"for the next block after %s @ %d, lightweight TBC represents an incorrect EVM state of %s @ %d",
				lastTip.Hash().String(), lastTip.Number.Uint64(), currentTbcEvmTip.Hash().String(),
				currentTbcEvmTip.Number.Uint64()))

			// Attempting to generate Bitcoin Attributes Deposited transaction for the block after current tip
			// but lightweight TBC's state isn't at the current tip, move it here manually
			err := bc.updateHvmHeaderConsensus(lastTip, false)
			if err != nil {
				// The lightweight TBC was already at the wrong EVM state on entry (that is why
				// we are here); a failed recovery move leaves it in an indeterminate state that
				// the next call's same sanity check (above) will detect and re-attempt to repair.
				// Return so the sequencer skips this block's BtcAttr tx and retries, instead of
				// crashing on this *returned* error.
				//
				// updateHvmHeaderConsensus may still terminate via log.Crit on a should-never-happen
				// internal condition before it returns an error here, so this recovery branch only converts
				// the returned error. Reaching this branch at
				// all requires the lightweight TBC to already be at the wrong EVM tip relative to the
				// canonical head, which the steady-state build path avoids (each head promotion re-syncs
				// the lightweight view); it is only transiently reachable on recovery/import paths.
				return nil, fmt.Errorf("when attempting to generate a Bitcoin Attributes Deposited transaction "+
					"for the next block after %s @ %d, lightweight TBC represented an incorrect EVM state of %s @ %d "+
					"and an error occurred trying to move its EVM state: %w",
					lastTip.Hash().String(), lastTip.Number.Uint64(), currentTbcEvmTip.Hash().String(),
					currentTbcEvmTip.Number.Uint64(), err)
			}
		} else {
			log.Info(fmt.Sprintf("Lightweight TBC correctly represents block %s @ %d when attempting to "+
				"generate a Bitcoin Attributes Deposited transaction for the next block",
				currentTbcEvmTip.Hash().String(), currentTbcEvmTip.Number.Uint64()))
		}
	} else {
		log.Info(fmt.Sprintf("The EVM block corresponding to lightweight TBC's current state is nil, "+
			"which should indicate that the next block after %s @ %d at time %d is the hVM Phase 0 "+
			"activation block", lastTip.Hash().String(), lastTip.Number.Uint64(), timestamp))
	}

	originalTbcUpstreamId, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil {
		// Read-only lookup; no lightweight-TBC mutation yet. Skip and retry next block.
		return nil, fmt.Errorf("unable to get the upstream state id from TBC when creating the Bitcoin "+
			"Attributes Deposited transaction for the block after %s @ %d: %w", lastTip.Hash().String(),
			lastTip.Number.Uint64(), err)
	}

	// Get current tips known by our lightweight and full TBC nodes
	lightTipHeight, lightTipHeader, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		// Read-only lookup; no lightweight-TBC mutation yet. Skip and retry next block.
		return nil, fmt.Errorf("unable to get the best block header from lightweight TBC node when attempting "+
			"to calculate the Bitcoin Attributes Deposited transaction for next block after %s @ %d: %w",
			lastTip.Hash().String(), lastTip.Number.Uint64(), err)
	}
	lightTipHash := lightTipHeader.BlockHash()

	fullTipHeight, fullTipHeader, err := vm.TBCFullNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		// Read-only lookup; no lightweight-TBC mutation yet. Skip and retry next block.
		return nil, fmt.Errorf("unable to get the best block header from TBC full node when attempting "+
			"to calculate the Bitcoin Attributes Deposited transaction for next block after %s @ %d: %w",
			lastTip.Hash().String(), lastTip.Number.Uint64(), err)
	}
	fullTipHash := fullTipHeader.BlockHash()

	// Build cache (BTC-view-aware). The cached BtcAttr is valid only while the EVM tip and both BTC tips
	// are unchanged (see btcAttrCacheKey). The reads above are cheap; the sanity check is read-only on a
	// hit but may have repaired a desynced lightweight view, in which case lightTipHash already reflects
	// the repaired view. The expensive work the cache saves is the walk-back + add/revert dry-run below.
	// Keying on the BTC tips (not the EVM tip alone) is what de-pins the permanent sequencer halt: a
	// full-node sync/reorg now forces a rebuild instead of re-serving a stale tx. Block availability is not
	// a key dimension: the fully-blocked case (no header available, i==0) returns errHvmBtcAttrPendingBlocked
	// without caching, so it is re-attempted. The partially-blocked case (availability truncates to a
	// non-empty prefix at i>0) does cache the truncated tx, but that is bounded and self-correcting: the
	// under-advance lasts only until the next L2 block promotes the EVM tip (new evmTip => cache miss =>
	// rebuild that re-checks availability), so a late-arriving full block is picked up on the next build.
	curCacheKey := btcAttrCacheKey{evmTip: lastTipHash, lightTip: lightTipHash, fullTip: fullTipHash}
	if cached := bc.cachedBtcAttrFor(curCacheKey); cached != nil {
		return cached, nil
	}

	// Check whether the TBC Full Node has new header information compared to lightweight TBC node.
	// Note this is looking at what block headers the TBC full node knows about, so is unrelated to
	// where the full node is indexed to.
	if bytes.Equal(lightTipHash[:], fullTipHash[:]) {
		log.Info(fmt.Sprintf("lightTipHash %s == fullTipHash %s, not generating Bitcoin Attributes Deposited "+
			"transaction", lightTipHash.String(), fullTipHash.String()))
		// Both TBC nodes have same consensus tip, nothing to do
		return nil, nil
	}

	// Tips are different - determine whether the lightweight tip is a direct ancestor.
	// Note: we aren't using existing methods for finding common ancestor, because there is an
	// edge case where lightweight consensus could know about a block header on a fork
	// which the TBC full node is not aware of, so in the event of a fork we need to walk
	// back each tip from their respective data source. This edge case could happen either
	// in a benign way when there is a Bitcoin reorg and our TBC full node only heard about
	// the canonical chain from peers, or if a malicious Sequencer intentionally privately
	// mined a Bitcoin block and included the header in a Bitcoin Attributes Deposited tx
	// in an attempt to cause an error in the hVM state transition.
	lightCursorHeader := lightTipHeader
	lightCursorHeight := lightTipHeight
	lightCursorHash := lightTipHeader.BlockHash()

	fullCursorHeader := fullTipHeader
	fullCursorHeight := fullTipHeight
	fullCursorHash := fullTipHeader.BlockHash()

	log.Info(fmt.Sprintf("Generating Bitcoin Attributes Deposited transaction for the next block after "+
		"%s @ %d, lightweight TBC node consensus tip is %s @ %d, full TBC node consensus tip is %s @ %d",
		lastTip.Hash().String(), lastTip.Number.Uint64(), lightCursorHash.String(), lightCursorHeight,
		fullCursorHash.String(), fullCursorHeight))

	// Walk back the light cursor until we get to the same height if it's ahead
	for lightCursorHeight > fullCursorHeight {
		// Get height even though we could calculate it as a sanity check
		header, height, err := bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, lightCursorHeader.PrevBlock)
		if err != nil {
			// Should never happen, implies lightweight TBC has a header before its current canonical
			// tip which it is unable to return, probably signals corruption. This walk-back is read-only
			// (no lightweight-TBC mutation), so we return and let the sequencer skip this block's BtcAttr
			// tx rather than crash. If such corruption were persistent the hVM Bitcoin view would stop
			// advancing while the L2 keeps producing blocks; the GetBitcoinAttributesForNextBlock wrapper
			// records hvmBtcAttrFailMeter / hvmBtcAttrFailingGauge as an alertable signal.
			// TODO: Lightweight TBC recovery from genesis
			return nil, fmt.Errorf("unable to get header %x @ %d from lightweight TBC node when walking "+
				"backwards from %x @ %d: %w", lightCursorHeader.PrevBlock[:], lightCursorHeight-1,
				lightCursorHash[:], lightCursorHeight, err)
		}
		if height != lightCursorHeight-1 {
			// Should never happen, means lightweight TBC node is returning bad heights
			return nil, fmt.Errorf("lightweight TBC node returned an incorrect height for block %x: "+
				"expected %d but got %d", lightCursorHeader.PrevBlock[:], lightCursorHeight-1, height)
		}
		lightCursorHeader = header
		lightCursorHeight = height // same as lightCursorHeight - 1
		lightCursorHash = lightCursorHeader.BlockHash()
	}
	// Walk back the full cursor until we get to the same height if it's ahead
	for fullCursorHeight > lightCursorHeight {
		// Get height even though we could calculate it as a sanity check
		header, height, err := vm.TBCFullNode.BlockHeaderByHash(bc.ctx, fullCursorHeader.PrevBlock)
		if err != nil {
			// Should never happen, implies full TBC node has a header before its current
			// canonical tip which it is unable to return, probably signals corruption.
			// Read-only walk-back; return and skip this block's BtcAttr tx rather than crash.
			// TODO: Full TBC node recovery?
			return nil, fmt.Errorf("unable to get header %x @ %d from full TBC node when walking "+
				"backwards from %x @ %d: %w", fullCursorHeader.PrevBlock[:], fullCursorHeight-1,
				fullCursorHash[:], fullCursorHeight, err)
		}
		if height != fullCursorHeight-1 {
			// Should never happen, means full TBC node is returning bad heights
			return nil, fmt.Errorf("full TBC node returned an incorrect height for block %x: "+
				"expected %d but got %d", fullCursorHeader.PrevBlock[:], fullCursorHeight-1, height)
		}
		fullCursorHeader = header
		fullCursorHeight = height // same as fullCursorHeight - 1
		fullCursorHash = fullCursorHeader.BlockHash()
	}

	// Whether or not lightweight and full TBC nodes are on the same chain, find their common ancestor
	// (the same as one of the node's tips if they are on the same chain) so the walk-back logic runs once
	// rather than separately for the different forking scenarios.
	var commonAncestorHash chainhash.Hash

	// Now the cursors for the lightweight and full node chains are at the same height.
	// If both cursors match, both chains' current tips are on the same chain.
	if bytes.Equal(fullCursorHash[:], lightCursorHash[:]) {
		// They match, so they are on the same chain.
		if lightTipHeight > fullTipHeight {
			// Lightweight TBC has consensus ahead of full tip, on same chain, so nothing to do. We did
			// not check this until both cursors were at the lowest height of either tip, because of an
			// edge case where the lightweight tip could have been advanced onto a fork higher than the
			// canonical block known by the full node, while the full node still has one or more canonical
			// headers that should be communicated to the lightweight view.
			return nil, nil
		} else {
			// Full TBC node has consensus ahead of lightweight tip on the
			// same chain, so we just need to provide the new headers between
			// lightweight TBC's tip and full TBC node's tip.
			commonAncestorHash = lightTipHash
			// Walk backwards from full TBC node's tip, adding all headers
			// until we get to this common ancestor.
			// We walk backwards from a known tip instead of advancing
			// by index even though we know the tip we are walking towards
			// is canonical to avoid an edge-case where the TBC full node
			// could experience a reorg deeper than the common ancestor
			// which would cause us to return headers which may not connect
			// to the ancestor we know the lightweight TBC node will be able
			// to progress on.
		}
	} else {
		// Lightweight tip isn't the common ancestor, meaning the two nodes
		// are on different chains. Need to continue walking both back
		// until we do find a common ancestor.
		// TODO: way to dedup this code with the previous walk-back to equal height,
		// could move to a walkback function where caller can specify whether height
		// or hash is used as exit condition and return all final cursors?
		for !bytes.Equal(fullCursorHash[:], lightCursorHash[:]) {
			lHeader, lHeight, err := bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, lightCursorHeader.PrevBlock)
			if err != nil {
				// Should never happen, implies lightweight TBC has a header before its current
				// canonical tip which it is unable to return, probably signals corruption.
				// Read-only walk-back; return and skip this block's BtcAttr tx rather than crash.
				// TODO: Lightweight TBC recovery from genesis
				return nil, fmt.Errorf("unable to get header %x @ %d from lightweight TBC node when walking "+
					"backwards from %x @ %d: %w", lightCursorHeader.PrevBlock[:], lightCursorHeight-1,
					lightCursorHash[:], lightCursorHeight, err)
			}
			if lHeight != lightCursorHeight-1 {
				// Should never happen, means lightweight TBC node is returning bad heights
				return nil, fmt.Errorf("lightweight TBC node returned an incorrect height for block %x: "+
					"expected %d but got %d", lightCursorHeader.PrevBlock[:], lightCursorHeight-1, lHeight)
			}

			fHeader, fHeight, err := vm.TBCFullNode.BlockHeaderByHash(bc.ctx, fullCursorHeader.PrevBlock)
			if err != nil {
				// Should never happen, implies full TBC node has a header before its current
				// canonical tip which it is unable to return, probably signals corruption.
				// Read-only walk-back; return and skip this block's BtcAttr tx rather than crash.
				// TODO: Full TBC node recovery?
				return nil, fmt.Errorf("unable to get header %x @ %d from full TBC node when walking "+
					"backwards from %x @ %d: %w", fullCursorHeader.PrevBlock[:], fullCursorHeight-1,
					fullCursorHash[:], fullCursorHeight, err)
			}
			if fHeight != fullCursorHeight-1 {
				// Should never happen, means full TBC node is returning bad heights
				return nil, fmt.Errorf("full TBC node returned an incorrect height for block %x: "+
					"expected %d but got %d", fullCursorHeader.PrevBlock[:], fullCursorHeight-1, fHeight)
			}

			lightCursorHeader = lHeader
			lightCursorHeight = lHeight
			lightCursorHash = lHeader.BlockHash()

			fullCursorHeader = fHeader
			fullCursorHeight = fHeight
			fullCursorHash = fHeader.BlockHash()
		}
		commonAncestorHash = fullCursorHash
	}

	// Whether or not the light and full TBC nodes are on the same chain, we
	// have their common ancestor so any headers from the TBC full node which
	// connect to this ancestor are guaranteed to fit onto lightweight TBC's
	// current knowledge.
	commonAncestorHeight := fullCursorHeight // Both former cursors are ancestor now

	log.Info(fmt.Sprintf("When generating the Bitcoin Attributes Deposited transaction for the next block "+
		"after %s @ %d, the common ancestor between lightweight TBC tip %x @ %d and full node TBC tip %x @ %d is "+
		"%x @ %d", lastTip.Hash().String(), lastTip.Number.Uint64(), lightTipHash[:], lightTipHeight,
		fullTipHash[:], fullTipHeight, commonAncestorHash[:], commonAncestorHeight))

	// # of headers will always be the full tip minus the height of the common ancestor
	var headersToTip []wire.BlockHeader
	cursorHeight := fullTipHeight
	cursorHeader := fullTipHeader
	cursorHash := fullTipHash

	// Loop until cursor's hash matches the common ancestor
	for !bytes.Equal(commonAncestorHash[:], cursorHash[:]) {
		headersToTip = append(headersToTip, *cursorHeader)
		tHeader, tHeight, err := vm.TBCFullNode.BlockHeaderByHash(bc.ctx, cursorHeader.PrevBlock)
		if err != nil {
			// Should never happen, implies full TBC node has a header before its current
			// canonical tip which it is unable to return, probably signals corruption.
			// Read-only walk-back; return and skip this block's BtcAttr tx rather than crash.
			// TODO: Full TBC node recovery?
			return nil, fmt.Errorf("unable to get header %x @ %d from full TBC node when walking "+
				"backwards from %x @ %d: %w", cursorHeader.PrevBlock[:], cursorHeight-1,
				cursorHash[:], cursorHeight, err)
		}
		if tHeight != cursorHeight-1 {
			// Should never happen, means full TBC node is returning bad heights
			return nil, fmt.Errorf("full TBC node returned an incorrect height for block %x: "+
				"expected %d but got %d", cursorHeader.PrevBlock[:], cursorHeight-1, tHeight)
		}
		cursorHeader = tHeader
		cursorHeight = tHeight
		cursorHash = tHeader.BlockHash()
	}

	if headersToTip == nil || len(headersToTip) == 0 {
		// Sanity check just in case, this should never happen because the only way this array
		// is empty should be if lightweight and full node tips are the same
		log.Error(fmt.Sprintf("When generating Bitcoin Attributes Deposited transaction for block after "+
			"%s @ %d got past checks for whether any new headers are available from TBC full node that should be "+
			"communicated to TBC light mode, but did not find any headers to add. Common ancestor: %x",
			lastTip.Hash().String(), lastTip.Number.Uint64(), commonAncestorHash[:]))
		// We already established the tips differ (pending work), so producing no headers here is
		// an anomaly, not an idle cycle — surface it as a stuck signal rather than "healthy".
		return nil, errHvmBtcAttrPendingBlocked
	}

	var headersToAdd []wire.BlockHeader
	// Check that none of the headers we are going to add are already known by lightweight TBC. This is
	// possible in an edge case where we are communicating a reorg, as lightweight TBC could know some
	// blocks on the fork since the common ancestor which we did not yet check for. Note headersToTip is
	// in reverse order, so this loop visits headers in ascending (ancestor->tip) order.
	//
	// Contiguity guarantee (keeps the kept AddExternalHeaders fail-stop unreachable on clean input): TBC
	// cannot hold a header without its parent, so the set of headers the lightweight node already knows
	// along this single ancestor->tip chain is downward-closed (a prefix). Dropping a prefix from an
	// ascending contiguous chain leaves a contiguous suffix, so headersToAdd is always contiguous. The
	// contiguity check that would otherwise reject a gap runs at the header store's service layer, not the
	// underlying DB write, so this invariant must be guaranteed here rather than relied upon downstream.
	// The NotFound discrimination below preserves it.
	for index := len(headersToTip) - 1; index >= 0; index-- {
		headerToAdd := headersToTip[index]
		headerToAddHash := headerToAdd.BlockHash()
		_, _, err := bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, headerToAddHash)
		if err != nil {
			// Only a genuine "not found" means lightweight TBC does not already know this header (so it
			// must be added). Discriminate it from a real backend/I/O error via errors.As against
			// database.NotFoundError: the header store's lookup returns the NotFoundError
			// unwrapped on a miss, and the store wrapper adds only a "db block header by hash: %w"
			// wrap, which errors.As traverses (the sibling "block header get: %w" string is the leveldb
			// I/O-error path, intentionally not a NotFoundError, so it does not match here).
			//
			// This matters: treating a transient read failure as "not found" and appending an
			// already-known interior header would make headersToAdd non-contiguous; the AddExternalHeaders
			// service wrapper's contiguity check then rejects it, hitting the deliberately-kept fail-stop
			// log.Crit and crashing the sequencer over a transient read (or, past it, the DB-layer insert
			// would write wrong heights). This loop is still read-only (no lightweight-TBC mutation), so on
			// any non-not-found error we skip this block's BtcAttr tx and retry next block.
			var notFound database.NotFoundError
			if errors.As(err, &notFound) {
				headersToAdd = append(headersToAdd, headerToAdd)
			} else {
				return nil, fmt.Errorf("unable to check whether lightweight TBC already knows header %s "+
					"while generating the Bitcoin Attributes Deposited transaction for the block after %s @ %d: %w",
					headerToAddHash.String(), lastTip.Hash().String(), lastTip.Number.Uint64(), err)
			}
		}
	}

	// It's possible that all headers were already known by lightweight TBC if it is
	// fully aware of the alternate chain in a chain-split scenario.
	if len(headersToAdd) == 0 {
		log.Info("No headers to add found!")
		return nil, nil
	}

	// Trim headersToAdd to the maximum number of headers we are allowed to include.
	// if len(headersToAdd) > types.MaximumBtcHeadersInTx {
	// 	headersToAdd = headersToAdd[0:types.MaximumBtcHeadersInTx]
	// }
	if len(headersToAdd) > 8 {
		headersToAdd = headersToAdd[0:8]
	}
	log.Info(fmt.Sprintf("Headers to add while generating Bitcoin Attributes Deposited transaction: %x", headersToAdd))

	// Walk up headersToAdd, and truncate blocks that TBC Full Node does not have complete information for
	for i := 0; i < len(headersToAdd); i++ {
		hashToCheck := headersToAdd[i].BlockHash()
		headerAvailable, err := vm.TBCFullNode.FullBlockAvailable(bc.ctx, hashToCheck)
		if err != nil {
			// A backend/I-O fault while determining availability is a genuine failure, distinct
			// from a full block that is simply not downloaded yet (handled below as a
			// non-advancing "blocked" round). Return it as a real error so it is counted on the
			// fail meter like every other backend read in this function, consistent rather than
			// being silently downgraded to "not available". Still read-only here, so the
			// sequencer just skips this block's BtcAttr tx and retries.
			return nil, fmt.Errorf("TBC full node was unable to determine whether the full block for hash %s "+
				"is available while generating the Bitcoin Attributes Deposited transaction for the block after "+
				"%s @ %d: %w", hashToCheck.String(), lastTip.Hash().String(), lastTip.Number.Uint64(), err)
		}

		if !headerAvailable {
			log.Warn(fmt.Sprintf("TBC does not have full block available for %s!", hashToCheck.String()))
			vm.TBCAttemptBlockRefetch(bc.ctx, &headersToAdd[i])

			// Header is not available; if this is the first block then return nothing, otherwise truncate
			if i == 0 {
				// No blocks to add this round: the full node has the consensus header but not yet
				// the full block for it (a refetch was just kicked off). The tips differ, so this
				// is pending work that did not advance — surface it as a stuck signal (the wrapper
				// hides the sentinel and the caller still builds a valid block without the tx).
				return nil, errHvmBtcAttrPendingBlocked
			} else {
				log.Info(fmt.Sprintf("Generating Bitcoin Attributes Deposited transaction for the next block "+
					"after %s @ %d, and TBC Full Node does not have the full block for %s, so removing from headers "+
					"to add to hVM's lightweight view", lastTip.Hash().String(), lastTip.Number.Uint64(),
					hashToCheck.String()))

				headersToAdd = headersToAdd[0:i]
				break
			}
		}
	}

	// Contextual-difficulty (sequencer build path — liveness). Before the dry-run AddExternalHeaders below mutates the
	// shared lightweight view, validate the candidate headers with the same floor-aware
	// contextual-difficulty validator the consensus apply path runs, and truncate to the longest prefix
	// the apply path will accept. This keeps the sequencer from packaging a header the apply path would
	// reject (which would leave the BTC view unable to advance); truncating advances the view by the
	// acceptable prefix instead. Build-path only, changes nothing about consensus: every validator
	// re-derives the BtcAttr independently and the apply path remains the enforcement point.
	headerPtrsForValidation := make([]*wire.BlockHeader, len(headersToAdd))
	for i := range headersToAdd {
		headerPtrsForValidation[i] = &headersToAdd[i]
	}
	validPrefix, skipBuild, verr := longestEnforceableBTCHeaderPrefix(headerPtrsForValidation, bc.enforceableBTCBatch)
	if skipBuild {
		// The validator collapses any read error into ErrBTCHeaderContextUnavailable, losing the underlying
		// identity. Defensive guard: if the chain's context has been cancelled, surface that (so
		// recordHvmBtcAttrResult classifies it as cancellation, not a backend fault). On a normal cmd/geth
		// shutdown bc.ctx is not cancelled (see the SnapSyncHvm note) and shutdown is caught instead by the
		// chainmu TryLock at the top of this function (errChainStopped), so this guard is not load-bearing
		// for cmd/geth today; it handles a parent-context cancellation (embeddings / tests) and future-proofs
		// the function if a future change wires Stop() to cancel bc.ctx.
		if ctxErr := bc.ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// Otherwise a genuine transient/corrupt lightweight-TBC read; no mutation yet. Skip this block's
		// BtcAttr tx and retry next build (surfaced on the fail meter like the other read faults here).
		return nil, fmt.Errorf("contextual BTC-difficulty validation hit a transient/unreadable lightweight-TBC "+
			"error while generating the Bitcoin Attributes Deposited transaction for the block after %s @ %d: %w",
			lastTip.Hash().String(), lastTip.Number.Uint64(), verr)
	}
	if len(validPrefix) < len(headersToAdd) {
		// Distinguish a contextually-invalid header (this meter) from a benign undownloaded-block stall:
		// both otherwise surface only as the shared stuck gauge, leaving operators without a distinct signal.
		// A sustained rate here => the full node is feeding contextually-invalid BTC headers.
		hvmBtcAttrDiffTruncMeter.Mark(1)
		if len(validPrefix) == 0 {
			// The very next BTC header the full node wants to communicate is contextually invalid (a
			// forged-difficulty header the apply path would reject). We cannot advance the hVM Bitcoin
			// view this round; build a block without a BtcAttr tx and retry. The stuck gauge (via the
			// pending-blocked sentinel) plus the truncation meter above make a persistent forged-base
			// stall alertable and distinguishable from a benign availability stall.
			log.Warn(fmt.Sprintf("Generating Bitcoin Attributes Deposited transaction for the block after %s @ %d: "+
				"the next BTC header %s is contextually invalid under contextual-difficulty enforcement (wrong difficulty / median-time-past / "+
				"version); not advancing the hVM Bitcoin view this build", lastTip.Hash().String(),
				lastTip.Number.Uint64(), headersToAdd[0].BlockHash().String()))
			return nil, errHvmBtcAttrPendingBlocked
		}
		log.Warn(fmt.Sprintf("Generating Bitcoin Attributes Deposited transaction for the block after %s @ %d: "+
			"truncating %d candidate BTC headers to the %d-header prefix that passes contextual-difficulty "+
			"validation (first rejected header %s)", lastTip.Hash().String(), lastTip.Number.Uint64(),
			len(headersToAdd), len(validPrefix), headersToAdd[len(validPrefix)].BlockHash().String()))
		headersToAdd = headersToAdd[:len(validPrefix)]
	}

	// Serialize headers to bytes
	headersToAddSerialized, err := types.SerializeHeadersToArray(headersToAdd)
	if err != nil {
		// Pure serialization, before any lightweight-TBC mutation. Skip and retry next block.
		return nil, fmt.Errorf("unable to serialize Bitcoin headers to create Bitcoin Attributes Deposited "+
			"transaction for the block after %s @ %d: %w", lastTip.Hash().String(), lastTip.Number.Uint64(), err)
	}

	// Add the headers to lightweight TBC's view to make sure they are valid, and also to
	// determine the correct new canonical tip (which won't be the last header in this array
	// if we are adding knowledge to a fork that doesn't become canonical). That is possible
	// if there is a split tip or if we are handling a BTC reorg that is more than
	// MaximumBtcHeadersInTx deep and requires multiple Bitcoin Attributes Deposited transactions
	// to communicate enough headers for it to be considered canonical by our lightweight view.

	// Convert []wire.BlockHeader to []*wire.BlockHeader
	// TODO: Review this, should we use []*wire.BlockHeader the entire time to be consistent?
	headersToAddPtr := make([]*wire.BlockHeader, len(headersToAdd))
	for i := 0; i < len(headersToAdd); i++ {
		headersToAddPtr[i] = &headersToAdd[i]
	}

	msgHeaders := &wire.MsgHeaders{
		Headers: headersToAddPtr,
	}

	log.Info(fmt.Sprintf("[Bitcoin Attributes for Next Block] *ADDING* external BTC headers:"))
	for i := 0; i < len(msgHeaders.Headers); i++ {
		log.Info(fmt.Sprintf("\t %s", msgHeaders.Headers[i].BlockHash().String()))
	}
	// Contextual-difficulty: headersToAdd was validated and truncated above to the longest prefix the apply path accepts
	// under contextual-difficulty enforcement, so this dry-run add cannot package a self-invalidating
	// header. The dry-run add itself remains the cumulative-work / canonical-tip oracle; the contextual-difficulty check only
	// narrows which headers reach it.
	_, canonical, _, _, err := bc.tbcHeaderNode.AddExternalHeaders(
		bc.ctx,
		msgHeaders,
		hVMDummyUpstreamId[:])

	if err != nil {
		// Fail-stop (one of the two deliberate crits in this function). This is the dry-run mutation of
		// the shared lightweight TBC node. The underlying store write is not atomic: a failure during the
		// write/commit phase can leave the canonical tip advanced onto the dry-run headers while other
		// records are not — a partially-mutated, dirty lightweight view. We cannot distinguish that from a clean pre-write
		// rejection here, and on a graceful return we would never run RemoveExternalHeaders to restore it,
		// so every later block build would silently compute its BTC view from a corrupted tip and diverge
		// consensus. Crashing stops this process from building further on the dirty state. It does not by
		// itself clean up: the lightweight TBC persists to leveldb, so the partial write may survive a
		// restart; restart-time recovery is out of scope here.
		first := headersToAdd[0].BlockHash()
		last := headersToAdd[len(headersToAdd)-1].BlockHash()
		log.Crit(fmt.Sprintf("Unable to add %d external headers %x to %x to lightweight TBC view on top "+
			"of prior canonical tip %x @ %d!", len(headersToAdd), first[:], last[:], lightTipHash[:],
			lightTipHeight), "err", err)
	}

	log.Info(fmt.Sprintf("[Bitcoin Attributes for Next Block] *REMOVING* external BTC headers:"))
	for i := 0; i < len(msgHeaders.Headers); i++ {
		log.Info(fmt.Sprintf("\t %s", msgHeaders.Headers[i].BlockHash().String()))
	}

	// Revert lightweight TBC's view back to what it was before we started.
	rt, prevHeader, err := bc.tbcHeaderNode.RemoveExternalHeaders(bc.ctx, msgHeaders, lightTipHeader, originalTbcUpstreamId[:])
	if err != nil {
		// Fail-stop (the second of the two deliberate crits). AddExternalHeaders above already persisted
		// the dry-run headers. This is the revert; like the insert it commits across multiple independent
		// (non-atomic) store transactions, so a failure here can leave the
		// canonical tip and/or upstream state id only partially restored — a dirty lightweight view. We
		// have no clean way to retry, and returning would let every subsequent block build silently
		// compute its BTC view from a corrupted tip, diverging consensus. Crashing stops this process from
		// building on the dirty state. (Same caveat as the Add crit: the dirty leveldb may survive a
		// restart; restart-time recovery is out of scope.) Should never happen: Remove mirrors an Add that
		// just succeeded on the same headers.
		first := headersToAdd[0].BlockHash()
		last := headersToAdd[len(headersToAdd)-1].BlockHash()
		log.Crit(fmt.Sprintf("Unable to remove %d external headers %x to %x from lightweight TBC view after "+
			"they were temporarily added when creating the Bitcoin Attributes Deposited transaction for the block "+
			"after %s @ %d", len(headersToAdd), first[:], last[:], lastTip.Hash().String(),
			lastTip.Number.Uint64()), "err", err)
	}

	log.Info(fmt.Sprintf("Successfully removed %d block headers from lightweight TBC view after temporarily "+
		"adding them when generating the Bitcoin Attributes Deposited transaction for the block after %s @ %d. "+
		"RemoveType=%d, prevHeader=%x", len(*headersToAddSerialized), lastTip.Hash().String(), lastTip.Number.Uint64(),
		rt, prevHeader.Hash[:]))

	// No post-revert assertion here, by design. Asserting on the returned value (prevHeader) would be
	// wrong: RemoveExternalHeaders returns parentToRemovalSet (the parent of the first removed header),
	// which equals the original light tip only on the same-chain case — in a fork/reorg it is the common
	// ancestor — so `prevHeader.Hash == lightTipHash` would false-positive and crash on normal reorg
	// handling. A re-read via BlockHeaderBest() compared to lightTipHash would be correct (the canonical
	// tip is written unconditionally to tipAfterRemoval==lightTipHeader in all geometries) but is
	// redundant: a nil error from RemoveExternalHeaders already implies its canonical-tip commit landed,
	// and the emitted tx uses `canonical` from AddExternalHeaders, not the post-revert state.
	canonHashAfterDepTx := canonical.BlockHash()
	btcAttrDepTx, err := types.MakeBtcAttributesDepositedTx(canonHashAfterDepTx, headersToAdd)
	if err != nil {
		// The lightweight TBC view has already been reverted to its original state above, so
		// returning here leaves no dirty state. Skip this block's BtcAttr tx and retry next block.
		return nil, fmt.Errorf("unable to construct a Bitcoin Attributes Deposited tx containing %d headers "+
			"with canonical hash %x for placement in the block after %s @ %d: %w", len(headersToAdd),
			canonHashAfterDepTx[:], lastTip.Hash().String(), lastTip.Number.Uint64(), err)
	}

	// Store the calculated Bitcoin Attributes Deposited transaction so we don't recalculate it on
	// subsequent calls — keyed on the EVM tip and both BTC views (curCacheKey), so any BTC-view change
	// invalidates it. lightTipHash/fullTipHash are the pre-build values read above and are unchanged here
	// (the dry-run add was reverted), so they correctly identify the view this tx extends.
	bc.storeBtcAttrCache(curCacheKey, btcAttrDepTx)

	return btcAttrDepTx, nil
}

// headersBetweenBlocks returns an array of headers from ancestor (inclusive) to head (inclusive).
// This function requires that ancestor is an ancestor of head; if the ancestor cannot be found by
// walking backwards from the head an error will be thrown.
// This function does not depend on canonical indexes, so it can safely be used to find the route
// to walk forward from an ancestor to its descendant whether or not some or all of the headers
// on the route are canonical, as long as all of the block headers exist in the database.
// Headers are returned in ascending order: [ancestor, ..., head]
func (bc *BlockChain) headersBetweenBlocks(ancestor *Header, head *Header) ([]*Header, error) {
	if ancestor == nil {
		return nil, fmt.Errorf("headersBetweenBlocks called with nil ancestor")
	}
	if head == nil {
		return nil, fmt.Errorf("headersBetweenBlocks called with nil head")
	}

	headIndex := head.Number.Uint64()
	ancestorIndex := ancestor.Number.Uint64()
	path := make([]*Header, headIndex-ancestorIndex+1)

	cursor := head
	path[headIndex-ancestorIndex] = cursor
	for index := int32(headIndex - ancestorIndex - 1); index >= 0; index-- {
		// Don't overwrite cursor so we can print error correctly
		cursorTmp := bc.getHeaderFromDiskOrHoldingPen(cursor.ParentHash)
		if cursorTmp == nil {
			return nil, fmt.Errorf("headersBetweenBlocks could not retrieve header %s @ %d",
				cursor.ParentHash.String(), cursor.Number.Uint64()-1)
		}
		path[index] = cursorTmp
		cursor = cursorTmp
	}

	return path, nil
}

func (bc *BlockChain) walkHvmHeaderConsensusForward(currentHead *Header, newHead *Header) error {
	// Can't walk forwards from a block that is the same height or higher than the destination
	if currentHead.Number.Uint64() >= newHead.Number.Uint64() {
		return fmt.Errorf("Cannot walk hVM consensus forewards from "+
			"%s @ %d to %s @ %d - bad geometry", currentHead.Hash().String(), currentHead.Number.Uint64(),
			newHead.Hash().String(), newHead.Number.Uint64())
	}

	// It may be unsafe to walk forwards by number in case this method is called
	// before the appropriate canonical chain is fully updated in the database
	// (meaning walking forward could return blocks that aren't between the
	// current and new head), so walk backwards from newHead until we get to
	// currentHead, and then walk forwards through the list.
	headers, err := bc.headersBetweenBlocks(currentHead, newHead)
	if err != nil {
		// Critical error, this indicates that a path between the block responsible
		// for hVM's latest state update and the new head we are setting cannot
		// be found.
		// TODO: Attempt to recover hVM state from genesis
		return fmt.Errorf("unable to find a path between hVM's latest state update block %s @ %d and "+
			"the new head %s @ %d", currentHead.Hash().String(), currentHead.Number.Uint64(),
			newHead.Hash().String(), newHead.Number.Uint64())
	}

	// Start at 1 to skip the currentHead which has been processed previously
	for index := 1; index < len(headers); index++ {
		err := bc.applyHvmHeaderConsensusUpdate(headers[index], true, true)
		if err != nil {
			if errors.Is(err, consensus.ErrInvalidHVMBlockFormat) || errors.Is(err, consensus.ErrInvalidHVMHeaders) {
				// Something is wrong with the block, report it as invalid
				badBlock := bc.getBlockFromDiskOrHoldingPen(headers[index].Hash())
				bc.reportBlock(badBlock, nil, err)
				for backIndex := index - 1; backIndex >= 1; backIndex-- {
					// Walk backwards to restore state to where it was originally. Unapply the
					// already-applied predecessor at backIndex, not the failing block headers[index]: the
					// failed apply committed no TBC state (every ErrInvalidHVMHeaders/ErrInvalidHVMBlockFormat
					// return path above leaves the upstream state id at the parent, and the
					// canonical-tip-mismatch path even removes the headers it added), so headers[index]
					// itself must not be unwound. Only the successfully-applied headers[index-1]..headers[1]
					// are rolled back, in reverse order (mirroring walkHvmHeaderConsensusBack). Unwinding the
					// constant headers[index] each iteration instead would repeatedly "unapply" the failed
					// block while leaving the real predecessors applied, corrupting the lightweight view.
					// headers[0] is currentHead (applied by a previous call) and is preserved by
					// backIndex >= 1.
					err := bc.unapplyHvmHeaderConsensusUpdate(headers[backIndex])
					if err != nil {
						if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
							// A torn-store condition surfaced mid-rollback (an orphaned block/header body).
							// Route it to recovery instead of halting, mirroring walkHvmHeaderConsensusBack
							// and the updateHvmHeaderConsensus dispatch: the caller rebuilds the lightweight
							// view from genesis, which supersedes this partial rollback. Effectively
							// unreachable on honest data (these predecessors were just applied in this same
							// call, so their bodies are present), but kept consistent for defense-in-depth.
							return err
						}
						// Unable to walk consensus updates we just performed backwards, critical
						log.Crit(fmt.Sprintf("Unable to undo hVM consensus updates when an error was encountered "+
							"walking from %s @ %d to %s @ %d and block %s @ %d was deemed invalid",
							currentHead.Hash().String(), currentHead.Number.Uint64(), newHead.Hash().String(),
							newHead.Number.Uint64(), badBlock.Hash().String(), badBlock.Number().Uint64()),
							"err", err)
					}
				}
				// Return the original error
				return err
			} else if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				return err
			} else {
				// Unhandled error, for now exit on critical
				log.Crit(fmt.Sprintf("Unhandled error occurred while walking hVM consensus forward from "+
					"%s @ %d to %s @ %d, the hVM state transition for block %s @ %d could not be handled",
					currentHead.Hash().String(), currentHead.Number.Uint64(), newHead.Hash().String(),
					newHead.Number.Uint64(), headers[index].Hash().String(), headers[index].Number.Uint64()), "err", err)
			}
			// Impossible to reach this, but placeholder to ensure error gets returned if logic for unhandled errors
			// is modified.
			return err
		}
	}

	return nil
}

func (bc *BlockChain) walkHvmHeaderConsensusBack(currentHead *Header, newHead *Header) error {
	// Can't walk backwards from a block that is the same height or lower than the destination
	if currentHead.Number.Uint64() <= newHead.Number.Uint64() {
		log.Error(fmt.Sprintf("Cannot walk hVM consensus backwards from "+
			"%s @ %d to %s @ %d - bad geometry", currentHead.Hash().String(), currentHead.Number.Uint64(),
			newHead.Hash().String(), newHead.Number.Uint64()))
		return consensus.ErrBadTraversalGeometry
	}

	log.Info(fmt.Sprintf("walkHvmHeaderConsensusBack called to walk backwards from %s @ %d to %s @ %d",
		currentHead.Hash().String(), currentHead.Number.Uint64(), newHead.Hash().String(), newHead.Number.Uint64()))

	cursor := currentHead
	// Loop walking back the cursor until the cursor points to the newHead, since
	// newHead is the ancestor and once we unapply the hVM state transition from
	// newHead's direct child TBC's state will be reverted to the appropriate state.
	for cursor.Hash().Cmp(newHead.Hash()) != 0 {
		if cursor.Number.Uint64() == newHead.Number.Uint64() {
			// Should be impossible, this indicates that newHead is not actually
			// a direct ancestor of currentHead and our common ancestor is incorrect
			log.Error(fmt.Sprintf("walking backwards from block %s @ %d, reached block %s @ %d but "+
				"was expecting the block at index %d to be %s which is the new head we are unwinding to",
				currentHead.Hash().String(), currentHead.Number.Uint64(), cursor.Hash().String(),
				cursor.Number.Uint64(), cursor.Number.Uint64(), newHead.Hash().String()))
			return consensus.ErrBadTraversalGeometry
		}

		err := bc.unapplyHvmHeaderConsensusUpdate(cursor)
		if err != nil {
			// If we are unable to apply a previously-applied consensus update, this is critical unless
			// due to a data corruption issue which can be recovered from
			if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				return err
			} else {
				log.Crit(fmt.Sprintf("Unable to unapply the hVM header %s @ %d",
					cursor.Hash().String(), cursor.Number.Uint64()), "err", err)
			}
		}
		newCursor := bc.getHeaderFromDiskOrHoldingPen(cursor.ParentHash)
		if newCursor == nil {
			// The ancestor header should have existed (it was already applied), so its absence is a torn-store
			// condition (a rewind/deep-reorg orphaned it), not a bad block. Mirror the orphaned-store guards
			// elsewhere in this file and return the recoverable sentinel so the caller rebuilds from genesis
			// via recoverReapplyHvmState rather than halting the process; effectively unreachable on honest
			// data with intact bodies, and every node would hit it identically (no fleet split).
			log.Error(fmt.Sprintf("header for block %s @ %d to walk hVM consensus back is nil; treating as corrupt hVM state",
				cursor.ParentHash.String(), cursor.Number.Uint64()-1))
			return consensus.ErrCorruptHVMHeaderOnlyModeState
		}
		cursor = newCursor
	}

	// We expect hVM to have an upstream state id corresponding to newHead, sanity check it
	upstreamStateId, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(upstreamStateId[:], newHead.Hash().Bytes()[:]) {
		// If we didn't get an error but the upstream state ID is not what we expect, exit as there is
		// likely a code bug (rather than a data corruption issue that could be repaired).
		log.Crit(fmt.Sprintf("after walking backwards from block %s @ %d to %s @ %d, expected TBC "+
			"upstream state id to be %s but got %x instead", currentHead.Hash().String(), currentHead.Number.Uint64(),
			newHead.Hash().String(), newHead.Number.Uint64(), newHead.Hash().String(), upstreamStateId[:]))
	}

	return nil
}

func (bc *BlockChain) calculateHvmIndexerTipLagTestnet3(cursorHeader *wire.BlockHeader, cursorHeight uint64, defaultTipLag uint64) (uint64, error) {
	lowDiffThreshold := blockchain.CalcWork(testnet3LowDiffThresholdForTipLag)

	tipDiff := blockchain.CalcWork(cursorHeader.Bits)
	if tipDiff.Cmp(lowDiffThreshold) <= 0 {
		tipTimestamp := cursorHeader.Timestamp.Unix()
		prevTipDiffLow := false
		var prevHeader *wire.BlockHeader
		if cursorHeight > bc.tbcHeaderNodeConfig.GenesisHeightOffset {
			// Grab previous block's difficulty.
			var err error
			prevHeader, _, err = bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, cursorHeader.PrevBlock)
			if err != nil {
				return 0, err
			}

			prevHeaderDiff := blockchain.CalcWork(prevHeader.Bits)
			if prevHeaderDiff.Cmp(lowDiffThreshold) <= 0 {
				prevTipDiffLow = true
			}
		}

		// If there are two blocks in a row with too low of a difficulty, set higher lag
		if prevTipDiffLow {
			// We are in a difficulty bomb scenario.
			// Generally we want to lag 100 blocks behind tip here to be safe against reorgs, but we don't
			// want to artificially index backwards before the start of the difficulty bomb, so walk backwards
			// in Bitcoin chain until we are either 100 blocks back, *or* we hit a block above the low
			// difficulty threshold.

			lookbackCursor := prevHeader

			// We are starting two blocks behind cursor (looking at previous-to-previous)
			// This is not based on the setting of hVMIndexerTipLag, but rather how far back we must be starting
			// in the walk-back to find the beginning of the difficulty bomb (if recent)
			effectiveHVMIndexerTipLag := defaultTipLag

			for lookback := int64(effectiveHVMIndexerTipLag); lookback <= 100; lookback++ {
				lookbackHeight := int64(cursorHeight) - lookback
				if lookbackHeight < 0 {
					// Can't look before the genesis block
					break
				}

				if lookbackHeight < int64(bc.tbcHeaderNodeConfig.GenesisHeightOffset) {
					log.Info(fmt.Sprintf("lookbackHeight of %d takes us lower than GenesisHeightOffset %d, "+
						"not looking further back", lookbackHeight, bc.tbcHeaderNodeConfig.GenesisHeightOffset))
					break
				}

				tempCursor, _, err := bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, lookbackCursor.PrevBlock)
				if err != nil {
					return 0, err
				}

				if tempCursor.Timestamp.Unix()+(3600*5) < tipTimestamp {
					// If we encounter a header with a timestamp more than 5 hours behind current tip (3600*5),
					// stop here to avoid overly long waits on false triggers of difficulty bomb
					// due to low mining difficulty or mining 1-diff blocks on 20-min interval as regular
					log.Info(fmt.Sprintf("while walking back during difficulty bomb, block %s has a timestamp "+
						"more than 5 hours in the past, breaking at lookback=%d", tempCursor.BlockHash().String(),
						lookback))
					break
				}

				tempDiff := blockchain.CalcWork(tempCursor.Bits)
				if tempDiff.Cmp(lowDiffThreshold) > 0 {
					// Walked back to a block whose difficulty is not below the threshold
					break
				} else {
					// cursor difficulty is still below threshold, keep going
					lookbackCursor = tempCursor
					effectiveHVMIndexerTipLag = uint64(lookback)
				}
			}

			// Should not be possible but additional sanity check
			if effectiveHVMIndexerTipLag < defaultTipLag {
				effectiveHVMIndexerTipLag = defaultTipLag
			}

			return effectiveHVMIndexerTipLag, nil
		} else {
			// This was just a single low-difficulty block which can be mined after 20 minutes of no regular-difficulty
			// blocks on testnet3 but does not indicate a difficulty bomb, so return the default tip lag
			return defaultTipLag, nil
		}
	} else {
		return defaultTipLag, nil
	}
}

// hvmIndexErrToConsensus maps the vm-local missing-header sentinel (core/vm cannot
// import consensus — import cycle) to the consensus.ErrFullTBCMissingBTCHeader the
// block-import path treats as a deferrable retry. Any other error (including nil) is
// returned unchanged, so genuine faults remain fail-stop.
func hvmIndexErrToConsensus(err error) error {
	if errors.Is(err, vm.ErrTBCMissingHeader) {
		return consensus.ErrFullTBCMissingBTCHeader
	}
	return err
}

// shouldWalkBackTipLag reports whether updateFullTBCToLightweight should walk the full-node cursor back `lag`
// BTC blocks from `cursorHeight`, or stay at the configured genesis offset. It returns true only when there
// are at least `lag` blocks above the genesis offset, i.e. cursorHeight > genesisOffset + lag. Phrased as an
// addition (not the subtraction cursorHeight - lag > genesisOffset, which underflows uint64 when
// cursorHeight < lag, e.g. right after the hVM Phase-0 transition on a near-zero-offset regtest network) so
// the boundary is overflow-safe and unit-testable.
func shouldWalkBackTipLag(cursorHeight, genesisOffset, lag uint64) bool {
	return cursorHeight > genesisOffset+lag
}

// Update TBC Full Node's indexing to represent lightweight view minus 2 BTC blocks (or more during testnet3 diff bomb)
func (bc *BlockChain) updateFullTBCToLightweight() error {
	lightTipHeight, lightTipHeader, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
	if err != nil {
		log.Error("Attempting to update the full TBC node's indexers based on the header-only TBC consensus "+
			"view of Bitcoin, but unable to get the best block from the lightweight TBC node", "err", err)
		// This likely indicates something is broken with the header-only TBC node which could be recovered
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}

	// if there was no error, but the tip hash is blank, no-op

	lightTipHash := lightTipHeader.BlockHash()

	cursorHeight, cursorHeader := lightTipHeight, lightTipHeader
	cursorHash := cursorHeader.BlockHash()

	effectiveHVMIndexerTipLag := uint64(hVMIndexerTipLag)
	if vm.TBCFullNodeConfig.Network == chaincfg.TestNet3Params.Name {
		// Special case to fix an issue with testnet3 difficulty bomb - when difficulty is low, give a longer tip lag
		log.Debug("Chain is testnet3, checking whether a difficulty bomb is ongoing and a longer effective " +
			"hVM indexer tip lag should be used")
		effectiveHVMIndexerTipLag, err = bc.calculateHvmIndexerTipLagTestnet3(cursorHeader, cursorHeight, effectiveHVMIndexerTipLag)
		if err != nil {
			log.Error("An unexpected error was encountered when attempting to determine whether a higher "+
				"hVM lag behind consensus tip should be used due to a difficulty bomb", "err", err)
			return err
		}

		if effectiveHVMIndexerTipLag != hVMIndexerTipLag {
			log.Info(fmt.Sprintf("Due to a difficulty bomb, hVM will be lagging %d blocks behind known "+
				"consensus tip", effectiveHVMIndexerTipLag))
		}
	}

	// walk back hVMIndexerTipLag blocks from tip
	// On initial init when we have less than hVMIndexerTipLag previous blocks (right after
	// hVM phase 0 transition), correct indexer behavior is to remain at the genesis-configured
	// height until walking backwards the specified number of lag blocks doesn't surpass
	// configured genesis.
	if shouldWalkBackTipLag(cursorHeight, bc.tbcHeaderNodeConfig.GenesisHeightOffset, effectiveHVMIndexerTipLag) {
		for i := uint64(0); i < effectiveHVMIndexerTipLag; i++ {
			head, height, err := bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, cursorHeader.PrevBlock)
			if err != nil {
				// Being unable to walk back through header-only TBC node's best block indicates data corruption
				// which could be solved by recovering the lightweight TBC node
				log.Error(fmt.Sprintf("an error occurred walking back Bitcoin headers from lightweight "+
					"TBC tip %s @ %d, unable to get header %s @ %d", lightTipHash.String(), lightTipHeight,
					cursorHeader.PrevBlock.String(), cursorHeight-1), "err", err)
				return consensus.ErrCorruptHVMHeaderOnlyModeState
			}

			cursorHeader, cursorHeight = head, height // Storing them temporarily for verbose logging
			cursorHash = cursorHeader.BlockHash()
		}
	}

	log.Info(fmt.Sprintf("After walking back from lightweight tip to determine full node indexer target, "+
		"cursorHeight=%d, cursorHeader=%s", cursorHeight, cursorHeader.BlockHash().String()))

	// Check that the TBC Full Node has sufficient chain knowledge to sync to this height.
	available, missingFullBlockHeaders, missingHeaderHash, err := vm.TBCBlocksAvailableToHeader(bc.ctx, cursorHeader)
	if err != nil {
		return err
	}

	if !available {
		log.Warn("Unable to update full TBC node to lightweight tip (minus effective lag) due to at least one missing block.")
		if missingHeaderHash != nil {
			// TBC Full Node does not even have knowledge of at least one header that is needed to update the Full TBC node
			// indexers. Op-geth should insert the missing header into the TBC full node so that the P2P fetcher can attempt
			// to re-fetch.
			log.Error(fmt.Sprintf("TBC missing block header for block: %s", missingHeaderHash))

			// A missing header hash indicates at least one missing tip block, need to determine how many blocks are actually
			// missing from full TBC compared to lightweight canonical

			// Only walk back a maximum of 100 blocks
			_, headerCursor, err := bc.tbcHeaderNode.BlockHeaderBest(bc.ctx)
			if err != nil || headerCursor == nil {
				log.Crit("Unable to fetch best header from TBC lightweight node!", "err", err)
			}

			headers := make([]*wire.BlockHeader, 0)

			for count := 0; count < 100; count++ {
				hash := headerCursor.BlockHash()
				header, _, err := vm.TBCFullNode.BlockHeaderByHash(bc.ctx, hash)
				if err != nil || header == nil {
					// Prepend so headers are in correct (ascending) order
					headers = append([]*wire.BlockHeader{headerCursor}, headers...)
					vm.TBCAttemptBlockRefetch(bc.ctx, headerCursor)
				}
				// Do NOT break when the full node already has this header: continue the back-walk so a NON-CONTIGUOUS
				// gap (a present block above a deeper absent one) is still re-injected/refetched, rather than stopping
				// at the first present block and missing the deeper hole. (In practice the full node's headers are
				// contiguous from genesis — BlockHeadersInsert enforces linkage — so present blocks are simply skipped
				// down to genesis; this is defensive against any future non-contiguous state. Bounded by the
				// 100-iteration cap and the genesis/PrevBlock-NotFound guard below.)
				headerCursor, _, err = bc.tbcHeaderNode.BlockHeaderByHash(bc.ctx, headerCursor.PrevBlock)
				if err != nil || headerCursor == nil {
					// Reached genesis (zero PrevBlock -> NotFound) or the lightweight node errored mid-walk (e.g. ctx
					// cancelled on shutdown). Stop here rather than letting the swallowed error leave headerCursor nil
					// and nil-deref headerCursor.BlockHash() on the next iteration.
					break
				}
			}

			msgHeaders := &wire.MsgHeaders{
				Headers: headers,
			}

			bc.setMissingProgressionBlocks(msgHeaders)

			// Best-effort injection so the full node's P2P fetcher can re-request the missing headers; log (don't fail)
			// if it errors, since this function already returns the missing-header sentinel below.
			if _, _, _, _, err = vm.TBCFullNode.BlockHeadersInsert(bc.ctx, msgHeaders); err != nil {
				log.Warn("Best-effort injection of missing progression headers into the full TBC node failed", "err", err)
			}

			hvmFullTBCBehindGauge.Update(1)
			return consensus.ErrFullTBCMissingBTCHeader
		}
		if missingFullBlockHeaders != nil && len(*missingFullBlockHeaders) > 0 {
			headers := make([]*wire.BlockHeader, len(*missingFullBlockHeaders))

			// Log all of the missing full blocks and trigger an attempt at refetching them over P2P
			for i := 0; i < len(*missingFullBlockHeaders); i++ {
				headers[i] = &(*missingFullBlockHeaders)[i]
				log.Warn(fmt.Sprintf("\tTBC missing full block: %s", (*missingFullBlockHeaders)[i].BlockHash().String()))
				vm.TBCAttemptBlockRefetch(bc.ctx, &(*missingFullBlockHeaders)[i])
			}

			msgHeaders := &wire.MsgHeaders{
				Headers: headers,
			}

			bc.setMissingProgressionBlocks(msgHeaders)

			hvmFullTBCBehindGauge.Update(1)
			return consensus.ErrFullTBCMissingFullBTCBlock
		} else {
			// Should never get available=false but neither a missing full block or block header, so if this happens
			// exit on crit.
			log.Crit(fmt.Sprintf("When attempting to update full TBC node indexers to tip %s @ %d, was "+
				"unable to determine whether any required headers or blocks were missing in full TBC node as the "+
				"call to TBCBlocksAvailableToHeader returned available=false but did not indicate any missing headers "+
				"or full blocks which should not be possible", cursorHeader.BlockHash().String(), cursorHeight))
		}
	}

	// If we got to here, there are no missing progression blocks
	bc.setMissingProgressionBlocks(nil)
	hvmFullTBCBehindGauge.Update(0)

	log.Info(fmt.Sprintf("Moving TBC Full Node indexes to BTC block %s", cursorHeader.BlockHash().String()))

	// This single indexer function handles any reorgs required to move the TBC full node to the specified index.
	err = vm.TBCIndexToHeader(cursorHeader, lightTipHeader)
	if err != nil {
		if mapped := hvmIndexErrToConsensus(err); errors.Is(mapped, consensus.ErrFullTBCMissingBTCHeader) {
			// The full TBC node is missing a not-yet-synced BTC header on the path to the target. The
			// vm-local sentinel is mapped (core/vm cannot import consensus) to the deferrable error so
			// the block-import path returns it before EVM execution and the engine API returns SYNCING;
			// the consensus layer then re-drives the payload once the header syncs, rather than crashing.
			// Matches the early missing-header return above.
			log.Warn(fmt.Sprintf("Deferring TBC full node index move to BTC block %s @ %d: full node is "+
				"missing a required BTC header; will retry as it continues to sync", cursorHash.String(), cursorHeight), "err", err)
			hvmFullTBCBehindGauge.Update(1)
			return mapped
		}
		// All required data was reported available by TBCBlocksAvailableToHeader above, so any
		// OTHER error here indicates a bug or data corruption in the full TBC node — fail-stop.
		log.Crit(fmt.Sprintf("Unable to move TBC Full Node indexers to BTC block %s @ %d",
			cursorHash.String(), cursorHeight), "err", err)
	}

	return nil
}

// updateHvmHeaderConsensus must be called each time when the canonical
// tip is changed. This method determines the change in chain geometry
// that the switch to the new block represents, and modifies the
// external-header-mode TBC instance's Bitcoin header knowledge to
// account for only information contained in the canonical chain ending
// at the new head.
func (bc *BlockChain) updateHvmHeaderConsensus(newHead *Header, updateFullNode bool) error {
	if !bc.hvmEnabled {
		log.Warn("updateHvmHeaderConsensus called but hVM is disabled")
		return nil
	}

	// Single chokepoint for the hVM snap-sync pause. While awaiting an in-flight hVM snap sync the
	// lightweight TBC node is owned by SnapSyncHvm (which rebuilds it via AddExternalHeaders, never through
	// this function), so no normal block-apply / head-move / reorg path may advance hVM consensus. ProcessBlock
	// gates its own apply block separately (it also calls updateFullTBCToLightweight directly); this early
	// return covers every OTHER caller — writeHeadBlock, setHeadBeyondRoot, SetCanonical, the reorg/revert
	// paths, and the build path — uniformly, so the latch cannot be bypassed. Blocks deferred during the
	// window are caught up by the first updateHvmHeaderConsensus after the snap completes (it walks the gap).
	if bc.isAwaitingHvmSnapSync() {
		log.Debug("updateHvmHeaderConsensus skipped: awaiting hVM snap sync (lightweight TBC owned by SnapSyncHvm)")
		return nil
	}

	log.Info(fmt.Sprintf("updateHvmHeaderConsensus called with new head: %s @ %d",
		newHead.Hash().String(), newHead.Number.Uint64()))

	if !bc.chainConfig.IsHvm0(newHead.Time) {
		log.Info(fmt.Sprintf("New head %s @ %d does not have hVM Phase 0 active yet.",
			newHead.Hash().String(), newHead.Number.Uint64()))
		return nil
	}

	// We store the EVM block which was last applied to update hVM
	// independently in order to gracefully handle updates to EVM
	// blockchain state that occurred without TBC's knowledge.
	// In the future, this may also be used for some kind of
	// snap sync of TBC state or similar.
	currentHeadHashRaw, err := bc.tbcHeaderNode.UpstreamStateId(bc.ctx)
	if err != nil || currentHeadHashRaw == nil {
		// A faulted lightweight-store read (a torn/IO-failed leveldb, or external-header-mode disabled)
		// returns a nil pointer, and the dereferences below (currentHeadHashRaw[:]) would nil-panic before
		// any of this function's currentHead==nil recovery guards run. Treat it as the recoverable torn-store
		// condition the re-apply callers heal from via recoverReapplyHvmState/performFullHvmHeaderStateRestore.
		// (applyHvmHeaderConsensusUpdate reads the same UpstreamStateId but log.Crits on its error and does not
		// guard the nil pointer; that read is reached only after this entry-point read has already succeeded,
		// so the narrower handling there is acceptable.)
		log.Error("unable to get upstream state id from lightweight TBC; treating as corrupt hVM state", "err", err)
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}
	log.Info(fmt.Sprintf("current upstream state id from TBC is %x", currentHeadHashRaw[:]))

	if bytes.Equal(currentHeadHashRaw[:], newHead.Hash().Bytes()[:]) {
		log.Info(fmt.Sprintf("updateHvmHeaderConsensus called to update chain to new head %x but lightweight "+
			"TBC node's state already reflects this block, no-op", currentHeadHashRaw[:]))
		return nil
	}

	var currentHead *Header
	currentHeadHash := common.BytesToHash(currentHeadHashRaw[:])

	if bytes.Equal(currentHeadHashRaw[:], hVMGenesisUpstreamId[:]) {
		// Should only be printed at hVM activation time or if something is wrong (genesis state but not activation time)
		log.Info(fmt.Sprintf("Current head from lightweight TBC upstream ID is the hVM Genesis Upstream ID"))
		// Upstream id is genesis, so this should be the first hVM block
		currentHead = bc.getHeaderFromDiskOrHoldingPen(newHead.ParentHash)
		if currentHead == nil {
			// Same recoverable corruption case the currentHead == nil guard below handles for the other
			// branch: the parent is absent from both disk and the holding pen (a rewind/deep-reorg orphaned
			// it, or recoverReapplyHvmState reset us to genesis with the parent already gone). Return the
			// recoverable sentinel instead of nil-dereferencing .Hash()/.Time here and crashing the process.
			log.Error(fmt.Sprintf("currentHead (parent %x of first hVM block %s) is nil; treating as corrupt hVM state",
				newHead.ParentHash[:], newHead.Hash().String()))
			return consensus.ErrCorruptHVMHeaderOnlyModeState
		}
		currentHeadHash = currentHead.Hash()
		if bc.chainConfig.IsHvm0(currentHead.Time) {
			// This is critical as it means hVM is in an unexpected state (upstream state ID is genesis but should not be)
			log.Crit(fmt.Sprintf("When updating hVM state transition for block %s @ %d, the upstream id is the "+
				" hVMGenesisUpstreamId, but the parent at time %d should have hVM Phase 0 activated!",
				newHead.Hash().String(), newHead.Number.Uint64(), currentHead.Time))
		}
	} else {
		log.Debug(fmt.Sprintf("Getting header %x from disk or holding pen", currentHeadHash[:]))
		currentHead = bc.getHeaderFromDiskOrHoldingPen(currentHeadHash)
	}

	if currentHead == nil {
		// The lightweight TBC's upstream-state-id references an EVM header that is absent from both disk and
		// the holding pen (e.g. a rewind/deep-reorg orphaned it). Falling through to findCommonAncestor would
		// nil-deref and crash the process (reachable on followers via the head-move callers, not just the
		// sequencer build path). Return the recoverable sentinel so the callers rebuild from genesis via
		// recoverReapplyHvmState instead of panicking.
		log.Error(fmt.Sprintf("currentHead is nil, but should have been %x; treating as corrupt hVM state", currentHeadHash[:]))
		return consensus.ErrCorruptHVMHeaderOnlyModeState
	}
	log.Debug(fmt.Sprintf("Going to look for ancestor of %s @ %d and %s @ %d", newHead.Hash().String(),
		newHead.Number.Uint64(), currentHead.Hash().String(), currentHead.Number.Uint64()))

	log.Debug(fmt.Sprintf("updateHvmHeaderConsensus found current head hash: %x", currentHeadHashRaw[:]))

	// Get common ancestor between newHead and currentHead
	ancestor, err := bc.findCommonAncestor(newHead, currentHead)
	if err != nil || ancestor == nil {
		log.Error(fmt.Sprintf("Unable to find common ancestor between %s @ %d and %s @ %d,"+
			" cannot transition hVM's header knowledge to the correct state",
			newHead.Hash().String(), newHead.Number.Uint64(),
			currentHead.Hash().String(), currentHead.Number.Uint64()), "err", err)
		// We are missing at least one block in the EVM chain geometry between the requested new head and
		// the current head which hVM currently represents state for
		return consensus.ErrUnknownAncestor
	}

	log.Debug(fmt.Sprintf("Common ancestor between %s @ %d and %s @ %d is %s @ %d",
		currentHead.Hash().String(), currentHead.Number.Uint64(), newHead.Hash().String(),
		newHead.Number.Uint64(), ancestor.Hash().String(), ancestor.Number.Uint64()))

	// If currentHead is direct parent, then just apply state change from newHead
	if newHead.ParentHash.Cmp(currentHead.Hash()) == 0 {
		err := bc.applyHvmHeaderConsensusUpdate(newHead, true, true)
		if err != nil {
			if errors.Is(err, consensus.ErrInvalidHVMBlockFormat) || errors.Is(err, consensus.ErrInvalidHVMHeaders) {
				// Block is invalid, ban block and bubble error up
				badBlock := bc.getBlockFromDiskOrHoldingPen(newHead.Hash())
				bc.reportBlock(badBlock, nil, err)
				return err
			} else if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				// Bubble up corruption to potentially fix upstream
				return err
			} else {
				// Error is unrecognized, for now fail with crit
				log.Crit(fmt.Sprintf("Encountered an error applying hVM header state transition for block %s @ %d",
					newHead.Hash().String(), newHead.Number.Uint64()), "err", err)
			}
			// Unreachable
			return err
		}
		log.Info(fmt.Sprintf("Successfully applied hVM header state transition for single block %s @ %d",
			newHead.Hash().String(), newHead.Number.Uint64()))
	} else if bytes.Equal(currentHead.Hash().Bytes(), ancestor.Hash().Bytes()) {
		// If currentHead is the ancestor, then we are walking directly forwards.
		err := bc.walkHvmHeaderConsensusForward(currentHead, newHead)
		if err != nil {
			if errors.Is(err, consensus.ErrInvalidHVMBlockFormat) || errors.Is(err, consensus.ErrInvalidHVMHeaders) {
				// The offending block has already been reported by the walk-forward function, bubble error up
				return err
			} else if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				// Bubble up corruption to potentially fix upstream
				return err
			} else {
				// Error is unrecognized, for now fail with crit
				log.Crit("Unable to walk hVM consensus forwards", "err", err)
			}
			// Unreachable
			return err
		}

	} else if bytes.Equal(newHead.Hash().Bytes(), ancestor.Hash().Bytes()) {
		// Otherwise if newHead is the ancestor, then we are walking directly backwards.
		err := bc.walkHvmHeaderConsensusBack(currentHead, newHead)
		if err != nil {
			if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				// Bubble potential corruption error upstream
				return err
			} else {
				// Any error that isn't possibly fixed by reconstructing lightweight TBC state is critical,
				// as any other error walking backwards means something that was applied could not be
				// unapplied and is likely a bug
				log.Crit("Unable to walk hVM consensus backwards", "err", err)
			}
		}
	} else {
		// Finally if neither newHead or currentHead is the ancestor, then we are in a fork and need to walk
		// backwards from currentHead until we reach ancestor, then forward to newHead.

		// First, walk backwards from currentHead to common ancestor
		err := bc.walkHvmHeaderConsensusBack(currentHead, ancestor)
		if err != nil {
			if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				// Bubble potential corruption error upstream
				return err
			} else {
				// Any error that isn't possibly fixed by reconstructing lightweight TBC state is critical,
				// as any other error walking backwards means something that was applied could not be
				// unapplied and is likely a bug
				log.Crit("Unable to walk hVM consensus backwards", "err", err)
			}
		}
		// Then, walk forwards from the common ancestor
		err = bc.walkHvmHeaderConsensusForward(ancestor, newHead)
		if err != nil {
			if errors.Is(err, consensus.ErrInvalidHVMBlockFormat) || errors.Is(err, consensus.ErrInvalidHVMHeaders) {
				// The offending block has already been reported by the walk-forward function, bubble error up
				return err
			} else if errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) {
				// Bubble up corruption to potentially fix upstream
				return err
			} else {
				// Error is unrecognized, for now fail with crit
				log.Crit("Unable to walk hVM consensus forwards", "err", err)
			}
			// Unreachable
			return err
		}
	}

	if updateFullNode {
		// Now make sure TBC indexer represents this final state
		err = bc.updateFullTBCToLightweight()
		if err != nil {
			log.Error("Unable to update full TBC node according to lightweight", "err", err)
			return err
		}
	}

	return nil
}

// revertHvmStateAfterInvalidBlock rolls the lightweight (and full) TBC nodes back to the EVM block they
// represented before this insert advanced them to `block`, captured in `tbcHeader`. It is used when
// `block` passed its hVM header-consensus update (so the upstream-state-id, and any BTC headers the
// block's Bitcoin Attributes Deposited tx added, were durably committed) but then failed EVM Process /
// ValidateState.
//
// Without this, the persisted TBC upstream-state-id keeps pointing at a block the EVM rejected and never
// wrote to disk — a consensus-divergence window (hVM precompile reads served from a Bitcoin view derived
// from a rejected block) until the next canonical head re-drives updateHvmHeaderConsensus or a restart
// triggers a full restore. The TBC commit deliberately precedes EVM execution (the hVM precompiles read
// the advanced view during Process), so the correct response is to revert on failure rather than reorder.
//
// The revert target is `tbcHeader` (the pre-insert EVM tip), not the rejected block's parent: a rejected
// reorg block leaves the node on the old canonical chain that tbcHeader represents, so reverting there is
// canonical-consistent. This mirrors the established non-canonical (!setHead) revert (and the
// updateFullTBCToLightweight-failure revert, which additionally skips the full-node re-advance since the
// full node just failed), with two deliberate differences: there is no "direct child
// of current head -> skip" optimization (an invalid block must always be unwound), and
// ErrCorruptHVMHeaderOnlyModeState is recovered via performFullHvmHeaderStateRestore (matching the
// parent-move path) rather than crashing. A nil tbcHeader means `block` was the first hVM block (the
// pre-state is TBC genesis, not an EVM header); we log and rely on restart recovery, matching the
// existing !setHead nil handling. Callers must invoke this only when hVM was activated for `block`.
func (bc *BlockChain) revertHvmStateAfterInvalidBlock(tbcHeader *Header, block *Block) {
	if tbcHeader == nil {
		// First hVM block failed EVM validation: its pre-state is TBC genesis, which we cannot express
		// as an EVM header to revert to here. Leave the state; SetupHvmHeaderNode performs a full
		// restore on restart when it finds the persisted upstream-state-id unknown on disk.
		log.Warn(fmt.Sprintf("hVM state was advanced for invalid first-hVM block %s @ %d; leaving TBC "+
			"state (recovers on restart via full restore)", block.Hash().String(), block.NumberU64()))
		return
	}

	err := bc.updateHvmHeaderConsensus(tbcHeader, true)
	if err != nil {
		if isHvmFullNodeBehind(err) {
			// Transient full-TBC BTC-sync lag, not fatal — the lightweight (consensus) view was reverted
			// successfully (it precedes the full-node advance); the full-node indexer catches up on a
			// later import. (Same predicate the head-set / !setHead paths use.)
			log.Warn(fmt.Sprintf("Full TBC node is behind reverted state at block %s @ %d after invalid "+
				"block %s @ %d; its indexers will catch up on a later import (deferred, not fatal)",
				tbcHeader.Hash().String(), tbcHeader.Number.Uint64(), block.Hash().String(), block.NumberU64()),
				"err", err)
		} else if isHvmReapplyRecoverableError(err) {
			// Re-apply (revert) onto the already-committed pre-invalid-block state: a torn store
			// (ErrCorrupt) or a fresh grandfathered-rule reject (ErrInvalidHVMHeaders/Format) on this
			// already-committed target is recoverable — rebuild from genesis (enforcement off) rather than
			// crashing the fleet. The revert target tbcHeader is already-committed, so a reject here is
			// never a genuine bad block.
			bc.recoverReapplyHvmState(fmt.Sprintf("revert to %s @ %d after invalid block %s @ %d",
				tbcHeader.Hash().String(), tbcHeader.Number.Uint64(), block.Hash().String(), block.NumberU64()), err)
		} else {
			log.Crit(fmt.Sprintf("Unable to revert TBC node to represent state at block %s @ %d after "+
				"invalid block %s @ %d.", tbcHeader.Hash().String(), tbcHeader.Number.Uint64(),
				block.Hash().String(), block.NumberU64()), "err", err)
		}
	} else {
		log.Info(fmt.Sprintf("Successfully reverted TBC node to represent state at block %s @ %d after "+
			"invalid block %s @ %d.", tbcHeader.Hash().String(), tbcHeader.Number.Uint64(),
			block.Hash().String(), block.NumberU64()))
	}
}

// rewindHashHead implements the logic of rewindHead in the context of hash scheme.

func isHvmFullNodeBehind(err error) bool {
	return errors.Is(err, consensus.ErrFullTBCMissingBTCHeader) || errors.Is(err, consensus.ErrFullTBCMissingFullBTCBlock)
}

// isHvmReapplyRecoverableError reports whether a non-nil updateHvmHeaderConsensus error on a re-apply path
// — the head-set / canonical / post-invalid-block revert / parent-move paths, which move the lightweight
// view onto a block already in the canonical chain (enforced at its first import) — should be recovered by
// rebuilding the lightweight view from genesis, rather than escalated to a fleet-wide log.Crit halt.
// Callers route a recoverable error through recoverReapplyHvmState (uniform log + metric + restore).
//
// On a re-apply path the block already passed contextual-difficulty validation at first import, so a fresh consensus reject here is
// not a genuine bad block: ErrInvalidHVMHeaders / ErrInvalidHVMBlockFormat can only mean already-committed
// history is being re-judged against a stricter/grandfathered rule (the same class the activation-gate-skip
// covers — impossible on the verified-clean mainnet), and ErrCorruptHVMHeaderOnlyModeState is a torn store.
// ErrUnknownAncestor is deliberately NOT recoverable here: at a re-apply site the block has connected
// ancestry, so a missing common ancestor is an unexpected geometry condition the from-genesis rebuild cannot
// reliably repair (a genuinely disconnected chain crits during replay anyway), and fail-stop is correct so it
// is not silently masked — see TestIsHvmReapplyRecoverableError.
//
// Why restore recovers (and its limits): performFullHvmHeaderStateRestore replays from genesis to the EVM
// tip (bc.CurrentBlock()) with enforcement off (applyHvmHeaderConsensusUpdate(_, _, false)). That suppresses
// the contextual-difficulty / PoW reject — the only fresh-grandfathered cause — and a torn store is
// discarded by the genesis rebuild, so the recoverable classes above self-heal. Restore is not crit-proof in
// general, though: the non-enforce-gated structural checks (header connectivity, BtcAttr parse) still run
// during replay and, if one fails, restore log.Crits. That is acceptable — those checks pass for
// genuinely-committed canonical blocks, so a structural failure during restore signals real disk corruption
// / a bug, not a grandfathered reject, and halting is correct.
//
// The naive alternative — downgrading the crit to warn-and-proceed — is unsafe: it would leave the
// lightweight view not advanced to the new head while the EVM head moved, silently diverging consensus.
// Restore target: the rebuild lands the view on bc.CurrentBlock(). For the head-set / canonical sites
// currentBlock is set to the new head before the call, so this targets the intended head. For the revert
// sites currentBlock is not modified (the rejected block never advanced the head); it remains the pre-insert
// canonical tip, so the rebuild lands on that tip — a correct (and, if the lightweight view lagged the EVM
// head, stronger) recovery than the narrow revert. The one site whose intended target is not CurrentBlock()
// is the ProcessBlock parent-move: during a fork import its target `parent` is a non-tip ancestor, so that
// site re-drives updateHvmHeaderConsensus(parent, false) after the rebuild (and crits only if that re-drive
// also fails) — see the comment there.
//
// First-import must not use this: the genuine first-import enforcement gate is ProcessBlock's
// `updateHvmHeaderConsensus(block.Header(), false)`, which reportBlocks and returns the reject so the import
// fails cleanly — never restored, which would accept an invalid block. (The hVM block guarded by
// `if status == CanonStatTy` in writeBlockAndSetHead is dead code — `status` is the zero-valued named return
// NonStatTy there, so the guard is always false; the ProcessBlock gate is what enforces first import.)
func isHvmReapplyRecoverableError(err error) bool {
	return errors.Is(err, consensus.ErrCorruptHVMHeaderOnlyModeState) ||
		errors.Is(err, consensus.ErrInvalidHVMHeaders) ||
		errors.Is(err, consensus.ErrInvalidHVMBlockFormat)
}

// recoverReapplyHvmState handles a recoverable (isHvmReapplyRecoverableError) hVM error on a re-apply path
// uniformly across all sites: it emits an alertable Error log + increments hvmReapplyRestoreMeter, then
// rebuilds the lightweight view from genesis (performFullHvmHeaderStateRestore) instead of halting the
// fleet. `where` names the call site (with block context) for the log. Callers must have already gated on
// isHvmReapplyRecoverableError(err); this exists so no site can silently restore (masking a re-judge of
// committed history) or skip the alertable metric.
func (bc *BlockChain) recoverReapplyHvmState(where string, err error) {
	log.Error(fmt.Sprintf("Re-applying already-committed hVM history (%s) hit a recoverable error; rebuilding "+
		"the lightweight view from genesis instead of halting the fleet", where), "err", err)
	hvmReapplyRestoreMeter.Mark(1)
	bc.performFullHvmHeaderStateRestore()
}

func unflattenBTCHeaders(rawHeaders [][types.BitcoinHeaderLengthBytes]byte) (*wire.MsgHeaders, error) {
	parsedHeaders := make([]*wire.BlockHeader, len(rawHeaders))
	for i := 0; i < len(rawHeaders); i++ {
		parsedHeader, err := bytes2Header(rawHeaders[i])
		if err != nil {
			log.Error(fmt.Sprintf("Error decoding Bitcoin header %x", rawHeaders[i][:]), "err", err)
			return nil, err
		}
		parsedHeaders[i] = parsedHeader
	}

	msgHeaders := &wire.MsgHeaders{
		Headers: parsedHeaders,
	}

	return msgHeaders, nil
}

func bytes2Header(header [80]byte) (*wire.BlockHeader, error) {
	var bh wire.BlockHeader
	err := bh.Deserialize(bytes.NewReader(header[:]))
	if err != nil {
		return nil, fmt.Errorf("deserialize block header: %w", err)
	}
	return &bh, nil
}
