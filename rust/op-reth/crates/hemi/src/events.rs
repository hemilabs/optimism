//! Process-wide hVM event plumbing between the engine API, RPC and devp2p.

use alloy_primitives::B256;
use std::sync::OnceLock;
use tokio::sync::{Notify, broadcast, mpsc};

static KEYSTONES: OnceLock<broadcast::Sender<String>> = OnceLock::new();
static BTC_REQUESTS: OnceLock<mpsc::UnboundedSender<Vec<B256>>> = OnceLock::new();
static BACKFILL: OnceLock<Notify> = OnceLock::new();

/// Notifications of new keystones (`kss` `newKeystones` subscription), as op-geth formats them.
pub fn keystones() -> &'static broadcast::Sender<String> {
    KEYSTONES.get_or_init(|| broadcast::channel(64).0)
}

/// Publishes a new keystone notification.
pub fn notify_keystone(abrev_hash_hex: &str) {
    let _ = keystones().send(format!("New Keystone Available: {abrev_hash_hex}"));
}

/// Wakes the keystone backfill task.
pub fn backfill() -> &'static Notify {
    BACKFILL.get_or_init(Notify::new)
}

/// Installs the channel devp2p reads Bitcoin block requests from.
pub fn set_btc_request_channel(tx: mpsc::UnboundedSender<Vec<B256>>) -> bool {
    BTC_REQUESTS.set(tx).is_ok()
}

/// Asks all L2 peers for the given Bitcoin blocks (hashes in internal byte order).
pub fn request_btc_blocks(hashes: Vec<B256>) {
    if hashes.is_empty() {
        return;
    }
    if let Some(tx) = BTC_REQUESTS.get() {
        let _ = tx.send(hashes);
    }
}
