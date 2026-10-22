//! Hemi hVM support for op-reth.
//!
//! hVM gives Hemi's EVM a deterministic view of Bitcoin. hemilabs/op-geth implements it with two
//! embedded instances of the TBC Bitcoin node: a header-only node that follows the Bitcoin headers
//! carried by Bitcoin Attributes Deposited (0x7C) transactions, and a full node whose indexers back
//! the hVM precompiles (0x40-0x49). This crate links that exact implementation (the Go bridge in
//! `hvm-go`, built from hemilabs/op-geth) into op-reth and connects it to the block executor, the
//! payload builder, the engine API, RPC and devp2p.

#![cfg_attr(not(test), warn(unused_crate_dependencies))]

mod args;
pub mod events;
mod ffi;

pub use args::HvmArgs;

use alloy_op_evm::hemi::{HemiExecution, HemiExecutionGuard, HemiExecutionHook};
use alloy_primitives::{Address, B256, Bytes};
use op_revm::hemi::{HvmPrecompileHandler, HvmPrecompileOutcome};
use serde::Serialize;
use std::sync::OnceLock;

/// Errors returned by hVM.
#[derive(Debug, thiserror::Error)]
pub enum HvmError {
    /// The full Bitcoin node is missing data needed for the block; retry later.
    #[error("hVM is missing Bitcoin data: {0}")]
    Defer(String),
    /// The block is invalid with respect to hVM consensus.
    #[error("invalid hVM block: {0}")]
    Invalid(String),
    /// hVM is not enabled.
    #[error("hVM is not enabled")]
    Disabled,
    /// Any other error.
    #[error("hVM error: {0}")]
    Other(String),
}

impl HvmError {
    fn from_status(status: i32, out: &ffi::Out) -> Self {
        let msg = out.string();
        match status {
            ffi::STATUS_DEFER => Self::Defer(msg),
            ffi::STATUS_INVALID => Self::Invalid(msg),
            ffi::STATUS_DISABLED => Self::Disabled,
            _ => Self::Other(msg),
        }
    }
}

fn check(status: i32, out: &ffi::Out) -> Result<(), HvmError> {
    if status == ffi::STATUS_OK { Ok(()) } else { Err(HvmError::from_status(status, out)) }
}

/// Configuration handed to the bridge (see `initConfig` in `hvm-go/ffi.go`).
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct HvmConfig {
    /// Whether hVM is enabled.
    pub enabled: bool,
    /// hVM Phase 0 activation timestamp.
    pub hvm0_time: Option<u64>,
    /// Bitcoin network.
    pub network: String,
    /// hVM effective genesis Bitcoin header (hex).
    pub genesis_header: String,
    /// hVM effective genesis Bitcoin height.
    pub genesis_height: u64,
    /// Data directory of the header-only TBC node.
    pub header_data_dir: String,
    /// Directory of the L2 block registry and keystones.
    pub registry_dir: String,
    /// Data directory of the full TBC node.
    pub tbc_level_db_home: String,
    /// Listen address of the full TBC node.
    pub tbc_listen_address: String,
    /// Transactions the full TBC node caches before flushing.
    pub tbc_max_cached_txs: i64,
    /// Block sanity checks of the full TBC node.
    pub tbc_block_sanity: bool,
    /// Prometheus address of the full TBC node.
    pub tbc_prometheus_address: String,
    /// Bitcoin seeds of the full TBC node.
    pub tbc_seeds: Vec<String>,
    /// geth verbosity (0-5).
    pub log_level: i32,
}

/// Handle to the hVM bridge. There is at most one per process.
#[derive(Debug)]
pub struct Hvm {
    config: HvmConfig,
}

static HVM: OnceLock<Hvm> = OnceLock::new();

/// Set once the header-only TBC node is set up; block execution waits for it.
static STARTED: (std::sync::Mutex<bool>, std::sync::Condvar) =
    (std::sync::Mutex::new(false), std::sync::Condvar::new());

fn wait_started() {
    let mut started = STARTED.0.lock().unwrap_or_else(|e| e.into_inner());
    while !*started {
        started = STARTED.1.wait(started).unwrap_or_else(|e| e.into_inner());
    }
}

/// Returns the hVM instance, if initialized.
pub fn hvm() -> Option<&'static Hvm> {
    HVM.get()
}

