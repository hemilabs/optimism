package client

import (
	"context"
	"github.com/hemilabs/heminetwork/api/bssapi"
	"github.com/hemilabs/heminetwork/hemi"
)

type NoOpBssClient struct {
}

func (bssc *NoOpBssClient) NotifyL2Keystone(ctx context.Context, keystone hemi.L2Keystone) error {
	return nil
}

func (bssc *NoOpBssClient) GetPoPPayouts(ctx context.Context, keystoneForPayout hemi.L2Keystone) ([]bssapi.PopPayout, error) {
	emptyPopPayouts := make([]bssapi.PopPayout, 0)
	return emptyPopPayouts, nil
}

func (bssc *NoOpBssClient) BtcFinalityByRecentKeystones(ctx context.Context, numRecentKeystones uint32) ([]hemi.L2BTCFinality, error) {
	emptyBtcFinalities := make([]hemi.L2BTCFinality, 0)
	return emptyBtcFinalities, nil
}

func (bssc *NoOpBssClient) BtcFinalityByKeystones(ctx context.Context, l2Keystones []hemi.L2Keystone) ([]hemi.L2BTCFinality, error) {
	emptyBtcFinalities := make([]hemi.L2BTCFinality, 0)
	return emptyBtcFinalities, nil
}

func (bssc *NoOpBssClient) Run(ctx context.Context) error {
	return nil
}
