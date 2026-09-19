// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "forge-std/Test.sol";

import { InteropMigrationInput, InteropMigration, InteropMigrationOutput } from "scripts/deploy/InteropMigration.s.sol";

// Libraries
import { Hash, GameType, Proposal } from "src/dispute/lib/Types.sol";

// Interfaces
import { IOPContractsManagerMigrator } from "interfaces/L1/opcm/IOPContractsManagerMigrator.sol";
import { IOPContractsManagerUtils } from "interfaces/L1/opcm/IOPContractsManagerUtils.sol";
import { IOptimismPortal2 as IOptimismPortal } from "interfaces/L1/IOptimismPortal2.sol";
import { ISystemConfig } from "interfaces/L1/ISystemConfig.sol";
import { Claim } from "src/dispute/lib/Types.sol";

contract InteropMigrationInput_Test is Test {
    InteropMigrationInput input;

    function setUp() public {
        input = new InteropMigrationInput();
    }

    function test_getters_whenNotSet_reverts() public {
        vm.expectRevert("InteropMigrationInput: prank not set");
        input.prank();

        vm.expectRevert("InteropMigrationInput: not set");
        input.opcm();

        vm.expectRevert("InteropMigrationInput: proposer not set");
        input.proposer();

        vm.expectRevert("InteropMigrationInput: challenger not set");
        input.challenger();

        vm.expectRevert("InteropMigrationInput: maxGameDepth not set");
        input.maxGameDepth();

        vm.expectRevert("InteropMigrationInput: splitDepth not set");
        input.splitDepth();

        vm.expectRevert("InteropMigrationInput: initBond not set");
        input.initBond();

        vm.expectRevert("InteropMigrationInput: clockExtension not set");
        input.clockExtension();

        vm.expectRevert("InteropMigrationInput: maxClockDuration not set");
        input.maxClockDuration();

        vm.expectRevert("InteropMigrationInput: startingAnchorL2SequenceNumber not set");
        input.startingAnchorL2SequenceNumber();

        vm.expectRevert("InteropMigrationInput: startingAnchorRoot not set");
        input.startingAnchorRoot();

        vm.expectRevert("InteropMigrationInput: not set");
        input.opChainConfigs();
    }

    function test_setAddress_succeeds() public {
        address mockPrank = makeAddr("prank");
        address mockOPCM = makeAddr("opcm");

        // Create mock contract at OPCM address
        vm.etch(mockOPCM, hex"01");

        input.set(input.prank.selector, mockPrank);
        input.set(input.opcm.selector, mockOPCM);

        assertEq(input.prank(), mockPrank);
        assertEq(address(input.opcm()), mockOPCM);
    }

    function test_setMigrateInputV2_succeeds() public {
        // Create sample V2 input
        ISystemConfig[] memory systemConfigs = new ISystemConfig[](1);
        address systemConfig1 = makeAddr("systemConfig1");
        vm.etch(systemConfig1, hex"01");
        systemConfigs[0] = ISystemConfig(systemConfig1);

        IOPContractsManagerUtils.DisputeGameConfig[] memory gameConfigs =
            new IOPContractsManagerUtils.DisputeGameConfig[](1);
        gameConfigs[0] = IOPContractsManagerUtils.DisputeGameConfig({
            enabled: true,
            initBond: 1 ether,
            gameType: GameType.wrap(0),
            gameArgs: abi.encodePacked(bytes32(uint256(0xabc)))
        });

        input.set(input.opChainConfigs.selector, configs);

        bytes memory storedConfigs = input.opChainConfigs();
        assertEq(storedConfigs, abi.encode(configs));

        // Additional verification of stored claims if needed
        IOPContractsManager.OpChainConfig[] memory decodedConfigs =
            abi.decode(storedConfigs, (IOPContractsManager.OpChainConfig[]));
        assertEq(Claim.unwrap(decodedConfigs[0].cannonPrestate), bytes32(uint256(1)));
        assertEq(Claim.unwrap(decodedConfigs[1].cannonPrestate), bytes32(uint256(2)));
    }

    function test_setAddress_withZeroAddress_reverts() public {
        vm.expectRevert("InteropMigrationInput: cannot set zero address");
        input.set(input.prank.selector, address(0));

        vm.expectRevert("InteropMigrationInput: cannot set zero address");
        input.set(input.opcm.selector, address(0));

        vm.expectRevert("InteropMigrationInput: cannot set zero address");
        input.set(input.proposer.selector, address(0));

        vm.expectRevert("InteropMigrationInput: cannot set zero address");
        input.set(input.challenger.selector, address(0));
    }

    function test_setOpChainConfigs_withEmptyArray_reverts() public {
        IOPContractsManager.OpChainConfig[] memory emptyConfigs = new IOPContractsManager.OpChainConfig[](0);

        vm.expectRevert("InteropMigrationInput: cannot set empty array");
        input.set(input.opChainConfigs.selector, emptyConfigs);
    }

    function test_set_withInvalidSelector_reverts() public {
        vm.expectRevert("InteropMigrationInput: unknown selector");
        input.set(bytes4(0xdeadbeef), makeAddr("test"));

contract MockOPCM {
    event MigrateV2Called(address indexed sysCfg, uint32 indexed gameType);

    function version() public pure returns (string memory) {
        return "7.0.0";
    }
}

contract MockOPCMRevert {
    function version() public pure returns (string memory) {
        return "7.0.0";
    }

    function migrate(IOPContractsManagerMigrator.MigrateInput memory /*_input*/ ) public pure {
        revert("MockOPCMRevert: revert migrate");
    }
}

contract InteropMigrationV2_Test is Test {
    MockOPCM mockOPCM;
    MockOPCMRevert mockOPCMRevert;
    InteropMigrationInput input;
    ISystemConfig systemConfig;
    InteropMigration migration;
    address prank;

    event MigrateV2Called(address indexed sysCfg, uint32 indexed gameType);

    function setUp() public {
        mockOPCM = new MockOPCM();
        input = new InteropMigrationInput();
        input.set(input.opcm.selector, address(mockOPCM));

        // Setup V2 migration input
        address systemConfigAddr = makeAddr("systemConfig");
        vm.etch(systemConfigAddr, hex"01");
        systemConfig = ISystemConfig(systemConfigAddr);

        ISystemConfig[] memory systemConfigs = new ISystemConfig[](1);
        systemConfigs[0] = systemConfig;

        IOPContractsManagerUtils.DisputeGameConfig[] memory gameConfigs =
            new IOPContractsManagerUtils.DisputeGameConfig[](1);
        gameConfigs[0] = IOPContractsManagerUtils.DisputeGameConfig({
            enabled: true,
            initBond: 1 ether,
            gameType: GameType.wrap(0),
            gameArgs: abi.encodePacked(bytes32(uint256(0xabc)))
        });

        IOPContractsManagerMigrator.MigrateInput memory migrateInput = IOPContractsManagerMigrator.MigrateInput({
            chainSystemConfigs: systemConfigs,
            disputeGameConfigs: gameConfigs,
            startingAnchorRoot: Proposal({ root: Hash.wrap(bytes32(uint256(1))), l2SequenceNumber: 100 }),
            startingRespectedGameType: GameType.wrap(0)
        });

        input.set(input.migrateInput.selector, migrateInput);

        prank = makeAddr("prank");
        input.set(input.prank.selector, prank);

        migration = new InteropMigration();
    }

    function test_migrateV2_succeeds() public {
        // MigrateV2Called should be emitted by the prank since it's a delegatecall.
        vm.expectEmit(address(prank));
        emit MigrateV2Called(address(systemConfig), 0);

        // mocks for post-migration checks
        address portal = makeAddr("optimismPortal");
        address dgf = makeAddr("disputeGameFactory");
        vm.mockCall(address(systemConfig), abi.encodeCall(ISystemConfig.optimismPortal, ()), abi.encode(portal));
        vm.etch(dgf, hex"01");
        vm.mockCall(portal, abi.encodeCall(IOptimismPortal.disputeGameFactory, ()), abi.encode(dgf));

        InteropMigrationOutput output = new InteropMigrationOutput();
        migration.run(input, output);

        assertEq(address(output.disputeGameFactory()), dgf);
    }

    function test_migrateV2_migrate_reverts() public {
        mockOPCMRevert = new MockOPCMRevert();
        input.set(input.opcm.selector, address(mockOPCMRevert));

        InteropMigrationOutput output = new InteropMigrationOutput();
        vm.expectRevert("MockOPCMRevert: revert migrate");
        migration.run(input, output);
    }

    function test_opcmv2_withNoCode_reverts() public {
        // Set an address with no code as OPCM
        address emptyOPCM = makeAddr("emptyOPCM");
        input.set(input.opcm.selector, emptyOPCM);

        InteropMigrationOutput output = new InteropMigrationOutput();

        vm.expectRevert("InteropMigration: OPCM address has no code");
        migration.run(input, output);
    }
}
