//! Implements the Optimism engine API RPC methods.

use alloy_eips::eip7685::Requests;
use alloy_primitives::{B256, BlockHash, U64};
use alloy_rpc_types_engine::{
    ClientVersionV1, ExecutionPayloadBodiesV1, ExecutionPayloadInputV2, ExecutionPayloadV3,
    ForkchoiceState, ForkchoiceUpdated, PayloadId, PayloadStatus,
};
use derive_more::Constructor;
use jsonrpsee::proc_macros::rpc;
use jsonrpsee_core::{RpcResult, server::RpcModule};
use op_alloy_rpc_types_engine::OpExecutionPayloadV4;
use reth_chainspec::EthereumHardforks;
use reth_node_api::{EngineApiValidator, EngineTypes};
use reth_optimism_payload_builder::OpExecData;
use reth_rpc_api::IntoEngineApiRpcModule;
use reth_rpc_engine_api::EngineApi;
use reth_storage_api::{BalProvider, BlockReader, HeaderProvider, StateProviderFactory};
use reth_transaction_pool::TransactionPool;
use tracing::{debug, trace};

/// The list of all supported Engine capabilities available over the engine endpoint.
///
/// Spec: <https://specs.optimism.io/protocol/exec-engine.html>
pub const OP_ENGINE_CAPABILITIES: &[&str] = &[
    "engine_forkchoiceUpdatedV1",
    "engine_forkchoiceUpdatedV2",
    "engine_forkchoiceUpdatedV3",
    "engine_getClientVersionV1",
    "engine_getPayloadV2",
    "engine_getPayloadV3",
    "engine_getPayloadV4",
    "engine_getPayloadV5",
    "engine_newPayloadV2",
    "engine_newPayloadV3",
    "engine_newPayloadV4",
    "engine_getPayloadBodiesByHashV1",
    "engine_getPayloadBodiesByRangeV1",
    "engine_newKeystone",
    "engine_popPayoutsByL2Keystone",
];

/// Extension trait that gives access to Optimism engine API RPC methods.
///
/// Note:
/// > The provider should use a JWT authentication layer.
///
/// This follows the Optimism specs that can be found at:
/// <https://specs.optimism.io/protocol/exec-engine.html#engine-api>
#[cfg_attr(not(feature = "client"), rpc(server, namespace = "engine"), server_bounds(Engine::PayloadAttributes: jsonrpsee::core::DeserializeOwned))]
#[cfg_attr(feature = "client", rpc(server, client, namespace = "engine", client_bounds(Engine::PayloadAttributes: jsonrpsee::core::Serialize + Clone), server_bounds(Engine::PayloadAttributes: jsonrpsee::core::DeserializeOwned)))]
pub trait OpEngineApi<Engine: EngineTypes> {
    /// Sends the given payload to the execution layer client, as specified for the Shanghai fork.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/584905270d8ad665718058060267061ecfd79ca5/src/engine/shanghai.md#engine_newpayloadv2>
    ///
    /// No modifications needed for OP compatibility.
    #[method(name = "newPayloadV2")]
    async fn new_payload_v2(&self, payload: ExecutionPayloadInputV2) -> RpcResult<PayloadStatus>;

    /// Sends the given payload to the execution layer client, as specified for the Cancun fork.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/main/src/engine/cancun.md#engine_newpayloadv3>
    ///
    /// OP modifications:
    /// - expected versioned hashes MUST be an empty array: therefore the `versioned_hashes`
    ///   parameter is removed.
    /// - parent beacon block root MUST be the parent beacon block root from the L1 origin block of
    ///   the L2 block.
    /// - blob versioned hashes MUST be empty list.
    #[method(name = "newPayloadV3")]
    async fn new_payload_v3(
        &self,
        payload: ExecutionPayloadV3,
        versioned_hashes: Vec<B256>,
        parent_beacon_block_root: B256,
    ) -> RpcResult<PayloadStatus>;

    /// Sends the given payload to the execution layer client, as specified for the Prague fork.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/03911ffc053b8b806123f1fc237184b0092a485a/src/engine/prague.md#engine_newpayloadv4>
    ///
    /// - blob versioned hashes MUST be empty list.
    /// - execution layer requests MUST be empty list.
    #[method(name = "newPayloadV4")]
    async fn new_payload_v4(
        &self,
        payload: OpExecutionPayloadV4,
        versioned_hashes: Vec<B256>,
        parent_beacon_block_root: B256,
        execution_requests: Requests,
    ) -> RpcResult<PayloadStatus>;

