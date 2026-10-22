// Copyright (c) 2026 Hemi Labs, Inc.
// Use of this source code is governed by the MIT License,
// which can be found in the LICENSE file.

// Bitcoin block exchange between L2 peers, keystones and PoP payouts: the hVM services that
// hemilabs/op-geth exposes over devp2p, the engine API and the kss RPC namespace.

package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"unsafe"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/hemilabs/heminetwork/api/protocol"
	"github.com/hemilabs/heminetwork/api/tbcapi"
	"github.com/hemilabs/heminetwork/database"
	"github.com/hemilabs/heminetwork/ethereum"
	"github.com/hemilabs/heminetwork/hemi"
	"github.com/hemilabs/heminetwork/hemi/pop"
)

const (
	// Limits of hemilabs/op-geth eth/protocols/eth/handler.go.
	softResponseLimitBTC = 9 * 1024 * 1024
	maxBtcBlocksServe    = 32
)

// encodeList encodes (len(4 BE) || item)*.
func encodeList(items [][]byte) []byte {
	var b bytes.Buffer
	for _, it := range items {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(it)))
		b.Write(it)
	}
	return b.Bytes()
}

func decodeList(b []byte) ([][]byte, error) {
	var items [][]byte
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, errors.New("malformed list")
		}
		l := binary.BigEndian.Uint32(b[:4])
		b = b[4:]
		if uint32(len(b)) < l {
			return nil, errors.New("malformed list")
		}
		items = append(items, b[:l])
		b = b[l:]
	}
	return items, nil
}

// HvmServeBtcBlocks answers a peer's request for Bitcoin blocks (ServiceGetBTCBlocksQuery in
// hemilabs/op-geth). hashes are concatenated 32-byte hashes; the output is a list of serialized
// Bitcoin blocks.
//
//export HvmServeBtcBlocks
func HvmServeBtcBlocks(hashes *C.uchar, hashesLen C.size_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if vm.TBCFullNode == nil {
		return statusDisabled
	}
	in := goBytes(hashes, hashesLen)
	var (
		bytesCount int
		blocks     [][]byte
	)
	for lookups := 0; len(in) >= 32; lookups++ {
		if bytesCount >= softResponseLimitBTC || len(blocks) >= maxBtcBlocksServe || lookups >= 2*maxBtcBlocksServe {
			break
		}
		var ch chainhash.Hash
		_ = ch.SetBytes(in[:32])
		in = in[32:]

		block, err := vm.TBCFullNode.BlockByHash(vm.MainCtx, ch)
		if err != nil || block == nil {
			log.Debug("did not find BTC block requested by peer", "hash", ch, "err", err)
			continue
		}
		var buf bytes.Buffer
		if err := block.MsgBlock().Serialize(&buf); err != nil {
			continue
		}
		if len(blocks) > 0 && bytesCount+buf.Len() > softResponseLimitBTC {
			break
		}
		blocks = append(blocks, buf.Bytes())
		bytesCount += buf.Len()
	}
	setOut(out, outLen, encodeList(blocks))
	return statusOK
}

var ancestorWalks = make(chan struct{}, 4)

// HvmIngestBtcBlocks inserts Bitcoin blocks received from an L2 peer into the full TBC node after
// the proof-of-work, contextual-difficulty and merkle-root gates of hemilabs/op-geth's
// handleBTCBlocks. The output lists the 32-byte hashes of missing ancestors to request from the
// same peer. statusInvalid means the peer sent a protocol-violating message.
//
//export HvmIngestBtcBlocks
func HvmIngestBtcBlocks(data *C.uchar, dataLen C.size_t, out **C.uchar, outLen *C.size_t) (status C.int32_t) {
	if vm.TBCFullNode == nil {
		return statusDisabled
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("dropping peer for invalid hVM BTC message data", "panic", r)
			status = statusInvalid
		}
	}()
	blocks, err := decodeList(goBytes(data, dataLen))
	if err != nil {
		setErr(out, outLen, err)
		return statusInvalid
	}
	if len(blocks) > maxBtcBlocksServe {
		setErr(out, outLen, fmt.Errorf("BtcBlocks response of %d exceeds cap %d", len(blocks), maxBtcBlocksServe))
		return statusInvalid
	}

	var request []byte
	for i, raw := range blocks {
		var msgBlock wire.MsgBlock
		if err := msgBlock.Deserialize(bytes.NewReader(raw)); err != nil {
			log.Error("Unable to deserialize BTC block", "badIndex", i, "err", err)
			continue
		}
		hash := msgBlock.BlockHash()
		if exists, err := vm.TBCFullNode.FullBlockAvailable(vm.MainCtx, hash); err == nil && exists {
			continue
		}
		if err := vm.CheckBTCHeaderPoW(&msgBlock.Header); err != nil && !errors.Is(err, vm.ErrBTCHeaderContextUnavailable) {
			log.Warn("hVM BTC gossip PoW REJECT — dropping header that fails proof-of-work", "block", hash)
			continue
		}
		if err := vm.ValidateBTCHeaderContext(&msgBlock.Header); err != nil && !errors.Is(err, vm.ErrBTCHeaderContextUnavailable) {
			log.Warn("hVM BTC contextual-difficulty REJECT — dropping header", "block", hash, "err", err)
			continue
		}
		headers := &wire.MsgHeaders{Headers: []*wire.BlockHeader{&msgBlock.Header}}
		if _, _, _, _, err := vm.TBCFullNode.BlockHeadersInsert(vm.MainCtx, headers); err != nil {
			log.Debug("Unable to add BTC header to TBC", "err", err)
		}
		if len(msgBlock.Transactions) == 0 {
			continue
		}
		if err := vm.CheckBTCBlockMerkleRoot(&msgBlock); err != nil {
			log.Warn("hVM BTC gossip merkle REJECT — body does not match header merkle root", "block", hash, "err", err)
			continue
		}
		height, err := vm.TBCFullNode.BlockInsert(vm.MainCtx, &msgBlock)
		if err != nil {
			log.Error("Unable to add BTC block to TBC", "block", hash, "err", err)
			continue
		}
		log.Info("Added BTC block from L2 P2P to TBC", "block", hash, "height", height)

		// requestMissingAncestors, bounded like op-geth's ancestor walk gate.
		select {
		case ancestorWalks <- struct{}{}:
			_, missing, _, err := vm.TBCBlocksAvailableToHeader(vm.MainCtx, &msgBlock.Header)
			<-ancestorWalks
			if err == nil && missing != nil {
				for _, m := range *missing {
					h := m.BlockHash()
					request = append(request, h[:]...)
				}
			}
		default:
		}
	}
	setOut(out, outLen, request)
	return statusOK
}

