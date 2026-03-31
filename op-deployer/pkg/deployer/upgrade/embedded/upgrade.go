package embedded

import (
	"encoding/json"
	"fmt"

	"github.com/ethereum-optimism/optimism/op-chain-ops/script"
	"github.com/ethereum-optimism/optimism/op-deployer/pkg/deployer/artifacts"
	"github.com/ethereum-optimism/optimism/op-deployer/pkg/deployer/opcm"
	"github.com/ethereum/go-ethereum/common"
	"github.com/lmittmann/w3"
)

type UpgradeOPChainInput struct {
	Prank               common.Address  `json:"prank"`
	Opcm                common.Address  `json:"opcm"`
	EncodedChainConfigs []OPChainConfig `evm:"-" json:"chainConfigs"`
}

type OPChainConfig struct {
	SystemConfigProxy  common.Address `json:"systemConfigProxy"`
	CannonPrestate     common.Hash    `json:"cannonPrestate"`
	CannonKonaPrestate common.Hash    `json:"cannonKonaPrestate"`
}

var opChainConfigEncoder = w3.MustNewFunc("dummy((address systemConfigProxy,bytes32 cannonPrestate,bytes32 cannonKonaPrestate)[])", "")

func (u *UpgradeOPChainInput) OpChainConfigs() ([]byte, error) {
	data, err := opChainConfigEncoder.EncodeArgs(u.EncodedChainConfigs)
	if err != nil {
		return nil, fmt.Errorf("failed to encode chain configs: %w", err)
	}
	return data[4:], nil
}

// EncodedUpgradeInputV2 encodes the upgrade input for the upgrade input, assumes is not nil
func (u *UpgradeOPChainInput) EncodedUpgradeInputV2() ([]byte, error) {

	encodableConfigs := make([]EncodableDisputeGameConfig, len(u.UpgradeInputV2.DisputeGameConfigs))

	// Validate and encode each game config.
	// We iterate over the game configs in the upgrade input config and encode them into the encodable configs.
	// We return an error if a game config is not valid.
	for i, gameConfig := range u.UpgradeInputV2.DisputeGameConfigs {
		var gameArgs []byte
		var err error

		if gameConfig.Enabled {
			switch gameConfig.GameType {
			case GameTypeCannon, GameTypeCannonKona, GameTypeSuperCannon, GameTypeSuperCannonKona:
				if gameConfig.FaultDisputeGameConfig == nil {
					return nil, fmt.Errorf("faultDisputeGameConfig is required for game type %d", gameConfig.GameType)
				}
				// Encode the fault dispute game args
				gameArgs, err = faultEncoder.EncodeArgs(gameConfig.FaultDisputeGameConfig)
				if err != nil {
					return nil, fmt.Errorf("failed to encode fault game config: %w", err)
				}
			case GameTypePermissionedCannon, GameTypeSuperPermCannon:
				if gameConfig.PermissionedDisputeGameConfig == nil {
					return nil, fmt.Errorf("permissionedDisputeGameConfig is required for game type %d", gameConfig.GameType)
				}
				// Encode the permissioned dispute game args
				gameArgs, err = permEncoder.EncodeArgs(gameConfig.PermissionedDisputeGameConfig)
				if err != nil {
					return nil, fmt.Errorf("failed to encode permissioned game config: %w", err)
				}
			default:
				return nil, fmt.Errorf("invalid game type %d for opcm v2", gameConfig.GameType)
			}

			// Edge case check when the encoded game args length is less than 4
			if len(gameArgs) < 4 {
				return nil, fmt.Errorf("encoded game args length is less than 4 for game type %d", gameConfig.GameType)
			}

			// Skip the selector bytes
			gameArgs = gameArgs[4:]
		}

		encodableConfigs[i] = EncodableDisputeGameConfig{
			Enabled:  gameConfig.Enabled,
			InitBond: gameConfig.InitBond,
			GameType: uint32(gameConfig.GameType),
			GameArgs: gameArgs,
		}
	}

	// Create encodable input
	encodableInput := EncodableUpgradeInput{
		SystemConfig:       u.UpgradeInputV2.SystemConfig,
		DisputeGameConfigs: encodableConfigs,
		ExtraInstructions:  u.UpgradeInputV2.ExtraInstructions,
	}

	data, err := upgradeInputEncoder.EncodeArgs(encodableInput)
	if err != nil {
		return nil, fmt.Errorf("failed to encode upgrade input: %w", err)
	}

	return data[4:], nil
}

type UpgradeOPChain struct {
	Run func(input common.Address)
}

func Upgrade(host *script.Host, input UpgradeOPChainInput) error {
	return opcm.RunScriptVoid(host, input, "UpgradeOPChain.s.sol", "UpgradeOPChain")
}

type Upgrader struct{}

func (u *Upgrader) Upgrade(host *script.Host, input json.RawMessage) error {
	var upgradeInput UpgradeOPChainInput
	if err := json.Unmarshal(input, &upgradeInput); err != nil {
		return fmt.Errorf("failed to unmarshal input: %w", err)
	}
	return Upgrade(host, upgradeInput)
}

func (u *Upgrader) ArtifactsURL() string {
	return artifacts.EmbeddedLocatorString
}

var DefaultUpgrader = new(Upgrader)
