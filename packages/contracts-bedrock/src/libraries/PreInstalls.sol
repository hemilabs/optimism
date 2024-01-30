// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

/// @title PreInstalls
/// @notice Contains constant addresses for contracts that are pre-installed to the L2 system.
library PreInstalls {
    /// @notice Address of the Permit2 preinstall.
    address internal constant PERMIT2 = 0x000000000022D473030F116dDEE9F6B43aC78BA3;
    /// @notice Address of the Multicall3 preinstall.
    address internal constant MULTICALL3 = 0xcA11bde05977b3631167028862bE2a173976CA11;
    /// @notice Address of the Create2Deployer preinstall.
    address internal constant CREATE2_DEPLOYER = 0x13b0D85CcB8bf860b6b79AF3029fCA081AE9beF2;
    /// @notice Address of the Safe_V130 preinstall.
    address internal constant SAFE_V130 = 0x69f4D1788e39c87893C980c06EdF4b7f686e2938;
    /// @notice Address of the SafeL2_V130 preinstall.
    address internal constant SAFE_L2_V130 = 0xfb1bffC9d739B8D520DaF37dF666da4C687191EA;
    /// @notice Address of the MULTI_SEND_CALL_ONLY_V130 preinstall.
    address internal constant MULTI_SEND_CALL_ONLY_V130 = 0xA1dabEF33b3B82c7814B6D82A79e50F4AC44102B;
}
