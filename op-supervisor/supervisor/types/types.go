package types

import (
	"fmt"
	"math"

	"github.com/holiman/uint256"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/ethereum-optimism/optimism/op-service/eth"
)

type Revision uint64

// RevisionAny is used as indicator to ignore the revision during lookups.
// This is used in the cross-safe queries,
// where there will only ever be a single derived block per derived block number,
// but where the revision is still tracked to match the local-safe DB block replacements.
// We use the max-uint64 value, since this is reserved, and will not be allowed to decode/encode.
const RevisionAny = ^Revision(0)

func (r Revision) Any() bool {
	return r == RevisionAny
}

// Number returns the block-number, where the revision started (i.e. the invalidated/replacement block height)
func (r Revision) Number() uint64 {
	return uint64(r) &^ uint64(1<<63)
}

func (r Revision) String() string {
	if r.Any() {
		return "Rev(any)"
	}
	return fmt.Sprintf("Rev(%d)", r.Number())
}

// Cmp returns:
// 0 if the revision matches any block number
// 1 if the revision is higher than the given number
// 0 if the revision is equal than the given number
// -1 if the revision is lower than the given number
func (r Revision) Cmp(blockNum uint64) int {
	if r.Any() {
		return 0
	}
	if r.Number() > blockNum {
		return 1
	}
	if r.Number() == blockNum {
		return 0
	}
	return -1
}

// DerivedBlockRefPair is a pair of block refs, where Derived (L2) is derived from Source (L1).
type DerivedBlockRefPair struct {
	Source  eth.BlockRef `json:"source"`
	Derived eth.BlockRef `json:"derived"`
}

func (refs *DerivedBlockRefPair) IDs() DerivedIDPair {
	return DerivedIDPair{
		Source:  refs.Source.ID(),
		Derived: refs.Derived.ID(),
	}
}

func (refs *DerivedBlockRefPair) Seals() DerivedBlockSealPair {
	return DerivedBlockSealPair{
		Source:  BlockSealFromRef(refs.Source),
		Derived: BlockSealFromRef(refs.Derived),
	}
}

func (refs DerivedBlockRefPair) String() string {
	return fmt.Sprintf("refPair(source: %s, derived: %s)", refs.Source, refs.Derived)
}

// DerivedBlockSealPair is a pair of block seals, where Derived (L2) is derived from Source (L1).
type DerivedBlockSealPair struct {
	Source  BlockSeal `json:"source"`
	Derived BlockSeal `json:"derived"`
}

func (seals *DerivedBlockSealPair) IDs() DerivedIDPair {
	return DerivedIDPair{
		Source:  seals.Source.ID(),
		Derived: seals.Derived.ID(),
	}
}

func (seals DerivedBlockSealPair) String() string {
	return fmt.Sprintf("sealPair(source: %s, derived: %s)", seals.Source, seals.Derived)
}

// DerivedIDPair is a pair of block IDs, where Derived (L2) is derived from Source (L1).
type DerivedIDPair struct {
	Source  eth.BlockID `json:"source"`
	Derived eth.BlockID `json:"derived"`
}

func (ids DerivedIDPair) String() string {
	return fmt.Sprintf("idPair(source: %s, derived: %s)", ids.Source, ids.Derived)
}

type BlockReplacement struct {
	Replacement eth.BlockRef `json:"replacement"`
	Invalidated common.Hash  `json:"invalidated"`
}

// IndexingEvent is an event sent by the indexing node to the supervisor,
// to share an update. One of the fields will be non-null; different kinds of updates may be sent.
type IndexingEvent struct {
	Reset                  *string              `json:"reset,omitempty"`
	UnsafeBlock            *eth.BlockRef        `json:"unsafeBlock,omitempty"`
	DerivationUpdate       *DerivedBlockRefPair `json:"derivationUpdate,omitempty"`
	ExhaustL1              *DerivedBlockRefPair `json:"exhaustL1,omitempty"`
	ReplaceBlock           *BlockReplacement    `json:"replaceBlock,omitempty"`
	DerivationOriginUpdate *eth.BlockRef        `json:"derivationOriginUpdate,omitempty"`
}

// MessageChecksum represents a message checksum, as used for access-list checks.
type MessageChecksum common.Hash

func (mc MessageChecksum) MarshalText() ([]byte, error) {
	return common.Hash(mc).MarshalText()
}

func (mc *MessageChecksum) UnmarshalText(data []byte) error {
	return (*common.Hash)(mc).UnmarshalText(data)
}

func (mc MessageChecksum) String() string {
	return common.Hash(mc).String()
}

// Access represents access to a message, parsed from an access-list
type Access struct {
	BlockNumber uint64
	Timestamp   uint64
	LogIndex    uint32
	ChainID     eth.ChainID
	Checksum    MessageChecksum
}

func (acc Access) Query() ContainsQuery {
	return ContainsQuery{
		Timestamp: acc.Timestamp,
		BlockNum:  acc.BlockNumber,
		LogIdx:    acc.LogIndex,
		Checksum:  acc.Checksum,
	}
}

