//! Exchange of Bitcoin blocks between L2 peers.
//!
//! hemilabs/op-geth extends eth/68 with `GetBtcBlocks` / `BtcBlocks` messages so a node whose
//! embedded Bitcoin node cannot get a block from the Bitcoin network (for example after a
//! Bitcoin reorg removed it) can get it from L2 peers that have it. op-reth carries the same
//! messages over a separate `hemi/1` RLPx subprotocol, since it does not modify eth/68.

use alloy_primitives::{B256, Bytes, bytes::BytesMut};
use alloy_rlp::{Decodable, Encodable, RlpDecodable, RlpEncodable};
use futures::{Stream, StreamExt};
use reth_eth_wire::{
    capability::SharedCapabilities, multiplex::ProtocolConnection, protocol::Protocol,
};
use reth_network::protocol::{ConnectionHandler, OnNotSupported, ProtocolHandler};
use reth_network_api::{Direction, PeerId};
use reth_tasks::TaskExecutor;
use std::{
    collections::HashMap,
    net::SocketAddr,
    pin::Pin,
    sync::{Arc, Mutex},
    task::{Context, Poll},
    time::{Duration, Instant},
};
use tokio::sync::mpsc;
use tokio_stream::wrappers::UnboundedReceiverStream;
use tracing::{debug, info, warn};

/// Maximum number of Bitcoin blocks asked for or served per message (op-geth
/// `maxBtcBlocksServe`).
const MAX_BTC_BLOCKS: usize = 32;
/// Interval of the missing Bitcoin block prefetch (op-geth `prefetchBTCBlocks`).
const PREFETCH_INTERVAL: Duration = Duration::from_secs(5);
/// How often the same Bitcoin block is asked for at most (op-geth `btcReqDedupTTL`).
const DEDUP_TTL: Duration = Duration::from_secs(3);

const GET_BTC_BLOCKS: u8 = 0x00;
const BTC_BLOCKS: u8 = 0x01;

#[derive(Debug, Clone, RlpEncodable, RlpDecodable)]
struct GetBtcBlocks {
    request_id: u64,
    hashes: Vec<B256>,
}

#[derive(Debug, Clone, RlpEncodable, RlpDecodable)]
struct BtcBlocks {
    request_id: u64,
    blocks: Vec<Bytes>,
}

fn encode<T: Encodable>(id: u8, msg: &T) -> BytesMut {
    let mut buf = BytesMut::new();
    buf.extend_from_slice(&[id]);
    msg.encode(&mut buf);
    buf
}

type Peers = Arc<Mutex<HashMap<PeerId, mpsc::UnboundedSender<BytesMut>>>>;

/// The `hemi/1` RLPx subprotocol.
#[derive(Debug, Clone)]
pub struct HemiBtcProtocol {
    peers: Peers,
}

impl HemiBtcProtocol {
    /// The protocol announced in the RLPx handshake.
    pub fn protocol() -> Protocol {
        Protocol::new(reth_eth_wire::Capability::new_static("hemi", 1), 2)
    }

    /// Creates the protocol and spawns the tasks that request Bitcoin blocks from peers.
    pub fn new(executor: &TaskExecutor) -> Self {
        let peers: Peers = Default::default();
        let (tx, mut rx) = mpsc::unbounded_channel::<Vec<B256>>();
        reth_optimism_hemi::events::set_btc_request_channel(tx);

        let request_peers = peers.clone();
        executor.spawn_task(async move {
            let mut dedup: HashMap<B256, Instant> = HashMap::new();
            let mut tick = tokio::time::interval(PREFETCH_INTERVAL);
            loop {
                let hashes = tokio::select! {
                    req = rx.recv() => match req { Some(h) => h, None => return },
                    _ = tick.tick() => {
                        match reth_optimism_hemi::hvm() {
                            Some(hvm) if hvm.enabled() => {
                                tokio::task::spawn_blocking(move || hvm.missing_btc_blocks())
                                    .await
                                    .unwrap_or_default()
                            }
                            _ => Vec::new(),
                        }
                    }
                };
                let now = Instant::now();
                dedup.retain(|_, t| now.duration_since(*t) < DEDUP_TTL);
                let hashes: Vec<B256> =
                    hashes.into_iter().filter(|h| dedup.insert(*h, now).is_none()).collect();
                if hashes.is_empty() {
                    continue;
                }
                info!(target: "hemi::p2p", count = hashes.len(), "requesting BTC blocks from L2 peers");
                broadcast_request(&request_peers, &hashes);
            }
        });

        Self { peers }
    }
}

fn broadcast_request(peers: &Peers, hashes: &[B256]) {
    let peers = peers.lock().unwrap_or_else(|e| e.into_inner());
    for chunk in hashes.chunks(MAX_BTC_BLOCKS) {
        let msg =
            encode(GET_BTC_BLOCKS, &GetBtcBlocks { request_id: rand_id(), hashes: chunk.to_vec() });
        for tx in peers.values() {
            let _ = tx.send(msg.clone());
        }
    }
}

