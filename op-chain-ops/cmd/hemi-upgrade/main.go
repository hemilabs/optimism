package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"encoding/hex"
	"crypto/ecdsa"
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v2"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"


	"github.com/ethereum-optimism/optimism/op-chain-ops/clients"
	"github.com/ethereum-optimism/optimism/op-chain-ops/genesis"
	"github.com/ethereum-optimism/optimism/op-chain-ops/safe"
	"github.com/ethereum-optimism/optimism/op-chain-ops/upgrades"

	"github.com/ethereum-optimism/optimism/op-bindings/bindings"

	"github.com/ethereum-optimism/superchain-registry/superchain"

)

// deployments contains the L1 addresses of the contracts that are being upgraded to.
// Note that the key is the L2 chain id. This is because the L1 contracts must be specific
// for a particular OP Stack chain and cannot currently be used by multiple chains.
var deployments = map[uint64]superchain.ImplementationList{
	// Hemi Sepolia, addresses were created with IMPL_SALT=something4
	743111: {
		L1CrossDomainMessenger: superchain.VersionedContract{
			Version: "2.3.0",
			Address: superchain.HexToAddress("0x07A160b7EaE398586A45748427b04b637Cbde769"),
		},
		L1ERC721Bridge: superchain.VersionedContract{
			Version: "2.1.0",
			Address: superchain.HexToAddress("0x9364e51948008a68c1639B511757bf3889cF9E84"),
		},
		L1StandardBridge: superchain.VersionedContract{
			Version: "2.1.0",
			Address: superchain.HexToAddress("0xF8E745A3F14C5A23Ac42AfE0F2EF0265342ceD06"),
		},
		OptimismPortal: superchain.VersionedContract{
			Version: "2.5.0",
			Address: superchain.HexToAddress("0x69b4e5eDaeE67295458BF57D769824B34b5d83AF"),
		},
		SystemConfig: superchain.VersionedContract{
			Version: "1.12.0",
			Address: superchain.HexToAddress("0x2425dA120dd224126DdB9a6Ed5A23322AD6f8901"),
		},
		L2OutputOracle: superchain.VersionedContract{
			Version: "1.8.0",
			Address: superchain.HexToAddress("0x393D882ba5C1d86Df0B4887b59aC2a22C68D0232"),
		},
		OptimismMintableERC20Factory: superchain.VersionedContract{
			Version: "1.9.0",
			Address: superchain.HexToAddress("0x2C902480463aA98078eB5e14e31F288a0e9675ea"),
		},
	},
}

var proxyAddresses = map[uint64]*superchain.AddressList{
	743111: {
		AddressManager: superchain.HexToAddress("0x23f0022354241fdb721dc43e7897d7af662a2995"),
		L1CrossDomainMessengerProxy: superchain.HexToAddress("0x9bcccf1d222539c4c47e4c6f5749e4d5fa33215c"),
		L1ERC721BridgeProxy: superchain.HexToAddress("0xa5ba2558b41f34f0b5cc4ed389386201a3d31aec"),
		L1StandardBridgeProxy: superchain.HexToAddress("0xc94b1bee63a3e101fe5f71c80f912b4f4b055925"),
		L2OutputOracleProxy: superchain.HexToAddress("0x032d1e1dd960a4b027a9a35ff8b2b672e333bc27"),
		OptimismMintableERC20FactoryProxy: superchain.HexToAddress("0xb4bCe3efD3282Da4eEC69429966a85f92298799B"),
		OptimismPortalProxy: superchain.HexToAddress("0xB6f9579980aE46f61217A99145645341E49E2516"),
		ProxyAdmin: superchain.HexToAddress("0xc43ED1E8D70d0e5801514833fAD3D93Ba16Da4Aa"),
	},
}

