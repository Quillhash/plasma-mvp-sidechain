package app

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"crypto/ecdsa"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	crypto "github.com/tendermint/tendermint/crypto"
	dbm "github.com/tendermint/tendermint/libs/db"
	"github.com/tendermint/tendermint/libs/log"

	types "github.com/FourthState/plasma-mvp-sidechain/types"
	utils "github.com/FourthState/plasma-mvp-sidechain/utils"
	rlp "github.com/ethereum/go-ethereum/rlp"
)

func newChildChain() *ChildChain {
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout)).With("module", "sdk/app")
	db := dbm.NewMemDB()
	return NewChildChain(logger, db, nil)
}

func InitTestChain(addr common.Address, cc *ChildChain) {
	// Currently only initialize chain with one deposited UTXO
	genState := GenesisUTXO{
		Address:  addr.Hex(),
		Denom:    "100",
		Position: [4]string{"0", "0", "0", fmt.Sprintf("%d", 1)},
	}
	genBytes, err := json.Marshal(genState)
	if err != nil {
		panic(err)
	}
	appStateBytes := []byte(fmt.Sprintf("{\"UTXOs\": [%s]}", string(genBytes)))

	initRequest := abci.RequestInitChain{AppStateBytes: appStateBytes}
	cc.InitChain(initRequest)
}

func GenerateSimpleMsg(Owner1, NewOwner1 common.Address, position [4]uint64, denom1 uint64, fee uint64) types.SpendMsg {
	confirmSigs := [2]types.Signature{types.Signature{}, types.Signature{}}
	return types.SpendMsg{
		Blknum1:      position[0],
		Txindex1:     uint16(position[1]),
		Oindex1:      uint8(position[2]),
		DepositNum1:  position[3],
		Owner1:       Owner1,
		ConfirmSigs1: confirmSigs,
		Blknum2:      0,
		Txindex2:     0,
		Oindex2:      0,
		DepositNum2:  0,
		Owner2:       common.Address{},
		ConfirmSigs2: confirmSigs,
		Newowner1:    NewOwner1,
		Denom1:       denom1,
		Newowner2:    common.Address{},
		Denom2:       0,
		Fee:          fee,
	}
}

// Returns a confirmsig array signed by privKey. two should be true if two positions are passed in.
// Assumes at least first position is passed in
func CreateConfirmSig(position1 types.Position, privKey1, privKey2 *ecdsa.PrivateKey, two bool) (confirmSigs [2]types.Signature) {
	confirmBytes := position1.GetSignBytes()
	hash := ethcrypto.Keccak256(confirmBytes)
	confirmSig, _ := ethcrypto.Sign(hash, privKey1)
	if two {
		confirmSig2, _ := ethcrypto.Sign(hash, privKey2)
		confirmSigs = [2]types.Signature{types.Signature{confirmSig}, types.Signature{confirmSig2}}
	} else {
		confirmSigs = [2]types.Signature{types.Signature{confirmSig}, types.Signature{}}
	}

	return confirmSigs
}

// helper for constructing single input tx
func GetTx(msg types.SpendMsg, privKey *ecdsa.PrivateKey) (tx sdk.Tx) {
	hash := ethcrypto.Keccak256(msg.GetSignBytes())
	sig, _ := ethcrypto.Sign(hash, privKey)
	tx = types.NewBaseTx(msg, []types.Signature{{
		Sig: sig,
	}})
	return tx
}