fn rand_id() -> u64 {
    use std::hash::{BuildHasher, RandomState};
    RandomState::new().hash_one(Instant::now())
}

impl ProtocolHandler for HemiBtcProtocol {
    type ConnectionHandler = HemiBtcConnectionHandler;

    fn on_incoming(&self, _socket_addr: SocketAddr) -> Option<Self::ConnectionHandler> {
        Some(HemiBtcConnectionHandler { peers: self.peers.clone() })
    }

    fn on_outgoing(&self, _socket_addr: SocketAddr, _peer_id: PeerId) -> Option<Self::ConnectionHandler> {
        Some(HemiBtcConnectionHandler { peers: self.peers.clone() })
    }
}

/// Connection handler of the `hemi/1` subprotocol.
#[derive(Debug)]
pub struct HemiBtcConnectionHandler {
    peers: Peers,
}

impl ConnectionHandler for HemiBtcConnectionHandler {
    type Connection = HemiBtcConnection;

    fn protocol(&self) -> Protocol {
        HemiBtcProtocol::protocol()
    }

    fn on_unsupported_by_peer(
        self,
        _supported: &SharedCapabilities,
        _direction: Direction,
        _peer_id: PeerId,
    ) -> OnNotSupported {
        OnNotSupported::KeepAlive
    }

    fn into_connection(
        self,
        _direction: Direction,
        peer_id: PeerId,
        conn: ProtocolConnection,
    ) -> Self::Connection {
        let (tx, rx) = mpsc::unbounded_channel();
        self.peers.lock().unwrap_or_else(|e| e.into_inner()).insert(peer_id, tx.clone());
        debug!(target: "hemi::p2p", %peer_id, "hemi/1 connection established");
        HemiBtcConnection {
            conn,
            outgoing: UnboundedReceiverStream::new(rx),
            to_self: tx,
            peer_id,
            peers: self.peers,
        }
    }
}

/// A `hemi/1` connection with a peer.
pub struct HemiBtcConnection {
    conn: ProtocolConnection,
    outgoing: UnboundedReceiverStream<BytesMut>,
    to_self: mpsc::UnboundedSender<BytesMut>,
    peer_id: PeerId,
    peers: Peers,
}

impl core::fmt::Debug for HemiBtcConnection {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        f.debug_struct("HemiBtcConnection").field("peer_id", &self.peer_id).finish()
    }
}

impl Drop for HemiBtcConnection {
    fn drop(&mut self) {
        self.peers.lock().unwrap_or_else(|e| e.into_inner()).remove(&self.peer_id);
    }
}

impl HemiBtcConnection {
    /// Handles a message from the peer. Replies are sent through `to_self`. Returns `false` if the
    /// peer misbehaved and must be disconnected.
    fn on_message(&self, msg: &[u8]) -> bool {
        let Some((&id, mut body)) = msg.split_first() else { return false };
        let Some(hvm) = reth_optimism_hemi::hvm().filter(|h| h.enabled()) else { return true };
        match id {
            GET_BTC_BLOCKS => {
                let Ok(req) = GetBtcBlocks::decode(&mut body) else { return false };
                let to_self = self.to_self.clone();
                tokio::task::spawn_blocking(move || {
                    let blocks = hvm.serve_btc_blocks(&req.hashes);
                    let _ = to_self
                        .send(encode(BTC_BLOCKS, &BtcBlocks { request_id: req.request_id, blocks }));
                });
                true
            }
            BTC_BLOCKS => {
                let Ok(res) = BtcBlocks::decode(&mut body) else { return false };
                if res.blocks.len() > MAX_BTC_BLOCKS {
                    warn!(target: "hemi::p2p", peer_id = %self.peer_id, "oversized BtcBlocks message");
                    return false;
                }
                let to_self = self.to_self.clone();
                let peer_id = self.peer_id;
                tokio::task::spawn_blocking(move || match hvm.ingest_btc_blocks(&res.blocks) {
                    Ok(missing) => {
                        for chunk in missing.chunks(MAX_BTC_BLOCKS) {
                            let _ = to_self.send(encode(
                                GET_BTC_BLOCKS,
                                &GetBtcBlocks { request_id: rand_id(), hashes: chunk.to_vec() },
                            ));
                        }
                    }
                    Err(err) => {
                        warn!(target: "hemi::p2p", %peer_id, %err, "invalid BtcBlocks message");
                    }
                });
                true
            }
            _ => false,
        }
    }
}

impl Stream for HemiBtcConnection {
    type Item = BytesMut;

    fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        let this = self.get_mut();
        loop {
            if let Poll::Ready(Some(msg)) = this.outgoing.poll_next_unpin(cx) {
                return Poll::Ready(Some(msg));
            }
            match this.conn.poll_next_unpin(cx) {
                Poll::Ready(Some(msg)) => {
                    if !this.on_message(&msg) {
                        return Poll::Ready(None);
                    }
                }
                Poll::Ready(None) => return Poll::Ready(None),
                Poll::Pending => return Poll::Pending,
            }
        }
    }
}