/// The canonical L2 block metadata hVM needs.
#[derive(Debug, Clone)]
pub struct HvmBlock {
    /// Block hash.
    pub hash: B256,
    /// Parent hash.
    pub parent_hash: B256,
    /// Block number.
    pub number: u64,
    /// Timestamp.
    pub timestamp: u64,
    /// Data of the Bitcoin Attributes Deposited transactions of the block.
    pub btc_attributes: Vec<Bytes>,
}

impl HvmBlock {
    /// Collects the hVM metadata of a block from its transactions.
    pub fn from_transactions<'a, T: alloy_consensus::Transaction + 'a>(
        hash: B256,
        parent_hash: B256,
        number: u64,
        timestamp: u64,
        transactions: impl IntoIterator<Item = &'a T>,
    ) -> Self {
        let btc_attributes = transactions
            .into_iter()
            .filter(|tx| tx.ty() == op_alloy_consensus::BTC_ATTRIBUTES_DEPOSITED_TX_TYPE_ID)
            .map(|tx| tx.input().clone())
            .collect();
        Self { hash, parent_hash, number, timestamp, btc_attributes }
    }
}

/// The Bitcoin Attributes Deposited transaction to include in a block being built.
#[derive(Debug, Clone)]
pub struct BtcAttributesTx {
    /// The HvmState contract.
    pub to: Address,
    /// Gas limit.
    pub gas: u64,
    /// `HvmState.update(...)` call data.
    pub data: Bytes,
}

