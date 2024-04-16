package node

import (
	"context"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-service/client"
	"github.com/ethereum/go-ethereum/common"
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

func getBTCFinalityForBlockNum(ctx context.Context, blockNum uint64, stateRoot []byte, driver driverClient, bssClient client.BssClient) ([]hemi.L2BTCFinality, error) {
	nextKeystoneHeight, err := getKeystoneProvidingFinality(blockNum)
	if err != nil {
		return nil, err
	}

	l2TIpHeight, err := getTipHeight(ctx, driver)
	if err != nil {
		return nil, err
	}

	if nextKeystoneHeight > l2TIpHeight {
		return nil, fmt.Errorf("keystone %d providing finality for block %d not yet produced, L2 tip = %d",
			nextKeystoneHeight, blockNum, l2TIpHeight)
	}
	nextKeystone, _, err := driver.BlockRefWithStatus(ctx, nextKeystoneHeight)
	if err != nil {
		return nil, err
	}

	// Get height of the previous keystone, so we can reconstruct the appropriate L2Keystone header
	prevKeystoneHeight := nextKeystoneHeight - hemi.KeystoneHeaderPeriod
	prevKeystoneHash := [common.HashLength]byte{}
	if prevKeystoneHeight >= 0 {
		prevKeystone, _, err := driver.BlockRefWithStatus(ctx, prevKeystoneHeight)
		if err != nil {
			return nil, err
		}
		prevKeystoneHash = [32]byte(prevKeystone.Hash[:])
	}

	l2Keystone := &hemi.L2Keystone{
		Version:            0x01,
		L1BlockNumber:      uint32(nextKeystone.L1Origin.Number),
		L2BlockNumber:      uint32(nextKeystone.Number),
		ParentEPHash:       nextKeystone.ParentHash[:],
		PrevKeystoneEPHash: prevKeystoneHash[:],
		StateRoot:          stateRoot,
		EPHash:             nextKeystone.Hash[:],
	}

	l2KeystonesToQuery := make([]hemi.L2Keystone, 1)
	l2KeystonesToQuery[0] = *l2Keystone

	return bssClient.BtcFinalityByKeystones(ctx, l2KeystonesToQuery)
}

func getBTCFinalityForBlockHash(ctx context.Context, blockHash common.Hash, l2Client l2EthClient, driver driverClient, bssClient client.BssClient) ([]hemi.L2BTCFinality, error) {
	block, err := l2Client.InfoByHash(ctx, blockHash)
	if err != nil {
		return nil, err
	}

	// Fetch the block from the canonical chain at the same index as the block corresponding to the provided hash to
	// ensure the block is part of the canonical chain
	blockNum := block.NumberU64()
	refetch, _, err := driver.BlockRefWithStatus(ctx, blockNum)
	if err != nil {
		return nil, err
	}

	// Check passed in hash matches hash of block from canonical chain at same height
	if refetch.Hash != blockHash {
		return nil, fmt.Errorf("block %x at height %d is not on the canonical chain, canonical block at height "+
			"%d is %x", blockHash, blockNum, blockNum, blockHash)
	}

	stateRoot := block.Root()

	return getBTCFinalityForBlockNum(ctx, blockNum, stateRoot[:], driver, bssClient)
}
