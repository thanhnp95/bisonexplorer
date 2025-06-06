// Copyright (c) 2021, The Decred developers
// See LICENSE for details.

package ltctxhelper

import (
	"fmt"
	"strings"
	"time"

	"github.com/decred/dcrdata/v8/txhelpers"
	"github.com/decred/dcrdata/v8/utils"
	"github.com/ltcsuite/ltcd/btcjson"
	"github.com/ltcsuite/ltcd/chaincfg"
	"github.com/ltcsuite/ltcd/chaincfg/chainhash"
	"github.com/ltcsuite/ltcd/ltcutil"
	"github.com/ltcsuite/ltcd/txscript"
	"github.com/ltcsuite/ltcd/wire"
)

// AtomicSwapContractPushes models the data pushes of an atomic swap contract.
type AtomicSwapContractPushes struct {
	ContractAddress   ltcutil.Address `json:"contract_address"`
	RecipientAddress  ltcutil.Address `json:"recipient_address"`
	RefundAddress     ltcutil.Address `json:"refund_address"`
	Locktime          int64           `json:"locktime"`
	SecretHash        [32]byte        `json:"secret_hash"`
	FormattedLocktime string          `json:"formatted_locktime"`
}

func ExtractSwapDataFromWitness(wit wire.TxWitness, params *chaincfg.Params) (*AtomicSwapContractPushes, []byte, []byte, bool, error) {
	var contract, secret []byte
	var refund bool

	switch len(wit) {
	case 5: // maybe redeem
		if len(wit[3]) != 1 || wit[3][0] == txscript.OP_FALSE {
			return nil, nil, nil, refund, nil
		}
		secret = wit[2]
		contract = wit[4]
	case 4: // maybe refund
		refund = true
		if len(wit[2]) != 0 {
			return nil, nil, nil, refund, nil
		} // allow a single zero byte?
		contract = wit[3]
	default:
		return nil, nil, nil, false, nil
	}

	// validate the contract script by attempting to parse it for contract info.
	contractData, err := ParseAtomicSwapContract(contract, params)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if contractData == nil {
		return nil, nil, nil, false, nil // not a contract script
	}

	return contractData, contract, secret, refund, nil
}

// ParseAtomicSwapContract checks if the provided script is an atomic swap
// contact and returns the data pushes of the contract.
func ParseAtomicSwapContract(script []byte, params *chaincfg.Params) (*AtomicSwapContractPushes, error) {
	// validate the contract by calling txscript.ExtractAtomicSwapDataPushes
	contractDataPushes, _ := txscript.ExtractAtomicSwapDataPushes(0, script)
	if contractDataPushes == nil {
		return nil, nil
	}

	contractP2SH, err := ltcutil.NewAddressScriptHash(script, params)
	if err != nil {
		return nil, fmt.Errorf("contract script to p2sh address error: %v", err)
	}

	recipientAddr, err := ltcutil.NewAddressPubKeyHash(contractDataPushes.RecipientHash160[:], params)
	if err != nil {
		return nil, fmt.Errorf("error parsing swap recipient address: %v", err)
	}

	refundAddr, err := ltcutil.NewAddressPubKeyHash(contractDataPushes.RefundHash160[:], params)
	if err != nil {
		return nil, fmt.Errorf("error parsing swap refund address: %v", err)
	}

	var formattedLockTime string
	if contractDataPushes.LockTime >= int64(txscript.LockTimeThreshold) {
		formattedLockTime = time.Unix(contractDataPushes.LockTime, 0).UTC().Format("2006-01-02 15:04:05 (MST)")
	} else {
		formattedLockTime = fmt.Sprintf("block %v", contractDataPushes.LockTime)
	}

	return &AtomicSwapContractPushes{
		ContractAddress:   contractP2SH,
		RecipientAddress:  recipientAddr,
		RefundAddress:     refundAddr,
		Locktime:          contractDataPushes.LockTime,
		SecretHash:        contractDataPushes.SecretHash,
		FormattedLocktime: formattedLockTime,
	}, nil
}

// OutputSpender describes a transaction input that spends an output by
// specifying the spending transaction and the index of the spending input.
type OutputSpender struct {
	Tx         *btcjson.TxRawResult
	InputIndex uint32
}

type OutputSpenderTxOut struct {
	Tx  *wire.MsgTx
	Vin uint32
}

var zeroHash chainhash.Hash