// Attempts to spend a non-existent utxo
// without depositing first.
func TestBadSpendMsg(t *testing.T) {
	cc := newChildChain()

	privKeyA, _ := ethcrypto.GenerateKey()
	privKeyB, _ := ethcrypto.GenerateKey()

	// Construct a SpendMsg
	msg := GenerateSimpleMsg(utils.PrivKeyToAddress(privKeyA), utils.PrivKeyToAddress(privKeyB),
		[4]uint64{1, 0, 0, 0}, 1000, 1)

	// Signs the hash of the transaction
	hash := ethcrypto.Keccak256(msg.GetSignBytes())
	sig, _ := ethcrypto.Sign(hash, privKeyA)
	tx := types.NewBaseTx(msg, []types.Signature{{
		Sig: crypto.SignatureSecp256k1(sig),
	}})

	txBytes, err := rlp.EncodeToBytes(tx)

	require.NoError(t, err)

	// Must Commit to set checkState
	cc.BeginBlock(abci.RequestBeginBlock{})
	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()

	// Run a check
	cres := cc.CheckTx(txBytes)
	require.Equal(t, sdk.CodeType(6),
		sdk.CodeType(cres.Code), cres.Log)

	// Simulate a Block
	cc.BeginBlock(abci.RequestBeginBlock{})
	dres := cc.DeliverTx(txBytes)
	require.Equal(t, sdk.CodeType(6), sdk.CodeType(dres.Code), dres.Log)

}

func TestSpendDeposit(t *testing.T) {
	cc := newChildChain()

	privKeyA, _ := ethcrypto.GenerateKey()
	privKeyB, _ := ethcrypto.GenerateKey()
	addrA := utils.PrivKeyToAddress(privKeyA)
	addrB := utils.PrivKeyToAddress(privKeyB)

	InitTestChain(addrA, cc)

	msg := GenerateSimpleMsg(addrA, addrB, [4]uint64{0, 0, 0, 1}, 100, 0)

	// Set confirm signatures
	msg.ConfirmSigs1 = CreateConfirmSig(types.NewPosition(0, 0, 0, 1), privKeyA, &ecdsa.PrivateKey{}, false)

	// Signs the hash of the transaction
	hash := ethcrypto.Keccak256(msg.GetSignBytes())
	sig, _ := ethcrypto.Sign(hash, privKeyA)
	tx := types.NewBaseTx(msg, []types.Signature{{
		Sig: sig,
	}})

	// Must commit for checkState to be set correctly. Should be fixed in next version of SDK
	cc.BeginBlock(abci.RequestBeginBlock{})
	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()

	// Simulate a block
	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 1}})

	// Run a check
	cres := cc.Check(tx)
	require.Equal(t, sdk.CodeType(0),
		sdk.CodeType(cres.Code), cres.Log)

	// Deliver tx, updates states
	dres := cc.Deliver(tx)

	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)

	// Create context
	ctx := cc.NewContext(false, abci.Header{})

	// Retrieve UTXO from context
	utxo := cc.utxoMapper.GetUTXO(ctx, addrB, types.NewPosition(1, 0, 0, 0))
	expected := types.NewBaseUTXO(addrB, [2]common.Address{addrA, common.Address{}}, 100, types.NewPosition(1, 0, 0, 0))

	require.Equal(t, expected, utxo, "UTXO did not get added to store correctly")

}

