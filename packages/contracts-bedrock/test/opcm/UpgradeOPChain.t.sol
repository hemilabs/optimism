// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "forge-std/Test.sol";
import { Claim } from "src/dispute/lib/Types.sol";

import { ISystemConfig } from "interfaces/L1/ISystemConfig.sol";

// Contracts
import { OPContractsManagerV2 } from "src/L1/opcm/OPContractsManagerV2.sol";

// Libraries
import { GameType } from "src/dispute/lib/LibUDT.sol";

// Interfaces
import { IOPContractsManagerUtils } from "interfaces/L1/opcm/IOPContractsManagerUtils.sol";
import { ISystemConfig } from "interfaces/L1/ISystemConfig.sol";

contract UpgradeOPChainInput_TestV2 is Test {
    UpgradeOPChainInput input;
    MockOPCMV2 mockOPCM;

    function setUp() public {
        input = new UpgradeOPChainInput();
        mockOPCM = new MockOPCMV2();
        input.set(input.opcm.selector, address(mockOPCM));
    }

    /// @notice Tests that the upgrade input can be set using the OPContractsManagerV2.UpgradeInput type.
    function testFuzz_setUpgradeInputV2_succeeds(
        address systemConfig,
        bool enabled,
        uint256 initBond,
        uint32 gameType,
        bytes memory gameArgs,
        string memory extraKey,
        bytes memory extraData
    )
        public
    {
        // Assume non-zero address for system config
        vm.assume(systemConfig != address(0));
        vm.assume(initBond > 0);

        // Create sample UpgradeInputV2
        IOPContractsManagerUtils.DisputeGameConfig[] memory disputeGameConfigs =
            new IOPContractsManagerUtils.DisputeGameConfig[](1);
        disputeGameConfigs[0] = IOPContractsManagerUtils.DisputeGameConfig({
            enabled: enabled,
            initBond: initBond,
            gameType: GameType.wrap(gameType),
            gameArgs: gameArgs
        });

        IOPContractsManagerUtils.ExtraInstruction[] memory extraInstructions =
            new IOPContractsManagerUtils.ExtraInstruction[](1);
        extraInstructions[0] = IOPContractsManagerUtils.ExtraInstruction({ key: extraKey, data: extraData });

        OPContractsManagerV2.UpgradeInput memory upgradeInput = OPContractsManagerV2.UpgradeInput({
            systemConfig: ISystemConfig(systemConfig),
            disputeGameConfigs: disputeGameConfigs,
            extraInstructions: extraInstructions
        });

        input.set(input.upgradeInput.selector, upgradeInput);

        bytes memory storedUpgradeInput = input.upgradeInput();
        assertEq(storedUpgradeInput, abi.encode(upgradeInput));

        // Additional verification of stored values if needed
        OPContractsManagerV2.UpgradeInput memory decodedUpgradeInput =
            abi.decode(storedUpgradeInput, (OPContractsManagerV2.UpgradeInput));
        // Check system config matches
        assertEq(address(decodedUpgradeInput.systemConfig), address(upgradeInput.systemConfig));
        // Check dispute game configs match
        assertEq(decodedUpgradeInput.disputeGameConfigs.length, disputeGameConfigs.length);
        assertEq(decodedUpgradeInput.disputeGameConfigs[0].enabled, enabled);
        assertEq(decodedUpgradeInput.disputeGameConfigs[0].initBond, initBond);
        assertEq(GameType.unwrap(decodedUpgradeInput.disputeGameConfigs[0].gameType), gameType);
        assertEq(keccak256(decodedUpgradeInput.disputeGameConfigs[0].gameArgs), keccak256(gameArgs));
        // Check extra instructions match
        assertEq(decodedUpgradeInput.extraInstructions.length, extraInstructions.length);
        assertEq(decodedUpgradeInput.extraInstructions[0].key, extraKey);
        assertEq(keccak256(decodedUpgradeInput.extraInstructions[0].data), keccak256(extraData));
    }

    /// @notice This test verifies that the UpgradeOPChain script correctly reverts when setting the upgrade input with
    /// a zero system config.
    function testFuzz_setUpgradeInputV2_withZeroSystemConfig_reverts() public {
        OPContractsManagerV2.UpgradeInput memory upgradeInput = OPContractsManagerV2.UpgradeInput({
            systemConfig: ISystemConfig(address(0)),
            disputeGameConfigs: new IOPContractsManagerUtils.DisputeGameConfig[](1),
            extraInstructions: new IOPContractsManagerUtils.ExtraInstruction[](0)
        });

        vm.expectRevert("UpgradeOPCMInput: cannot set zero address");
        input.set(input.upgradeInput.selector, upgradeInput);
    }

    /// @notice This test verifies that the UpgradeOPChain script correctly reverts when setting the upgrade input with
    /// an empty dispute game configs array.
    function testFuzz_setUpgradeInputV2_withEmptyDisputeGameConfigs_reverts(address systemConfig) public {
        vm.assume(systemConfig != address(0));

        OPContractsManagerV2.UpgradeInput memory upgradeInput = OPContractsManagerV2.UpgradeInput({
            systemConfig: ISystemConfig(systemConfig),
            disputeGameConfigs: new IOPContractsManagerUtils.DisputeGameConfig[](0),
            extraInstructions: new IOPContractsManagerUtils.ExtraInstruction[](0)
        });

        vm.expectRevert("UpgradeOPCMInput: cannot set empty dispute game configs array");
        input.set(input.upgradeInput.selector, upgradeInput);
    }
}

contract MockOPCMV2 {
    event UpgradeCalled(
        address indexed systemConfig,
        IOPContractsManagerUtils.DisputeGameConfig[] indexed disputeGameConfigs,
        IOPContractsManagerUtils.ExtraInstruction[] indexed extraInstructions
    );

    function version() public pure returns (string memory) {
        return "7.0.0";
    }

    function upgrade(OPContractsManagerV2.UpgradeInput memory _upgradeInput) public {
        emit UpgradeCalled(
            address(_upgradeInput.systemConfig), _upgradeInput.disputeGameConfigs, _upgradeInput.extraInstructions
        );
    }
}

contract UpgradeOPChain_TestV2 is Test {
    MockOPCMV2 mockOPCM;
    UpgradeOPChainInput uoci;
    UpgradeOPChain upgradeOPChain;
    address prank;

    event UpgradeCalled(
        address indexed systemConfig,
        IOPContractsManagerUtils.DisputeGameConfig[] indexed disputeGameConfigs,
        IOPContractsManagerUtils.ExtraInstruction[] indexed extraInstructions
    );

    function setUp() public {
        mockOPCM = new MockOPCMV2();
        uoci = new UpgradeOPChainInput();
        uoci.set(uoci.opcm.selector, address(mockOPCM));

        prank = makeAddr("prank");
        uoci.set(uoci.prank.selector, prank);
        upgradeOPChain = new UpgradeOPChain();
    }

    function test_upgrade_succeeds() public {
        // UpgradeCalled should be emitted by the prank since it's a delegate call.
        vm.expectEmit(address(prank));
        emit UpgradeCalled(
            address(config.systemConfigProxy),
            Claim.unwrap(config.cannonPrestate),
            Claim.unwrap(config.cannonKonaPrestate)
        );
        upgradeOPChain.run(uoci);
    }
}