// --- keystones ---

var keystoneMtx sync.RWMutex

func keystoneAbrevHash(k hemi.L2Keystone) *chainhash.Hash {
	return hemi.L2KeystoneAbbreviate(k).Hash()
}

// HvmNewKeystone stores a keystone received from op-node (engine_newKeystone). The output is the
// hex abbreviated hash used in the kss newKeystones notification.
//
//export HvmNewKeystone
func HvmNewKeystone(ksJSON *C.uchar, ksLen C.size_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if keystoneDB == nil {
		return statusDisabled
	}
	var k hemi.L2Keystone
	if err := json.Unmarshal(goBytes(ksJSON, ksLen), &k); err != nil {
		setErr(out, outLen, err)
		return statusInvalid
	}
	keystoneMtx.Lock()
	defer keystoneMtx.Unlock()
	h := keystoneAbrevHash(k)
	log.Info("inserting l2 keystone", "l2KeystoneAbrevHash", hex.EncodeToString(h.CloneBytes()))
	if err := rawdb.WriteL2Keystone(keystoneDB, k); err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	setOut(out, outLen, []byte(hex.EncodeToString(h.CloneBytes())))
	return statusOK
}

// HvmUpsertKeystone stores the keystone of a canonical L2 block (BackfillKeystones in
// hemilabs/op-geth). prev may be null. Returns statusOK when the keystone is new, statusDefer when
// it already existed.
//
//export HvmUpsertKeystone
func HvmUpsertKeystone(l1Block, l2Block C.uint32_t, parent, prev, stateRoot, epHash *C.uchar, out **C.uchar, outLen *C.size_t) C.int32_t {
	if keystoneDB == nil {
		return statusDisabled
	}
	k := hemi.L2Keystone{
		Version:       1,
		L1BlockNumber: uint32(l1Block),
		L2BlockNumber: uint32(l2Block),
		ParentEPHash:  C.GoBytes(unsafe.Pointer(parent), 32),
		StateRoot:     C.GoBytes(unsafe.Pointer(stateRoot), 32),
		EPHash:        C.GoBytes(unsafe.Pointer(epHash), 32),
	}
	if prev != nil {
		k.PrevKeystoneEPHash = C.GoBytes(unsafe.Pointer(prev), 32)
	}
	keystoneMtx.Lock()
	defer keystoneMtx.Unlock()
	_, err := rawdb.ReadL2KeystoneByAbrevHash(keystoneDB, keystoneAbrevHash(k).CloneBytes())
	insertedNew := err != nil
	if err := rawdb.WriteL2Keystone(keystoneDB, k); err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	if insertedNew {
		return statusOK
	}
	return statusDefer
}

// HvmDeleteKeystonesAbove deletes the keystones above the given L2 height.
//
//export HvmDeleteKeystonesAbove
func HvmDeleteKeystonesAbove(height C.uint64_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if keystoneDB == nil {
		return statusDisabled
	}
	keystoneMtx.Lock()
	defer keystoneMtx.Unlock()
	if err := rawdb.DeleteL2KeystonesAboveHeight(keystoneDB, uint64(height)); err != nil {
		setErr(out, outLen, err)
		return statusError
	}
	return statusOK
}

const maxL2KeystoneCount uint = 50

type l2KeystoneResponse struct {
	L2Keystones []hemi.L2Keystone `json:"keystones"`
	Error       *protocol.Error   `json:"error,omitempty"`
}

