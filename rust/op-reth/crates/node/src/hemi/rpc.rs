//! The `kss` (keystone) RPC namespace of hemilabs/op-geth, used by the PoP miner (popmd).

use jsonrpsee::{
    PendingSubscriptionSink, RpcModule,
    types::{ErrorObject, ErrorObjectOwned},
};

fn err(msg: impl ToString) -> ErrorObjectOwned {
    ErrorObject::owned(-32000, msg.to_string(), None::<()>)
}

/// Builds the `kss` RPC module: `kss_getLatestKeystones`, `kss_getKeystone` and the
/// `newKeystones` subscription (`kss_subscribe`).
pub fn kss_rpc_module() -> eyre::Result<RpcModule<()>> {
    let mut module = RpcModule::new(());

    module.register_blocking_method("kss_getLatestKeystones", |params, _, _| {
        let (count,): (u64,) = params.parse()?;
        let hvm = reth_optimism_hemi::hvm().ok_or_else(|| err("hVM is not enabled"))?;
        hvm.kss_latest_keystones(count).map_err(err)
    })?;

    module.register_blocking_method("kss_getKeystone", |params, _, _| {
        let (abrev_hash, count): (String, u64) = params.parse()?;
        let hvm = reth_optimism_hemi::hvm().ok_or_else(|| err("hVM is not enabled"))?;
        hvm.kss_keystone(&abrev_hash, count).map_err(err)
    })?;

    module.register_subscription(
        "kss_subscribe",
        "kss_subscription",
        "kss_unsubscribe",
        |params, pending: PendingSubscriptionSink, _, _| async move {
            let kind: String = match params.one() {
                Ok(kind) => kind,
                Err(e) => {
                    pending.reject(e).await;
                    return Ok(());
                }
            };
            if kind != "newKeystones" {
                pending.reject(err(format!("no \"{kind}\" subscription in kss namespace"))).await;
                return Ok(());
            }
            let mut rx = reth_optimism_hemi::events::keystones().subscribe();
            let sink = pending.accept().await?;
            loop {
                tokio::select! {
                    _ = sink.closed() => break,
                    msg = rx.recv() => match msg {
                        Ok(msg) => {
                            let msg = serde_json::value::to_raw_value(&msg)?;
                            if sink.send(msg).await.is_err() {
                                break;
                            }
                        }
                        Err(tokio::sync::broadcast::error::RecvError::Lagged(_)) => continue,
                        Err(_) => break,
                    },
                }
            }
            Ok(())
        },
    )?;

    Ok(module)
}