var chainConfigs = map[uint64]*superchain.ChainConfig{
	// Clayton note: audit this
	743111: {
		Name: "Hemi Sepolia",
		ChainID: 743111,
		SystemConfigAddr: superchain.HexToAddress("0xfa73580F4D72294Ae9EE3DAaC36D8bF111B37Ce9"),
	},
}

func main() {
	log.Root().SetHandler(log.StreamHandler(os.Stderr, log.TerminalFormat(isatty.IsTerminal(os.Stderr.Fd()))))

	app := &cli.App{
		Name:  "op-upgrade",
		Usage: "Build transactions useful for upgrading the Superchain",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "l1-rpc-url",
				Value:    "http://127.0.0.1:8545",
				Usage:    "L1 RPC URL, the chain ID will be used to determine the superchain",
				Required: true,
				EnvVars:  []string{"L1_RPC_URL"},
			},
			&cli.StringFlag{
				Name:     "l2-chain-id",
				Value:    "743111",
				Usage:    "L2 CHAIN ID",
				Required: true,
				EnvVars:  []string{"L2_CHAIN_ID"},
			},
			&cli.PathFlag{
				Name:     "deploy-config",
				Usage:    "The path to the deploy config file",
				Required: true,
				EnvVars:  []string{"DEPLOY_CONFIG"},
			},
			&cli.PathFlag{
				Name:    "outfile",
				Usage:   "The file to write the output to. If not specified, output is written to stdout",
				EnvVars: []string{"OUTFILE"},
			},
			&cli.PathFlag{
				Name: "send-txs",
				Value: "true",
				Required: false,
				EnvVars: []string{"SEND_TXS"},
			},
			&cli.PathFlag{
				Name: "private-key",
				Required: true,
				EnvVars: []string{"PRIVATE_KEY"},
			},
		},
		Action: entrypoint,
	}

	if err := app.Run(os.Args); err != nil {
		log.Crit("error op-upgrade", "err", err)
	}
}

