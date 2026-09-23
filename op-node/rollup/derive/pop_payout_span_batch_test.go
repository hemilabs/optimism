package derive

import (
	"bytes"
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
)

// TestSpanBatchTxsPoPPayoutRoundTrip checks that hemi PoP payout txs (type 0x7D in
// hemilabs/op-geth) survive span batch encoding byte-for-byte, including through the
// encode/decode wire form, so that derived blocks match the sequencer's blocks.
func TestSpanBatchTxsPoPPayoutRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7d))
	chainID := big.NewInt(43111)
	signer := types.NewPragueSigner(chainID)

	rawNormalTx, err := testutils.RandomDynamicFeeTx(rng, signer).MarshalBinary()
	require.NoError(t, err)
	popTx, err := PoPPayoutTxBytes(100,
		[]common.Address{{0x01}, {0x02}},
		[]*big.Int{big.NewInt(1e18), big.NewInt(2e18)})
	require.NoError(t, err)
	require.Equal(t, byte(types.PopPayoutTxType), popTx[0])
	txs := [][]byte{rawNormalTx, popTx}

	sbt, err := newSpanBatchTxs(txs, chainID)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, sbt.encode(&buf))

	var decoded spanBatchTxs
	decoded.totalBlockTxCount = uint64(len(txs))
	require.NoError(t, decoded.decode(bytes.NewReader(buf.Bytes())))
	require.NoError(t, decoded.recoverV(chainID))
	txs2, err := decoded.fullTxs(chainID)
	require.NoError(t, err)
	require.Equal(t, txs, txs2)

	// The batch validity rules must accept the PoP payout tx, independent of SDM.
	require.Equal(t, BatchValidity(BatchAccept), checkSequencerTxData(testlog.Logger(t, log.LevelError), 1, popTx, true, false))
}
