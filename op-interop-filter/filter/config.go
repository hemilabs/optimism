package filter

import (
	"errors"
	"fmt"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/optimism/op-interop-filter/flags"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
)

type Config struct {
	L2RPCs           []string
	DataDir          string
	BackfillDuration time.Duration
	JWTSecretPath    string
	Version          string

	LogConfig     oplog.CLIConfig
	MetricsConfig opmetrics.CLIConfig
	PprofConfig   oppprof.CLIConfig
	RPC           oprpc.CLIConfig
}

func (c *Config) Check() error {
	var result error
	if len(c.L2RPCs) == 0 {
		result = errors.Join(result, errors.New("at least one L2 RPC is required"))
	}
	// Admin API requires JWT authentication
	if c.RPC.EnableAdmin && c.JWTSecretPath == "" {
		result = errors.Join(result, errors.New("admin RPC requires JWT setup, but no JWT path was specified"))
	}
	result = errors.Join(result, c.MetricsConfig.Check())
	result = errors.Join(result, c.PprofConfig.Check())
	result = errors.Join(result, c.RPC.Check())
	return result
}

func NewConfig(ctx *cli.Context, version string) (*Config, error) {
	backfillDuration, err := time.ParseDuration(ctx.String(flags.BackfillDurationFlag.Name))
	if err != nil {
		return nil, fmt.Errorf("invalid backfill-duration: %w", err)
	}

	return &Config{
		L2RPCs:           ctx.StringSlice(flags.L2RPCsFlag.Name),
		DataDir:          ctx.String(flags.DataDirFlag.Name),
		BackfillDuration: backfillDuration,
		JWTSecretPath:    ctx.String(flags.JWTSecretFlag.Name),
		Version:          version,
		LogConfig:        oplog.ReadCLIConfig(ctx),
		MetricsConfig:    opmetrics.ReadCLIConfig(ctx),
		PprofConfig:      oppprof.ReadCLIConfig(ctx),
		RPC:              oprpc.ReadCLIConfig(ctx),
	}, nil
}

// loadRollupConfigs loads rollup configs from networks (superchain registry) and custom JSON files.
func loadRollupConfigs(networks []string, configPaths []string) (map[eth.ChainID]*rollup.Config, error) {
	configs := make(map[eth.ChainID]*rollup.Config)

	// Load from superchain registry by network name
	for _, network := range networks {
		cfg, err := chaincfg.GetRollupConfig(network)
		if err != nil {
			return nil, fmt.Errorf("failed to load rollup config for network %q: %w", network, err)
		}
		chainID := eth.ChainIDFromBig(cfg.L2ChainID)
		if _, exists := configs[chainID]; exists {
			return nil, fmt.Errorf("duplicate chain ID %s: network %q conflicts with another config", chainID, network)
		}
		configs[chainID] = cfg
	}

	// Load from custom JSON files
	for _, path := range configPaths {
		cfg, err := loadRollupConfigFromFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to load rollup config from %q: %w", path, err)
		}
		chainID := eth.ChainIDFromBig(cfg.L2ChainID)
		if _, exists := configs[chainID]; exists {
			return nil, fmt.Errorf("duplicate chain ID %s: file %q conflicts with another config", chainID, path)
		}
		configs[chainID] = cfg
	}

	return configs, nil
}

// loadRollupConfigFromFile loads a rollup config from a JSON file.
func loadRollupConfigFromFile(path string) (*rollup.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var rollupConfig rollup.Config
	return &rollupConfig, rollupConfig.ParseRollupConfig(file)
}