impl Hvm {
    /// Initializes hVM: opens its databases and, when enabled, starts the full TBC node and waits
    /// until it is indexed to the hVM effective genesis. Installs the precompile and execution hooks.
    pub fn init(config: HvmConfig) -> Result<&'static Self, HvmError> {
        let cfg = serde_json::to_vec(&config).map_err(|e| HvmError::Other(e.to_string()))?;
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmInit(cfg.as_ptr(), cfg.len(), &mut out.ptr, &mut out.len) };
        check(status, &out)?;
        let hvm = HVM.get_or_init(|| Self { config });
        if hvm.config.enabled {
            op_revm::hemi::install_hvm_handler(Box::new(PrecompileHandler));
            alloy_op_evm::hemi::install_execution_hook(Box::new(ExecutionHook));
        }
        Ok(hvm)
    }

    /// Returns the configuration.
    pub const fn config(&self) -> &HvmConfig {
        &self.config
    }

    /// Whether hVM is enabled.
    pub const fn enabled(&self) -> bool {
        self.config.enabled
    }

    /// Whether hVM Phase 0 is active at `timestamp`.
    pub fn is_active(&self, timestamp: u64) -> bool {
        self.config.enabled && self.config.hvm0_time.is_some_and(|t| t <= timestamp)
    }

    /// Starts the header-only TBC node. The canonical head must be registered and set first.
    pub fn start(&self) -> Result<(), HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmStart(&mut out.ptr, &mut out.len) };
        let result = match status {
            ffi::STATUS_DISABLED => Ok(()),
            s => check(s, &out),
        };
        if result.is_ok() {
            *STARTED.0.lock().unwrap_or_else(|e| e.into_inner()) = true;
            STARTED.1.notify_all();
        }
        result
    }

    /// Whether a block is registered with hVM.
    pub fn has_block(&self, hash: B256) -> bool {
        // SAFETY: 32-byte hash.
        unsafe { ffi::HvmHasBlock(hash.as_ptr()) == ffi::STATUS_OK }
    }

    /// Registers the hVM metadata of an L2 block.
    pub fn register_block(&self, block: &HvmBlock) -> Result<(), HvmError> {
        let mut attrs = Vec::new();
        for a in &block.btc_attributes {
            attrs.extend_from_slice(&(a.len() as u32).to_be_bytes());
            attrs.extend_from_slice(a);
        }
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers; hashes are 32 bytes.
        let status = unsafe {
            ffi::HvmRegisterBlock(
                block.hash.as_ptr(),
                block.parent_hash.as_ptr(),
                block.number,
                block.timestamp,
                attrs.as_ptr(),
                attrs.len(),
                &mut out.ptr,
                &mut out.len,
            )
        };
        check(status, &out)
    }

    /// Makes `hash` the canonical head and moves the hVM state to it.
    pub fn set_head(&self, hash: B256) -> Result<(), HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmSetHead(hash.as_ptr(), &mut out.ptr, &mut out.len) };
        match status {
            ffi::STATUS_DISABLED => Ok(()),
            s => check(s, &out),
        }
    }

    /// Moves the hVM state to a registered block about to be executed. On success the returned
    /// guard holds the hVM lock until it is dropped.
    pub fn prepare_block(&self, hash: B256) -> Result<Option<HvmGuard>, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmPrepareBlock(hash.as_ptr(), &mut out.ptr, &mut out.len) };
        match status {
            ffi::STATUS_OK => Ok(Some(HvmGuard { ok: false })),
            ffi::STATUS_DISABLED => Ok(None),
            s => Err(HvmError::from_status(s, &out)),
        }
    }

    /// Registers a block and checks that hVM can process it now (the full Bitcoin node has the
    /// data it needs). Used by the engine API before importing a payload.
    pub fn check_block(&self, block: &HvmBlock) -> Result<(), HvmError> {
        if !self.enabled() {
            return Ok(());
        }
        // Only check (and register) blocks that extend a block hVM knows, so the registry stays a
        // connected chain: a payload whose ancestors have not been processed yet cannot be imported
        // now anyway, and checking it would move the hVM state along an unknown path.
        if !self.has_block(block.parent_hash) {
            return Ok(());
        }
        self.register_block(block)?;
        if !self.is_active(block.timestamp) {
            return Ok(());
        }
        wait_started();
        let guard = self.prepare_block(block.hash)?;
        if let Some(mut guard) = guard {
            guard.succeed();
        }
        Ok(())
    }

    /// Moves the hVM state to the parent of a block about to be built and holds the hVM lock.
    pub fn prepare_build(&self, parent: B256, timestamp: u64) -> Result<Option<HvmGuard>, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status =
            unsafe { ffi::HvmPrepareBuild(parent.as_ptr(), timestamp, &mut out.ptr, &mut out.len) };
        match status {
            ffi::STATUS_OK => Ok(Some(HvmGuard { ok: true })),
            ffi::STATUS_DISABLED => Ok(None),
            s => Err(HvmError::from_status(s, &out)),
        }
    }

    /// Builds the Bitcoin Attributes Deposited transaction for a block on top of `parent`.
    pub fn next_btc_attributes(
        &self,
        parent: B256,
        timestamp: u64,
    ) -> Result<Option<BtcAttributesTx>, HvmError> {
        if !self.is_active(timestamp) {
            return Ok(None);
        }
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status =
            unsafe { ffi::HvmNextBtcAttr(parent.as_ptr(), timestamp, &mut out.ptr, &mut out.len) };
        if status == ffi::STATUS_DISABLED {
            return Ok(None);
        }
        check(status, &out)?;
        let b = out.bytes();
        if b.len() < 28 {
            return Ok(None);
        }
        Ok(Some(BtcAttributesTx {
            to: Address::from_slice(&b[..20]),
            gas: u64::from_be_bytes(b[20..28].try_into().unwrap()),
            data: Bytes::copy_from_slice(&b[28..]),
        }))
    }

    /// Whether the full TBC node has the Bitcoin block (hash in internal byte order).
    pub fn btc_block_available(&self, hash: B256) -> bool {
        // SAFETY: 32-byte hash.
        unsafe { ffi::HvmBtcBlockAvailable(hash.as_ptr()) == ffi::STATUS_OK }
    }

    /// Bitcoin blocks hVM progression is waiting for.
    pub fn missing_btc_blocks(&self) -> Vec<B256> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmMissingBtcBlocks(&mut out.ptr, &mut out.len) };
        if status != ffi::STATUS_OK {
            return Vec::new();
        }
        out.bytes().chunks_exact(32).map(B256::from_slice).collect()
    }

    /// Serialized Bitcoin blocks to answer a peer's request with, length-prefixed.
    pub fn serve_btc_blocks(&self, hashes: &[B256]) -> Vec<Bytes> {
        let req: Vec<u8> = hashes.iter().flat_map(|h| h.0).collect();
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe {
            ffi::HvmServeBtcBlocks(req.as_ptr(), req.len(), &mut out.ptr, &mut out.len)
        };
        if status != ffi::STATUS_OK {
            return Vec::new();
        }
        decode_list(&out.bytes())
    }

    /// Inserts Bitcoin blocks received from a peer; returns the ancestors to request from it.
    pub fn ingest_btc_blocks(&self, blocks: &[Bytes]) -> Result<Vec<B256>, HvmError> {
        let mut data = Vec::new();
        for b in blocks {
            data.extend_from_slice(&(b.len() as u32).to_be_bytes());
            data.extend_from_slice(b);
        }
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe {
            ffi::HvmIngestBtcBlocks(data.as_ptr(), data.len(), &mut out.ptr, &mut out.len)
        };
        check(status, &out)?;
        Ok(out.bytes().chunks_exact(32).map(B256::from_slice).collect())
    }

    /// Stores a keystone sent by op-node; returns its abbreviated hash (hex).
    pub fn new_keystone(&self, keystone_json: &[u8]) -> Result<String, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe {
            ffi::HvmNewKeystone(
                keystone_json.as_ptr(),
                keystone_json.len(),
                &mut out.ptr,
                &mut out.len,
            )
        };
        check(status, &out)?;
        Ok(out.string())
    }

    /// Stores the keystone of a canonical L2 block; returns whether it was new.
    #[expect(clippy::too_many_arguments)]
    pub fn upsert_keystone(
        &self,
        l1_block: u32,
        l2_block: u32,
        parent: B256,
        prev: Option<B256>,
        state_root: B256,
        ep_hash: B256,
    ) -> Result<bool, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers; `prev` is null or 32 bytes.
        let status = unsafe {
            ffi::HvmUpsertKeystone(
                l1_block,
                l2_block,
                parent.as_ptr(),
                prev.as_ref().map_or(core::ptr::null(), |p| p.as_ptr()),
                state_root.as_ptr(),
                ep_hash.as_ptr(),
                &mut out.ptr,
                &mut out.len,
            )
        };
        match status {
            ffi::STATUS_OK => Ok(true),
            ffi::STATUS_DEFER => Ok(false),
            s => Err(HvmError::from_status(s, &out)),
        }
    }

    /// Deletes the keystones above `height`.
    pub fn delete_keystones_above(&self, height: u64) -> Result<(), HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmDeleteKeystonesAbove(height, &mut out.ptr, &mut out.len) };
        check(status, &out)
    }

    /// `kss_getLatestKeystones` response (JSON).
    pub fn kss_latest_keystones(&self, count: u64) -> Result<serde_json::Value, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe { ffi::HvmKssGetLatestKeystones(count, &mut out.ptr, &mut out.len) };
        check(status, &out)?;
        serde_json::from_slice(&out.bytes()).map_err(|e| HvmError::Other(e.to_string()))
    }

    /// `kss_getKeystone` response (JSON).
    pub fn kss_keystone(&self, abrev_hash: &str, count: u64) -> Result<serde_json::Value, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe {
            ffi::HvmKssGetKeystone(
                abrev_hash.as_ptr(),
                abrev_hash.len(),
                count,
                &mut out.ptr,
                &mut out.len,
            )
        };
        check(status, &out)?;
        serde_json::from_slice(&out.bytes()).map_err(|e| HvmError::Other(e.to_string()))
    }

    /// `engine_popPayoutsByL2Keystone` result (JSON); `abrev_hash_json` is the JSON parameter.
    pub fn pop_payouts(&self, abrev_hash_json: &[u8]) -> Result<serde_json::Value, HvmError> {
        let mut out = ffi::Out::new();
        // SAFETY: valid buffers.
        let status = unsafe {
            ffi::HvmPopPayouts(
                abrev_hash_json.as_ptr(),
                abrev_hash_json.len(),
                &mut out.ptr,
                &mut out.len,
            )
        };
        check(status, &out)?;
        serde_json::from_slice(&out.bytes()).map_err(|e| HvmError::Other(e.to_string()))
    }
}

