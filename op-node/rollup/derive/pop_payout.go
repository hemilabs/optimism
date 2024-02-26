package derive

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/ethereum-optimism/optimism/op-bindings/predeploys"
	"github.com/ethereum-optimism/optimism/op-service/solabi"
)

const (
	PoPPayoutFuncSignature       = "mintPoPRewards(uint64,address[],uint256[])"
	MaximumPoPPayoutsInTx        = 64  // TODO: Implement restriction, review quantity, and ensure it falls under gas allowance (or spare from gas budget?)
	SmartContractArgumentByteLen = 32  // Each argument to smart contract is padded to 32 bytes
	PoPPayoutDelay               = 200 // How long to wait after a block to payout PoP transactions which endorse it

	// MinimumSerializedPoPPayoutLen based on function sig + block # + starting Positions + 1-address array + 1-amount array
	MinimumSerializedPoPPayoutLen = 4 + 32 + (2 * 32) + (2 * 32) + (2 * 32)
)

var (
	PoPPayoutFuncBytes4 = crypto.Keccak256([]byte(PoPPayoutFuncSignature))[:4]
	PoPPayoutAddress    = predeploys.GovernanceTokenAddr
)

// PoPPayout presents the information stored in a GovernanceToken.mintPoPRewards call
type PoPPayout struct {
	BlockRewarded     uint64
	PoPMinerAddresses []common.Address
	PoPMinerAmounts   []*big.Int
}

// Binary Format
// +---------+------------------------------+
// | Bytes   | Field                        |
// +---------+------------------------------+
// | 4       | Function Signature           |
// | 32      | Rewarded Block Number        |
// | 32      | Starting Pos of Address Arr  |
// | 32      | Starting Pos of Amount Arr   |
// | 32      | Address Arr Length           |
// | 32      | First PoP Payout Address     |
// |                  ...                   |
// | 32      | Last PoP Payout Address      |
// | 32      | Amount Arr Length            |
// | 32      | First PoP Amount             |
// |                  ...                   |
// | 32      | Last PoP Amount              |
// +---------+------------------------------+
//
// Example:
// 0x1725877d // keccak256("mintPoPRewards(uint64,address[],uint256[])")
// 00000000000000000000000000000000000000000000000000000000000003e8 // Rewarded Block Number = 1000
// 0000000000000000000000000000000000000000000000000000000000000060 // Address array starts 96 bytes in (4th line)
// 00000000000000000000000000000000000000000000000000000000000000e0 // Amount array starts 240 bytes (8th line)
// 0000000000000000000000000000000000000000000000000000000000000003 // Address array length = 3, 4th line
// 00000000000000000000000043f7d4f2e15a668b443ac9bbcf944fc5200a68da // Address 1 (zero-padded)
// 0000000000000000000000000bc2e7ecc5efc445b77737509256d8e0b2f98852 // Address 2 (zero-padded)
// 0000000000000000000000007a9cd08fcc037fa50b95833624f9640f308c23cc // Address 3 (zero-padded)
// 0000000000000000000000000000000000000000000000000000000000000003 // Amount array length = 3, 8th line
// 0000000000000000000000000000000000000000000001100000000000000000 // 1.1 Tokens to Address 1
// 0000000000000000000000000000000000000000000002000000000000000000 // 2.0 Tokens to Address 2
// 0000000000000000000000000000000000000000000000500000000000000000 // 0.5 Tokens to Address 3
//

func (popPayout *PoPPayout) MarshalBinary() ([]byte, error) {
	// See above format for calculation, assumes addresses and amounts are always same length
	popPayoutsLen := 4 +
		SmartContractArgumentByteLen +
		(SmartContractArgumentByteLen * 4) + // 4 used for scaffolding; see example above
		(SmartContractArgumentByteLen * len(popPayout.PoPMinerAddresses)) +
		(SmartContractArgumentByteLen * len(popPayout.PoPMinerAmounts))

	w := bytes.NewBuffer(make([]byte, 0, popPayoutsLen))
	if err := solabi.WriteSignature(w, PoPPayoutFuncBytes4); err != nil {
		return nil, fmt.Errorf("WriteSignature Failed: %v", err)
	}

	if err := solabi.WriteUint64(w, popPayout.BlockRewarded); err != nil {
		return nil, fmt.Errorf("WriteUint64 for BlockRewarded Failed: %v", err)
	}

	// Address array start is always the same
	if err := solabi.WriteUint64(w, SmartContractArgumentByteLen*3); err != nil {
		return nil, fmt.Errorf("WriteUint64 for Addr Array Start Failed: %v", err)
	}

	// Amount array start is based on address arr length, 3 of the 4 scaffolding values are before amount array starts
	if err := solabi.WriteUint64(w, uint64(SmartContractArgumentByteLen*(4+len(popPayout.PoPMinerAddresses)))); err != nil {
		return nil, fmt.Errorf("WriteUint64 for Amount Array Start Failed: %v", err)
	}

	// Write length of address array
	if err := solabi.WriteUint64(w, uint64(len(popPayout.PoPMinerAddresses))); err != nil {
		return nil, fmt.Errorf("WriteUint64 for Length of Addr Array Failed: %v", err)
	}

	// Write each address in order, zero-padded
	for i := 0; i < len(popPayout.PoPMinerAddresses); i++ {
		if err := solabi.WriteAddress(w, popPayout.PoPMinerAddresses[i]); err != nil {
			return nil, fmt.Errorf("WriteAddress for Addr index %d Array Failed: %v", i, err)
		}
	}

	// Write length of amount array (must always be same as address array)
	if err := solabi.WriteUint64(w, uint64(len(popPayout.PoPMinerAmounts))); err != nil {
		return nil, fmt.Errorf("WriteUint64 for Length of Amount Array Failed: %v", err)
	}

	// Write each amount in order, zero-padded
	for i := 0; i < len(popPayout.PoPMinerAmounts); i++ {
		if err := solabi.WriteUint256(w, popPayout.PoPMinerAmounts[i]); err != nil {
			return nil, fmt.Errorf("WriteUint256 for Amount index %d Array Failed: %v", i, err)
		}
	}

	return w.Bytes(), nil
}

