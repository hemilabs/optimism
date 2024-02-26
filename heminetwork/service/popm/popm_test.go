// Copyright (c) 2024 Hemi Labs, Inc.
// Use of this source code is governed by the MIT License,
// which can be found in the LICENSE file.

package popm

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btctxscript "github.com/btcsuite/btcd/txscript"
	btcwire "github.com/btcsuite/btcd/wire"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/go-test/deep"
	"nhooyr.io/websocket"

	"github.com/hemilabs/heminetwork/api/auth"
	"github.com/hemilabs/heminetwork/api/bfgapi"
	"github.com/hemilabs/heminetwork/api/protocol"
	"github.com/hemilabs/heminetwork/bitcoin"
	"github.com/hemilabs/heminetwork/hemi"
	"github.com/hemilabs/heminetwork/hemi/pop"
)

const (
	EventConnected = "event_connected"
)

func TestBTCPrivateKeyFromHex(t *testing.T) {
	tests := []struct {
		input string
		want  []byte
	}{
		{
			input: "0000000000000000000000000000000000000000000000000000000000000001",
			want: []byte{
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
			},
		},
		{
			input: "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364140",
			want: []byte{
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe,
				0xba, 0xae, 0xdc, 0xe6, 0xaf, 0x48, 0xa0, 0x3b,
				0xbf, 0xd2, 0x5e, 0x8c, 0xd0, 0x36, 0x41, 0x40,
			},
		},
		{
			input: "0000000000000000000000000000000000000000000000000000000000000000",
			want:  nil,
		},
		{
			input: "fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141",
			want:  nil,
		},
		{
			input: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			want:  nil,
		},
	}
	for i, test := range tests {
		got, err := bitcoin.PrivKeyFromHexString(test.input)
		switch {
		case test.want == nil && err == nil:
			t.Errorf("Test %d - succeeded, want error", i)
		case test.want != nil && err != nil:
			t.Errorf("Test %d - failed with error: %v", i, err)
		case test.want != nil && err == nil:
			if !bytes.Equal(got.Serialize(), test.want) {
				t.Errorf("Test %d - got private key %x, want %x", i, got.Serialize(), test.want)
			}
		}
	}
}

func TestNewMiner(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.BTCPrivateKey = "ebaaedce6af48a03bbfd25e8cd0364140ebaaedce6af48a03bbfd25e8cd03641"

	m, err := NewMiner(cfg)
	if err != nil {
		t.Fatalf("Failed to create new miner: %v", err)
	}

	got, want := m.btcAddress.EncodeAddress(), "mnwAf6TWJK1MjbKkK9rq8MGvWBRUuo3PJk"
	if got != want {
		t.Errorf("Got BTC pubkey hash address %q, want %q", got, want)
	}
	got, want = m.btcAddress.String(), "mnwAf6TWJK1MjbKkK9rq8MGvWBRUuo3PJk"
	if got != want {
		t.Errorf("Got BTC pubkey hash address %q, want %q", got, want)
	}
}

// TestProcessReceivedKeystones ensures that we store the latest keystone
// correctly as well as data stored in slices within the struct
func TestProcessReceivedKeystones(t *testing.T) {
	firstBatchOfL2Keystones := []hemi.L2Keystone{
		{
			L2BlockNumber: 3,
			EPHash:        []byte{3},
		},
		{
			L2BlockNumber: 2,
			EPHash:        []byte{2},
		},
		{
			L2BlockNumber: 1,
			EPHash:        []byte{1},
		},
	}

	secondBatchOfL2Keystones := []hemi.L2Keystone{
		{
			L2BlockNumber: 6,
			EPHash:        []byte{6},
		},
		{
			L2BlockNumber: 5,
			EPHash:        []byte{5},
		},
		{
			L2BlockNumber: 4,
			EPHash:        []byte{4},
		},
	}

	miner := Miner{}

	miner.processReceivedKeystones(context.Background(), firstBatchOfL2Keystones)
	diff := deep.Equal(*miner.lastKeystone, hemi.L2Keystone{
		L2BlockNumber: 3,
		EPHash:        []byte{3},
	})

	if len(diff) != 0 {
		t.Fatalf("unexpected diff: %v", diff)
	}

	miner.processReceivedKeystones(context.Background(), secondBatchOfL2Keystones)
	diff = deep.Equal(*miner.lastKeystone, hemi.L2Keystone{
		L2BlockNumber: 6,
		EPHash:        []byte{6},
	})

	if len(diff) != 0 {
		t.Fatalf("unexpected diff: %v", diff)
	}
}