func TestSpendTx(t *testing.T) {
	cc := newChildChain()

	privKeyA, _ := ethcrypto.GenerateKey()
	privKeyB, _ := ethcrypto.GenerateKey()
	addrA := utils.PrivKeyToAddress(privKeyA)
	addrB := utils.PrivKeyToAddress(privKeyB)

	InitTestChain(addrA, cc)
	cc.Commit()

	msg := GenerateSimpleMsg(addrA, addrB, [4]uint64{0, 0, 0, 1}, 100, 0)

	// Set confirm signatures
	msg.ConfirmSigs1 = CreateConfirmSig(types.NewPosition(0, 0, 0, 1), privKeyA, &ecdsa.PrivateKey{}, false)

	// Signs the hash of the transaction
	hash := ethcrypto.Keccak256(msg.GetSignBytes())
	sig, _ := ethcrypto.Sign(hash, privKeyA)
	tx := types.NewBaseTx(msg, []types.Signature{{
		Sig: crypto.SignatureSecp256k1(sig),
	}})

	// Simulate a block
	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 1}})

	// Deliver tx, updates states
	res := cc.Deliver(tx)

	require.True(t, res.IsOK(), res.Log)

	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()

	// Test that spending from a non-deposit/non-genesis UTXO works

	// generate simple msg
	msg = GenerateSimpleMsg(addrB, addrA, [4]uint64{1, 0, 0, 0}, 100, 0)

	// Set confirm signatures
	msg.ConfirmSigs1 = CreateConfirmSig(types.NewPosition(1, 0, 0, 0), privKeyA, &ecdsa.PrivateKey{}, false)

	// Signs the hash of the transaction
	hash = ethcrypto.Keccak256(msg.GetSignBytes())
	sig, _ = ethcrypto.Sign(hash, privKeyB)
	tx = types.NewBaseTx(msg, []types.Signature{{
		Sig: crypto.SignatureSecp256k1(sig),
	}})

	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 5}})

	// Run a check
	cres := cc.Check(tx)
	require.Equal(t, sdk.CodeType(0),
		sdk.CodeType(cres.Code), cres.Log)

	dres := cc.Deliver(tx)

	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)

	// Create context
	ctx := cc.NewContext(false, abci.Header{})

	// Retrieve UTXO from context
	utxo := cc.utxoMapper.GetUTXO(ctx, addrA, types.NewPosition(5, 0, 0, 0))
	expected := types.NewBaseUTXO(addrA, [2]common.Address{addrB, common.Address{}}, 100, types.NewPosition(5, 0, 0, 0))

	require.Equal(t, expected, utxo, "UTXO did not get added to store correctly")

}