func (popPayout *PoPPayout) UnmarshalBinary(data []byte) error {
	if len(data) < MinimumSerializedPoPPayoutLen {
		return fmt.Errorf("serialized pop payout data must be at least %d bytes, but only %d bytes provided",
			MinimumSerializedPoPPayoutLen, len(data))
	}

	reader := bytes.NewReader(data)

	var err error
	if _, err := solabi.ReadAndValidateSignature(reader, PoPPayoutFuncBytes4); err != nil {
		return err
	}

	// Solabi already handles checks to ensure extra padding above uint64 are empty
	blockRewarded, err := solabi.ReadUint64(reader)
	if err != nil {
		return err
	}

	popPayout.BlockRewarded = blockRewarded

	addrArrayOffset, err := solabi.ReadUint64(reader)
	if err != nil {
		return err
	}

	if addrArrayOffset != 0x60 {
		return errors.New("address array should always start at offset 0x40")
	}

	amountArrayOffset, err := solabi.ReadUint64(reader)
	if err != nil {
		return err
	}
	// Can't check amount array offset value any further until we read address array length

	addressArrayLength, err := solabi.ReadUint64(reader)
	if err != nil {
		return err
	}

	if addressArrayLength > MaximumPoPPayoutsInTx {
		return fmt.Errorf("encoded address array length %d is greater than maximum allowed (%d)",
			addressArrayLength, MaximumPoPPayoutsInTx)
	}

	// Now we can check amount array offset value
	expectedAmountArrayOffset := SmartContractArgumentByteLen * (4 + addressArrayLength)
	if amountArrayOffset != expectedAmountArrayOffset {
		return fmt.Errorf("encoded amount offset is %d but was expected to be %d",
			amountArrayOffset, expectedAmountArrayOffset)
	}

	addresses := make([]common.Address, addressArrayLength)
	for i := 0; i < len(addresses); i++ {
		address, err := solabi.ReadAddress(reader)
		if err != nil {
			return err
		}

		addresses[i] = address
	}

	popPayout.PoPMinerAddresses = addresses

	amountArrayLength, err := solabi.ReadUint64(reader)
	if err != nil {
		return err
	}

	if amountArrayLength > MaximumPoPPayoutsInTx {
		return fmt.Errorf("encoded amount array length %d is greater than maximum allowed (%d)",
			addressArrayLength, MaximumPoPPayoutsInTx)
	}

	if addressArrayLength != amountArrayLength {
		return fmt.Errorf("address array legnth (%d) is not the same as amount array length (%d)",
			addressArrayLength, amountArrayLength)
	}

	amounts := make([]*big.Int, amountArrayLength)
	for i := 0; i < len(amounts); i++ {
		reward, err := solabi.ReadUint256(reader)
		if err != nil {
			return err
		}

		amounts[i] = reward
	}

	popPayout.PoPMinerAmounts = amounts

	if !solabi.EmptyReader(reader) {
		return errors.New("too many bytes")
	}

	return nil
}

// PoPPayoutTxData is the inverse of PoPPayout
func PoPPayoutTxData(data []byte) (PoPPayout, error) {
	var popPayout PoPPayout
	err := popPayout.UnmarshalBinary(data)
	return popPayout, err
}

// PoPPayoutTx creates a special PoP Payout transaction based on the specified PoP Payout info
func PoPPayoutTx(regolith bool, blockRewarded uint64, popMinerAddresses []common.Address, popMinerAmounts []*big.Int) (*types.PopPayoutTx, error) {
	popPayoutDat := PoPPayout{
		BlockRewarded:     blockRewarded,
		PoPMinerAddresses: popMinerAddresses,
		PoPMinerAmounts:   popMinerAmounts,
	}

	data, err := popPayoutDat.MarshalBinary()
	if err != nil {
		return nil, err
	}

	// Set a very large gas limit with `IsSystemTransaction` to ensure
	// that the L1 PoP Payout Transaction does not run out of gas.
	out := &types.PopPayoutTx{
		To:   &PoPPayoutAddress,
		Gas:  150_000_000,
		Data: data,
	}
	// With the regolith fork we disable the IsSystemTx functionality, and allocate real gas
	if regolith {
		out.Gas = RegolithSystemTxGas
	}
	return out, nil
}

// PoPPayoutTxBytes returns a serialized PoP Payout transaction
func PoPPayoutTxBytes(blockRewarded uint64, popMinerAddresses []common.Address, popMinerAmounts []*big.Int) ([]byte, error) {
	if len(popMinerAddresses) != len(popMinerAmounts) {
		return nil, fmt.Errorf("PoP Payout tx was created with %d addresses and %d amounts; "+
			"the quantity of each must be the same",
			len(popMinerAddresses), len(popMinerAmounts))
	}

	dep, err := PoPPayoutTx(true, blockRewarded, popMinerAddresses, popMinerAmounts)
	if err != nil {
		return nil, fmt.Errorf("failed to create PoP Payout tx: %w", err)
	}
	popTx := types.NewTx(dep)
	opaquePoPTx, err := popTx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to encode PoP Payout tx: %w", err)
	}
	return opaquePoPTx, nil
}
