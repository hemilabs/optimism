// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package bindings

import (
	"encoding/json"

	"github.com/ethereum-optimism/optimism/op-bindings/solc"
)

const DelayedWETHStorageLayoutJSON = "{\"storage\":[{\"astId\":1000,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"_initialized\",\"offset\":0,\"slot\":\"0\",\"type\":\"t_uint8\"},{\"astId\":1001,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"_initializing\",\"offset\":1,\"slot\":\"0\",\"type\":\"t_bool\"},{\"astId\":1002,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"__gap\",\"offset\":0,\"slot\":\"1\",\"type\":\"t_array(t_uint256)50_storage\"},{\"astId\":1003,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"_owner\",\"offset\":0,\"slot\":\"51\",\"type\":\"t_address\"},{\"astId\":1004,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"__gap\",\"offset\":0,\"slot\":\"52\",\"type\":\"t_array(t_uint256)49_storage\"},{\"astId\":1005,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"balanceOf\",\"offset\":0,\"slot\":\"101\",\"type\":\"t_mapping(t_address,t_uint256)\"},{\"astId\":1006,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"allowance\",\"offset\":0,\"slot\":\"102\",\"type\":\"t_mapping(t_address,t_mapping(t_address,t_uint256))\"},{\"astId\":1007,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"withdrawals\",\"offset\":0,\"slot\":\"103\",\"type\":\"t_mapping(t_address,t_mapping(t_address,t_struct(WithdrawalRequest)1010_storage))\"},{\"astId\":1008,\"contract\":\"src/dispute/weth/DelayedWETH.sol:DelayedWETH\",\"label\":\"config\",\"offset\":0,\"slot\":\"104\",\"type\":\"t_contract(SuperchainConfig)1009\"}],\"types\":{\"t_address\":{\"encoding\":\"inplace\",\"label\":\"address\",\"numberOfBytes\":\"20\"},\"t_array(t_uint256)49_storage\":{\"encoding\":\"inplace\",\"label\":\"uint256[49]\",\"numberOfBytes\":\"1568\",\"base\":\"t_uint256\"},\"t_array(t_uint256)50_storage\":{\"encoding\":\"inplace\",\"label\":\"uint256[50]\",\"numberOfBytes\":\"1600\",\"base\":\"t_uint256\"},\"t_bool\":{\"encoding\":\"inplace\",\"label\":\"bool\",\"numberOfBytes\":\"1\"},\"t_contract(SuperchainConfig)1009\":{\"encoding\":\"inplace\",\"label\":\"contract SuperchainConfig\",\"numberOfBytes\":\"20\"},\"t_mapping(t_address,t_mapping(t_address,t_struct(WithdrawalRequest)1010_storage))\":{\"encoding\":\"mapping\",\"label\":\"mapping(address =\u003e mapping(address =\u003e struct IDelayedWETH.WithdrawalRequest))\",\"numberOfBytes\":\"32\",\"key\":\"t_address\",\"value\":\"t_mapping(t_address,t_struct(WithdrawalRequest)1010_storage)\"},\"t_mapping(t_address,t_mapping(t_address,t_uint256))\":{\"encoding\":\"mapping\",\"label\":\"mapping(address =\u003e mapping(address =\u003e uint256))\",\"numberOfBytes\":\"32\",\"key\":\"t_address\",\"value\":\"t_mapping(t_address,t_uint256)\"},\"t_mapping(t_address,t_struct(WithdrawalRequest)1010_storage)\":{\"encoding\":\"mapping\",\"label\":\"mapping(address =\u003e struct IDelayedWETH.WithdrawalRequest)\",\"numberOfBytes\":\"32\",\"key\":\"t_address\",\"value\":\"t_struct(WithdrawalRequest)1010_storage\"},\"t_mapping(t_address,t_uint256)\":{\"encoding\":\"mapping\",\"label\":\"mapping(address =\u003e uint256)\",\"numberOfBytes\":\"32\",\"key\":\"t_address\",\"value\":\"t_uint256\"},\"t_struct(WithdrawalRequest)1010_storage\":{\"encoding\":\"inplace\",\"label\":\"struct IDelayedWETH.WithdrawalRequest\",\"numberOfBytes\":\"64\"},\"t_uint256\":{\"encoding\":\"inplace\",\"label\":\"uint256\",\"numberOfBytes\":\"32\"},\"t_uint8\":{\"encoding\":\"inplace\",\"label\":\"uint8\",\"numberOfBytes\":\"1\"}}}"