// entrypoint contains the main logic of the script
func entrypoint(ctx *cli.Context) error {
	config, err := genesis.NewDeployConfig(ctx.Path("deploy-config"))
	if err != nil {
		return err
	}
	if err := config.Check(); err != nil {
		return fmt.Errorf("error checking deploy config: %w", err)
	}

	clients, err := clients.NewClients(ctx.String("l1-rpc-url"), "")
	if err != nil {
		return fmt.Errorf("cannot create RPC clients: %w", err)
	}
	if clients.L1Client == nil {
		return errors.New("Cannot create L1 client")
	}

	l1ChainID, err := clients.L1Client.ChainID(ctx.Context)
	if err != nil {
		return fmt.Errorf("cannot fetch L1 chain ID: %w", err)
	}
	l2ChainID := ctx.Uint64("l2-chain-id")

	log.Info("connected to chains", "l1-chain-id", l1ChainID, "l2-chain-id", l2ChainID)

	// Create a batch of transactions
	batch := safe.Batch{}

	list, ok := deployments[l2ChainID]
	if !ok {
		return fmt.Errorf("no implementations for chain ID %d", l2ChainID)
	}

	chainConfig, ok := chainConfigs[l2ChainID]
	if !ok {
		return fmt.Errorf("no chain config for chain ID %d", l2ChainID)
	}

	log.Info("Upgrading to the following versions")
	log.Info("L1CrossDomainMessenger", "version", list.L1CrossDomainMessenger.Version, "address", list.L1CrossDomainMessenger.Address)
	log.Info("L1ERC721Bridge", "version", list.L1ERC721Bridge.Version, "address", list.L1ERC721Bridge.Address)
	log.Info("L1StandardBridge", "version", list.L1StandardBridge.Version, "address", list.L1StandardBridge.Address)
	log.Info("L2OutputOracle", "version", list.L2OutputOracle.Version, "address", list.L2OutputOracle.Address)
	log.Info("OptimismMintableERC20Factory", "version", list.OptimismMintableERC20Factory.Version, "address", list.OptimismMintableERC20Factory.Address)
	log.Info("OptimismPortal", "version", list.OptimismPortal.Version, "address", list.OptimismPortal.Address)
	log.Info("SystemConfig", "version", list.SystemConfig.Version, "address", list.SystemConfig.Address)

	if err := upgrades.CheckL1(ctx.Context, &list, clients.L1Client); err != nil {
		return fmt.Errorf("error checking L1 contracts: %w", err)
	}

	var proxyAddressesToUse *superchain.AddressList
	if proxyAddressesToUse, ok = proxyAddresses[l2ChainID]; !ok {
		return fmt.Errorf("could not find proxy addresses for l2 chain id %d", l2ChainID)
	}

	// Build the batch
	if err := upgrades.L1(&batch, list, *proxyAddressesToUse, config, chainConfig, clients.L1Client); err != nil {
		return fmt.Errorf("cannot build L1 upgrade batch: %w", err)
	}

	// Write the batch to disk or stdout
	if outfile := ctx.Path("outfile"); outfile != "" {
		if err := writeJSON(outfile, batch); err != nil {
			return err
		}
	} else {
		data, err := json.MarshalIndent(batch, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	}

	for _, tx := range batch.Transactions {
		dataStr := fmt.Sprintf("0x%s", hex.EncodeToString(tx.Data))
		log.Info("found transaction", "to", tx.To.String(), "data", dataStr)

		privateKeyArg := ctx.String("private-key")

		privateKey, err := crypto.HexToECDSA(privateKeyArg)
		if err != nil {
			return fmt.Errorf("could not parse private key: %s", err)
		}

		publicKey := privateKey.Public()

		publicKeyEcdsa, ok := publicKey.(*ecdsa.PublicKey)
		if ! ok {
			return fmt.Errorf("failed to create ecdsa public key")
		}

		address := crypto.PubkeyToAddress(*publicKeyEcdsa)

		log.Info("my info", "public key", publicKey, "address", address)

		nonce, err := clients.L1Client.NonceAt(ctx.Context, address, nil)
		if err != nil {
			return fmt.Errorf("could not get none for address: %s", err)
		}

		dynamicFeeTx := types.LegacyTx{
			Nonce: nonce,
			To: &tx.To,
			Data: tx.Data,
		}

		txToSign := types.NewTx(&dynamicFeeTx)

		signer := types.NewEIP155Signer(l1ChainID)

		signedTx, err := types.SignTx(txToSign, signer, privateKey)
		if err != nil {
			return fmt.Errorf("failed to sign tx: %s", err)
		}

		safe, err := bindings.NewSafeV130Transactor(
			common.HexToAddress("0x382D0AA958998408DD7695c8965C46BdaBBC3003"),
			clients.L1Client,
		)
		if err != nil {
			return fmt.Errorf("could not create safe: %s", err)
		}

		r, s, v := signedTx.RawSignatureValues()

		signatures := []byte{}
		signatures = append(signatures, s.Bytes()...)
		signatures = append(signatures, r.Bytes()...)
		signatures = append(signatures, v.Bytes()...)

		bigZero := big.NewInt(0)

		safeTx, err := safe.ExecTransaction(
			&bind.TransactOpts{},
			*signedTx.To(),
			bigZero,
			signedTx.Data(),
			1 /* operation? */,
			bigZero,
			bigZero,
			bigZero,
			common.HexToAddress("0x"),
			common.HexToAddress("0x"),
			signatures,
		)
		if err != nil {
			return fmt.Errorf("could not create safe.ExecTransaction: %s", err)
		}

		log.Info("created signed transaction", "hash", signedTx.Hash())

		err = clients.L1Client.SendTransaction(ctx.Context, safeTx)
		if err != nil {
			return fmt.Errorf("could not send tx: %s", err)
		}
	}

	return nil
}

func writeJSON(outfile string, input interface{}) error {
	f, err := os.OpenFile(outfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(input)
}
