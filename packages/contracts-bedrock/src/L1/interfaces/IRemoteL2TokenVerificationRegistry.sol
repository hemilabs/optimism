// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

// Contracts
import { L2TunnelTokenVerifier } from "src/L2/L2TunnelTokenVerifier.sol";

// Interfaces
import { ICrossDomainMessenger } from "src/universal/interfaces/ICrossDomainMessenger.sol";

interface IRemoteL2TokenVerificationRegistry {
    function markDestinationContractFullyInvalid(address _invalidL2Contract) external;

    function markDestinationContractInvalidForL1Contract(address _l1Token, address _invalidL2Contract) external;

    function isInvalidL2Contract(address _l1Token, address _l2Contract) external view returns (bool);
}