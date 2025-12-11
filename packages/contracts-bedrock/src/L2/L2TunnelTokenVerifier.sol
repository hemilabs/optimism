// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

// Libraries
import { ERC165Checker } from "@openzeppelin/contracts/utils/introspection/ERC165Checker.sol";

// Interfaces
import { ICrossDomainMessenger } from "src/universal/interfaces/ICrossDomainMessenger.sol";
import { IOptimismMintableERC20 } from "src/universal/interfaces/IOptimismMintableERC20.sol";
import { ILegacyMintableERC20 } from "src/universal/interfaces/ILegacyMintableERC20.sol";
import { IRemoteL2TokenVerificationRegistry } from "src/L1/interfaces/IRemoteL2TokenVerificationRegistry.sol";
import { IL2TunnelTokenVerifier } from "src/L2/interfaces/IL2TunnelTokenVerifier.sol";


/// @title L2TunnelTokenVerifier
/// @notice This contract runs on L2, checks whether a particular L2 contract appears to be
///         a valid implementation of OptimismMintableERC20, and if so checks which L1 token
///         it is a vaid tunnel contract for.
///
///         Depending on the results, one of the following will happen:
///           1. L2 contract is not a valid implementation of OptimismMintableERC20:
///              -> Send cross-chain message to RemoteL2TokenVerificationRegistry on L1 that the
///                 L2 contract is not a valid recipient for any L1 token
///           2. L2 contract is a valid implementation of OptimismMintableERC20:
///              -> Send cross-chain message to RemoteL2TokenVerificationRegistry on L1 that the
///                 L2 contract is a valid recipient for a specific L1 token
///
///         This contract does not *ensure* that the L2 contract is valid and non-malicious.
///         Rather, it checks whether the contract self-reports
contract L2TunnelTokenVerifier is IL2TunnelTokenVerifier {
    /// L2 messenger contract
    ICrossDomainMessenger public messenger;

    /// L1 remote token verification registry where results of verification are sent cross-chain to
    IRemoteL2TokenVerificationRegistry public remoteL2TokenVerificationRegistry;

    // The admin able to perform the initial configuration, required because this contract must
    // be deployed, then the remote L2 token verification registry must be deployed on L1 with this
    // contract address, then this contract must be configured with that matching deployment
    address public initAdmin;

    constructor (ICrossDomainMessenger _messenger, address _initAdmin) {
        require(address(_messenger) != address(0),
         "messenger cannot be zero address");

        require(_initAdmin != address(0),
          "init admin cannot be zero address");

         messenger = _messenger;
         initAdmin = _initAdmin;
    }

    /// @notice Function for initial configuration of the remote L2 token verification registry
    /// @param _remoteL2TokenVerificationRegistry The remote L2 token verification registry
    function configureRemoteL2TokenVerificationRegistry(IRemoteL2TokenVerificationRegistry _remoteL2TokenVerificationRegistry) external {
        require(msg.sender == initAdmin,
          "only init admin can configure the remote L2 token verification registry");

        require(address(_remoteL2TokenVerificationRegistry) != address(0),
          "remote l2 token verification registry cannot be zero address");

        require(address(remoteL2TokenVerificationRegistry) == address(0),
          "remote l2 token verification registry can only be configured once");

         remoteL2TokenVerificationRegistry = _remoteL2TokenVerificationRegistry;
    }

    /// @notice Function for checking an an L2 contract to see if it is a valid tunnel destination for an L1 ERC20.
    /// @param _l1Token The address of the l1 ERC20 token
    /// @param _contract The address of the contract to check
    /// @param _minGasLimit Minimum amount of gas that the message can be relayed with
    function checkContract(address _l1Token, address _contract, uint32 _minGasLimit) external {
        // This check relies on the honesty of the contract.
        bool isOptimismMintableERC20 =  ERC165Checker.supportsInterface(_contract, type(ILegacyMintableERC20).interfaceId)
            || ERC165Checker.supportsInterface(_contract, type(IOptimismMintableERC20).interfaceId);

        if (!isOptimismMintableERC20) {
            // Contract can't possibly be a valid tunnel destination for any L1 ERC20 asset
            messenger.sendMessage({
                _target: address(remoteL2TokenVerificationRegistry),
                _message: abi.encodeWithSelector(
                    IRemoteL2TokenVerificationRegistry.markDestinationContractFullyInvalid.selector,
                    _contract
                ),
                _minGasLimit: _minGasLimit
            });
        } else {
            // Check if the L2 OptimismMintableERC20 is configured for the specified L1 token as the remote token
            IOptimismMintableERC20 l2Token = IOptimismMintableERC20(_contract);
            address configuredL1Token = l2Token.remoteToken();
            if (configuredL1Token == _l1Token) {
                // Configured correctly
                return;
            } else {
                messenger.sendMessage({
                    _target: address(remoteL2TokenVerificationRegistry),
                    _message: abi.encodeWithSelector(
                        IRemoteL2TokenVerificationRegistry.markDestinationContractInvalidForL1Contract.selector,
                        _l1Token,
                        _contract
                    ),
                    _minGasLimit: _minGasLimit
                });
            }
        }
    }
}