var DelayedWETHStorageLayout = new(solc.StorageLayout)

var DelayedWETHDeployedBin = "0x6080604052600436106101845760003560e01c8063715018a6116100d6578063a9059cbb1161007f578063dd62ed3e11610059578063dd62ed3e1461052b578063f2fde38b14610563578063f3fef3a31461058357600080fd5b8063a9059cbb146104af578063cd47bde1146104cf578063d0e30db01461052357600080fd5b80638da5cb5b116100b05780638da5cb5b1461041b57806395d89b4114610446578063977a5ec51461048f57600080fd5b8063715018a61461039457806379502c55146103a95780637eee288d146103fb57600080fd5b80632e1a7d4d1161013857806354fd4d501161011257806354fd4d50146102eb5780636a42b8f81461033457806370a082311461036757600080fd5b80632e1a7d4d14610284578063313ce567146102a4578063485cc955146102cb57600080fd5b80630ca35682116101695780630ca356821461022757806318160ddd1461024757806323b872dd1461026457600080fd5b806306fdde0314610198578063095ea7b3146101f757600080fd5b36610193576101916105a3565b005b600080fd5b3480156101a457600080fd5b506101e16040518060400160405280600d81526020017f577261707065642045746865720000000000000000000000000000000000000081525081565b6040516101ee91906114fb565b60405180910390f35b34801561020357600080fd5b50610217610212366004611590565b6105fe565b60405190151581526020016101ee565b34801561023357600080fd5b506101916102423660046115bc565b610677565b34801561025357600080fd5b50475b6040519081526020016101ee565b34801561027057600080fd5b5061021761027f3660046115d5565b6107be565b34801561029057600080fd5b5061019161029f3660046115bc565b6109d5565b3480156102b057600080fd5b506102b9601281565b60405160ff90911681526020016101ee565b3480156102d757600080fd5b506101916102e6366004611616565b6109e2565b3480156102f757600080fd5b506101e16040518060400160405280600581526020017f302e322e3000000000000000000000000000000000000000000000000000000081525081565b34801561034057600080fd5b507f0000000000000000000000000000000000000000000000000000000000000000610256565b34801561037357600080fd5b5061025661038236600461164f565b60656020526000908152604090205481565b3480156103a057600080fd5b50610191610bbf565b3480156103b557600080fd5b506068546103d69073ffffffffffffffffffffffffffffffffffffffff1681565b60405173ffffffffffffffffffffffffffffffffffffffff90911681526020016101ee565b34801561040757600080fd5b50610191610416366004611590565b610bd3565b34801561042757600080fd5b5060335473ffffffffffffffffffffffffffffffffffffffff166103d6565b34801561045257600080fd5b506101e16040518060400160405280600481526020017f574554480000000000000000000000000000000000000000000000000000000081525081565b34801561049b57600080fd5b506101916104aa366004611590565b610d1f565b3480156104bb57600080fd5b506102176104ca366004611590565b610e0c565b3480156104db57600080fd5b5061050e6104ea366004611616565b60676020908152600092835260408084209091529082529020805460019091015482565b604080519283526020830191909152016101ee565b6101916105a3565b34801561053757600080fd5b50610256610546366004611616565b606660209081526000928352604080842090915290825290205481565b34801561056f57600080fd5b5061019161057e36600461164f565b610e20565b34801561058f57600080fd5b5061019161059e366004611590565b610ed4565b33600090815260656020526040812080543492906105c290849061169b565b909155505060405134815233907fe1fffcc4923d04b559f4d29a8bfc6cda04eb5b0d3c460751c2402c5c5cc9109c9060200160405180910390a2565b33600081815260666020908152604080832073ffffffffffffffffffffffffffffffffffffffff8716808552925280832085905551919290917f8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925906106669086815260200190565b60405180910390a350600192915050565b60335473ffffffffffffffffffffffffffffffffffffffff1633146106fd576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152601660248201527f44656c61796564574554483a206e6f74206f776e65720000000000000000000060448201526064015b60405180910390fd5b8047101561078d576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602160248201527f44656c61796564574554483a20696e73756666696369656e742062616c616e6360448201527f650000000000000000000000000000000000000000000000000000000000000060648201526084016106f4565b604051339082156108fc029083906000818181858888f193505050501580156107ba573d6000803e3d6000fd5b5050565b73ffffffffffffffffffffffffffffffffffffffff83166000908152606560205260408120548211156107f057600080fd5b73ffffffffffffffffffffffffffffffffffffffff84163314801590610866575073ffffffffffffffffffffffffffffffffffffffff841660009081526066602090815260408083203384529091529020547fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff14155b156108ee5773ffffffffffffffffffffffffffffffffffffffff841660009081526066602090815260408083203384529091529020548211156108a857600080fd5b73ffffffffffffffffffffffffffffffffffffffff84166000908152606660209081526040808320338452909152812080548492906108e89084906116b3565b90915550505b73ffffffffffffffffffffffffffffffffffffffff8416600090815260656020526040812080548492906109239084906116b3565b909155505073ffffffffffffffffffffffffffffffffffffffff83166000908152606560205260408120805484929061095d90849061169b565b925050819055508273ffffffffffffffffffffffffffffffffffffffff168473ffffffffffffffffffffffffffffffffffffffff167fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef846040516109c391815260200190565b60405180910390a35060019392505050565b6109df3382610ed4565b50565b600054610100900460ff1615808015610a025750600054600160ff909116105b80610a1c5750303b158015610a1c575060005460ff166001145b610aa8576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602e60248201527f496e697469616c697a61626c653a20636f6e747261637420697320616c72656160448201527f647920696e697469616c697a656400000000000000000000000000000000000060648201526084016106f4565b600080547fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff001660011790558015610b0657600080547fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff00ff166101001790555b610b0e61121e565b610b17836112bd565b606880547fffffffffffffffffffffffff00000000000000000000000000000000000000001673ffffffffffffffffffffffffffffffffffffffff84161790558015610bba57600080547fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff00ff169055604051600181527f7f26b83ff96e1f2b6a682f133852f6798a09c465da95921460cefb38474024989060200160405180910390a15b505050565b610bc7611334565b610bd160006112bd565b565b606860009054906101000a900473ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff16635c975abb6040518163ffffffff1660e01b8152600401602060405180830381865afa158015610c40573d6000803e3d6000fd5b505050506040513d601f19601f82011682018060405250810190610c6491906116ca565b15610ccb576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152601f60248201527f44656c61796564574554483a20636f6e7472616374206973207061757365640060448201526064016106f4565b33600090815260676020908152604080832073ffffffffffffffffffffffffffffffffffffffff861684529091528120426001820155805490918391839190610d1590849061169b565b9091555050505050565b60335473ffffffffffffffffffffffffffffffffffffffff163314610da0576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152601660248201527f44656c61796564574554483a206e6f74206f776e65720000000000000000000060448201526064016106f4565b33600081815260666020908152604080832073ffffffffffffffffffffffffffffffffffffffff871680855290835292819020859055518481529192917f8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925910160405180910390a35050565b6000610e193384846107be565b9392505050565b610e28611334565b73ffffffffffffffffffffffffffffffffffffffff8116610ecb576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602660248201527f4f776e61626c653a206e6577206f776e657220697320746865207a65726f206160448201527f646472657373000000000000000000000000000000000000000000000000000060648201526084016106f4565b6109df816112bd565b606860009054906101000a900473ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff16635c975abb6040518163ffffffff1660e01b8152600401602060405180830381865afa158015610f41573d6000803e3d6000fd5b505050506040513d601f19601f82011682018060405250810190610f6591906116ca565b15610fcc576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152601f60248201527f44656c61796564574554483a20636f6e7472616374206973207061757365640060448201526064016106f4565b33600090815260676020908152604080832073ffffffffffffffffffffffffffffffffffffffff861684529091529020805482111561108d576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602d60248201527f44656c61796564574554483a20696e73756666696369656e7420756e6c6f636b60448201527f6564207769746864726177616c0000000000000000000000000000000000000060648201526084016106f4565b6000816001015411611120576040517f08c379a0000000000000000000000000000000000000000000000000000000008152602060048201526024808201527f44656c61796564574554483a207769746864726177616c206e6f7420756e6c6f60448201527f636b65640000000000000000000000000000000000000000000000000000000060648201526084016106f4565b427f00000000000000000000000000000000000000000000000000000000000000008260010154611151919061169b565b11156111df576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602560248201527f44656c61796564574554483a207769746864726177616c2064656c6179206e6f60448201527f74206d657400000000000000000000000000000000000000000000000000000060648201526084016106f4565b818160000160008282546111f391906116b3565b90915550610bba9050826113b5565b73ffffffffffffffffffffffffffffffffffffffff163b151590565b600054610100900460ff166112b5576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602b60248201527f496e697469616c697a61626c653a20636f6e7472616374206973206e6f74206960448201527f6e697469616c697a696e6700000000000000000000000000000000000000000060648201526084016106f4565b610bd161145b565b6033805473ffffffffffffffffffffffffffffffffffffffff8381167fffffffffffffffffffffffff0000000000000000000000000000000000000000831681179093556040519116919082907f8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e090600090a35050565b60335473ffffffffffffffffffffffffffffffffffffffff163314610bd1576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820181905260248201527f4f776e61626c653a2063616c6c6572206973206e6f7420746865206f776e657260448201526064016106f4565b336000908152606560205260409020548111156113d157600080fd5b33600090815260656020526040812080548392906113f09084906116b3565b9091555050604051339082156108fc029083906000818181858888f19350505050158015611422573d6000803e3d6000fd5b5060405181815233907f7fcf532c15f0a6db0bd6d0e038bea71d30d808c7d98cb3bf7268a95bf5081b659060200160405180910390a250565b600054610100900460ff166114f2576040517f08c379a000000000000000000000000000000000000000000000000000000000815260206004820152602b60248201527f496e697469616c697a61626c653a20636f6e7472616374206973206e6f74206960448201527f6e697469616c697a696e6700000000000000000000000000000000000000000060648201526084016106f4565b610bd1336112bd565b600060208083528351808285015260005b818110156115285785810183015185820160400152820161150c565b8181111561153a576000604083870101525b50601f017fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe016929092016040019392505050565b73ffffffffffffffffffffffffffffffffffffffff811681146109df57600080fd5b600080604083850312156115a357600080fd5b82356115ae8161156e565b946020939093013593505050565b6000602082840312156115ce57600080fd5b5035919050565b6000806000606084860312156115ea57600080fd5b83356115f58161156e565b925060208401356116058161156e565b929592945050506040919091013590565b6000806040838503121561162957600080fd5b82356116348161156e565b915060208301356116448161156e565b809150509250929050565b60006020828403121561166157600080fd5b8135610e198161156e565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052601160045260246000fd5b600082198211156116ae576116ae61166c565b500190565b6000828210156116c5576116c561166c565b500390565b6000602082840312156116dc57600080fd5b81518015158114610e1957600080fdfea164736f6c634300080f000a"


func init() {
	if err := json.Unmarshal([]byte(DelayedWETHStorageLayoutJSON), DelayedWETHStorageLayout); err != nil {
		panic(err)
	}

	layouts["DelayedWETH"] = DelayedWETHStorageLayout
	deployedBytecodes["DelayedWETH"] = DelayedWETHDeployedBin
	immutableReferences["DelayedWETH"] = true
}