/// The Bitcoin blocks a Bitcoin Attributes Deposited transaction refers to (canonical tip and
/// headers), hashes in internal byte order.
pub fn btc_attributes_block_hashes(data: &[u8]) -> Result<Vec<B256>, HvmError> {
    let mut out = ffi::Out::new();
    // SAFETY: valid buffers.
    let status =
        unsafe { ffi::HvmBtcAttrHashes(data.as_ptr(), data.len(), &mut out.ptr, &mut out.len) };
    check(status, &out)?;
    Ok(out.bytes().chunks_exact(32).map(B256::from_slice).collect())
}

fn decode_list(mut b: &[u8]) -> Vec<Bytes> {
    let mut items = Vec::new();
    while b.len() >= 4 {
        let l = u32::from_be_bytes(b[..4].try_into().unwrap()) as usize;
        b = &b[4..];
        if b.len() < l {
            break;
        }
        items.push(Bytes::copy_from_slice(&b[..l]));
        b = &b[l..];
    }
    items
}

/// Holds the hVM lock while a block executes; reverts the hVM state on drop unless the execution
/// succeeded.
#[derive(Debug)]
pub struct HvmGuard {
    ok: bool,
}

impl HemiExecutionGuard for HvmGuard {
    fn succeed(&mut self) {
        self.ok = true;
    }
}