func MsgTxAtomicSwapsInfo(msgTx *wire.MsgTx, outputSpenders map[uint32]*OutputSpenderTxOut,
	params *chaincfg.Params) (*txhelpers.MultichainTxSwapResults, error) {

	// Skip if the tx is generating coins (coinbase, treasurybase, stakebase).
	for _, input := range msgTx.TxIn {
		if input.PreviousOutPoint.Hash == zeroHash {
			return nil, nil
		}
	}

	hash := msgTx.TxHash()

	txSwaps := &txhelpers.MultichainTxSwapResults{
		TxID: hash.String(),
	}

	appendFound := func(found, swapType string) {
		if txSwaps.Found == "" {
			txSwaps.Found = found
			txSwaps.SwapType = swapType
			return
		}
		if strings.Contains(txSwaps.Found, found) {
			return
		}
		txSwaps.Found = fmt.Sprintf("%s, %s", txSwaps.Found, found)
		txSwaps.SwapType = swapType
	}

	// Check if any of this tx's inputs are redeems or refunds, i.e. inputs that
	// spend the output of an atomic swap contract.
	for i, vin := range msgTx.TxIn {
		contractData, contractScript, secret, isRefund, err :=
			ExtractSwapDataFromWitness(vin.Witness, params)
		if err != nil {
			return nil, fmt.Errorf("error checking if input redeems a contract: %v", err)
		}
		if contractData == nil {
			continue
		}
		swapInfo := &txhelpers.MultichainAtomicSwapData{
			ContractTx:   vin.PreviousOutPoint.Hash.String(),
			ContractVout: vin.PreviousOutPoint.Index,
			SpendTx:      hash.String(),
			SpendVin:     uint32(i),
			// Value:            vin.ValueIn, // lame, caller needs to retrieve the prev tx output
			ContractAddress:  contractData.ContractAddress.String(),
			RecipientAddress: contractData.RecipientAddress.String(),
			RefundAddress:    contractData.RefundAddress.String(),
			Locktime:         contractData.Locktime,
			SecretHash:       contractData.SecretHash,
			Secret:           secret, // should be empty for refund
			Contract:         contractScript,
			IsRefund:         isRefund,
		}
		if isRefund {
			if txSwaps.Refunds == nil {
				txSwaps.Refunds = make(map[uint32]*txhelpers.MultichainAtomicSwapData)
			}
			txSwaps.Refunds[uint32(i)] = swapInfo
			appendFound("Refund", utils.REFUND_TYPE)
		} else {
			if txSwaps.Redemptions == nil {
				txSwaps.Redemptions = make(map[uint32]*txhelpers.MultichainAtomicSwapData)
			}
			txSwaps.Redemptions[uint32(i)] = swapInfo
			appendFound("Redemption", utils.REDEMPTION_TYPE)
		}
	}

	if len(outputSpenders) == 0 {
		return txSwaps, nil
	}

	// Check if any of this tx's outputs are contracts. Requires the output to
	// be spent AND the spending input to have the correct sigscript type.
	for i, vout := range msgTx.TxOut {
		spender, spent := outputSpenders[uint32(i)]
		if !spent {
			continue // output must be spent to determine if it is a contract
		}

		scriptClass := txscript.GetScriptClass(vout.PkScript)
		if scriptClass != txscript.WitnessV0ScriptHashTy {
			continue // non-p2wsh outputs cannot currently be contracts
		}

		spendHash := spender.Tx.TxHash()

		// Sanity check that the provided `spender` actually spends this output.
		if len(spender.Tx.TxIn) <= int(spender.Vin) {
			fmt.Println("invalid:", spender.Vin)
		}
		spendingVin := spender.Tx.TxIn[spender.Vin]
		if spendingVin.PreviousOutPoint.Hash != hash {
			return nil, fmt.Errorf("invalid tx spending data, %s:%d not spent by %s",
				hash, i, spendHash)
		}
		// Use the spending tx input script to retrieve swap details.
		contractData, contractScript, secret, isRefund, err :=
			ExtractSwapDataFromWitness(spendingVin.Witness, params)
		if err != nil {
			return nil, fmt.Errorf("error checking if tx output is a contract: %v", err)
		}
		if contractData != nil {
			appendFound("Contract", utils.CONTRACT_TYPE)
			if txSwaps.Contracts == nil {
				txSwaps.Contracts = make(map[uint32]*txhelpers.MultichainAtomicSwapData)
			}
			txSwaps.Contracts[uint32(i)] = &txhelpers.MultichainAtomicSwapData{
				ContractTx:       hash.String(),
				ContractVout:     uint32(i),
				SpendTx:          spendHash.String(),
				SpendVin:         spender.Vin,
				Value:            vout.Value,
				ContractAddress:  contractData.ContractAddress.String(),
				RecipientAddress: contractData.RecipientAddress.String(),
				RefundAddress:    contractData.RefundAddress.String(),
				Locktime:         contractData.Locktime,
				SecretHash:       contractData.SecretHash,
				Secret:           secret,
				Contract:         contractScript,
				IsRefund:         isRefund,
			}
		}
	}

	return txSwaps, nil
}
