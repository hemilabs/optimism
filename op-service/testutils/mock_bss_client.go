package testutils

import (
	"context"
	"github.com/hemilabs/heminetwork/api/bssapi"
	"github.com/hemilabs/heminetwork/hemi"
	"github.com/stretchr/testify/mock"
)

type MockBssClient struct {
	mock.Mock
}

func (m *MockBssClient) NotifyL2Keystone(ctx context.Context, keystone hemi.L2Keystone) error {
	out := m.Mock.MethodCalled("NotifyL2Keystone", keystone)
	return *out[1].(*error)
}

func (m *MockBssClient) ExpectNotifyL2Keystone(keystone hemi.L2Keystone, err error) {
	m.Mock.On("NotifyL2Keystone", keystone).Once().Return(&err)
}

func (m *MockBssClient) GetPoPPayouts(ctx context.Context, keystoneForPayout hemi.L2Keystone) ([]bssapi.PopPayout, error) {
	out := m.Mock.MethodCalled("GetPoPPayouts", keystoneForPayout)
	return out[0].([]bssapi.PopPayout), *out[1].(*error)
}

func (m *MockBssClient) ExpectGetPoPPayouts(keystoneForPayout hemi.L2Keystone, popPayout []bssapi.PopPayout, err error) {
	m.Mock.On("GetPoPPayouts", keystoneForPayout).Once().Return(popPayout, &err)
}

func (m *MockBssClient) BtcFinalityByRecentKeystones(ctx context.Context, numRecentKeystones uint32) ([]hemi.L2BTCFinality, error) {
	out := m.Mock.MethodCalled("BtcFinalityByRecentKeystones", numRecentKeystones)
	return out[0].([]hemi.L2BTCFinality), *out[1].(*error)
}

func (m *MockBssClient) ExpectBtcFinalityByRecentKeystones(numRecentKeystones uint32, btcFinalities []hemi.L2BTCFinality, err error) {
	m.Mock.On("BtcFinalityByRecentKeystones", numRecentKeystones).Once().Return(btcFinalities, &err)
}

func (m *MockBssClient) BtcFinalityByKeystones(ctx context.Context, l2Keystones []hemi.L2Keystone) ([]hemi.L2BTCFinality, error) {
	out := m.Mock.MethodCalled("BtcFinalityByKeystones", l2Keystones)
	return out[0].([]hemi.L2BTCFinality), *out[1].(*error)
}

func (m *MockBssClient) ExpectBtcFinalityByKeystones(l2Keystones []hemi.L2Keystone, btcFinalities []hemi.L2BTCFinality, err error) {
	m.Mock.On("BtcFinalityByKeystones", l2Keystones).Once().Return(btcFinalities, &err)
}

func (m *MockBssClient) Run(ctx context.Context) error {
	return nil
}
