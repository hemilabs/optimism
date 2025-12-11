// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

// Interfaces
import { IRemoteL2TokenVerificationRegistry } from "src/L1/interfaces/IRemoteL2TokenVerificationRegistry.sol";

interface IL2TunnelTokenVerifier {
    function configureRemoteL2TokenVerificationRegistry(IRemoteL2TokenVerificationRegistry _remoteL2TokenVerificationRegistry) external;
    function checkContract(address _l1Token, address _contract, uint32 _minGasLimit) external;
}