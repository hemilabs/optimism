// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

/// @title PreInstalls
/// @notice Contains constant addresses for contracts that are pre-installed to the L2 system.
library PreInstalls {
    /// @notice Address of the Multicall3 preinstall.
    address internal constant MULTICALL3 = 0xcA11bde05977b3631167028862bE2a173976CA11;

    /// @notice Address of the Create2Deployer preinstall.
    address internal constant CREATE2_DEPLOYER = 0x13b0D85CcB8bf860b6b79AF3029fCA081AE9beF2;
}
