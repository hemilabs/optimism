// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import { Script } from "forge-std/Script.sol";

import { ISuperchainConfig } from "interfaces/L1/ISuperchainConfig.sol";
import { IProxyAdmin } from "interfaces/universal/IProxyAdmin.sol";
import { IProxy } from "interfaces/universal/IProxy.sol";
import { EIP1967Helper } from "test/mocks/EIP1967Helper.sol";

contract ReadSuperchainDeployment is Script {
    struct Input {
        ISuperchainConfig superchainConfigProxy;
    }

    struct Output {
        ISuperchainConfig superchainConfigImpl;
        ISuperchainConfig superchainConfigProxy;
        IProxyAdmin superchainProxyAdmin;
        address guardian;
        address protocolVersionsOwner;
        address superchainProxyAdminOwner;
        bytes32 recommendedProtocolVersion;
        bytes32 requiredProtocolVersion;
    }

    function run(Input memory _input) public returns (Output memory output_) {
        require(
            address(_input.superchainConfigProxy).code.length > 0,
            "ReadSuperchainDeployment: superchainConfigProxy has no code"
        );

        output_.superchainConfigProxy = _input.superchainConfigProxy;
        output_.superchainProxyAdmin = IProxyAdmin(EIP1967Helper.getAdmin(address(output_.superchainConfigProxy)));

        IProxy superchainConfigProxy = IProxy(payable(address(output_.superchainConfigProxy)));

        vm.startPrank(address(0));
        output_.superchainConfigImpl = ISuperchainConfig(superchainConfigProxy.implementation());
        vm.stopPrank();

        output_.guardian = output_.superchainConfigProxy.guardian();
        output_.superchainProxyAdminOwner = output_.superchainProxyAdmin.owner();
    }

        output_.protocolVersionsProxy = IProtocolVersions(opcm.protocolVersions());
        output_.superchainConfigProxy = ISuperchainConfig(opcm.superchainConfig());
        output_.superchainProxyAdmin = IProxyAdmin(EIP1967Helper.getAdmin(address(output_.superchainConfigProxy)));

        IProxy protocolVersionsProxy = IProxy(payable(address(output_.protocolVersionsProxy)));
        IProxy superchainConfigProxy = IProxy(payable(address(output_.superchainConfigProxy)));

        vm.startPrank(address(0));
        output_.protocolVersionsImpl = IProtocolVersions(address(protocolVersionsProxy.implementation()));
        output_.superchainConfigImpl = ISuperchainConfig(address(superchainConfigProxy.implementation()));
        output_.protocolVersionsImpl = IProtocolVersions(protocolVersionsProxy.implementation());
        output_.superchainConfigImpl = ISuperchainConfig(superchainConfigProxy.implementation());
        vm.stopPrank();

        output_.guardian = output_.superchainConfigProxy.guardian();
        output_.protocolVersionsOwner = output_.protocolVersionsProxy.owner();
        output_.superchainProxyAdminOwner = output_.superchainProxyAdmin.owner();
        output_.recommendedProtocolVersion =
            bytes32(ProtocolVersion.unwrap(output_.protocolVersionsProxy.recommended()));
        output_.requiredProtocolVersion = bytes32(ProtocolVersion.unwrap(output_.protocolVersionsProxy.required()));
    }
}
