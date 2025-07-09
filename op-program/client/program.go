package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	preimage "github.com/ethereum-optimism/optimism/op-preimage"
	"github.com/ethereum-optimism/optimism/op-program/client/boot"
	"github.com/ethereum-optimism/optimism/op-program/client/claim"
	"github.com/ethereum-optimism/optimism/op-program/client/interop"
	"github.com/ethereum-optimism/optimism/op-program/client/l1"
	"github.com/ethereum-optimism/optimism/op-program/client/l2"
	"github.com/ethereum-optimism/optimism/op-program/client/tasks"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethdb/memorydb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

var errInvalidConfig = errors.New("invalid config")

type Config struct {
	SkipValidation bool
	InteropEnabled bool
	DB             l2.KeyValueStore
	StoreBlockData bool
}

// Main executes the client program in a detached context and exits the current process.
// The client runtime environment must be preset before calling this function.
func Main(useInterop bool) {
	// Default to a machine parsable but relatively human friendly log format.
	// Don't do anything fancy to detect if color output is supported.
	logger := oplog.NewLogger(os.Stdout, oplog.CLIConfig{
		Level:  log.LevelInfo,
		Format: oplog.FormatLogFmt,
		Color:  false,
	})
	oplog.SetGlobalLogHandler(logger.Handler())

	logger.Info("Starting fault proof program client", "useInterop", useInterop)
	preimageOracle := preimage.ClientPreimageChannel()
	preimageHinter := preimage.ClientHinterChannel()
	config := Config{
		InteropEnabled: useInterop,
		DB:             memorydb.New(),
	}
	if err := RunProgram(logger, preimageOracle, preimageHinter, config); errors.Is(err, claim.ErrClaimNotValid) {
		log.Error("Claim is invalid", "err", err)
		os.Exit(1)
	} else if err != nil {
		log.Error("Program failed", "err", err)
		os.Exit(2)
	} else {
		log.Info("Claim successfully verified")
		os.Exit(0)
	}
}

// RunProgram executes the Program, while attached to an IO based pre-image oracle, to be served by a host.
func RunProgram(logger log.Logger, preimageOracle io.ReadWriter, preimageHinter io.ReadWriter, cfg Config) error {
	pClient := preimage.NewOracleClient(preimageOracle)
	hClient := preimage.NewHintWriter(preimageHinter)
	l1PreimageOracle := l1.NewCachingOracle(l1.NewPreimageOracle(pClient, hClient))
	l2PreimageOracle := l2.NewCachingOracle(l2.NewPreimageOracle(pClient, hClient, cfg.InteropEnabled))

	bootInfo := NewBootstrapClient(pClient).BootInfo()
	logger.Info("Program Bootstrapped", "bootInfo", bootInfo)
	return runDerivation(
		logger,
		bootInfo.RollupConfig,
		bootInfo.L2ChainConfig,
		bootInfo.L1Head,
		bootInfo.L2OutputRoot,
		bootInfo.L2Claim,
		bootInfo.L2ClaimBlockNumber,
		l1PreimageOracle,
		l2PreimageOracle,
	)
}

// runDerivation executes the L2 state transition, given a minimal interface to retrieve data.
func runDerivation(logger log.Logger, cfg *rollup.Config, l2Cfg *params.ChainConfig, l1Head common.Hash, l2OutputRoot common.Hash, l2Claim common.Hash, l2ClaimBlockNum uint64, l1Oracle l1.Oracle, l2Oracle l2.Oracle) error {
	l1Source := l1.NewOracleL1Client(logger, l1Oracle, l1Head)
	l1BlobsSource := l1.NewBlobFetcher(logger, l1Oracle)
	engineBackend, err := l2.NewOracleBackedL2Chain(logger, l2Oracle, l1Oracle /* kzg oracle */, l2Cfg, l2OutputRoot)
	if err != nil {
		return fmt.Errorf("failed to create oracle-backed L2 chain: %w", err)
	}
	l2Source := l2.NewOracleEngine(cfg, logger, engineBackend)

	logger.Info("Starting derivation")
	d := cldr.NewDriver(logger, cfg, l1Source, l1BlobsSource, l2Source, l2ClaimBlockNum)
	for {
		if err = d.Step(context.Background()); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
	}
	if cfg.InteropEnabled {
		bootInfo := boot.BootstrapInterop(pClient)
		return interop.RunInteropProgram(logger, bootInfo, l1PreimageOracle, l2PreimageOracle, !cfg.SkipValidation)
	}
	if cfg.DB == nil {
		return fmt.Errorf("%w: db config is required", errInvalidConfig)
	}
	bootInfo := boot.NewBootstrapClient(pClient).BootInfo()
	derivationOptions := tasks.DerivationOptions{StoreBlockData: cfg.StoreBlockData}
	return RunPreInteropProgram(logger, bootInfo, l1PreimageOracle, l2PreimageOracle, cfg.DB, derivationOptions)
}
