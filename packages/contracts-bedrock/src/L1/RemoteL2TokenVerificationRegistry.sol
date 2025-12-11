// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

// Contracts
import { L2TunnelTokenVerifier } from "src/L2/L2TunnelTokenVerifier.sol";

// Interfaces
import { ICrossDomainMessenger } from "src/universal/interfaces/ICrossDomainMessenger.sol";
import { IRemoteL2TokenVerificationRegistry } from "src/L1/interfaces/IRemoteL2TokenVerificationRegistry.sol";


/// @title RemoteL2TokenVerificationRegistry
/// @notice The RemoteL2TokenVerificationRegistry is a registry of known bad L1->L2
///         token mappings. The primary purpose of this contract is to securely validate that
///         a particular L2 token is not a valid destination for a particular L1 token.
///         Known bad tokens can only be updated by a verifier that runs on the L2 and checks
///         whether an L2 contract is a valid OptimismMintableERC20 (ERC165 checks are
///         self-reported, and even adherence to the OptimismMintableERC20 interface does
///         not ensure correct behavior) and has the expected fields set for a particular L1
///         source token.
///
///         This validation involves checking:
///           1. Whether the L2 contract is an OptimismMintableERC20
///           2. If so, whether the L2 contract has the appropriate L1 source token set
///         It should be noted that this verification does *not* ensure that the particular L2
///         token is not malicious. This contract is initialized with a trusted remote L2
///         verification contract which performs these verifications on L2 and sends an L2->L1
///         message if it detects a particular L2 contract is invalid (either entirely, or for
///         a particular L1 token).
///
///         This contract enables two types of L2 tunnel token contract checks:
///           1. That an L2 contract is only valid for a particular L1 ERC20
///           2. That an L2 contract is not valid for any L1 ERC20
///
///         An L2 contract not being marked as invalid (either overall, or given a specific L1
///         ERC20 source) should *not* be construed to mean the L2 contract is valid, since
///         adding to this registry requires explicitly calling the L2 validation contract, and
///         the L2 validation contract cannot perform anything beyond basic sanity checks.
contract RemoteL2TokenVerificationRegistry is IRemoteL2TokenVerificationRegistry {
    // The L1 ICrossDomainMessenger contract that any cross-chain messages must come from
    ICrossDomainMessenger public messenger;

    // The remote L2 Tunnel Token Verifier contract which performs the checks on L2 and
    // sends a cross-domain message to this contract to mark L2 contracts as invalid or not
    // compatible with a particular L1 ERC20 contract
    L2TunnelTokenVerifier public verifier;

    // All remote contracts which are known not to implement the OptimismMintableERC20 interface
    mapping(address => bool) public fullyInvalidRemoteContracts;

    // All l1 -> l2 known invalid ERC20 tunnel mappings
    mapping(address => mapping(address => bool)) public invalidDepositMappings;

    /// @notice Ensures that the caller is a cross-chain message from the other bridge.
    modifier onlyRemoteL2TokenVerifier() {
        require(
            msg.sender == address(messenger) && messenger.xDomainMessageSender() == address(verifier),
            "RemoteL2TokenVerificationRegistry: function can only be called from the configured L2TunnelTokenVerifier"
        );
        _;
    }

    constructor (ICrossDomainMessenger _messenger, L2TunnelTokenVerifier _verifier) {
        require(address(_messenger) != address(0),
        "messenger cannot be the zero address");

        require(address(_verifier) != address(0),
        "verifier cannot be the zero address");

        messenger = _messenger;
        verifier = _verifier;
    }

    function markDestinationContractFullyInvalid(address _invalidL2Contract) external onlyRemoteL2TokenVerifier {
        fullyInvalidRemoteContracts[_invalidL2Contract] = true;
    }

    function markDestinationContractInvalidForL1Contract(address _l1Token, address _invalidL2Contract) external onlyRemoteL2TokenVerifier {
        invalidDepositMappings[_l1Token][_invalidL2Contract] = true;
    }

    function isInvalidL2Contract(address _l1Token, address _l2Contract) external view returns (bool) {
        if (fullyInvalidRemoteContracts[_l2Contract]) {
            // Invalid for all L1 ERC20 tokens
            return true;
        }
        if (invalidDepositMappings[_l1Token][_l2Contract]) {
            // The L2 contract is OptimismMintableERC20 but not compatible with the provided L1 token
            return true;
        }

        // Does not mean the L2 contract is the correct l2Token for the l1Token, but it has not
        // been explicitly marked as invalid.
        return false;
    }
}