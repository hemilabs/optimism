package node

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/davecgh/go-spew/spew"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/hemilabs/heminetwork/api/bfgapi"
	"github.com/hemilabs/heminetwork/hemi"
)

func getKeystoneProvidingFinality(blockNum uint64) (uint64, error) {
	// If this block is not a keystone itself, get the next keystone block which gives this block finality
	if blockNum%hemi.KeystoneHeaderPeriod == 0 {
		// Block is already a keystone, so provides finality to itself
		return blockNum, nil
	} else {
		// Block is not a keystone, so get the height of the next keystone in the chain
		return blockNum + (hemi.KeystoneHeaderPeriod - (blockNum % hemi.KeystoneHeaderPeriod)), nil
	}
}

func getTipHeight(ctx context.Context, driver driverClient) (uint64, error) {
	syncStatus, err := driver.SyncStatus(ctx)
	if err != nil {
		// Return 0 for height with error, which would be just a genesis block
		return 0, err
	}

	return syncStatus.UnsafeL2.Number, nil
}

func getBTCFinalityForBlockNum(ctx context.Context, blockNum uint64, driver driverClient, l2Client l2EthClient, bfgURL string) (bfgapi.L2KeystoneBitcoinFinalityResponse, error) {
	emptyFin := bfgapi.L2KeystoneBitcoinFinalityResponse{}
	nextKeystoneHeight, err := getKeystoneProvidingFinality(blockNum)
	if err != nil {
		return emptyFin, err
	}

	log.Trace("getBTCFinalityForBlockNum", "nextKeystoneHeight", nextKeystoneHeight)

	l2TIpHeight, err := getTipHeight(ctx, driver)
	if err != nil {
		return emptyFin, err
	}

	if nextKeystoneHeight > l2TIpHeight {
		return emptyFin, fmt.Errorf("keystone %d providing finality for block %d not yet produced, L2 tip = %d",
			nextKeystoneHeight, blockNum, l2TIpHeight)
	}
	nextKeystone, _, err := driver.BlockRefWithStatus(ctx, nextKeystoneHeight)
	if err != nil {
		return emptyFin, err
	}

	// Get height of the previous keystone, so we can reconstruct the appropriate L2Keystone header
	prevKeystoneHeight := nextKeystoneHeight - hemi.KeystoneHeaderPeriod
	prevKeystoneHash := [common.HashLength]byte{}
	if prevKeystoneHeight >= 0 {
		prevKeystone, _, err := driver.BlockRefWithStatus(ctx, prevKeystoneHeight)
		if err != nil {
			return emptyFin, err
		}
		prevKeystoneHash = [32]byte(prevKeystone.Hash[:])
	}

	block, err := l2Client.InfoByHash(ctx, nextKeystone.Hash)
	if err != nil {
		return emptyFin, err
	}

	stateRoot := block.Root()

	l2Keystone := &hemi.L2Keystone{
		Version:            0x01,
		L1BlockNumber:      uint32(nextKeystone.L1Origin.Number),
		L2BlockNumber:      uint32(nextKeystone.Number),
		ParentEPHash:       nextKeystone.ParentHash[:],
		PrevKeystoneEPHash: prevKeystoneHash[:],
		StateRoot:          stateRoot[:],
		EPHash:             nextKeystone.Hash[:],
	}

	log.Trace("going to query for keystone", "keystone", spew.Sdump(l2Keystone), "nextKeystone", spew.Sdump(nextKeystone), "prevKeystoneHash", hex.EncodeToString(prevKeystoneHash[:]))

	client := &http.Client{}
	kssHash := hemi.L2KeystoneAbbreviate(*l2Keystone).Hash()
	u := fmt.Sprintf("%v/v%v/keystonefinality/%v",
		bfgURL, bfgapi.APIVersion, kssHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return emptyFin, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return emptyFin, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return emptyFin, fmt.Errorf("unexpected status code: %v", resp.StatusCode)
	}

	fin := bfgapi.L2KeystoneBitcoinFinalityResponse{}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return emptyFin, err
	}

	if err = resp.Body.Close(); err != nil {
		return emptyFin, err
	}

	if err = json.Unmarshal(body, &fin); err != nil {
		return emptyFin, err
	}

	return fin, nil
}

func getBTCFinalityForBlockHash(ctx context.Context, blockHash common.Hash, l2Client l2EthClient, driver driverClient, bfgURL string) (bfgapi.L2KeystoneBitcoinFinalityResponse, error) {
	emptyFin := bfgapi.L2KeystoneBitcoinFinalityResponse{}
	block, err := l2Client.InfoByHash(ctx, blockHash)
	if err != nil {
		return emptyFin, err
	}

	// Fetch the block from the canonical chain at the same index as the block corresponding to the provided hash to
	// ensure the block is part of the canonical chain
	blockNum := block.NumberU64()
	refetch, _, err := driver.BlockRefWithStatus(ctx, blockNum)
	if err != nil {
		return emptyFin, err
	}

	// Check passed in hash matches hash of block from canonical chain at same height
	if refetch.Hash != blockHash {
		return emptyFin, fmt.Errorf("block %x at height %d is not on the canonical chain, canonical block at height "+
			"%d is %x", blockHash, blockNum, blockNum, blockHash)
	}

	return getBTCFinalityForBlockNum(ctx, blockNum, driver, l2Client, bfgURL)
}
