//! Hemi hVM integration of the op-reth node: hVM startup, canonical chain tracking, keystone
//! backfill, the `kss` RPC namespace and the Bitcoin block devp2p subprotocol.

pub mod p2p;
pub mod rpc;

pub use p2p::HemiBtcProtocol;
pub use rpc::kss_rpc_module;

use alloy_consensus::{BlockHeader, Transaction, Typed2718};
use alloy_primitives::B256;
use reth_chain_state::CanonStateSubscriptions;
use reth_node_api::NodePrimitives;
use reth_optimism_forks::OpHardforks;
use reth_optimism_hemi::{Hvm, HvmArgs, HvmBlock};
use reth_primitives_traits::{Block as _, BlockBody as _};
use reth_provider::{BlockIdReader, BlockNumReader, BlockReader, HeaderProvider};
use reth_tasks::TaskExecutor;
use std::{path::Path, sync::Arc};
use tracing::{error, info, warn};

/// Reads `hvm0Time` from the chain configuration of the genesis (hemilabs/op-geth
/// `params.ChainConfig.Hvm0Time`).
pub fn genesis_hvm0_time(genesis: &alloy_genesis::Genesis) -> Option<u64> {
    genesis.config.extra_fields.get("hvm0Time").and_then(|v| v.as_u64())
}

/// Initializes hVM. When enabled this starts the full TBC node and blocks until it is indexed to
/// the hVM effective genesis, like hemilabs/op-geth does at startup.
pub async fn init_hvm(
    args: &HvmArgs,
    genesis: &alloy_genesis::Genesis,
    datadir: &Path,
) -> eyre::Result<()> {
    let config = args.to_config(genesis_hvm0_time(genesis), datadir);
    info!(target: "hemi::hvm", enabled = config.enabled, hvm0 = ?config.hvm0_time,
        network = %config.network, "initializing hVM; waiting for the TBC full node to reach the hVM genesis");
    tokio::task::spawn_blocking(move || Hvm::init(config))
        .await?
        .map_err(|e| eyre::eyre!("hVM initialization failed: {e}"))?;
    Ok(())
}

fn hvm_block<H: BlockHeader, B: reth_primitives_traits::BlockBody>(
    header: &H,
    body: &B,
    hash: B256,
) -> HvmBlock {
    HvmBlock::from_transactions(
        hash,
        header.parent_hash(),
        header.number(),
        header.timestamp(),
        body.transactions(),
    )
}

