//! Builds the hVM Go bridge (`hvm-go`) as a static C archive and links it.
//!
//! The bridge embeds hemilabs/op-geth's hVM (the TBC Bitcoin node, the hVM precompiles and the
//! hVM state management), so op-reth runs exactly the same hVM code as op-geth. A Go toolchain
//! (`go`, or `$GO`) is required.

use std::{env, path::PathBuf, process::Command};

fn main() {
    let manifest = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let go_dir = manifest.join("hvm-go");
    let out = PathBuf::from(env::var("OUT_DIR").unwrap());
    let lib = out.join("libhvm.a");

    println!("cargo:rerun-if-env-changed=GO");
    for f in ["go.mod", "go.sum", "compat.go", "ffi.go", "services.go", "ported_blockchain.go",
        "ported_btcdiff_writers.go"]
    {
        println!("cargo:rerun-if-changed={}", go_dir.join(f).display());
    }

    let go = env::var("GO").unwrap_or_else(|_| "go".to_string());
    let status = Command::new(&go)
        .current_dir(&go_dir)
        .env("CGO_ENABLED", "1")
        .args(["build", "-buildmode=c-archive", "-trimpath", "-o"])
        .arg(&lib)
        .arg(".")
        .status()
        .unwrap_or_else(|e| panic!("failed to run `{go}` to build the hVM bridge: {e}"));
    assert!(status.success(), "building the hVM Go bridge failed");

    println!("cargo:rustc-link-search=native={}", out.display());
    println!("cargo:rustc-link-lib=static=hvm");
    println!("cargo:rustc-link-lib=dylib=pthread");
    println!("cargo:rustc-link-lib=dylib=dl");
    println!("cargo:rustc-link-lib=dylib=m");
    println!("cargo:rustc-link-lib=dylib=resolv");
}
