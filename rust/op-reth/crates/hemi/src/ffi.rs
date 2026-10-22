//! Raw bindings to the hVM Go bridge (`hvm-go/ffi.go`, `hvm-go/services.go`).

#![allow(missing_docs, unreachable_pub, dead_code)]

use core::ffi::c_void;

pub const STATUS_OK: i32 = 0;
pub const STATUS_DEFER: i32 = 1;
pub const STATUS_INVALID: i32 = 2;
pub const STATUS_ERROR: i32 = 3;
pub const STATUS_DISABLED: i32 = 4;
pub const STATUS_OUT_OF_GAS: i32 = 5;

unsafe extern "C" {
    pub fn HvmFree(p: *mut c_void);
    pub fn HvmInit(cfg: *const u8, cfg_len: usize, out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmStart(out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmRegisterBlock(
        hash: *const u8,
        parent: *const u8,
        number: u64,
        timestamp: u64,
        attrs: *const u8,
        attrs_len: usize,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmSetHead(hash: *const u8, out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmPrepareBlock(hash: *const u8, out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmFinishBlock(ok: i32);
    pub fn HvmPrepareBuild(
        parent: *const u8,
        timestamp: u64,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmNextBtcAttr(
        parent: *const u8,
        timestamp: u64,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmPrecompile(
        addr: u8,
        input: *const u8,
        input_len: usize,
        gas: u64,
        gas_used: *mut u64,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmBtcBlockAvailable(hash: *const u8) -> i32;
    pub fn HvmHasBlock(hash: *const u8) -> i32;
    pub fn HvmMissingBtcBlocks(out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmServeBtcBlocks(
        hashes: *const u8,
        hashes_len: usize,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmIngestBtcBlocks(
        data: *const u8,
        data_len: usize,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmNewKeystone(
        ks: *const u8,
        ks_len: usize,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmUpsertKeystone(
        l1_block: u32,
        l2_block: u32,
        parent: *const u8,
        prev: *const u8,
        state_root: *const u8,
        ep_hash: *const u8,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmDeleteKeystonesAbove(height: u64, out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmKssGetLatestKeystones(count: u64, out: *mut *mut u8, out_len: *mut usize) -> i32;
    pub fn HvmKssGetKeystone(
        abrev: *const u8,
        abrev_len: usize,
        count: u64,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmBtcAttrHashes(
        data: *const u8,
        data_len: usize,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
    pub fn HvmPopPayouts(
        abrev: *const u8,
        abrev_len: usize,
        out: *mut *mut u8,
        out_len: *mut usize,
    ) -> i32;
}

/// Output buffer of a bridge call, freed on drop.
#[derive(Debug)]
pub struct Out {
    pub ptr: *mut u8,
    pub len: usize,
}

impl Out {
    pub const fn new() -> Self {
        Self { ptr: core::ptr::null_mut(), len: 0 }
    }

    pub fn bytes(&self) -> Vec<u8> {
        if self.ptr.is_null() || self.len == 0 {
            return Vec::new();
        }
        // SAFETY: the bridge allocated `len` bytes at `ptr`.
        unsafe { core::slice::from_raw_parts(self.ptr, self.len).to_vec() }
    }

    pub fn string(&self) -> String {
        String::from_utf8_lossy(&self.bytes()).into_owned()
    }
}

impl Drop for Out {
    fn drop(&mut self) {
        if !self.ptr.is_null() {
            // SAFETY: allocated with malloc by the bridge.
            unsafe { HvmFree(self.ptr.cast()) }
        }
    }
}
