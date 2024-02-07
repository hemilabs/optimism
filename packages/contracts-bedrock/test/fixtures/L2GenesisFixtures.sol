// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import { Script } from "forge-std/Script.sol";
import { console2 as console } from "forge-std/console2.sol";

import { Predeploys } from "src/libraries/Predeploys.sol";
import { L2GenesisDeployedBytecode } from "test/fixtures/L2GenesisDeployedBytecode.sol";
import { L2GenesisHelpers } from "scripts/libraries/L2GenesisHelpers.sol";

/// @dev This contract serve the L2Genesis.t.sol test fixture data.
///      The expected storage slots, their values, and deployed bytecode are hardcoded
///      to ensure the L2 genesis allocs are being set exactly how we expect it to, with nothing
///      more or less. The rational behind the below mappings and their getters is:
///      1. Check if the number of storage slots parsed from L2 genesis outfile matches what we expect
///      for the predeploy (`numExpectedSlotKeys[predeployAddress]`).
///      2. Check that each parsed storage slot is expected to be set
///      (`expectedSlotKeys[predeployAddress][storageSlot]`).
///      3. Check that each parsed storage slot has the expected corresponding value
///      (`slotValueByKey[predeployAddress][storageSlot]`).
///      With these checks we get the gurantee that no more or less storage slots are set for each predeploy,
///      and that each storage slot has the expected corresponding value.
contract L2GenesisFixtures {
    mapping(address => uint256) numExpectedSlotKeys;
    mapping(address => mapping(bytes32 => bool)) expectedSlotKeys;
    mapping(address => mapping(bytes32 => bytes32)) slotValueByKey;

    mapping(address => bytes) expectedDeployedBytecode;

    function setUp() public virtual {
        _setProxyStorage();
        _setImplementationStorage();
    }

    /// @notice Returns the number of expected storage slots to be set for a specific address.
    function getNumExpectedSlotKeys(address _addr) public view returns(uint256) {
        return numExpectedSlotKeys[_addr];
    }

    /// @notice Returns whether or not the given storage slot is expected to be set for the give address.
    function isExpectedSlotKey(address _addr, bytes32 _slot) public view returns(bool) {
        return expectedSlotKeys[_addr][_slot];
    }

    /// @notice Returns the expected value for a given storage slot for a given address.
    ///         It's important that you call `isExpectedSlotKey` before calling this function
    ///         to verify that the storage slot is supposed to be set before trying to get it's value.
    ///         Because `slotValueByKey` is a mapping, all queried `_slot`s for any address will return something.
    ///         If the storage slot isn't set for an address, the default value `bytes32(0)` will be returned.
    function getSlotValueByKey(address _addr, bytes32 _slot) public view returns(bytes32) {
        return slotValueByKey[_addr][_slot];
    }

    /// @notice Returns the expected hardcoded deployed bytecode for a given address. Depending on the
    ///         constructor args/initialization args, the hardcoded bytecode may need to be updated to
    ///         what's expected for your specific usecase.
    function getExpectedDeployedBytecode(address _addr) public view returns(bytes memory) {
        return expectedDeployedBytecode[_addr];
    }

    /// @notice Sets the above mappings (`numExpectedSlotKeys`, `expectedSlotKeys`,
    ///         and `slotValueByKey`) for each predeploy proxy.
    function _setProxyStorage() internal {
        uint160 prefix = uint160(0x420) << 148;

        for (uint256 i; i < L2GenesisHelpers.PROXY_COUNT; i++) {
            address addr = address(prefix | uint160(i));

            if (L2GenesisHelpers._notProxied(addr)) continue;

            numExpectedSlotKeys[addr] = ++numExpectedSlotKeys[addr];
            expectedSlotKeys[addr][L2GenesisHelpers.PROXY_ADMIN_ADDRESS] = true;
            slotValueByKey[addr][L2GenesisHelpers.PROXY_ADMIN_ADDRESS] = bytes32(uint256(uint160(Predeploys.PROXY_ADMIN)));

            /// @dev It's important this code execute after the above `L2GenesisHelpers._notProxied` check.
            /// This is because WETH9 and GOVERNANCE_TOKEN are included in `L2GenesisHelpers._isDefinedPredeploy`,
            /// but are NOT proxied and therefore shouldn't have the following storage slot set.
            if (L2GenesisHelpers._isDefinedPredeploy(addr)) {
                address implementation = L2GenesisHelpers._predeployToCodeNamespace(addr);
                numExpectedSlotKeys[addr] = ++numExpectedSlotKeys[addr];
                expectedSlotKeys[addr][L2GenesisHelpers.PROXY_IMPLEMENTATION_ADDRESS] = true;
                slotValueByKey[addr][L2GenesisHelpers.PROXY_IMPLEMENTATION_ADDRESS] = bytes32(uint256(uint160(implementation)));
            }
        }

        _setWETH9ProxyStorage();
        _setL2CrossDomainMessengerProxyStorage();
        _setL2StandardBridgeProxyStorage();
        _setOptimismMintableERC20FactoryProxyStorage();
        _setGovernanceTokenProxyStorage();
    }

    /// @notice Sets the `expectedDeployedBytecode` mapping for each predeploy implementation.
    function _setImplementationStorage() internal {
        _setLegacyMessagePasserImplStorage();
        _setDeployerWhitelistImplStorage();
        _setL2CrossDomainMessengerImplStorage();
        _setGasPriceOracleImplStorage();
        _setL2StandardBridgeImplStorage();
        _setSequencerFeeWalletImplStorage();
        _setOptimismMintableERC20FactoryImplStorage();
        _setL1BlockNumberImplStorage();
        _setL1BlockAttributesImplStorage();
    }

    //////////////////////////////////////////////////////
    /// Predeploy Proxy Storage Setters
    //////////////////////////////////////////////////////
    function _setWETH9ProxyStorage() internal {
        bytes32[3] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000002)
        ];
        bytes32[3] memory expectedStorageValues = [
            bytes32(0x577261707065642045746865720000000000000000000000000000000000001a),
            bytes32(0x5745544800000000000000000000000000000000000000000000000000000008),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000012)
        ];

        _setFixtureData(Predeploys.WETH9, expectedStorageKeys, expectedStorageValues);
    }

    function _setL2CrossDomainMessengerProxyStorage() internal {
        bytes32[3] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x00000000000000000000000000000000000000000000000000000000000000cc),
            bytes32(0x00000000000000000000000000000000000000000000000000000000000000cf)
        ];
        bytes32[3] memory expectedStorageValues = [
            bytes32(0x0000000000000000000000010000000000000000000000000000000000000000),
            bytes32(0x000000000000000000000000000000000000000000000000000000000000dead),
            bytes32(0x00000000000000000000000020a42a5a785622c6ba2576b2d6e924aa82bfa11d)
        ];

        _setFixtureData(Predeploys.L2_CROSS_DOMAIN_MESSENGER, expectedStorageKeys, expectedStorageValues);
    }

    function _setL2StandardBridgeProxyStorage() internal {
        bytes32[3] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000003),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000004)
        ];
        bytes32[3] memory expectedStorageValues = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001),
            bytes32(0x0000000000000000000000004200000000000000000000000000000000000007),
            bytes32(0x0000000000000000000000000c8b5822b6e02cda722174f19a1439a7495a3fa6)
        ];

        _setFixtureData(Predeploys.L2_STANDARD_BRIDGE, expectedStorageKeys, expectedStorageValues);
    }

    function _setOptimismMintableERC20FactoryProxyStorage() internal {
        bytes32[2] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001)
        ];
        bytes32[2] memory expectedStorageValues = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001),
            bytes32(0x0000000000000000000000004200000000000000000000000000000000000010)
        ];

        _setFixtureData(Predeploys.OPTIMISM_MINTABLE_ERC20_FACTORY, expectedStorageKeys, expectedStorageValues);
    }

    function _setGovernanceTokenProxyStorage() internal {
        bytes32[3] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000003),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000004),
            bytes32(0x000000000000000000000000000000000000000000000000000000000000000a)
        ];
        bytes32[3] memory expectedStorageValues = [
            bytes32(0x4f7074696d69736d000000000000000000000000000000000000000000000010),
            bytes32(0x4f50000000000000000000000000000000000000000000000000000000000004),
            bytes32(0x000000000000000000000000a0ee7a142d267c1f36714e4a8f75612f20a79720)
        ];

        _setFixtureData(Predeploys.GOVERNANCE_TOKEN, expectedStorageKeys, expectedStorageValues);
    }

    //////////////////////////////////////////////////////
    /// Predeploy Implementation Storage Setters
    //////////////////////////////////////////////////////
    function _setLegacyMessagePasserImplStorage() internal {
        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.LEGACY_MESSAGE_PASSER)] = L2GenesisDeployedBytecode.LEGACY_MESSAGE_PASSER_BYTECODE;
    }

    function _setDeployerWhitelistImplStorage() internal {
        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.DEPLOYER_WHITELIST)] = L2GenesisDeployedBytecode.DEPLOYER_WHITELIST_BYTECODE;
    }

    function _setL2CrossDomainMessengerImplStorage() internal {
        bytes32[3] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x00000000000000000000000000000000000000000000000000000000000000cc),
            bytes32(0x00000000000000000000000000000000000000000000000000000000000000cf)
        ];
        bytes32[3] memory expectedStorageValues = [
            bytes32(0x0000000000000000000000010000000000000000000000000000000000000000),
            bytes32(0x000000000000000000000000000000000000000000000000000000000000dead),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000)
        ];

        _setFixtureData(
            L2GenesisHelpers._predeployToCodeNamespace(Predeploys.L2_CROSS_DOMAIN_MESSENGER),
            expectedStorageKeys,
            expectedStorageValues
        );

        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.L2_CROSS_DOMAIN_MESSENGER)] = L2GenesisDeployedBytecode.L2_CROSS_DOMAIN_MESSENGER_BYTECODE;
    }

    function _setGasPriceOracleImplStorage() internal {
        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.GAS_PRICE_ORACLE)] = L2GenesisDeployedBytecode.GAS_PRICE_ORACLE_BYTECODE;
    }

    function _setL2StandardBridgeImplStorage() internal {
        bytes32[3] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000003),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000004)
        ];
        bytes32[3] memory expectedStorageValues = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001),
            bytes32(0x0000000000000000000000004200000000000000000000000000000000000007),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000)
        ];

        _setFixtureData(
            L2GenesisHelpers._predeployToCodeNamespace(Predeploys.L2_STANDARD_BRIDGE),
            expectedStorageKeys,
            expectedStorageValues
        );

        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.L2_STANDARD_BRIDGE)] = L2GenesisDeployedBytecode.L2_STANDARD_BRIDGE_BYTECODE;
    }

    function _setSequencerFeeWalletImplStorage() internal {
        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.SEQUENCER_FEE_WALLET)] = L2GenesisDeployedBytecode.SEQUENCER_FEE_WALLET_BYTECODE;
    }

    function _setOptimismMintableERC20FactoryImplStorage() internal {
        bytes32[2] memory expectedStorageKeys = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001)
        ];
        bytes32[2] memory expectedStorageValues = [
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000001),
            bytes32(0x0000000000000000000000000000000000000000000000000000000000000000)
        ];

        _setFixtureData(
            L2GenesisHelpers._predeployToCodeNamespace(Predeploys.OPTIMISM_MINTABLE_ERC20_FACTORY),
            expectedStorageKeys,
            expectedStorageValues
        );

        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.OPTIMISM_MINTABLE_ERC20_FACTORY)] = L2GenesisDeployedBytecode.OPTIMISM_MINTABLE_ERC20_FACTORY_BYTECODE;
    }

    function _setL1BlockNumberImplStorage() internal {
        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.L1_BLOCK_NUMBER)] = L2GenesisDeployedBytecode.L1_BLOCK_NUMBER_BYTECODE;
    }

    function _setL1BlockAttributesImplStorage() internal {
        expectedDeployedBytecode[L2GenesisHelpers._predeployToCodeNamespace(Predeploys.L1_BLOCK_ATTRIBUTES)] = L2GenesisDeployedBytecode.L1_BLOCK_ATTRIBUTES_BYTECODE;
    }

    //////////////////////////////////////////////////////
    /// Helper Functions
    //////////////////////////////////////////////////////
    function _setFixtureData(address _addr, bytes32[2] memory _expectedStorageKeys, bytes32[2] memory _expectedStorageValues) internal {
        for(uint256 i; i < _expectedStorageKeys.length; i++) {
            numExpectedSlotKeys[_addr] = ++numExpectedSlotKeys[_addr];
            expectedSlotKeys[_addr][_expectedStorageKeys[i]] = true;
            slotValueByKey[_addr][_expectedStorageKeys[i]] = _expectedStorageValues[i];
        }
    }

    function _setFixtureData(address _addr, bytes32[3] memory _expectedStorageKeys, bytes32[3] memory _expectedStorageValues) internal {
        for(uint256 i; i < _expectedStorageKeys.length; i++) {
            numExpectedSlotKeys[_addr] = ++numExpectedSlotKeys[_addr];
            expectedSlotKeys[_addr][_expectedStorageKeys[i]] = true;
            slotValueByKey[_addr][_expectedStorageKeys[i]] = _expectedStorageValues[i];
        }
    }
}