// Tests 1 input 2 ouput, 2 input (different addresses) 1 output,
// 2 input (different addresses) 2 ouputs, and 2 input (same address) 1 output
func TestDifferentTxForms(t *testing.T) {
	// Initialize child chain with deposit
	cc := newChildChain()
	var keys [6]*ecdsa.PrivateKey
	var addrs [6]common.Address

	for i := 0; i < 6; i++ {
		keys[i], _ = ethcrypto.GenerateKey()
		addrs[i] = utils.PrivKeyToAddress(keys[i])
	}

	InitTestChain(addrs[0], cc)
	cc.Commit()

	// Create confirm signature
	confirmSig1 := CreateConfirmSig(types.NewPosition(0, 0, 0, 1), keys[0], &ecdsa.PrivateKey{}, false)

	// Create first tx, 1 input 2 output
	msg := types.SpendMsg{
		Blknum1:      0,
		Txindex1:     uint16(0),
		Oindex1:      uint8(0),
		DepositNum1:  1,
		Owner1:       addrs[0],
		ConfirmSigs1: confirmSig1,
		Blknum2:      0,
		Txindex2:     0,
		Oindex2:      0,
		DepositNum2:  0,
		Owner2:       common.Address{},
		ConfirmSigs2: [2]types.Signature{types.Signature{}, types.Signature{}},
		Newowner1:    addrs[1],
		Denom1:       20,
		Newowner2:    addrs[2],
		Denom2:       80,
		Fee:          0,
	}

	// Sign the hash of the transaction
	tx := GetTx(msg, keys[0])

	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 7}})

	// Run a check
	cres := cc.Check(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(cres.Code), cres.Log)

	dres := cc.Deliver(tx)

	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)

	// Create context
	ctx := cc.NewContext(false, abci.Header{})

	// Retrieve UTXO from context
	utxo1 := cc.utxoMapper.GetUTXO(ctx, addrs[1], types.NewPosition(7, 0, 0, 0))
	expected1 := types.NewBaseUTXO(addrs[1], [2]common.Address{addrs[0], common.Address{}}, 20, types.NewPosition(7, 0, 0, 0))
	utxo2 := cc.utxoMapper.GetUTXO(ctx, addrs[2], types.NewPosition(7, 0, 1, 0))
	expected2 := types.NewBaseUTXO(addrs[2], [2]common.Address{addrs[0], common.Address{}}, 80, types.NewPosition(7, 0, 1, 0))

	require.Equal(t, expected1, utxo1, "First UTXO did not get added to store correctly")
	require.Equal(t, expected2, utxo2, "Second UTXO did not get added to store correctly")

	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()

	// 2 different inputs 1 output
	confirmSig1 = CreateConfirmSig(types.NewPosition(7, 0, 0, 0), keys[0], &ecdsa.PrivateKey{}, false)
	confirmSig2 := CreateConfirmSig(types.NewPosition(7, 0, 1, 0), keys[0], &ecdsa.PrivateKey{}, false)

	msg = types.SpendMsg{
		Blknum1:      7,
		Txindex1:     uint16(0),
		Oindex1:      uint8(0),
		DepositNum1:  0,
		Owner1:       addrs[1],
		ConfirmSigs1: confirmSig1,
		Blknum2:      7,
		Txindex2:     uint16(0),
		Oindex2:      uint8(1),
		DepositNum2:  0,
		Owner2:       addrs[2],
		ConfirmSigs2: confirmSig2,
		Newowner1:    addrs[3],
		Denom1:       100,
		Newowner2:    common.Address{},
		Denom2:       0,
		Fee:          0,
	}

	// Sign the hash of the transaction
	hash := ethcrypto.Keccak256(msg.GetSignBytes())
	sig1, _ := ethcrypto.Sign(hash, keys[1])
	sig2, _ := ethcrypto.Sign(hash, keys[2])
	tx = types.NewBaseTx(msg, []types.Signature{{
		Sig: sig1,
	}, {
		Sig: sig2,
	}})

	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 8}})

	// Run a check
	cres = cc.Check(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(cres.Code), cres.Log)

	dres = cc.Deliver(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)

	ctx = cc.NewContext(false, abci.Header{})

	// Retrieve UTXO from context
	utxo1 = cc.utxoMapper.GetUTXO(ctx, addrs[3], types.NewPosition(8, 0, 0, 0))
	expected1 = types.NewBaseUTXO(addrs[3], [2]common.Address{addrs[1], addrs[2]}, 100, types.NewPosition(8, 0, 0, 0))

	require.Equal(t, expected1, utxo1, "UTXO with 2 different inputs did not get added to the store correctly")

	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()

	// split utxo up so 4 outputs can be used
	confirmSig1 = CreateConfirmSig(types.NewPosition(8, 0, 0, 0), keys[1], keys[2], true)

	msg = types.SpendMsg{
		Blknum1:      8,
		Txindex1:     uint16(0),
		Oindex1:      uint8(0),
		DepositNum1:  0,
		Owner1:       addrs[3],
		ConfirmSigs1: confirmSig1,
		Blknum2:      0,
		Txindex2:     uint16(0),
		Oindex2:      uint8(0),
		DepositNum2:  0,
		Owner2:       common.Address{},
		ConfirmSigs2: [2]types.Signature{types.Signature{}, types.Signature{}},
		Newowner1:    addrs[3],
		Denom1:       75,
		Newowner2:    addrs[4],
		Denom2:       25,
		Fee:          0,
	}

	tx = GetTx(msg, keys[3])

	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 9}})

	// Run a check
	cres = cc.Check(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(cres.Code), cres.Log)

	dres = cc.Deliver(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)

	ctx = cc.NewContext(false, abci.Header{})

	utxo1 = cc.utxoMapper.GetUTXO(ctx, addrs[3], types.NewPosition(9, 0, 0, 0))
	expected1 = types.NewBaseUTXO(addrs[3], [2]common.Address{addrs[3], common.Address{}}, 75, types.NewPosition(9, 0, 0, 0))
	utxo2 = cc.utxoMapper.GetUTXO(ctx, addrs[4], types.NewPosition(9, 0, 1, 0))
	expected2 = types.NewBaseUTXO(addrs[4], [2]common.Address{addrs[3], common.Address{}}, 25, types.NewPosition(9, 0, 1, 0))

	require.Equal(t, expected1, utxo1, "First UTXO created from split did not get added to the store correctly")
	require.Equal(t, expected2, utxo2, "Second UTXO created from split did not get added to the store correctly")

	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()
	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 10}})

	// 2 different inputs, 2 outputs (same)
	confirmSig1 = CreateConfirmSig(types.NewPosition(9, 0, 0, 0), keys[3], &ecdsa.PrivateKey{}, false)
	confirmSig2 = CreateConfirmSig(types.NewPosition(9, 0, 1, 0), keys[3], &ecdsa.PrivateKey{}, false)

	msg.Blknum1 = 9
	msg.ConfirmSigs1 = confirmSig1
	msg.Blknum2 = 9
	msg.Oindex2 = uint8(1)
	msg.Owner2 = addrs[4]
	msg.ConfirmSigs2 = confirmSig2
	msg.Denom1 = 70
	msg.Newowner2 = addrs[3]
	msg.Denom2 = 30

	hash = ethcrypto.Keccak256(msg.GetSignBytes())
	sig1, _ = ethcrypto.Sign(hash, keys[3])
	sig2, _ = ethcrypto.Sign(hash, keys[4])
	tx = types.NewBaseTx(msg, []types.Signature{{
		Sig: sig1,
	}, {
		Sig: sig2,
	}})

	cres = cc.Check(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(cres.Code), cres.Log)

	dres = cc.Deliver(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)
	ctx = cc.NewContext(false, abci.Header{})

	utxo1 = cc.utxoMapper.GetUTXO(ctx, addrs[3], types.NewPosition(10, 0, 0, 0))
	expected1 = types.NewBaseUTXO(addrs[3], [2]common.Address{addrs[3], addrs[4]}, 70, types.NewPosition(10, 0, 0, 0))
	utxo2 = cc.utxoMapper.GetUTXO(ctx, addrs[3], types.NewPosition(10, 0, 1, 0))
	expected2 = types.NewBaseUTXO(addrs[3], [2]common.Address{addrs[3], addrs[4]}, 30, types.NewPosition(10, 0, 1, 0))

	require.Equal(t, expected1, utxo1, "First UTXO created from 2 differnet inputs 2 outputs did not get added to the store correctly")
	require.Equal(t, expected2, utxo2, "Second UTXO created from 2 different inputs 2 outputs did not get added to the store correctly")

	cc.EndBlock(abci.RequestEndBlock{})
	cc.Commit()
	cc.BeginBlock(abci.RequestBeginBlock{Header: abci.Header{Height: 11}})

	// merge utxos
	confirmSig1 = CreateConfirmSig(types.NewPosition(10, 0, 0, 0), keys[3], keys[4], true)
	confirmSig2 = CreateConfirmSig(types.NewPosition(10, 0, 1, 0), keys[3], keys[4], true)

	msg.Blknum1 = 10
	msg.ConfirmSigs1 = confirmSig1
	msg.Blknum2 = 10
	msg.Owner2 = addrs[3]
	msg.ConfirmSigs2 = confirmSig2
	msg.Denom1 = 100
	msg.Newowner2 = common.Address{}
	msg.Denom2 = 0

	hash = ethcrypto.Keccak256(msg.GetSignBytes())
	sig1, _ = ethcrypto.Sign(hash, keys[3])
	tx = types.NewBaseTx(msg, []types.Signature{{
		Sig: sig1,
	}, {
		Sig: sig1,
	}})

	cres = cc.Check(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(cres.Code), cres.Log)

	dres = cc.Deliver(tx)
	require.Equal(t, sdk.CodeType(0), sdk.CodeType(dres.Code), dres.Log)

	ctx = cc.NewContext(false, abci.Header{})

	utxo1 = cc.utxoMapper.GetUTXO(ctx, addrs[3], types.NewPosition(11, 0, 0, 0))
	expected1 = types.NewBaseUTXO(addrs[3], [2]common.Address{addrs[3], addrs[3]}, 100, types.NewPosition(11, 0, 0, 0))

	require.Equal(t, expected1, utxo1, "First UTXO created from merge tx did not get added to the store correctly")
}