func TestCreateTxVersion2(t *testing.T) {
	l2Keystone := hemi.L2Keystone{}

	utxo := bfgapi.BitcoinUTXO{
		Hash: make([]byte, 32),
	}

	mockPayToScript := []byte{}

	btx, err := createTx(&l2Keystone, 1, &utxo, mockPayToScript, 1)
	if err != nil {
		t.Fatal(err)
	}

	if btx.Version != 2 {
		t.Fatalf("the tx version must be 2, received %d", btx.Version)
	}
}

func TestCreateTxLockTime(t *testing.T) {
	l2Keystone := hemi.L2Keystone{}

	utxo := bfgapi.BitcoinUTXO{
		Hash: make([]byte, 32),
	}

	mockPayToScript := []byte{}

	var height uint64 = 99

	btx, err := createTx(&l2Keystone, height, &utxo, mockPayToScript, 1)
	if err != nil {
		t.Fatal(err)
	}

	if uint64(btx.LockTime) != height {
		t.Fatalf("received unexpected lock time %d", btx.LockTime)
	}
}

func TestCreateTxTxIn(t *testing.T) {
	l2Keystone := hemi.L2Keystone{}

	utxo := bfgapi.BitcoinUTXO{
		Hash:  make([]byte, 32),
		Index: 5,
		Value: 10,
	}

	copy(utxo.Hash, []byte{1, 2, 3})

	mockPayToScript := []byte{4, 5, 6}

	var height uint64 = 99

	var feeAmount int64 = 10

	btx, err := createTx(&l2Keystone, height, &utxo, mockPayToScript, feeAmount)
	if err != nil {
		t.Fatal(err)
	}

	outPoint := btcwire.OutPoint{
		Hash:  btcchainhash.Hash(utxo.Hash),
		Index: utxo.Index,
	}

	expectedTxIn := []*btcwire.TxIn{btcwire.NewTxIn(&outPoint, mockPayToScript, nil)}

	diff := deep.Equal(expectedTxIn, btx.TxIn)
	if len(diff) != 0 {
		t.Fatalf("got unexpected diff %s", diff)
	}
}

func TestCreateTxTxOutPayTo(t *testing.T) {
	l2Keystone := hemi.L2Keystone{}

	utxo := bfgapi.BitcoinUTXO{
		Hash:  make([]byte, 32),
		Index: 5,
		Value: 10,
	}

	copy(utxo.Hash, []byte{1, 2, 3})

	mockPayToScript := []byte{4, 5, 6}

	var height uint64 = 99

	var feeAmount int64 = 10

	btx, err := createTx(&l2Keystone, height, &utxo, mockPayToScript, feeAmount)
	if err != nil {
		t.Fatal(err)
	}

	expectexTxOut := btcwire.NewTxOut(utxo.Value-feeAmount, mockPayToScript)
	diff := deep.Equal(expectexTxOut, btx.TxOut[0])
	if len(diff) != 0 {
		t.Fatalf("got unexpected diff %s", diff)
	}
}

