package rollup

import (
	"encoding/binary"
	gomath "math"

	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
)

// This file contains ephemeral feature toggles for the next
// fork while it is in development. They should be removed
// after the fork scope is locked.

const JovianExtraDataVersionByte = uint8(0x01)

// Example:
func (c *Config) IsMinBaseFee(time uint64) bool {
	return c.IsJovian(time) // Replace with return false to disable
}

// DecodeJovianExtraData decodes the extraData parameters from the encoded form defined here:
// https://specs.optimism.io/protocol/jovian/exec-engine.html
//
// Returns 0,0,nil if the format is invalid, and d, e, nil for the Holocene length, to provide best effort behavior for non-MinBaseFee extradata, though ValidateJovianExtraData should be used instead of this function for
// validity checking.
func DecodeJovianExtraData(extra []byte) (uint64, uint64, *uint64) {
	// Best effort to decode the extraData for every block in the chain's history,
	// including blocks before the minimum base fee feature was enabled.
	if len(extra) == 9 {
		// This is Holocene extraData
		denominator, elasticity := eip1559.DecodeHolocene1559Params(extra[1:9])
		return denominator, elasticity, nil
	} else if len(extra) == 17 {
		// Decode extraData when the minimum base fee fork is enabled
		denominator, elasticity := eip1559.DecodeHolocene1559Params(extra[1:9])
		minBaseFee := binary.BigEndian.Uint64(extra[9:])
		return denominator, elasticity, &minBaseFee
	}
	return 0, 0, nil
}

// EncodeJovianExtraData encodes the eip-1559 and minBaseFee parameters into the header 'ExtraData' format.
// Will panic if eip-1559 parameters are outside uint32 range.
func EncodeJovianExtraData(denom, elasticity, minBaseFee uint64) []byte {
	r := make([]byte, 17)
	if denom > gomath.MaxUint32 || elasticity > gomath.MaxUint32 {
		panic("eip-1559 parameters out of uint32 range")
	}
	r[0] = JovianExtraDataVersionByte
	binary.BigEndian.PutUint32(r[1:5], uint32(denom))
	binary.BigEndian.PutUint32(r[5:9], uint32(elasticity))
	binary.BigEndian.PutUint64(r[9:], minBaseFee)
	return r
}
