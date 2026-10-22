//! Hemi hVM precompiles.
//!
//! hemilabs/op-geth adds ten precompiles at `0x40`-`0x49` from the hVM Phase 0 fork on. They answer
//! queries about Bitcoin (balances, UTXOs, transactions, headers) from an embedded Bitcoin indexer
//! (TBC). op-revm does not implement them itself: a Hemi node installs an [`HvmPrecompileHandler`]
//! with [`install_hvm_handler`] that runs them. Without a handler, op-revm behaves exactly like
//! upstream.
//!
//! On a Hemi chain op-geth also pre-warms only the hVM precompiles once an OP-stack fork from Fjord
//! on is active (the standard precompiles are charged a cold access on first use); see
//! [`hemi_warm_addresses`].

use revm::primitives::{Address, AddressSet, OnceLock};
use std::{boxed::Box, string::String, vec::Vec};

/// First hVM precompile address byte (`0x40`, BTC balance by address).
pub const HVM_PRECOMPILE_FIRST: u8 = 0x40;
/// Last hVM precompile address byte (`0x49`, BTC transaction input witness).
pub const HVM_PRECOMPILE_LAST: u8 = 0x49;

/// Returns the hVM precompile id (the last address byte) if `address` is one.
pub fn hvm_precompile_id(address: &Address) -> Option<u8> {
    let bytes = address.as_slice();
    if bytes[..19].iter().all(|b| *b == 0) &&
        (HVM_PRECOMPILE_FIRST..=HVM_PRECOMPILE_LAST).contains(&bytes[19])
    {
        Some(bytes[19])
    } else {
        None
    }
}

/// Addresses of the hVM precompiles.
pub fn hvm_precompile_addresses() -> impl Iterator<Item = Address> {
    (HVM_PRECOMPILE_FIRST..=HVM_PRECOMPILE_LAST).map(|b| {
        let mut a = [0u8; 20];
        a[19] = b;
        Address::from(a)
    })
}

/// Outcome of an hVM precompile call.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HvmPrecompileOutcome {
    /// The call succeeded, consuming `gas_used` and returning `output`.
    Success {
        /// Gas consumed.
        gas_used: u64,
        /// Return data.
        output: Vec<u8>,
    },
    /// The gas limit is below the precompile's required gas.
    OutOfGas,
    /// The call failed and consumes all gas.
    Error(String),
}

/// Runs the hVM precompiles of a Hemi node.
pub trait HvmPrecompileHandler: Send + Sync + core::fmt::Debug {
    /// Returns whether hVM Phase 0 is active at the given block timestamp.
    fn is_active(&self, timestamp: u64) -> bool;

    /// Runs the hVM precompile `id` (`0x40`-`0x49`).
    fn run(&self, id: u8, input: &[u8], gas_limit: u64) -> HvmPrecompileOutcome;
}

static HVM_HANDLER: OnceLock<Box<dyn HvmPrecompileHandler>> = OnceLock::new();

/// Installs the hVM precompile handler. Returns `false` if one was already installed.
pub fn install_hvm_handler(handler: Box<dyn HvmPrecompileHandler>) -> bool {
    HVM_HANDLER.set(handler).is_ok()
}

/// Returns the installed hVM precompile handler, if any.
pub fn hvm_handler() -> Option<&'static dyn HvmPrecompileHandler> {
    HVM_HANDLER.get().map(|h| h.as_ref())
}

/// Builds the set of addresses hemilabs/op-geth pre-warms at the start of a transaction
/// (`vm.ActivePrecompiles`): the standard precompiles before Fjord and none from Fjord on, plus the
/// hVM precompiles when hVM Phase 0 is active.
pub fn hemi_warm_addresses(
    standard: &AddressSet,
    fjord_active: bool,
    hvm_active: bool,
) -> AddressSet {
    let mut warm = if fjord_active { AddressSet::default() } else { standard.clone() };
    if hvm_active {
        warm.extend(hvm_precompile_addresses());
    }
    warm
}

/// Replaces the precompile addresses warmed by `load_accounts` with the set hemilabs/op-geth warms
/// (see [`hemi_warm_addresses`]). No-op unless an hVM handler is installed.
pub fn apply_hemi_warm_precompiles<CTX>(ctx: &mut CTX)
where
    CTX: revm::context_interface::ContextTr<Cfg: revm::context_interface::Cfg<Spec = crate::OpSpecId>>,
{
    use revm::context_interface::{Block, Cfg, JournalTr};
    let Some(handler) = hvm_handler() else { return };
    let timestamp: u64 = ctx.block().timestamp().saturating_to();
    let active = handler.is_active(timestamp);
    let fjord = ctx.cfg().spec().is_enabled_in(crate::OpSpecId::FJORD);
    let warm = hemi_warm_addresses(ctx.journal().precompile_addresses(), fjord, active);
    ctx.journal_mut().warm_precompiles(&warm);
}

#[cfg(test)]
mod tests {
    use super::*;
    use revm::primitives::address;

    #[test]
    fn precompile_ids() {
        assert_eq!(hvm_precompile_id(&address!("0x0000000000000000000000000000000000000040")), Some(0x40));
        assert_eq!(hvm_precompile_id(&address!("0x0000000000000000000000000000000000000049")), Some(0x49));
        assert_eq!(hvm_precompile_id(&address!("0x000000000000000000000000000000000000004a")), None);
        assert_eq!(hvm_precompile_id(&address!("0x0000000000000000000000000000000000000140")), None);
        assert_eq!(hvm_precompile_addresses().count(), 10);
    }
}
