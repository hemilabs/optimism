//! Hemi hVM precompiles.
//!
//! When a Hemi node installed an [`op_revm::hemi::HvmPrecompileHandler`] and hVM Phase 0 is active
//! at the block timestamp, the EVM gets the ten hVM precompiles (`0x40`-`0x49`). Their results
//! depend on the hVM view of Bitcoin, which moves with the L2 chain, so they are never cached.

use alloy_evm::precompiles::{DynPrecompile, PrecompileInput, PrecompilesMap};
use op_revm::hemi::{HvmPrecompileOutcome, hvm_handler, hvm_precompile_addresses};
use revm::precompile::{PrecompileHalt, PrecompileId, PrecompileOutput};

/// Adds the hVM precompiles to `precompiles` if they are active at `timestamp`.
pub fn extend_with_hvm_precompiles(precompiles: &mut PrecompilesMap, timestamp: u64) {
    let Some(handler) = hvm_handler() else { return };
    if !handler.is_active(timestamp) {
        return;
    }
    precompiles.extend_precompiles(hvm_precompile_addresses().map(|address| {
        let id = address.as_slice()[19];
        let precompile = DynPrecompile::new_stateful(
            PrecompileId::Custom(alloc::format!("hvm-0x{id:02x}").into()),
            move |input: PrecompileInput<'_>| {
                let Some(handler) = hvm_handler() else {
                    return Ok(PrecompileOutput::halt(
                        PrecompileHalt::other("hVM handler missing"),
                        input.reservoir,
                    ));
                };
                Ok(match handler.run(id, input.data, input.gas) {
                    HvmPrecompileOutcome::Success { gas_used, output } => {
                        PrecompileOutput::new(gas_used, output.into(), input.reservoir)
                    }
                    HvmPrecompileOutcome::OutOfGas => {
                        PrecompileOutput::halt(PrecompileHalt::OutOfGas, input.reservoir)
                    }
                    HvmPrecompileOutcome::Error(err) => {
                        PrecompileOutput::halt(PrecompileHalt::other(err), input.reservoir)
                    }
                })
            },
        );
        (address, precompile)
    }));
}

/// What a block executor is doing, as far as hVM is concerned.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum HemiExecution {
    /// Executing an existing block (validation, sync or re-execution).
    Block {
        /// Block hash.
        hash: alloy_primitives::B256,
        /// Parent hash.
        parent_hash: alloy_primitives::B256,
        /// Block number.
        number: u64,
        /// Block timestamp.
        timestamp: u64,
        /// Data of the block's Bitcoin Attributes Deposited transactions.
        btc_attributes: alloc::vec::Vec<alloy_primitives::Bytes>,
    },
    /// Building a new block on top of `parent_hash`.
    Build {
        /// Parent hash.
        parent_hash: alloy_primitives::B256,
        /// Timestamp of the block being built.
        timestamp: u64,
    },
}

/// Moves the hVM state of a Hemi node before a block is executed.
pub trait HemiExecutionHook: Send + Sync + core::fmt::Debug {
    /// Prepares the hVM state for `execution`. The returned guard is held while the block executes
    /// and dropped afterwards; [`HemiExecutionGuard::succeed`] is called first if execution
    /// succeeded.
    fn begin(
        &self,
        execution: &HemiExecution,
    ) -> Result<Option<alloc::boxed::Box<dyn HemiExecutionGuard>>, alloc::string::String>;
}

/// Held while a block executes, see [`HemiExecutionHook::begin`].
pub trait HemiExecutionGuard: Send + Sync + core::fmt::Debug {
    /// Marks the execution as successful.
    fn succeed(&mut self);
}

static EXECUTION_HOOK: revm::primitives::OnceLock<alloc::boxed::Box<dyn HemiExecutionHook>> =
    revm::primitives::OnceLock::new();

/// Installs the hVM execution hook. Returns `false` if one was already installed.
pub fn install_execution_hook(hook: alloc::boxed::Box<dyn HemiExecutionHook>) -> bool {
    EXECUTION_HOOK.set(hook).is_ok()
}

/// Returns the installed hVM execution hook.
pub fn execution_hook() -> Option<&'static dyn HemiExecutionHook> {
    EXECUTION_HOOK.get().map(|h| h.as_ref())
}

/// Error raised when hVM refuses a block.
#[derive(Debug, thiserror::Error)]
#[error("hVM: {0}")]
pub struct HemiExecutionError(pub alloc::string::String);