/// Registers the canonical chain with hVM (back to the hVM activation or to the first block hVM
/// already knows), sets the head and starts the header-only TBC node.
fn start_hvm<P>(provider: &P, hvm: &'static Hvm) -> eyre::Result<()>
where
    P: BlockReader + HeaderProvider + BlockNumReader,
{
    let best = provider.best_block_number()?;
    let mut number = best;
    let mut head = None;
    loop {
        let Some(header) = provider.sealed_header(number)? else { break };
        let hash = header.hash();
        if head.is_none() {
            head = Some(hash);
        } else if hvm.has_block(hash) {
            break;
        }
        let Some(block) = provider.block_by_number(number)? else { break };
        hvm.register_block(&hvm_block(block.header(), block.body(), hash))?;
        // the registry needs the chain from the block before the hVM activation on
        if number == 0 || !hvm.is_active(header.timestamp()) {
            break;
        }
        number -= 1;
    }
    let head = head.ok_or_else(|| eyre::eyre!("no canonical head"))?;
    hvm.set_head(head)?;
    hvm.start()?;
    info!(target: "hemi::hvm", %head, number = best, "hVM started");
    Ok(())
}

/// Starts hVM for a launched node and spawns the tasks that keep it in sync with the canonical
/// chain and backfill keystones.
pub fn on_node_started<P, N>(provider: P, chain_spec: Arc<impl OpHardforks + Send + Sync + 'static>, executor: TaskExecutor)
where
    N: NodePrimitives,
    P: BlockReader<Block = N::Block>
        + HeaderProvider
        + BlockNumReader
        + BlockIdReader
        + CanonStateSubscriptions<Primitives = N>
        + Clone
        + 'static,
{
    let Some(hvm) = reth_optimism_hemi::hvm() else { return };
    if !hvm.enabled() {
        return;
    }

    let mut notifications = provider.subscribe_to_canonical_state();
    let start_provider = provider.clone();
    executor.spawn_critical_task("hemi-hvm", async move {
        let started = tokio::task::spawn_blocking(move || start_hvm(&start_provider, hvm)).await;
        match started {
            Ok(Ok(())) => {}
            Ok(Err(err)) => {
                error!(target: "hemi::hvm", %err, "failed to start hVM");
                return;
            }
            Err(err) => {
                error!(target: "hemi::hvm", %err, "failed to start hVM");
                return;
            }
        }
        // Follow the canonical chain: register new blocks and move the hVM state to the new head,
        // as op-geth does when it writes a new head block.
        loop {
            let notification = match notifications.recv().await {
                Ok(n) => n,
                Err(tokio::sync::broadcast::error::RecvError::Lagged(skipped)) => {
                    warn!(target: "hemi::hvm", skipped, "lagged behind canonical notifications");
                    continue;
                }
                Err(_) => return,
            };
            let chain = notification.committed();
            let blocks: Vec<HvmBlock> = chain
                .blocks_iter()
                .map(|b| hvm_block(b.header(), b.body(), b.hash()))
                .collect();
            let tip = chain.tip().hash();
            let res = tokio::task::spawn_blocking(move || -> Result<(), reth_optimism_hemi::HvmError> {
                for b in &blocks {
                    hvm.register_block(b)?;
                }
                hvm.set_head(tip)
            })
            .await;
            if let Ok(Err(err)) = res {
                warn!(target: "hemi::hvm", %tip, %err, "failed to move hVM state to the new head");
            }
        }
    });

    executor.spawn_critical_task("hemi-keystones", async move {
        let mut backfilled = false;
        loop {
            reth_optimism_hemi::events::backfill().notified().await;
            let provider = provider.clone();
            let chain_spec = chain_spec.clone();
            let res = tokio::task::spawn_blocking(move || {
                backfill_keystones(&provider, &*chain_spec, hvm, backfilled)
            })
            .await;
            match res {
                Ok(Ok(())) => backfilled = true,
                Ok(Err(err)) => error!(target: "hemi::keystones", %err, "error backfilling keystones"),
                Err(err) => error!(target: "hemi::keystones", %err, "error backfilling keystones"),
            }
        }
    });
}

/// Every how many L2 blocks there is a keystone (heminetwork `hemi.KeystoneHeaderPeriod`).
const KEYSTONE_HEADER_PERIOD: u64 = 25;

/// Port of hemilabs/op-geth `BlockChain.BackfillKeystones`.
fn backfill_keystones<P>(
    provider: &P,
    chain_spec: &impl OpHardforks,
    hvm: &Hvm,
    backfilled: bool,
) -> eyre::Result<()>
where
    P: BlockReader + HeaderProvider + BlockNumReader + BlockIdReader,
{
    let head = provider.best_block_number()?;
    hvm.delete_keystones_above(head)?;
    let finalized = provider.finalized_block_number()?.unwrap_or_default();
    if head < finalized {
        eyre::bail!("unsafe block behind finalized block? {head} < {finalized}");
    }
    let mut number = head;
    loop {
        if number == 0 {
            break;
        }
        let inserted = upsert_keystone(provider, chain_spec, hvm, number)?;
        if backfilled && number < finalized && !inserted {
            break;
        }
        if number < KEYSTONE_HEADER_PERIOD {
            break;
        }
        number -= KEYSTONE_HEADER_PERIOD;
    }
    Ok(())
}

fn upsert_keystone<P>(
    provider: &P,
    chain_spec: &impl OpHardforks,
    hvm: &Hvm,
    number: u64,
) -> eyre::Result<bool>
where
    P: BlockReader + HeaderProvider,
{
    let (header, l1_block) = keystone_parts(provider, chain_spec, number)?;
    let prev = if number >= KEYSTONE_HEADER_PERIOD && number - KEYSTONE_HEADER_PERIOD > 0 {
        let prev_number = number - KEYSTONE_HEADER_PERIOD;
        // the previous keystone must be derivable too
        keystone_parts(provider, chain_spec, prev_number)?;
        Some(provider.sealed_header(prev_number)?.ok_or_else(|| eyre::eyre!("missing block {prev_number}"))?.hash())
    } else {
        None
    };
    Ok(hvm.upsert_keystone(
        l1_block as u32,
        number as u32,
        header.parent_hash(),
        prev,
        header.state_root(),
        header.hash(),
    )?)
}

/// Returns the sealed header of L2 block `number` and the L1 block number of its L1 info deposit
/// (hemilabs/op-geth `l2BlockNumberToKeystone` / `deriveL1BlockNumberFromData`).
fn keystone_parts<P>(
    provider: &P,
    chain_spec: &impl OpHardforks,
    number: u64,
) -> eyre::Result<(reth_primitives_traits::SealedHeader<P::Header>, u64)>
where
    P: BlockReader + HeaderProvider,
{
    let header =
        provider.sealed_header(number)?.ok_or_else(|| eyre::eyre!("could not get l2blockbynumber"))?;
    let block = provider.block_by_number(number)?.ok_or_else(|| eyre::eyre!("could not get l2blockbynumber"))?;
    let Some(first) = block.body().transactions().first() else {
        eyre::bail!("no transactions on block?");
    };
    if first.ty() != op_alloy_consensus::DEPOSIT_TX_TYPE_ID {
        eyre::bail!("incorrect transaction type found: {}", first.ty());
    }
    let data = first.input();
    let timestamp = header.timestamp();
    let expected_len = if chain_spec.is_isthmus_active_at_timestamp(timestamp) {
        4 + 32 * 5 + 4 + 8
    } else if chain_spec.is_ecotone_active_at_timestamp(timestamp) {
        4 + 32 * 5
    } else {
        4 + 32 * 8
    };
    if data.len() != expected_len {
        eyre::bail!("data is unexpected length: {}", data.len());
    }
    // the L1 block number sits at the same offset in the Bedrock, Ecotone and Isthmus formats
    let l1_block = u64::from_be_bytes(data[28..36].try_into().expect("8 bytes"));
    Ok((header, l1_block))
}
