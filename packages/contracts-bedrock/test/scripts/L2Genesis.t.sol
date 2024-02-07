// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Test } from "forge-std/Test.sol";
import { stdJson } from "forge-std/StdJson.sol";
import { console2 as console } from "forge-std/console2.sol";
import { Strings } from "@openzeppelin/contracts/utils/Strings.sol";

import { Artifacts } from "scripts/Artifacts.s.sol";
import { DeployConfig } from "scripts/DeployConfig.s.sol";
import { Executables } from "scripts/Executables.sol";
import { Predeploys } from "src/libraries/Predeploys.sol";
import { L2GenesisFixtures } from "test/fixtures/L2GenesisFixtures.sol";
import { L2GenesisHelpers } from "scripts/libraries/L2GenesisHelpers.sol";

/// @notice Reads a `genesis-l2.json` file, parses the `alloc`s, and runs assertions
///         against each alloc depending on whether it's a precompile, predeploy proxy,
///         or predeploy implementation.
contract L2Genesis_Test is Test, Artifacts {
    DeployConfig public constant cfg =
        DeployConfig(address(uint160(uint256(keccak256(abi.encode("optimism.deployconfig"))))));

    string internal genesisPath;
    L2GenesisFixtures l2GenesisFixtures;

    struct StorageData {
        bytes32 key;
        bytes32 value;
    }

    /// @notice `balance` and `nonce` are being parsed as `bytes` even though their JSON representations are hex strings.
    ///         This is because Foundry has a limitation around parsing strings as numbers when using `vm.parseJson`,
    ///         and because we're using `abi.decode` to convert the JSON string, we can't use coersion (i.e. `vm.parseJsonUint`)
    ///         to tell Foundry that the strings are numbers. So instead we treat them as `byte` strings and parse as
    ///         `uint`s when needed. Additional context: https://github.com/foundry-rs/foundry/issues/3754
    struct Alloc {
        address addr;
        bytes balance;
        bytes code;
        bytes nonce;
        StorageData[] storageData;
    }

    /// @notice Checks if deploy config file exists at `deployConfigPath`, if it doesn't
    ///         reverts with warning to run `make devnet-allocs` at monorepo root.
    ///         Afterwards, checks if genesis file exists at `genesisPath`, and if not
    ///         runs `L2Genesis.s.sol` script to generate it.
    function setUp() public override {
        super.setUp();
        Artifacts.setUp();

        string memory deployConfigPath = string.concat(vm.projectRoot(), "/deploy-config/", deploymentContext, ".json");
        if (!vm.exists(deployConfigPath)) {
            revert(string.concat(
                "Did not find deploy config at: ",
                deployConfigPath,
                ", try running make devnet-allocs in monorepo root"
            ));
        }

        vm.etch(address(cfg), vm.getDeployedCode("DeployConfig.s.sol:DeployConfig"));
        vm.label(address(cfg), "DeployConfig");
        vm.allowCheatcodes(address(cfg));
        cfg.read(deployConfigPath);

        genesisPath = string.concat(vm.projectRoot(), "/deployments/", deploymentContext, "/genesis-l2.json");
        if (!vm.exists(genesisPath)) {
            string[] memory commands = new string[](3);
            commands[0] = "bash";
            commands[1] = "-c";
            commands[2] = string.concat(
                "DEPLOYMENT_CONTEXT=",
                deploymentContext,
                " forge script --chain-id ",
                Strings.toString(cfg.l2ChainID()),
                " ./scripts/L2Genesis.s.sol:L2Genesis"
            );
            vm.ffi(commands);
        }

        l2GenesisFixtures = new L2GenesisFixtures();
        l2GenesisFixtures.setUp();
    }

    /// @notice Iterates over every alloc parsed from `genesisPath`, and depending on
    ///         the alloc address, runs specific checks based on whether the alloc is
    ///         a precompile, predeploy proxy, or predeploy implementation. If the
    ///         alloc address doesn't pass any of the checks to determine what it is,
    ///         the function reverts with
    ///         `string.concat("Unknown alloc: ", Strings.toHexString(allocs[i].addr)`.
    function test_allocs() external {
        Alloc[] memory allocs = _parseAllocs(genesisPath);

        for(uint256 i; i < allocs.length; i++) {
            uint160 numericAddress = uint160(allocs[i].addr);
            if (numericAddress < L2GenesisHelpers.PRECOMPILE_COUNT) {
                _checkPrecompile(allocs[i]);
            } else if (_isProxyAddress(allocs[i].addr)) {
                _checkProxy(allocs[i]);
            } else if (_isImplementationAddress(allocs[i].addr)) {
                _checkImplementation(allocs[i]);
            } else {
                revert(string.concat("Unknown alloc: ", Strings.toHexString(allocs[i].addr)));
            }
        }
    }

    /// @notice Runs checks against `_alloc` to determine if it's an expected precompile.
    ///         The following should hold true for every precompile:
    ///         1. The alloc should have a balance of `1`.
    ///         2. The alloc should not have `code` set.
    ///         3. The alloc should have a `nonce` of `0`.
    ///         4. The alloc should not have any storage slots set.
    function _checkPrecompile(Alloc memory _alloc) internal {
        assertEq(_alloc.balance, hex'01');
        assertEq(_alloc.code, hex'');
        assertEq(_alloc.nonce, hex'00');
        assertEq(_alloc.storageData.length, 0);
    }

    /// @notice Runs checks against `_alloc` to determine if it's an expected predeploy proxy.
    ///         The following should hold true for every predeploy proxy:
    ///         1. The alloc should have a balance of `0`.
    ///         2. The alloc should have `code` set `Proxy.sol` deployed bytecode.
    ///         3. The alloc should have a `nonce` of `0`.
    ///         4. The alloc should two storage slots set:
    ///            1. L2GenesisHelpers.PROXY_ADMIN_ADDRESS
    ///            2. L2GenesisHelpers.PROXY_IMPLEMENTATION_ADDRESS
    function _checkProxy(Alloc memory _alloc) internal {
        assertEq(_alloc.balance, hex'00');
        assertEq(_alloc.nonce, hex'00');

        if (!L2GenesisHelpers._notProxied(_alloc.addr)) {
            assertEq(_alloc.code, vm.getDeployedCode("Proxy.sol:Proxy"));
        }

        _checkStorage(_alloc);
    }

    /// @notice Runs checks against `_alloc` to determine if it's an expected predeploy implementation.
    ///         The following should hold true for every predeploy implementation:
    ///         1. The alloc should have a balance of `0`.
    ///         2. The alloc should have `code` set to it's respective predeploy deployed bytecode.
    ///         3. The alloc should have a `nonce` of `0`.
    ///         4. The alloc should have the storage slots set according to the specifc predeploy.
    function _checkImplementation(Alloc memory _alloc) internal {
        assertEq(_alloc.balance, hex'00');
        assertEq(_alloc.nonce, hex'00');

        _checkStorage(_alloc);

        assertEq(
            _alloc.code,
            l2GenesisFixtures.getExpectedDeployedBytecode(_alloc.addr)
        );
    }

    /// @notice Parses a given `_filePath` into a `Alloc[]`.
    function _parseAllocs(string memory _filePath) internal returns(Alloc[] memory) {
        string[] memory cmd = new string[](3);
        cmd[0] = "bash";
        cmd[1] = "-c";
        cmd[2] = string.concat(
            Executables.jq,
            " -cr 'to_entries | map({addr: .key, balance: .value.balance, code: .value.code, nonce: .value.nonce, storageData: (.value.storage | to_entries | map({key: .key, value: .value}))})' ",
            _filePath
        );
        bytes memory result = vm.ffi(cmd);
        bytes memory parsedJson = vm.parseJson(string(result));
        return abi.decode(parsedJson, (Alloc[]));
    }

    /// @notice Returns whether the given address has a given prefix.
    function _addressHasPrefix(address _addr, uint160 _prefix, uint160 _mask) internal pure returns(bool) {
        uint160 numericAddress = uint160(_addr);
        return (numericAddress & _mask) == _prefix;
    }

    /// @notice Returns whether a given address has the expected predeploy proxy prefix.
    function _isProxyAddress(address _addr) internal pure returns(bool) {
        return _addressHasPrefix(
            _addr,
            uint160(0x4200000000000000000000000000000000000000),
            uint160(0xfFFFFfffFFFfFfFFffFFFfFfffFFfFfF00000000)
        );
    }

    /// @notice Returns whether a given address has the expected predeploy implementation prefix.
    function _isImplementationAddress(address _addr) internal pure returns(bool) {
        return _addressHasPrefix(
            _addr,
            uint160(0xc0D3C0d3C0d3C0D3c0d3C0d3c0D3C0d3c0d30000),
            uint160(0xfFfffFFFfffFFfFFFFffFFFFffffFfFFFFff0000)
        );
    }

    /// @notice Checks whether a given alloc has the expected number of set storage slots,
    ///         whether all the set storage slots are expected to be set, and whether the values of
    ///         the set storage slots matches what's expected.
    function _checkStorage(Alloc memory _alloc) internal {
        /// First we assert we have the same number of set storage slots for `_alloc` that we expect to be set.
        assertEq(_alloc.storageData.length, l2GenesisFixtures.getNumExpectedSlotKeys(_alloc.addr));
        /// Then we loop through all of `_alloc`'s storage slots and check if that storage slot is supposed be set,
        /// lastly we assert that the corresponding slot value matches what's expected for that slot.
        for (uint256 i; i < _alloc.storageData.length; i++) {
            assertTrue(l2GenesisFixtures.isExpectedSlotKey(_alloc.addr, _alloc.storageData[i].key));
            assertEq(_alloc.storageData[i].value, l2GenesisFixtures.getSlotValueByKey(_alloc.addr, _alloc.storageData[i].key));
        }
    }
}