func TestCreateTxTxOutPopTx(t *testing.T) {
	l2Keystone := hemi.L2Keystone{}

	utxo := bfgapi.BitcoinUTXO{
		Hash:  make([]byte, 32),
		Index: 5,
		Value: 10,
	}

	copy(utxo.Hash, []byte{1, 2, 3})

	mockPayToScript := []byte{4, 5, 6}

	var height uint64 = 99

	var feeAmount int64 = 10

	btx, err := createTx(&l2Keystone, height, &utxo, mockPayToScript, feeAmount)
	if err != nil {
		t.Fatal(err)
	}

	aks := hemi.L2KeystoneAbbreviate(l2Keystone)
	popTx := pop.TransactionL2{L2Keystone: aks}
	popTxOpReturn, err := popTx.EncodeToOpReturn()
	if err != nil {
		t.Fatalf("failed to encode PoP transaction: %v", err)
	}

	expectexTxOut := btcwire.NewTxOut(0, popTxOpReturn)
	diff := deep.Equal(expectexTxOut, btx.TxOut[1])
	if len(diff) != 0 {
		t.Fatalf("got unexpected diff %s", diff)
	}
}

func TestSignTx(t *testing.T) {
	type TestTableItem struct {
		name          string
		expectedError error
		l2Keystone    hemi.L2Keystone
		utxo          bfgapi.BitcoinUTXO
		utxoHash      []byte
		payToScript   []byte
		height        uint64
		feeAmount     int64
		keyPair       func() (*dcrsecp256k1.PrivateKey, *dcrsecp256k1.PublicKey)
	}

	testTable := []TestTableItem{
		{
			name:       "Test Sign Tx",
			l2Keystone: hemi.L2Keystone{},
			utxo: bfgapi.BitcoinUTXO{
				Hash:  make([]byte, 32),
				Index: 5,
				Value: 10,
			},
			utxoHash:    []byte{1, 2, 3},
			payToScript: []byte{4, 5, 6, 7, 8},
			height:      99,
			feeAmount:   10,
			keyPair: func() (*dcrsecp256k1.PrivateKey, *dcrsecp256k1.PublicKey) {
				privateKey, err := dcrsecp256k1.GeneratePrivateKey()
				if err != nil {
					t.Fatal(err)
				}

				publicKey := privateKey.PubKey()

				return privateKey, publicKey
			},
		},
		{
			name:       "Test Sign Tx key mismatch",
			l2Keystone: hemi.L2Keystone{},
			utxo: bfgapi.BitcoinUTXO{
				Hash:  make([]byte, 32),
				Index: 5,
				Value: 10,
			},
			utxoHash:    []byte{1, 2, 3},
			payToScript: []byte{4, 5, 6, 7, 8},
			height:      99,
			feeAmount:   10,
			keyPair: func() (*dcrsecp256k1.PrivateKey, *dcrsecp256k1.PublicKey) {
				privateKey, err := dcrsecp256k1.GeneratePrivateKey()
				if err != nil {
					t.Fatal(err)
				}

				otherPrivateKey, err := dcrsecp256k1.GeneratePrivateKey()
				if err != nil {
					t.Fatal(err)
				}

				publicKey := otherPrivateKey.PubKey()

				return privateKey, publicKey
			},
			expectedError: errors.New("wrong public key for private key"),
		},
	}

	for _, testTableItem := range testTable {
		t.Run(testTableItem.name, func(t *testing.T) {
			copy(testTableItem.utxo.Hash, testTableItem.utxoHash)
			btx, err := createTx(
				&testTableItem.l2Keystone,
				testTableItem.height,
				&testTableItem.utxo,
				testTableItem.payToScript,
				testTableItem.feeAmount,
			)
			if err != nil {
				t.Fatal(err)
			}

			sigHash, err := btctxscript.CalcSignatureHash(
				testTableItem.payToScript,
				btctxscript.SigHashAll,
				btx,
				0,
			)
			if err != nil {
				t.Fatalf("failed to calculate signature hash: %v", err)
			}

			privateKey, publicKey := testTableItem.keyPair()

			err = bitcoin.SignTx(btx, testTableItem.payToScript, privateKey, publicKey)

			if testTableItem.expectedError != nil {
				if err == nil {
					t.Fatal("expected error, received nil")
				} else {
					if testTableItem.expectedError.Error() != err.Error() {
						t.Fatalf("unexpected error: %s", err)
					}
					return
				}
			} else if err != nil {
				t.Fatal(err)
			}

			pubKeyBytes := publicKey.SerializeCompressed()
			sig := dcrecdsa.Sign(privateKey, sigHash)
			sigBytes := append(sig.Serialize(), byte(btctxscript.SigHashAll))
			sigScript, err := btctxscript.
				NewScriptBuilder().AddData(sigBytes).AddData(pubKeyBytes).Script()
			if err != nil {
				t.Fatalf("failed to build signature script: %v", err)
			}

			diff := deep.Equal(sigScript, btx.TxIn[0].SignatureScript)
			if len(diff) != 0 {
				t.Fatalf("unexpected diff %s", diff)
			}
		})
	}
}

