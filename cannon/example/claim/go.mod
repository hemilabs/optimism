module claim

go 1.22.2

toolchain go1.22.6

require github.com/ethereum-optimism/optimism v0.0.0

require (
	golang.org/x/crypto v0.19.0 // indirect
	golang.org/x/sys v0.17.0 // indirect
)

replace github.com/ethereum-optimism/optimism v0.0.0 => ../../..
