package testutils

import (
	"context"
	"encoding/json"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/ethereum/go-ethereum/common"
	"github.com/hemilabs/heminetwork/hemi"

	"github.com/ethereum-optimism/optimism/op-service/eth"
)

type MockEngine struct {
	MockL2Client
}

func (m *MockEngine) GetPayload(ctx context.Context, payloadInfo eth.PayloadInfo) (*eth.ExecutionPayloadEnvelope, error) {
	out := m.Mock.Called(payloadInfo.ID)
	return out.Get(0).(*eth.ExecutionPayloadEnvelope), out.Error(1)
}

func (m *MockEngine) ExpectGetPayload(payloadId eth.PayloadID, payload *eth.ExecutionPayloadEnvelope, err error) {
	m.Mock.On("GetPayload", payloadId).Once().Return(payload, err)
}

func (m *MockEngine) ForkchoiceUpdate(ctx context.Context, state *eth.ForkchoiceState, attr *eth.PayloadAttributes) (*eth.ForkchoiceUpdatedResult, error) {
	out := m.Mock.Called(mustJson(state), mustJson(attr))
	return out.Get(0).(*eth.ForkchoiceUpdatedResult), out.Error(1)
}

func (m *MockEngine) ExpectForkchoiceUpdate(state *eth.ForkchoiceState, attr *eth.PayloadAttributes, result *eth.ForkchoiceUpdatedResult, err error) {
	m.Mock.On("ForkchoiceUpdate", mustJson(state), mustJson(attr)).Once().Return(result, err)
}

func (m *MockEngine) NewPayload(ctx context.Context, payload *eth.ExecutionPayload, parentBeaconBlockRoot *common.Hash) (*eth.PayloadStatusV1, error) {
	out := m.Mock.Called(mustJson(payload), mustJson(parentBeaconBlockRoot))
	return out.Get(0).(*eth.PayloadStatusV1), out.Error(1)
}

func (m *MockEngine) ExpectNewPayload(payload *eth.ExecutionPayload, parentBeaconBlockRoot *common.Hash, result *eth.PayloadStatusV1, err error) {
	m.Mock.On("NewPayload", mustJson(payload), mustJson(parentBeaconBlockRoot)).Once().Return(result, err)
}

// NewKeystone mocks the engine_newKeystone RPC call
func (m *MockEngine) NewKeystone(ctx context.Context, keystone hemi.L2Keystone) (*eth.KeystoneStatus, error) {
	out := m.Mock.Called(keystone)
	return out.Get(0).(*eth.KeystoneStatus), out.Error(1)
}

func (m *MockEngine) ExpectNewKeystone(keystone hemi.L2Keystone, status *eth.KeystoneStatus, err error) {
	m.Mock.On("NewKeystone", keystone).Once().Return(status, err)
}

// PopPayoutsByL2Keystone mocks the engine_popPayoutsByL2Keystone RPC call (V1)
func (m *MockEngine) PopPayoutsByL2Keystone(ctx context.Context, abrevHash chainhash.Hash) ([]eth.PopPayout, error) {
	out := m.Mock.Called(abrevHash)
	// Handle nil case to avoid panic on type assertion
	if out.Get(0) == nil {
		return nil, out.Error(1)
	}
	return out.Get(0).([]eth.PopPayout), out.Error(1)
}

func (m *MockEngine) ExpectPopPayoutsByL2Keystone(abrevHash chainhash.Hash, payouts []eth.PopPayout, err error) {
	m.Mock.On("PopPayoutsByL2Keystone", abrevHash).Once().Return(payouts, err)
}

// PopPublicationsByL2Keystone mocks the engine_popPublicationsByL2Keystone RPC call (V2)
func (m *MockEngine) PopPublicationsByL2Keystone(ctx context.Context, abrevHash chainhash.Hash) ([]eth.PopPublication, error) {
	out := m.Mock.Called(abrevHash)
	// Handle nil case to avoid panic on type assertion
	if out.Get(0) == nil {
		return nil, out.Error(1)
	}
	return out.Get(0).([]eth.PopPublication), out.Error(1)
}

func (m *MockEngine) ExpectPopPublicationsByL2Keystone(abrevHash chainhash.Hash, publications []eth.PopPublication, err error) {
	m.Mock.On("PopPublicationsByL2Keystone", abrevHash).Once().Return(publications, err)
}

// PayloadByNumber mocks the eth_getBlockByNumber RPC call
func (m *MockEngine) PayloadByNumber(ctx context.Context, number uint64) (*eth.ExecutionPayloadEnvelope, error) {
	out := m.Mock.Called(number)
	return out.Get(0).(*eth.ExecutionPayloadEnvelope), out.Error(1)
}

func (m *MockEngine) ExpectPayloadByNumber(number uint64, payload *eth.ExecutionPayloadEnvelope, err error) {
	m.Mock.On("PayloadByNumber", number).Once().Return(payload, err)
}

// PayloadByHash mocks getting a payload by its hash
func (m *MockEngine) PayloadByHash(ctx context.Context, hash common.Hash) (*eth.ExecutionPayloadEnvelope, error) {
	out := m.Mock.Called(hash)
	return out.Get(0).(*eth.ExecutionPayloadEnvelope), out.Error(1)
}

func (m *MockEngine) ExpectPayloadByHash(hash common.Hash, payload *eth.ExecutionPayloadEnvelope, err error) {
	m.Mock.On("PayloadByHash", hash).Once().Return(payload, err)
}

func mustJson[E any](elem E) string {
	data, err := json.MarshalIndent(elem, "  ", "  ")
	if err != nil {
		panic(err)
	}
	return string(data)
}