    /// See also <https://github.com/ethereum/execution-apis/blob/6709c2a795b707202e93c4f2867fa0bf2640a84f/src/engine/paris.md#engine_forkchoiceupdatedv1>
    ///
    /// This exists because it is used by op-node: <https://github.com/ethereum-optimism/optimism/blob/0bc5fe8d16155dc68bcdf1fa5733abc58689a618/op-node/rollup/types.go#L615-L617>
    ///
    /// Caution: This should not accept the `withdrawals` field in the payload attributes.
    #[method(name = "forkchoiceUpdatedV1")]
    async fn fork_choice_updated_v1(
        &self,
        fork_choice_state: ForkchoiceState,
        payload_attributes: Option<Engine::PayloadAttributes>,
    ) -> RpcResult<ForkchoiceUpdated>;

    /// Updates the execution layer client with the given fork choice, as specified for the Shanghai
    /// fork.
    ///
    /// Caution: This should not accept the `parentBeaconBlockRoot` field in the payload attributes.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/6709c2a795b707202e93c4f2867fa0bf2640a84f/src/engine/shanghai.md#engine_forkchoiceupdatedv2>
    ///
    /// OP modifications:
    /// - The `payload_attributes` parameter is extended with the [`EngineTypes::PayloadAttributes`](EngineTypes) type as described in <https://specs.optimism.io/protocol/exec-engine.html#extended-payloadattributesv2>
    #[method(name = "forkchoiceUpdatedV2")]
    async fn fork_choice_updated_v2(
        &self,
        fork_choice_state: ForkchoiceState,
        payload_attributes: Option<Engine::PayloadAttributes>,
    ) -> RpcResult<ForkchoiceUpdated>;

    /// Updates the execution layer client with the given fork choice, as specified for the Cancun
    /// fork.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/main/src/engine/cancun.md#engine_forkchoiceupdatedv3>
    ///
    /// OP modifications:
    /// - Must be called with an Ecotone payload
    /// - Attributes must contain the parent beacon block root field
    /// - The `payload_attributes` parameter is extended with the [`EngineTypes::PayloadAttributes`](EngineTypes) type as described in <https://specs.optimism.io/protocol/exec-engine.html#extended-payloadattributesv2>
    #[method(name = "forkchoiceUpdatedV3")]
    async fn fork_choice_updated_v3(
        &self,
        fork_choice_state: ForkchoiceState,
        payload_attributes: Option<Engine::PayloadAttributes>,
    ) -> RpcResult<ForkchoiceUpdated>;

