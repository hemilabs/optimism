package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/ethereum/go-ethereum/log"
	"github.com/hemilabs/heminetwork/api/bssapi"
	"github.com/hemilabs/heminetwork/api/protocol"
	"github.com/hemilabs/heminetwork/hemi"
)

const (
	defaultRequestTimeout = 10 * time.Second
	defaultHoldoffTimeout = 5 * time.Second
)

type BssClient interface {
	NotifyL2Keystone(ctx context.Context, keystone hemi.L2Keystone) error
	GetPoPPayouts(ctx context.Context, keystoneForPayout hemi.L2Keystone) ([]bssapi.PopPayout, error)
	BtcFinalityByRecentKeystones(ctx context.Context, numRecentKeystones uint32) ([]hemi.L2BTCFinality, error)
	BtcFinalityByKeystones(ctx context.Context, l2Keystones []hemi.L2Keystone) ([]hemi.L2BTCFinality, error)
	Run(ctx context.Context) error
}

type bssCmd struct {
	msg any
	ch  chan any
}

type LiveBssClient struct {
	log log.Logger
	cfg *BssEndpointConfig

	bssWG    sync.WaitGroup
	bssCmdCh chan bssCmd
}

type BssEndpointConfig struct {
	BSSURL string
}

func NewLiveBssClient(log log.Logger, cfg *BssEndpointConfig) (*LiveBssClient, error) {
	bssc := &LiveBssClient{
		log:      log,
		cfg:      cfg,
		bssCmdCh: make(chan bssCmd, 10),
	}

	return bssc, nil
}

func (bssc *LiveBssClient) callBSS(parentCtx context.Context, timeout time.Duration, msg any) (any, error) {
	bc := bssCmd{
		msg: msg,
		ch:  make(chan any),
	}

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	// attempt to send
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case bssc.bssCmdCh <- bc:
	default:
		return nil, fmt.Errorf("BSS command queue full")
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case payload := <-bc.ch:
		if err, ok := payload.(error); ok {
			return nil, err
		}
		return payload, nil
	}

	// Won't get here
}

func (bssc *LiveBssClient) NotifyL2Keystone(ctx context.Context, keystone hemi.L2Keystone) error {
	l2KeystoneRequest := bssapi.L2KeystoneRequest{
		L2Keystone: keystone,
	}

	_, err := bssc.callBSS(ctx, defaultRequestTimeout, &l2KeystoneRequest)
	if err != nil {
		return err
	}

	return err
}

func (bssc *LiveBssClient) GetPoPPayouts(ctx context.Context, keystoneForPayout hemi.L2Keystone) ([]bssapi.PopPayout, error) {
	abrevKeystone := hemi.L2KeystoneAbbreviate(keystoneForPayout)
	abrevKeystoneEnc := abrevKeystone.Serialize()

	getPoPPayoutsRequest := bssapi.PopPayoutsRequest{
		L2BlockForPayout: abrevKeystoneEnc[:],
	}

	payoutsResp, err := bssc.callBSS(ctx, defaultRequestTimeout, &getPoPPayoutsRequest)
	if err != nil {
		return nil, err
	}

	payouts := payoutsResp.(*bssapi.PopPayoutsResponse)

	return payouts.PopPayouts, nil
}

func (bssc *LiveBssClient) BtcFinalityByRecentKeystones(ctx context.Context, numRecentKeystones uint32) ([]hemi.L2BTCFinality, error) {
	btcFinalityByRecentKeystonesRequest := bssapi.BTCFinalityByRecentKeystonesRequest{
		NumRecentKeystones: numRecentKeystones,
	}

	btcFinalityResp, err := bssc.callBSS(ctx, defaultRequestTimeout, &btcFinalityByRecentKeystonesRequest)
	if err != nil {
		return nil, err
	}

	btcFinalities := btcFinalityResp.(*bssapi.BTCFinalityByRecentKeystonesResponse)

	return btcFinalities.L2BTCFinalities, nil
}

