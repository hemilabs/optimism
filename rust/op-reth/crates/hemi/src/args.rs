//! hVM command line arguments, named and defaulted like hemilabs/op-geth's flags.

use crate::HvmConfig;
use std::path::Path;

/// Default hVM effective genesis: Bitcoin testnet3 block 3522419 (hemilabs/op-geth
/// `ethconfig.Defaults`).
const DEFAULT_GENESIS_HEADER: &str = "00c05732cdc3e0d654efe86351f0cbfc6c79325e9f9fa7886a39b552f5c4d90700000000dae4079485e26f1f77425b84a13760038a352d07a0fef92b5188bd04c2999162afca58679121011962b9d0a5";
const DEFAULT_GENESIS_HEIGHT: u64 = 3522419;

/// hVM / TBC arguments.
#[derive(Debug, Clone, clap::Args, PartialEq, Eq)]
#[command(next_help_heading = "Hemi hVM")]
pub struct HvmArgs {
    /// Whether hVM is enabled.
    #[arg(id = "hvm_enabled", long = "hvm.enabled", default_value_t = true, action = clap::ArgAction::Set,
        num_args = 0..=1, default_missing_value = "true")]
    pub enabled: bool,

    /// The genesis Bitcoin block header (hex) where hVM starts tracking Bitcoin consensus.
    #[arg(id = "hvm_genesis_header", long = "hvm.genesisheader", default_value = DEFAULT_GENESIS_HEADER)]
    pub genesis_header: String,

    /// The height of the hVM genesis Bitcoin block.
    #[arg(id = "hvm_genesis_height", long = "hvm.genesisheight", default_value_t = DEFAULT_GENESIS_HEIGHT)]
    pub genesis_height: u64,

    /// Data directory where the Bitcoin headers for hVM consensus tracking are stored.
    #[arg(id = "hvm_header_data_dir", long = "hvm.headerdatadir", default_value = "~/.tbcdheaders")]
    pub header_data_dir: String,

    /// Deucalion address for health checks (unused by op-reth, which has its own health
    /// endpoints; accepted for compatibility with op-geth command lines).
    #[arg(id = "hvm_deucalion_address", long = "hvm.deucalionaddress", default_value = "")]
    pub deucalion_address: String,

    /// hVM Phase 0 activation timestamp, overriding the chain configuration (`hvm0Time`).
    #[arg(id = "hvm_override_hvm0", long = "override.hvm0")]
    pub override_hvm0: Option<u64>,

    /// Address for the TBC daemon to listen on (TBC's default when unset).
    #[arg(id = "hvm_tbc_listen_address", long = "tbc.listenaddress", default_value = "")]
    pub tbc_listen_address: String,

    /// Max number of transactions TBC will cache before flushing to disk (TBC's default when 0).
    #[arg(id = "hvm_tbc_max_cached_txs", long = "tbc.maxcachedtxs", default_value_t = 0)]
    pub tbc_max_cached_txs: i64,

    /// Directory for TBC's LevelDB database.
    #[arg(id = "hvm_tbc_level_db_home", long = "tbc.leveldbhome", default_value = "~/.tbcd")]
    pub tbc_level_db_home: String,

    /// Enable/disable block sanity checks before inserting.
    #[arg(id = "hvm_tbc_block_sanity", long = "tbc.blocksanity", default_value_t = false, action = clap::ArgAction::Set,
        num_args = 0..=1, default_missing_value = "true")]
    pub tbc_block_sanity: bool,

    /// Bitcoin network TBC is running on.
    #[arg(id = "hvm_tbc_network", long = "tbc.network", default_value = "testnet3")]
    pub tbc_network: String,

    /// Prometheus address for TBC metric monitoring.
    #[arg(id = "hvm_tbc_prometheus_address", long = "tbc.prometheusaddress", default_value = "")]
    pub tbc_prometheus_address: String,

    /// Override TBC seeds when finding peers.
    #[arg(id = "hvm_tbc_seeds", long = "tbc.seeds", value_delimiter = ',')]
    pub tbc_seeds: Vec<String>,

    /// hVM / TBC log verbosity (geth levels 0-5).
    #[arg(id = "hvm_verbosity", long = "hvm.verbosity", default_value_t = 3)]
    pub hvm_verbosity: i32,
}

impl Default for HvmArgs {
    fn default() -> Self {
        Self {
            enabled: true,
            genesis_header: DEFAULT_GENESIS_HEADER.to_string(),
            genesis_height: DEFAULT_GENESIS_HEIGHT,
            header_data_dir: "~/.tbcdheaders".to_string(),
            deucalion_address: String::new(),
            override_hvm0: None,
            tbc_listen_address: String::new(),
            tbc_max_cached_txs: 0,
            tbc_level_db_home: "~/.tbcd".to_string(),
            tbc_block_sanity: false,
            tbc_network: "testnet3".to_string(),
            tbc_prometheus_address: String::new(),
            tbc_seeds: Vec::new(),
            hvm_verbosity: 3,
        }
    }
}

impl HvmArgs {
    /// Builds the bridge configuration. `genesis_hvm0` is the `hvm0Time` of the chain
    /// configuration, `datadir` the node data directory (the L2 registry lives in `<datadir>/hvm`).
    pub fn to_config(&self, genesis_hvm0: Option<u64>, datadir: &Path) -> HvmConfig {
        HvmConfig {
            enabled: self.enabled,
            hvm0_time: self.override_hvm0.or(genesis_hvm0),
            network: self.tbc_network.clone(),
            genesis_header: self.genesis_header.clone(),
            genesis_height: self.genesis_height,
            header_data_dir: self.header_data_dir.clone(),
            registry_dir: datadir.join("hvm").to_string_lossy().into_owned(),
            tbc_level_db_home: self.tbc_level_db_home.clone(),
            tbc_listen_address: self.tbc_listen_address.clone(),
            tbc_max_cached_txs: self.tbc_max_cached_txs,
            tbc_block_sanity: self.tbc_block_sanity,
            tbc_prometheus_address: self.tbc_prometheus_address.clone(),
            tbc_seeds: self.tbc_seeds.clone(),
            log_level: self.hvm_verbosity,
        }
    }
}