    /// Retrieves an execution payload from a previously started build process, as specified for the
    /// Shanghai fork.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/6709c2a795b707202e93c4f2867fa0bf2640a84f/src/engine/shanghai.md#engine_getpayloadv2>
    ///
    /// Note:
    /// > Provider software MAY stop the corresponding build process after serving this call.
    ///
    /// No modifications needed for OP compatibility.
    #[method(name = "getPayloadV2")]
    async fn get_payload_v2(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<Engine::ExecutionPayloadEnvelopeV2>;

    /// Retrieves an execution payload from a previously started build process, as specified for the
    /// Cancun fork.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/main/src/engine/cancun.md#engine_getpayloadv3>
    ///
    /// Note:
    /// > Provider software MAY stop the corresponding build process after serving this call.
    ///
    /// OP modifications:
    /// - the response type is extended to [`EngineTypes::ExecutionPayloadEnvelopeV3`].
    #[method(name = "getPayloadV3")]
    async fn get_payload_v3(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<Engine::ExecutionPayloadEnvelopeV3>;

    /// Returns the most recent version of the payload that is available in the corresponding
    /// payload build process at the time of receiving this call.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/main/src/engine/prague.md#engine_getpayloadv4>
    ///
    /// Note:
    /// > Provider software MAY stop the corresponding build process after serving this call.
    ///
    /// OP modifications:
    /// - the response type is extended to [`EngineTypes::ExecutionPayloadEnvelopeV4`].
    #[method(name = "getPayloadV4")]
    async fn get_payload_v4(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<Engine::ExecutionPayloadEnvelopeV4>;

    /// Returns the most recent version of the payload that is available in the corresponding
    /// payload build process at the time of receiving this call.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/main/src/engine/osaka.md#engine_getpayloadv5>
    ///
    /// Note:
    /// > Provider software MAY stop the corresponding build process after serving this call.
    ///
    /// OP modifications:
    /// - the response type is extended to [`EngineTypes::ExecutionPayloadEnvelopeV5`].
    #[method(name = "getPayloadV5")]
    async fn get_payload_v5(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<Engine::ExecutionPayloadEnvelopeV5>;

    /// Returns the execution payload bodies by the given hash.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/6452a6b194d7db269bf1dbd087a267251d3cc7f8/src/engine/shanghai.md#engine_getpayloadbodiesbyhashv1>
    #[method(name = "getPayloadBodiesByHashV1")]
    async fn get_payload_bodies_by_hash_v1(
        &self,
        block_hashes: Vec<BlockHash>,
    ) -> RpcResult<ExecutionPayloadBodiesV1>;

    /// Returns the execution payload bodies by the range starting at `start`, containing `count`
    /// blocks.
    ///
    /// WARNING: This method is associated with the `BeaconBlocksByRange` message in the consensus
    /// layer p2p specification, meaning the input should be treated as untrusted or potentially
    /// adversarial.
    ///
    /// Implementers should take care when acting on the input to this method, specifically
    /// ensuring that the range is limited properly, and that the range boundaries are computed
    /// correctly and without panics.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/6452a6b194d7db269bf1dbd087a267251d3cc7f8/src/engine/shanghai.md#engine_getpayloadbodiesbyrangev1>
    #[method(name = "getPayloadBodiesByRangeV1")]
    async fn get_payload_bodies_by_range_v1(
        &self,
        start: U64,
        count: U64,
    ) -> RpcResult<ExecutionPayloadBodiesV1>;

    /// Returns the execution client version information.
    ///
    /// Note:
    /// > The `client_version` parameter identifies the consensus client.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/main/src/engine/identification.md#engine_getclientversionv1>
    #[method(name = "getClientVersionV1")]
    async fn get_client_version_v1(
        &self,
        client_version: ClientVersionV1,
    ) -> RpcResult<Vec<ClientVersionV1>>;

    /// Returns the list of Engine API methods supported by the execution layer client software.
    ///
    /// See also <https://github.com/ethereum/execution-apis/blob/6452a6b194d7db269bf1dbd087a267251d3cc7f8/src/engine/common.md#capabilities>
    #[method(name = "exchangeCapabilities")]
    async fn exchange_capabilities(&self, capabilities: Vec<String>) -> RpcResult<Vec<String>>;

    /// Hemi: stores an L2 keystone sent by op-node and notifies `kss` subscribers.
    #[method(name = "newKeystone")]
    async fn new_keystone(&self, keystone: serde_json::Value) -> RpcResult<serde_json::Value>;

    /// Hemi: returns the PoP payouts (miner address and amount) of the Bitcoin PoP transactions
    /// that endorse the L2 keystone with the given abbreviated hash.
    #[method(name = "popPayoutsByL2Keystone")]
    async fn pop_payouts_by_l2_keystone(
        &self,
        abrev_hash: serde_json::Value,
    ) -> RpcResult<serde_json::Value>;
}

/// Hemi: before importing a payload, checks that hVM can process it, i.e. that the full Bitcoin
/// node has every Bitcoin block it needs. Returns `SYNCING` otherwise, like hemilabs/op-geth
/// (`delayPayloadImport`), so op-node retries instead of the block being marked invalid.
async fn hemi_check_payload(payload: &OpExecData) -> Option<PayloadStatus> {
    let hvm = reth_optimism_hemi::hvm()?;
    let p = &payload.payload;
    if !hvm.is_active(p.timestamp()) {
        return None;
    }
    let mut btc_attributes = Vec::new();
    for raw in p.transactions() {
        if raw.first() == Some(&op_alloy_consensus::BTC_ATTRIBUTES_DEPOSITED_TX_TYPE_ID) {
            match <op_alloy_consensus::TxBtcAttributesDeposited as alloy_eips::Decodable2718>::decode_2718_exact(raw) {
                Ok(tx) => btc_attributes.push(tx.input),
                // malformed: let regular validation reject the payload
                Err(_) => return None,
            }
        }
    }
    let block = reth_optimism_hemi::HvmBlock {
        hash: p.block_hash(),
        parent_hash: p.parent_hash(),
        number: p.block_number(),
        timestamp: p.timestamp(),
        btc_attributes,
    };
    let result = tokio::task::spawn_blocking(move || hvm.check_block(&block)).await.ok()?;
    match result {
        Err(reth_optimism_hemi::HvmError::Defer(msg)) => {
            debug!(target: "rpc::engine", block = %p.block_hash(), %msg, "hVM is missing Bitcoin data, delaying payload import");
            reth_optimism_hemi::events::request_btc_blocks(hvm.missing_btc_blocks());
            Some(PayloadStatus::from_status(alloy_rpc_types_engine::PayloadStatusEnum::Syncing))
        }
        Err(err) => {
            debug!(target: "rpc::engine", block = %p.block_hash(), %err, "hVM pre-check failed, leaving it to block validation");
            None
        }
        Ok(()) => None,
    }
}

/// Hemi: Bitcoin Attributes Deposited transactions in payload attributes (a verifier rebuilding a
/// derived block) need their Bitcoin blocks; request the missing ones from L2 peers and fail the
/// forkchoice update so op-node retries, like hemilabs/op-geth.
fn hemi_check_attributes<A: serde::Serialize>(attributes: Option<&A>) -> RpcResult<()> {
    let (Some(attributes), Some(hvm)) = (attributes, reth_optimism_hemi::hvm()) else {
        return Ok(());
    };
    if !hvm.enabled() {
        return Ok(());
    }
    let Ok(value) = serde_json::to_value(attributes) else { return Ok(()) };
    let Some(txs) = value.get("transactions").and_then(|t| t.as_array()) else { return Ok(()) };
    let mut missing = Vec::new();
    for tx in txs {
        let Some(raw) = tx.as_str().and_then(|s| alloy_primitives::hex::decode(s).ok()) else {
            continue;
        };
        if raw.first() != Some(&op_alloy_consensus::BTC_ATTRIBUTES_DEPOSITED_TX_TYPE_ID) {
            continue;
        }
        let tx = <op_alloy_consensus::TxBtcAttributesDeposited as alloy_eips::Decodable2718>::decode_2718_exact(&raw)
            .map_err(|_| invalid_attributes("invalid Bitcoin Attributes Deposited transaction"))?;
        let hashes = reth_optimism_hemi::btc_attributes_block_hashes(&tx.input)
            .map_err(|_| invalid_attributes("invalid canonical tip BtcAttributesDepositData"))?;
        missing.extend(hashes.into_iter().filter(|h| !hvm.btc_block_available(*h)));
    }
    if missing.is_empty() {
        return Ok(());
    }
    reth_optimism_hemi::events::request_btc_blocks(missing);
    Err(jsonrpsee_types::ErrorObject::owned(
        -32000,
        "missing blocks in forkchoiceupdated, will try to fetch",
        None::<()>,
    ))
}

fn invalid_attributes(msg: &str) -> jsonrpsee_types::ErrorObjectOwned {
    jsonrpsee_types::ErrorObject::owned(-38003, msg.to_string(), None::<()>)
}

/// The Engine API implementation that grants the Consensus layer access to data and
/// functions in the Execution layer that are crucial for the consensus process.
#[derive(Debug, Constructor)]
pub struct OpEngineApi<Provider, EngineT: EngineTypes, Pool, Validator, ChainSpec> {
    inner: EngineApi<Provider, EngineT, Pool, Validator, ChainSpec>,
}

impl<Provider, PayloadT, Pool, Validator, ChainSpec> Clone
    for OpEngineApi<Provider, PayloadT, Pool, Validator, ChainSpec>
where
    PayloadT: EngineTypes,
{
    fn clone(&self) -> Self {
        Self { inner: self.inner.clone() }
    }
}

#[async_trait::async_trait]
impl<Provider, EngineT, Pool, Validator, ChainSpec> OpEngineApiServer<EngineT>
    for OpEngineApi<Provider, EngineT, Pool, Validator, ChainSpec>
where
    Provider: HeaderProvider + BlockReader + StateProviderFactory + BalProvider + 'static,
    EngineT: EngineTypes<ExecutionData = OpExecData>,
    Pool: TransactionPool + 'static,
    Validator: EngineApiValidator<EngineT>,
    ChainSpec: EthereumHardforks + Send + Sync + 'static,
    EngineT::PayloadAttributes: serde::Serialize,
{
    async fn new_payload_v2(&self, payload: ExecutionPayloadInputV2) -> RpcResult<PayloadStatus> {
        trace!(target: "rpc::engine", "Serving engine_newPayloadV2");
        let payload = OpExecData::from(op_alloy_rpc_types_engine::OpExecutionData::v2(payload));
        if let Some(status) = hemi_check_payload(&payload).await {
            return Ok(status);
        }
        Ok(self.inner.new_payload_v2_metered(payload).await?)
    }

    async fn new_payload_v3(
        &self,
        payload: ExecutionPayloadV3,
        versioned_hashes: Vec<B256>,
        parent_beacon_block_root: B256,
    ) -> RpcResult<PayloadStatus> {
        trace!(target: "rpc::engine", "Serving engine_newPayloadV3");
        let payload = OpExecData::from(op_alloy_rpc_types_engine::OpExecutionData::v3(
            payload,
            versioned_hashes,
            parent_beacon_block_root,
        ));
        if let Some(status) = hemi_check_payload(&payload).await {
            return Ok(status);
        }

        Ok(self.inner.new_payload_v3_metered(payload).await?)
    }

    async fn new_payload_v4(
        &self,
        payload: OpExecutionPayloadV4,
        versioned_hashes: Vec<B256>,
        parent_beacon_block_root: B256,
        execution_requests: Requests,
    ) -> RpcResult<PayloadStatus> {
        trace!(target: "rpc::engine", "Serving engine_newPayloadV4");
        let payload = OpExecData::from(op_alloy_rpc_types_engine::OpExecutionData::v4(
            payload,
            versioned_hashes,
            parent_beacon_block_root,
            execution_requests,
        ));
        if let Some(status) = hemi_check_payload(&payload).await {
            return Ok(status);
        }

        Ok(self.inner.new_payload_v4_metered(payload).await?)
    }

    async fn fork_choice_updated_v1(
        &self,
        fork_choice_state: ForkchoiceState,
        payload_attributes: Option<EngineT::PayloadAttributes>,
    ) -> RpcResult<ForkchoiceUpdated> {
        hemi_check_attributes(payload_attributes.as_ref())?;
        Ok(self.inner.fork_choice_updated_v1_metered(fork_choice_state, payload_attributes).await?)
    }

    async fn fork_choice_updated_v2(
        &self,
        fork_choice_state: ForkchoiceState,
        payload_attributes: Option<EngineT::PayloadAttributes>,
    ) -> RpcResult<ForkchoiceUpdated> {
        trace!(target: "rpc::engine", "Serving engine_forkchoiceUpdatedV2");
        hemi_check_attributes(payload_attributes.as_ref())?;
        Ok(self.inner.fork_choice_updated_v2_metered(fork_choice_state, payload_attributes).await?)
    }

    async fn fork_choice_updated_v3(
        &self,
        fork_choice_state: ForkchoiceState,
        payload_attributes: Option<EngineT::PayloadAttributes>,
    ) -> RpcResult<ForkchoiceUpdated> {
        trace!(target: "rpc::engine", "Serving engine_forkchoiceUpdatedV3");
        hemi_check_attributes(payload_attributes.as_ref())?;
        Ok(self.inner.fork_choice_updated_v3_metered(fork_choice_state, payload_attributes).await?)
    }

    async fn get_payload_v2(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<EngineT::ExecutionPayloadEnvelopeV2> {
        debug!(target: "rpc::engine", id = %payload_id, "Serving engine_getPayloadV2");
        Ok(self.inner.get_payload_v2_metered(payload_id).await?)
    }

    async fn get_payload_v3(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<EngineT::ExecutionPayloadEnvelopeV3> {
        trace!(target: "rpc::engine", "Serving engine_getPayloadV3");
        Ok(self.inner.get_payload_v3_metered(payload_id).await?)
    }

    async fn get_payload_v4(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<EngineT::ExecutionPayloadEnvelopeV4> {
        trace!(target: "rpc::engine", "Serving engine_getPayloadV4");
        Ok(self.inner.get_payload_v4_metered(payload_id).await?)
    }

    async fn get_payload_v5(
        &self,
        payload_id: PayloadId,
    ) -> RpcResult<EngineT::ExecutionPayloadEnvelopeV5> {
        trace!(target: "rpc::engine", "Serving engine_getPayloadV5");
        Ok(self.inner.get_payload_v5_metered(payload_id).await?)
    }

    async fn get_payload_bodies_by_hash_v1(
        &self,
        block_hashes: Vec<BlockHash>,
    ) -> RpcResult<ExecutionPayloadBodiesV1> {
        trace!(target: "rpc::engine", "Serving engine_getPayloadBodiesByHashV1");
        Ok(self.inner.get_payload_bodies_by_hash_v1_metered(block_hashes).await?)
    }

    async fn get_payload_bodies_by_range_v1(
        &self,
        start: U64,
        count: U64,
    ) -> RpcResult<ExecutionPayloadBodiesV1> {
        trace!(target: "rpc::engine", "Serving engine_getPayloadBodiesByRangeV1");
        Ok(self.inner.get_payload_bodies_by_range_v1_metered(start.to(), count.to()).await?)
    }

    async fn get_client_version_v1(
        &self,
        client: ClientVersionV1,
    ) -> RpcResult<Vec<ClientVersionV1>> {
        trace!(target: "rpc::engine", "Serving engine_getClientVersionV1");
        Ok(self.inner.get_client_version_v1(client)?)
    }

    async fn exchange_capabilities(&self, _capabilities: Vec<String>) -> RpcResult<Vec<String>> {
        Ok(self.inner.capabilities().list())
    }

    async fn new_keystone(&self, keystone: serde_json::Value) -> RpcResult<serde_json::Value> {
        trace!(target: "rpc::engine", "Serving engine_newKeystone");
        let Some(hvm) = reth_optimism_hemi::hvm() else {
            return Err(hemi_error("keystones are not available on this node"));
        };
        let raw = serde_json::to_vec(&keystone).map_err(|e| hemi_error(&e.to_string()))?;
        match tokio::task::spawn_blocking(move || hvm.new_keystone(&raw)).await {
            Ok(Ok(abrev_hash)) => {
                reth_optimism_hemi::events::notify_keystone(&abrev_hash);
                reth_optimism_hemi::events::backfill().notify_one();
                Ok(serde_json::json!({ "status": "VALID" }))
            }
            Ok(Err(err)) => Err(hemi_error(&err.to_string())),
            Err(err) => Err(hemi_error(&err.to_string())),
        }
    }

    async fn pop_payouts_by_l2_keystone(
        &self,
        abrev_hash: serde_json::Value,
    ) -> RpcResult<serde_json::Value> {
        trace!(target: "rpc::engine", "Serving engine_popPayoutsByL2Keystone");
        let Some(hvm) = reth_optimism_hemi::hvm() else {
            return Err(hemi_error("hVM/TBC full node is not enabled on this node"));
        };
        let raw = serde_json::to_vec(&abrev_hash).map_err(|e| hemi_error(&e.to_string()))?;
        match tokio::task::spawn_blocking(move || hvm.pop_payouts(&raw)).await {
            Ok(Ok(payouts)) => Ok(payouts),
            Ok(Err(err)) => Err(hemi_error(&err.to_string())),
            Err(err) => Err(hemi_error(&err.to_string())),
        }
    }
}

fn hemi_error(msg: &str) -> jsonrpsee_types::ErrorObjectOwned {
    jsonrpsee_types::ErrorObject::owned(-32000, msg.to_string(), None::<()>)
}

impl<Provider, EngineT, Pool, Validator, ChainSpec> IntoEngineApiRpcModule
    for OpEngineApi<Provider, EngineT, Pool, Validator, ChainSpec>
where
    EngineT: EngineTypes,
    Self: OpEngineApiServer<EngineT>,
{
    fn into_rpc_module(self) -> RpcModule<()> {
        self.into_rpc().remove_context()
    }
}

#[cfg(test)]
mod tests {
    use super::OP_ENGINE_CAPABILITIES;

    /// Osaka `engine_getPayloadV5` must be advertised: once Osaka/Karst is active the CL fetches
    /// payloads via V5, and an unadvertised method is rejected with -38005 "Unsupported fork".
    /// V4 stays advertised — V5 is additive (`newPayload` remains V4).
    #[test]
    fn advertises_get_payload_v5() {
        assert!(OP_ENGINE_CAPABILITIES.contains(&"engine_getPayloadV5"));
        assert!(OP_ENGINE_CAPABILITIES.contains(&"engine_getPayloadV4"));
    }
}