impl Drop for HvmGuard {
    fn drop(&mut self) {
        // SAFETY: plain call.
        unsafe { ffi::HvmFinishBlock(i32::from(self.ok)) }
    }
}

/// Runs the hVM precompiles through the bridge.
#[derive(Debug)]
struct PrecompileHandler;

impl HvmPrecompileHandler for PrecompileHandler {
    fn is_active(&self, timestamp: u64) -> bool {
        hvm().is_some_and(|h| h.is_active(timestamp))
    }

    fn run(&self, id: u8, input: &[u8], gas_limit: u64) -> HvmPrecompileOutcome {
        let mut out = ffi::Out::new();
        let mut gas_used = 0u64;
        // SAFETY: valid buffers.
        let status = unsafe {
            ffi::HvmPrecompile(
                id,
                input.as_ptr(),
                input.len(),
                gas_limit,
                &mut gas_used,
                &mut out.ptr,
                &mut out.len,
            )
        };
        match status {
            ffi::STATUS_OK => HvmPrecompileOutcome::Success { gas_used, output: out.bytes() },
            ffi::STATUS_OUT_OF_GAS => HvmPrecompileOutcome::OutOfGas,
            _ => HvmPrecompileOutcome::Error(out.string()),
        }
    }
}

/// How long block execution waits for missing Bitcoin data before failing.
const DEFER_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(600);

/// Moves the hVM state before each block execution.
#[derive(Debug)]
struct ExecutionHook;

impl HemiExecutionHook for ExecutionHook {
    fn begin(
        &self,
        execution: &HemiExecution,
    ) -> Result<Option<Box<dyn HemiExecutionGuard>>, String> {
        let Some(hvm) = hvm() else { return Ok(None) };
        wait_started();
        match execution {
            HemiExecution::Block { hash, parent_hash, number, timestamp, btc_attributes } => {
                // Register every block, so the parent of the first hVM block is known.
                let block = HvmBlock {
                    hash: *hash,
                    parent_hash: *parent_hash,
                    number: *number,
                    timestamp: *timestamp,
                    btc_attributes: btc_attributes.clone(),
                };
                hvm.register_block(&block).map_err(|e| e.to_string())?;
                if !hvm.is_active(*timestamp) {
                    return Ok(None);
                }
                // Missing Bitcoin data is transient (the full node or L2 peers will deliver it):
                // wait for it rather than rejecting a valid block. The engine API checks ahead of
                // execution, so this only waits on sync paths.
                let start = std::time::Instant::now();
                loop {
                    match hvm.prepare_block(*hash) {
                        Ok(guard) => {
                            return Ok(guard.map(|g| Box::new(g) as Box<dyn HemiExecutionGuard>));
                        }
                        Err(HvmError::Defer(msg)) if start.elapsed() < DEFER_TIMEOUT => {
                            tracing::debug!(target: "hemi::hvm", %hash, %msg, "waiting for Bitcoin data");
                            std::thread::sleep(std::time::Duration::from_millis(500));
                        }
                        Err(err) => return Err(err.to_string()),
                    }
                }
            }
            HemiExecution::Build { parent_hash, timestamp } => {
                if !hvm.is_active(*timestamp) {
                    return Ok(None);
                }
                let guard = hvm.prepare_build(*parent_hash, *timestamp).map_err(|e| e.to_string())?;
                Ok(guard.map(|g| Box::new(g) as Box<dyn HemiExecutionGuard>))
            }
        }
    }
}