func TestSignTxDifferingPubPrivKeys(t *testing.T) {
	l2Keystone := hemi.L2Keystone{}

	utxo := bfgapi.BitcoinUTXO{
		Hash:  make([]byte, 32),
		Index: 5,
		Value: 10,
	}

	copy(utxo.Hash, []byte{1, 2, 3})

	mockPayToScript := []byte("something")

	var height uint64 = 99

	var feeAmount int64 = 10

	btx, err := createTx(&l2Keystone, height, &utxo, mockPayToScript, feeAmount)
	if err != nil {
		t.Fatal(err)
	}

	privateKey, err := dcrsecp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	otherPrivateKey, err := dcrsecp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	publicKey := otherPrivateKey.PubKey()

	err = bitcoin.SignTx(btx, mockPayToScript, privateKey, publicKey)
	if err == nil || err.Error() != "wrong public key for private key" {
		t.Fatalf("unexpected error %s", err)
	}
}

// TestProcessReceivedInAscOrder ensures that we sort and process the latest
// N (3) L2Keystones in ascending order to handle the oldest first
func TestProcessReceivedInAscOrder(t *testing.T) {
	firstBatchOfL2Keystones := []hemi.L2Keystone{
		{
			L2BlockNumber: 3,
			EPHash:        []byte{3},
		},
		{
			L2BlockNumber: 2,
			EPHash:        []byte{2},
		},
		{
			L2BlockNumber: 1,
			EPHash:        []byte{1},
		},
	}

	miner, err := NewMiner(&Config{
		BTCPrivateKey: "ebaaedce6af48a03bbfd25e8cd0364140ebaaedce6af48a03bbfd25e8cd03641",
		BTCChainName:  "testnet3",
	})
	if err != nil {
		t.Fatal(err)
	}
	miner.processReceivedKeystones(context.Background(), firstBatchOfL2Keystones)

	receivedKeystones := []hemi.L2Keystone{}

	for {
		select {
		case l2Keystone := <-miner.keystoneCh:
			receivedKeystones = append(receivedKeystones, *l2Keystone)
			continue
		default:
			break
		}
		break
	}

	diff := deep.Equal(firstBatchOfL2Keystones, receivedKeystones)
	if len(diff) != 0 {
		t.Fatalf("received unexpected diff: %s", diff)
	}
}