// HvmKssGetLatestKeystones implements kss_getLatestKeystones; the output is the JSON response.
//
//export HvmKssGetLatestKeystones
func HvmKssGetLatestKeystones(count C.uint64_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	var resp l2KeystoneResponse
	if uint(count) > maxL2KeystoneCount {
		resp.Error = protocol.Errorf("count too large")
	} else {
		keystoneMtx.RLock()
		kss, err := rawdb.ReadMostRecentL2Keystones(keystoneDB, uint(count))
		keystoneMtx.RUnlock()
		if err != nil {
			resp.Error = protocol.Errorf("failed to retrieve keystones")
		} else {
			resp.L2Keystones = kss
		}
	}
	b, _ := json.Marshal(resp)
	setOut(out, outLen, b)
	return statusOK
}

// HvmKssGetKeystone implements kss_getKeystone; the output is the JSON response.
//
//export HvmKssGetKeystone
func HvmKssGetKeystone(abrev *C.uchar, abrevLen C.size_t, count C.uint64_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	var resp l2KeystoneResponse
	abrevHash := string(goBytes(abrev, abrevLen))
	if uint(count) > maxL2KeystoneCount {
		resp.Error = protocol.Errorf("count too large")
	} else if _, err := chainhash.NewHashFromStr(abrevHash); err != nil {
		resp.Error = protocol.Errorf("%s", err.Error())
	} else if b, err := hex.DecodeString(abrevHash); err != nil {
		resp.Error = protocol.Errorf("%s", err.Error())
	} else {
		keystoneMtx.RLock()
		kss, err := rawdb.GetKeystoneAndDescendants(keystoneDB, b, uint(count))
		keystoneMtx.RUnlock()
		if err != nil {
			resp.Error = protocol.Errorf("%s", err.Error())
		} else {
			resp.L2Keystones = kss
		}
	}
	b, _ := json.Marshal(resp)
	setOut(out, outLen, b)
	return statusOK
}

type popPayout struct {
	MinerAddress common.Address `json:"miner_address"`
	Amount       *big.Int       `json:"amount"`
}

// HvmPopPayouts implements engine_popPayoutsByL2Keystone; abrev is the JSON encoded
// chainhash.Hash parameter and the output the JSON result.
//
//export HvmPopPayouts
func HvmPopPayouts(abrev *C.uchar, abrevLen C.size_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	if vm.TBCFullNode == nil || vm.TBCFullNodeConfig == nil {
		setErr(out, outLen, errors.New("hVM/TBC full node is not enabled on this node"))
		return statusDisabled
	}
	var abrevHash chainhash.Hash
	if err := json.Unmarshal(goBytes(abrev, abrevLen), &abrevHash); err != nil {
		setErr(out, outLen, err)
		return statusInvalid
	}
	req := tbcapi.KeystoneTxsByL2KeystoneAbrevHashRequest{Depth: 3, L2KeystoneAbrevHash: abrevHash}
	if vm.TBCFullNodeConfig.Network == "localnet" {
		req.Depth = 1000
	}
	payouts := make([]popPayout, 0)
	resp, err := vm.TBCFullNode.KeystoneTxsByHash(vm.MainCtx, &req)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		setErr(out, outLen, err)
		return statusError
	}
	if err == nil {
		amount := big.NewInt(hemi.HEMIBase)
		for _, ksTx := range resp.KeystoneTxs {
			var msgTx wire.MsgTx
			if err := msgTx.Deserialize(bytes.NewReader(ksTx.RawTx)); err != nil {
				setErr(out, outLen, err)
				return statusError
			}
			pub, err := pop.ParsePublicKeyFromSignatureScript(msgTx.TxIn[0].SignatureScript)
			if err != nil {
				setErr(out, outLen, err)
				return statusError
			}
			payouts = append(payouts, popPayout{MinerAddress: ethereum.PublicKeyToAddress(pub), Amount: amount})
		}
	}
	b, _ := json.Marshal(payouts)
	setOut(out, outLen, b)
	return statusOK
}

// HvmBtcAttrHashes returns the Bitcoin blocks a Bitcoin Attributes Deposited transaction refers
// to — its canonical tip and the hash of each header it carries — as concatenated 32-byte hashes
// in internal byte order.
//
//export HvmBtcAttrHashes
func HvmBtcAttrHashes(data *C.uchar, dataLen C.size_t, out **C.uchar, outLen *C.size_t) C.int32_t {
	var attr types.BtcAttributesDepositData
	if err := attr.UnmarshalBinary(goBytes(data, dataLen)); err != nil {
		setErr(out, outLen, err)
		return statusInvalid
	}
	b := append([]byte{}, attr.CanonicalTip[:]...)
	for _, raw := range attr.Headers {
		var h wire.BlockHeader
		if err := h.Deserialize(bytes.NewReader(raw[:])); err != nil {
			setErr(out, outLen, err)
			return statusInvalid
		}
		hash := h.BlockHash()
		b = append(b, hash[:]...)
	}
	setOut(out, outLen, b)
	return statusOK
}