// lookupEntry encodes a lookup entry for an access-list
func (acc Access) lookupEntry() common.Hash {
	var out common.Hash
	out[0] = PrefixLookup
	binary.BigEndian.PutUint64(out[4:12], (*uint256.Int)(&acc.ChainID).Uint64())
	binary.BigEndian.PutUint64(out[12:20], acc.BlockNumber)
	binary.BigEndian.PutUint64(out[20:28], acc.Timestamp)
	binary.BigEndian.PutUint32(out[28:32], acc.LogIndex)
	return out
}

// chainIDExtensionEntry encodes a chainID-extension entry for an access-list
func (acc Access) chainIDExtensionEntry() common.Hash {
	var out common.Hash
	dat := (*uint256.Int)(&acc.ChainID).Bytes32()
	out[0] = PrefixChainIDExtension
	copy(out[8:32], dat[0:24])
	return out
}

type accessMarshaling struct {
	BlockNumber hexutil.Uint64  `json:"blockNumber"`
	Timestamp   hexutil.Uint64  `json:"timestamp"`
	LogIndex    uint32          `json:"logIndex"`
	ChainID     eth.ChainID     `json:"chainID"`
	Checksum    MessageChecksum `json:"checksum"`
}

func (a Access) MarshalJSON() ([]byte, error) {
	enc := accessMarshaling{
		BlockNumber: hexutil.Uint64(a.BlockNumber),
		Timestamp:   hexutil.Uint64(a.Timestamp),
		LogIndex:    a.LogIndex,
		ChainID:     a.ChainID,
		Checksum:    a.Checksum,
	}
	return json.Marshal(&enc)
}

func (a *Access) UnmarshalJSON(input []byte) error {
	var dec accessMarshaling
	if err := json.Unmarshal(input, &dec); err != nil {
		return err
	}
	a.BlockNumber = uint64(dec.BlockNumber)
	a.Timestamp = uint64(dec.Timestamp)
	a.LogIndex = dec.LogIndex
	a.ChainID = dec.ChainID
	a.Checksum = dec.Checksum
	return nil
}

const (
	PrefixLookup           = 1
	PrefixChainIDExtension = 2
	PrefixChecksum         = 3
)

var (
	errExpectedEntry       = errors.New("expected entry")
	errMalformedEntry      = errors.New("malformed entry")
	errUnexpectedEntryType = errors.New("unexpected entry type")
)

// ParseAccess parses some access-list entries into an Access, and returns the remaining entries.
// This process can be repeated until no entries are left, to parse an access-list.
func ParseAccess(entries []common.Hash) ([]common.Hash, Access, error) {
	if len(entries) == 0 {
		return nil, Access{}, errExpectedEntry
	}
	entry := entries[0]
	entries = entries[1:]
	if typeByte := entry[0]; typeByte != PrefixLookup {
		return nil, Access{}, fmt.Errorf("expected lookup, got entry type %d: %w",
			typeByte, errUnexpectedEntryType)
	}
	if ([3]byte)(entry[1:4]) != ([3]byte{}) {
		return nil, Access{}, fmt.Errorf("expected zero bytes: %w", errMalformedEntry)
	}
	var access Access
	access.ChainID = eth.ChainIDFromUInt64(binary.BigEndian.Uint64(entry[4:12]))
	access.BlockNumber = binary.BigEndian.Uint64(entry[12:20])
	access.Timestamp = binary.BigEndian.Uint64(entry[20:28])
	access.LogIndex = binary.BigEndian.Uint32(entry[28:32])

	if len(entries) == 0 {
		return nil, Access{}, errExpectedEntry
	}
	entry = entries[0]
	entries = entries[1:]
	if typeByte := entry[0]; typeByte == PrefixChainIDExtension {
		if ([7]byte)(entry[1:8]) != ([7]byte{}) {
			return nil, Access{}, fmt.Errorf("expected zero bytes: %w", errMalformedEntry)
		}
		// The lower 8 bytes is set to the uint64 in the first entry.
		// The upper 24 bytes are set with this extension entry.
		chIDBytes32 := access.ChainID.Bytes32()
		copy(chIDBytes32[0:24], entry[8:32])
		access.ChainID = eth.ChainIDFromBytes32(chIDBytes32)
		if len(entries) == 0 {
			return nil, Access{}, errExpectedEntry
		}
		entry = entries[0]
		entries = entries[1:]
	}
	if typeByte := entry[0]; typeByte != PrefixChecksum {
		return nil, Access{}, fmt.Errorf("expected checksum, got entry type %d: %w",
			typeByte, errUnexpectedEntryType)
	}
	access.Checksum = MessageChecksum(entry)
	return entries, access, nil
}

func EncodeAccessList(accesses []Access) []common.Hash {
	out := make([]common.Hash, 0, len(accesses)*2)
	for _, acc := range accesses {
		out = append(out, acc.lookupEntry())

		if !(*uint256.Int)(&acc.ChainID).IsUint64() {
			out = append(out, acc.chainIDExtensionEntry())
		}

		if acc.Checksum[0] != PrefixChecksum {
			panic("invalid checksum entry")
		}
		out = append(out, common.Hash(acc.Checksum))
	}
	return out
}