func TestConnectToBFGAndPerformMineWithAuth(t *testing.T) {
	privateKey, err := dcrsecp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	publicKey := hex.EncodeToString(privateKey.PubKey().SerializeCompressed())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, msgCh, cleanup := createMockBFG(ctx, t, []string{publicKey})
	defer cleanup()

	go func() {
		miner, err := NewMiner(&Config{
			BFGWSURL:      server.URL + bfgapi.RouteWebsocketPublic,
			BTCChainName:  "testnet3",
			BTCPrivateKey: hex.EncodeToString(privateKey.Serialize()),
		})
		if err != nil {
			panic(err)
		}

		err = miner.Run(ctx)
		if err != nil && err != context.Canceled {
			panic(err)
		}
	}()

	// we can't guarantee order here, so test that we get all expected messages
	// from popm within the timeout

	messagesReceived := make(map[string]bool)

	messagesExpected := []protocol.Command{
		EventConnected,
		bfgapi.CmdL2KeystonesRequest,
		bfgapi.CmdBitcoinInfoRequest,
		bfgapi.CmdBitcoinBalanceRequest,
		bfgapi.CmdBitcoinUTXOsRequest,
		bfgapi.CmdBitcoinBroadcastRequest,
	}

	for {
		select {
		case msg := <-msgCh:
			t.Logf("received message %v", msg)
			messagesReceived[msg] = true
		case <-ctx.Done():
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
		}
		missing := false
		for _, m := range messagesExpected {
			if !messagesReceived[fmt.Sprintf("%s", m)] {
				t.Logf("still missing message %v", m)
				missing = true
			}
		}
		if missing == false {
			break
		}
	}
}

func TestConnectToBFGAndPerformMine(t *testing.T) {
	privateKey, err := dcrsecp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, msgCh, cleanup := createMockBFG(ctx, t, []string{})
	defer cleanup()

	go func() {
		miner, err := NewMiner(&Config{
			BFGWSURL:      server.URL + bfgapi.RouteWebsocketPublic,
			BTCChainName:  "testnet3",
			BTCPrivateKey: hex.EncodeToString(privateKey.Serialize()),
		})
		if err != nil {
			panic(err)
		}

		err = miner.Run(ctx)
		if err != nil && err != context.Canceled {
			panic(err)
		}
	}()

	// we can't guarantee order here, so test that we get all expected messages
	// from popm within the timeout

	messagesReceived := make(map[string]bool)

	messagesExpected := []protocol.Command{
		EventConnected,
		bfgapi.CmdL2KeystonesRequest,
		bfgapi.CmdBitcoinInfoRequest,
		bfgapi.CmdBitcoinBalanceRequest,
		bfgapi.CmdBitcoinUTXOsRequest,
		bfgapi.CmdBitcoinBroadcastRequest,
	}

	for {
		select {
		case msg := <-msgCh:
			t.Logf("received message %v", msg)
			messagesReceived[msg] = true
		case <-ctx.Done():
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
		}
		missing := false
		for _, m := range messagesExpected {
			if !messagesReceived[fmt.Sprintf("%s", m)] {
				t.Logf("still missing message %v", m)
				missing = true
			}
		}
		if missing == false {
			break
		}
	}
}

func TestConnectToBFGAndPerformMineWithAuthError(t *testing.T) {
	privateKey, err := dcrsecp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, msgCh, cleanup := createMockBFG(ctx, t, []string{"incorrect"})
	defer cleanup()

	miner, err := NewMiner(&Config{
		BFGWSURL:      server.URL + bfgapi.RouteWebsocketPublic,
		BTCChainName:  "testnet3",
		BTCPrivateKey: hex.EncodeToString(privateKey.Serialize()),
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-msgCh:
			}
		}
	}()
	if err := miner.Run(ctx); err != nil {
		for err != nil {
			if errors.Is(err, protocol.PublicKeyAuthError) {
				return
			}
			err = errors.Unwrap(err)
		}
		t.Fatalf("want protocol.PublicKeyAuthError, got: %v", err)
	}
}

