package opcm

import (
	"github.com/ethereum-optimism/optimism/op-chain-ops/script"
	"github.com/ethereum/go-ethereum/common"
)

type ReadSuperchainDeploymentInput struct {
	SuperchainConfigProxy common.Address
}

type ReadSuperchainDeploymentOutput struct {
	SuperchainConfigImpl      common.Address `abi:"superchainConfigImpl"`
	SuperchainConfigProxy     common.Address `abi:"superchainConfigProxy"`
	SuperchainProxyAdmin      common.Address `abi:"superchainProxyAdmin"`
	Guardian                  common.Address `abi:"guardian"`
	SuperchainProxyAdminOwner common.Address `abi:"superchainProxyAdminOwner"`
}

type ReadSuperchainDeploymentScript script.DeployScriptWithOutput[ReadSuperchainDeploymentInput, ReadSuperchainDeploymentOutput]

func NewReadSuperchainDeploymentScript(host *script.Host) (ReadSuperchainDeploymentScript, error) {
	return script.NewDeployScriptWithOutputFromFile[ReadSuperchainDeploymentInput, ReadSuperchainDeploymentOutput](host, "ReadSuperchainDeployment.s.sol", "ReadSuperchainDeployment")
}
