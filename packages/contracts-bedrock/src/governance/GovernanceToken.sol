// SPDX-License-Identifier: MIT
pragma solidity 0.8.15;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/token/ERC20/extensions/ERC20Burnable.sol";
import "@openzeppelin/contracts/token/ERC20/extensions/ERC20Votes.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

/// @custom:predeploy 0x4200000000000000000000000000000000000042
/// @title GovernanceToken
/// @notice The Optimism token used in governance and supporting voting and delegation. Implements
///         EIP 2612 allowing signed approvals. Contract is "owned" by a `MintManager` instance with
///         permission to the `mint` function only, for the purposes of enforcing the token
///         inflation schedule. Tokens minted by the protocol for PoP rewards do not go through
///         the MintManager.
contract GovernanceToken is ERC20Burnable, ERC20Votes, Ownable {
    // Holds the most recent block height in which PoP Payouts were processed
    uint64 public lastBlockRewarded = 0;

    uint256 public totalPoPRewards = 0;

    // @notice Address of the special depositor account.
    address public constant DEPOSITOR_ACCOUNT = 0x8888888888888888888888888888888888888888;

    /// @notice Constructs the GovernanceToken contract.
    constructor() ERC20("xxxtestnet", "XXXt") ERC20Permit("xxxtestnet") { }

    /// @notice Allows the owner to mint tokens.
    /// @param _account The account receiving minted tokens.
    /// @param _amount  The amount of tokens to mint.
    function mint(address _account, uint256 _amount) public onlyOwner {
        _mint(_account, _amount);
    }

    /// @notice Allows the protocol to mint tokens for PoP payouts
    /// @param _accounts The accounts receiving minted tokens.
    /// @param _amounts  The amounts of tokens to mint to the respective addresses.
    function mintPoPRewards(uint64 _blockRewarded, address[] calldata _accounts, uint256[] calldata _amounts) public {
        require(msg.sender == DEPOSITOR_ACCOUNT, "GovernanceToken: Only the depositor account can execute PoP payouts");
        require(_accounts.length == _amounts.length, "GovernanceToken: Minting PoP rewards requires the accounts and amounts to be the same");

        for (uint i = 0; i < _accounts.length; i++) {
            _mint(_accounts[i], _amounts[i]);
            totalPoPRewards += _amounts[i];
        }

        lastBlockRewarded = _blockRewarded;

        // TODO: set in-contract limits to mintage to make sure compromised Sequencer can't hyperinflate
    }

    /// @notice Callback called after a token transfer.
    /// @param from   The account sending tokens.
    /// @param to     The account receiving tokens.
    /// @param amount The amount of tokens being transfered.
    function _afterTokenTransfer(address from, address to, uint256 amount) internal override(ERC20, ERC20Votes) {
        super._afterTokenTransfer(from, to, amount);
    }

    /// @notice Internal mint function.
    /// @param to     The account receiving minted tokens.
    /// @param amount The amount of tokens to mint.
    function _mint(address to, uint256 amount) internal override(ERC20, ERC20Votes) {
        super._mint(to, amount);
    }

    /// @notice Internal burn function.
    /// @param account The account that tokens will be burned from.
    /// @param amount  The amount of tokens that will be burned.
    function _burn(address account, uint256 amount) internal override(ERC20, ERC20Votes) {
        super._burn(account, amount);
    }
}