func createMockBFG(ctx context.Context, t *testing.T, publicKeys []string) (*httptest.Server, chan string, func()) {
	msgCh := make(chan string)

	handler := func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			panic(err)
		}

		defer func() {
			if err := c.Close(websocket.StatusNormalClosure, ""); err != nil {
				t.Logf("error closing websocket: %s", err)
			}
		}()

		conn := protocol.NewWSConn(c)

		go func() {
			select {
			case msgCh <- EventConnected:
			case <-ctx.Done():
				return
			}
		}()

		authServer, err := auth.NewSecp256k1AuthServer()
		if err != nil {
			t.Fatalf("could not create auth server: %s", err)
		}

		if err := authServer.HandshakeServer(r.Context(), conn); err != nil {
			t.Fatalf("error with server handshake: %s", err)
		}

		publicKey := authServer.RemotePublicKey().SerializeCompressed()

		publicKeyEncoded := hex.EncodeToString(publicKey)

		log.Tracef("successful handshake with public key: %s", publicKeyEncoded)
		if len(publicKeys) > 0 {

			found := false
			for _, v := range publicKeys {
				if publicKeyEncoded == v {
					found = true
				}
			}

			if !found {
				c.Close(protocol.PublicKeyAuthError.Code, protocol.PublicKeyAuthError.Reason)
				return
			}

			log.Infof("authorized connection with public key: %s", publicKeyEncoded)
		}

		if err := bfgapi.Write(ctx, conn, "someid", bfgapi.PingRequest{}); err != nil {
			panic(err)
		}

		if err := bfgapi.Write(ctx, conn, "someid", bfgapi.L2KeystonesNotification{}); err != nil {
			panic(err)
		}

		for {
			command, id, _, err := bfgapi.Read(ctx, conn)
			if err != nil {
				if !errors.Is(ctx.Err(), context.Canceled) {
					panic(err)
				}

				return
			}

			t.Logf("command is %s", command)

			go func() {
				select {
				case msgCh <- fmt.Sprintf("%s", command):
				case <-ctx.Done():
					return
				}
			}()

			if command == bfgapi.CmdL2KeystonesRequest {
				if err := bfgapi.Write(ctx, conn, id, bfgapi.L2KeystonesResponse{
					L2Keystones: []hemi.L2Keystone{
						{
							L2BlockNumber: 100,
						},
					},
				}); err != nil {
					if !errors.Is(ctx.Err(), context.Canceled) {
						panic(err)
					}
				}
			}

			if command == bfgapi.CmdBitcoinInfoRequest {
				if err := bfgapi.Write(ctx, conn, id, bfgapi.BitcoinInfoResponse{
					Height: 809,
				}); err != nil {
					if !errors.Is(ctx.Err(), context.Canceled) {
						panic(err)
					}
				}
			}

			if command == bfgapi.CmdBitcoinBalanceRequest {
				if err := bfgapi.Write(ctx, conn, id, bfgapi.BitcoinBalanceResponse{
					Unconfirmed: 809,
				}); err != nil {
					if !errors.Is(ctx.Err(), context.Canceled) {
						panic(err)
					}
				}
			}

			if command == bfgapi.CmdBitcoinUTXOsRequest {
				if err := bfgapi.Write(ctx, conn, id, bfgapi.BitcoinUTXOsResponse{
					UTXOs: []*bfgapi.BitcoinUTXO{
						{
							Index: 9999,
							Value: 999999,
							Hash: []byte{
								2, 1, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 10, 9, 8,
								7, 6, 5, 4, 3, 2, 1, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1,
							},
						},
					},
				}); err != nil {
					if !errors.Is(ctx.Err(), context.Canceled) {
						panic(err)
					}
				}
			}

			if command == bfgapi.CmdBitcoinBroadcastRequest {
				if err := bfgapi.Write(ctx, conn, id, bfgapi.BitcoinBroadcastResponse{
					TXID: []byte{
						2, 1, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 10, 9, 8,
						7, 6, 5, 4, 3, 2, 1, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1,
					},
				}); err != nil {
					if !errors.Is(ctx.Err(), context.Canceled) {
						panic(err)
					}
				}
			}

		}
	}

	s := httptest.NewServer(http.HandlerFunc(handler))

	return s, msgCh, func() {
		s.Close()
	}
}
