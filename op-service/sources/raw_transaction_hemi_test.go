package sources

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// TestRawTransactionJSONPopPayout checks that hemi's PoP payout tx (type 0x7D in
// hemilabs/op-geth) decodes from the eth_getBlockBy* JSON form to its exact canonical bytes.
// Upstream routes 0x7D to the SDM post-exec codec; hemi must route it to go-ethereum, or L2
// blocks carrying PoP payouts fail block-hash verification when fetched over RPC.
func TestRawTransactionJSONPopPayout(t *testing.T) {
	to := common.HexToAddress("0x455f8F0B8dF5399873700f60aa931D6b89Ac9c79")
	data := []byte{0xde, 0xad, 0xbe, 0xef}
	tx := types.NewTx(&types.PopPayoutTx{To: &to, Gas: 20_000_000, Data: data})
	require.Equal(t, uint8(types.PopPayoutTxType), tx.Type())
	want, err := tx.MarshalBinary()
	require.NoError(t, err)

	// The fields ethapi.RPCTransaction serves for a PoP payout tx.
	txJSON, err := json.Marshal(map[string]any{
		"type":     hexutil.Uint64(types.PopPayoutTxType),
		"to":       to,
		"gas":      hexutil.Uint64(20_000_000),
		"gasPrice": (*hexutil.Big)(common.Big0),
		"input":    hexutil.Bytes(data),
		"value":    (*hexutil.Big)(common.Big0),
		"hash":     tx.Hash(),
	})
	require.NoError(t, err)

	var raw RawTransaction
	require.NoError(t, json.Unmarshal(txJSON, &raw))
	require.Equal(t, want, []byte(raw))
	require.Equal(t, tx.Hash(), raw.Hash())

	userTxs, err := RawTransactions{raw}.UserTxs()
	require.NoError(t, err)
	require.Len(t, userTxs, 1)
	require.Equal(t, tx.Hash(), userTxs[0].Hash())
}
