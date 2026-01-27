// Copyright (c) 2018-2021, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"database/sql"

	"github.com/decred/dcrdata/db/dcrpg/v8/internal"
	"github.com/decred/dcrdata/v8/mutilchain"
	"github.com/decred/dcrdata/v8/txhelpers"
)

// Check exist and create btc_swaps table
func checkExistAndCreateBtcSwapsTable(db *sql.DB) error {
	err := createTable(db, BtcSwapsTable, internal.CreateBtcAtomicSwapTable)
	return err
}

// --- btc atomic swap tables
func InsertBtcSwap(db *sql.DB, spendHeight int64, swapInfo *txhelpers.MultichainAtomicSwapData) error {
	// check secret hash on decred swaps. And get dcr contract tx
	var dcrContractTx string
	err := db.QueryRow(internal.SelectExistSwapBySecretHash, swapInfo.SecretHash[:]).Scan(&dcrContractTx)
	if err != nil && err != sql.ErrNoRows {
		log.Infof("BTC: Check dcrContract tx with secret hash failed: %v", err)
		return err
	}
	if err == sql.ErrNoRows {
		dcrContractTx = ""
	} else {
		log.Infof("Matched with Decred swap contract tx: %s", dcrContractTx)
	}
	var secret interface{} // only nil interface stores a NULL, not even nil slice
	if len(swapInfo.Secret) > 0 {
		secret = swapInfo.Secret
	}
	var contractTx string
	err = db.QueryRow(internal.InsertBtcContractSpend, swapInfo.ContractTx, dcrContractTx,
		swapInfo.ContractVout, swapInfo.SpendTx, swapInfo.SpendVin, spendHeight, swapInfo.ContractAddress, swapInfo.Value,
		swapInfo.SecretHash[:], secret, swapInfo.Locktime).Scan(&contractTx)
	if err != nil {
		return err
	}
	// update target token on decred swap if match with secrethash
	if dcrContractTx != "" {
		_, err = db.Exec(internal.UpdateTargetToken, mutilchain.TYPEBTC, dcrContractTx)
		if err != nil {
			return err
		}
		log.Infof("Inserted Btc Swap match with Decred swap. Decred contract tx: %s, Bitcoin contract tx: %s", dcrContractTx, swapInfo.ContractTx)
	}
	return nil
}