func (bssc *LiveBssClient) BtcFinalityByKeystones(ctx context.Context, l2Keystones []hemi.L2Keystone) ([]hemi.L2BTCFinality, error) {
	btcFinalityByKeystonesRequest := bssapi.BTCFinalityByKeystonesRequest{
		L2Keystones: l2Keystones,
	}

	btcFinalityResp, err := bssc.callBSS(ctx, defaultRequestTimeout, &btcFinalityByKeystonesRequest)
	if err != nil {
		return nil, err
	}

	btcFinalities := btcFinalityResp.(*bssapi.BTCFinalityByKeystonesResponse)

	return btcFinalities.L2BTCFinalities, nil
}

func (bssc *LiveBssClient) handleBSSCallCompletion(parentCtx context.Context, conn *protocol.Conn, bc bssCmd) {
	ctx, cancel := context.WithTimeout(parentCtx, defaultRequestTimeout)
	defer cancel()
	log.Trace("handleBSSCallCompletion sending command", "msg", bc.msg)

	_, _, payload, err := bssapi.Call(ctx, conn, bc.msg)
	log.Trace("handleBSSCallCompletion received response", "resp", payload)
	if err != nil {
		log.Error("handleBSSCallCompletion failed", "msg", spew.Sdump(bc.msg), "err", err)
		select {
		case bc.ch <- err:
		default:
		}
	}

	select {
	case bc.ch <- payload:
	default:
	}
}

func (bssc *LiveBssClient) handleBSSWebsocketCallUnauth(ctx context.Context, conn *protocol.Conn) {
	defer bssc.bssWG.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case bc := <-bssc.bssCmdCh:
			go bssc.handleBSSCallCompletion(ctx, conn, bc)
		}
	}
}

func (bssc *LiveBssClient) handleBSSWebsocketReadUnauth(ctx context.Context, conn *protocol.Conn) {
	defer bssc.bssWG.Done()

	for {
		// See if we were terminated
		select {
		case <-ctx.Done():
			return
		default:
		}

		cmd, rid, payload, err := bssapi.ReadConn(ctx, conn)
		if err != nil {
			log.Error("error reading from websocket", "err", err)
			time.Sleep(defaultHoldoffTimeout) // XXX exponential hold off?
			continue
		}

		log.Trace("handleBSSWebsocketReadUnauth read", "cmd",
			cmd, "rid", rid, "payload", payload)

		switch cmd {
		case bssapi.CmdPingRequest:
			p := payload.(*bssapi.PingRequest)
			reply := &bssapi.PingResponse{
				OriginTimestamp: p.Timestamp,
				Timestamp:       time.Now().Unix(),
			}
			if err := bssapi.Write(ctx, conn, rid, reply); err != nil {
				log.Error("error writing to websocket", "err", err)
			}
		case bssapi.CmdBTCFinalityNotification:
			log.Debug("Ignoring new BTC finality notification")
		case bssapi.CmdBTCNewBlockNotification:
			log.Debug("Ignoring new BTC block notification")
		default:
			log.Error("unknown command read from BSS", "cmd", cmd)
			return // XXX exit for now to cause a ruckus in the logs
		}
	}
}

func (bssc *LiveBssClient) bss(ctx context.Context) {
	for {
		if err := bssc.connectBSS(ctx); err != nil {
			// Do nothing
			log.Error("Error connecting to BSS", "url", bssc.cfg.BSSURL, "err", err)
		}
		// See if we were terminated
		select {
		case <-ctx.Done():
			return
		default:
		}

		// hold off reconnect for a couple of seconds
		// Exponential?
		time.Sleep(defaultHoldoffTimeout)
		log.Info("Reconnecting to BSS", "url", bssc.cfg.BSSURL)
	}
}

func (bssc *LiveBssClient) Run(ctx context.Context) error {
	go bssc.bss(ctx) // Attempt to talk to BSS
	return nil
}

func (bssc *LiveBssClient) connectBSS(ctx context.Context) error {
	url := bssc.cfg.BSSURL

	log.Info("Connecting to BSS websocket", "url", url)

	conn, err := protocol.NewConn(url, nil)
	if err != nil {
		return err
	}
	err = conn.Connect(ctx)
	if err != nil {
		return err
	}

	bssc.bssWG.Add(1)
	go bssc.handleBSSWebsocketCallUnauth(ctx, conn)

	bssc.bssWG.Add(1)
	go bssc.handleBSSWebsocketReadUnauth(ctx, conn)

	// Wait for exit
	bssc.bssWG.Wait()

	return nil
}
