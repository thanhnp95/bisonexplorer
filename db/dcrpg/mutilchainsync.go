// Copyright (c) 2018-2021, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"database/sql"
	"fmt"

	"github.com/decred/dcrdata/db/dcrpg/v8/internal"
	"github.com/decred/dcrdata/db/dcrpg/v8/internal/mutilchainquery"
	apitypes "github.com/decred/dcrdata/v8/api/types"
	"github.com/decred/dcrdata/v8/db/dbtypes"
	"github.com/decred/dcrdata/v8/mutilchain"
	"github.com/decred/dcrdata/v8/txhelpers"
)

func (pgb *ChainDB) Sync24BlocksAsync() {
	//delete all invalid row
	numRow, delErr := DeleteInvalid24hBlocksRow(pgb.db)
	if delErr != nil {
		log.Errorf("failed to delete invalid block from DB: %v", delErr)
		return
	}

	if numRow > 0 {
		log.Debugf("Sync24BlocksAsync: Deleted %d rows on 24hblocks table", numRow)
	}
	chainList := []string{mutilchain.TYPEDCR}
	chainList = append(chainList, dbtypes.MutilchainList...)
	//Get valid blockchain
	for _, chain := range chainList {
		pgb.Sync24hMetricsByChainType(chain)
	}
}

func (pgb *ChainDB) Sync24hMetricsByChainType(chain string) {
	if pgb.ChainDisabledMap[chain] {
		return
	}
	if chain == mutilchain.TYPEDCR {
		log.Debugf("Start syncing for 24hblocks info. ChainType: %s", mutilchain.TYPEDCR)
		pgb.SyncDecred24hBlocks()
		log.Debugf("Finish syncing for 24hblocks info. ChainType: %s", mutilchain.TYPEDCR)
		return
	}
	bbheight, _ := pgb.GetMutilchainBestBlock(chain)
	if bbheight == 0 {
		return
	}
	pgb.SyncMutilchain24hBlocks(bbheight, chain)
}

func (pgb *ChainDB) SyncDecred24hBlocks() {
	blockList, err := Retrieve24hBlockData(pgb.ctx, pgb.db)
	if err != nil {
		log.Errorf("Sync Decred blocks in 24h failed: %v", err)
		return
	}
	dbTx, err := pgb.db.BeginTx(pgb.ctx, nil)
	if err != nil {
		log.Errorf("failed to start new DB transaction: %v", err)
		return
	}
	//prepare query
	stmt, err := dbTx.Prepare(internal.Insert24hBlocksRow)
	if err != nil {
		dbTx.Rollback()
		log.Errorf("insert block info to 24hblocks table failed: %v", err)
		return
	}
	for _, block := range blockList {
		var exist bool
		//check exist on DB
		err := pgb.db.QueryRowContext(pgb.ctx, internal.CheckExist24Blocks, mutilchain.TYPEDCR, block.BlockHeight).Scan(&exist)
		if err != nil || exist {
			continue
		}
		var txnum, spent, sent, numvin, numvout int64
		pgb.db.QueryRowContext(pgb.ctx, internal.Select24hBlockSummary, block.BlockHeight).Scan(&txnum, &spent, &sent, &numvin, &numvout)
		//handler for fees
		block.Fees, _ = pgb.GetDecredBlockFees(block.BlockHash)
		//insert to db
		var id uint64
		err = stmt.QueryRow(mutilchain.TYPEDCR, block.BlockHash, block.BlockHeight, block.BlockTime,
			spent, sent, block.Fees, txnum, numvin, numvout).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbTx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return
		}
	}
	stmt.Close()
	dbTx.Commit()
}

func (pgb *ChainDB) GetDecredBlockFees(blockHash string) (int64, error) {
	data := pgb.GetBlockVerboseByHash(blockHash, true)
	if data == nil {
		return 0, fmt.Errorf("Unable to get block for block hash: %s", blockHash)
	}
	totalFees := int64(0)
	//Get fees from stake txs
	for i := range data.RawTx {
		stx := &data.RawTx[i]
		msgTx, err := txhelpers.MsgTxFromHex(stx.Hex)
		if err != nil {
			continue
		}
		fees, _ := txhelpers.TxFeeRate(msgTx)
		if int64(fees) < 0 {
			continue
		}
		totalFees += int64(fees)
	}
	return totalFees, nil
}

func (pgb *ChainDB) SyncMutilchain24hBlocks(height int64, chainType string) {
	if chainType == mutilchain.TYPEXMR {
		pgb.SyncXMR24hBlockInfo(height)
		return
	}
	log.Infof("Start syncing for 24hblocks info. ChainType: %s", chainType)
	dbTx, err := pgb.db.BeginTx(pgb.ctx, nil)
	if err != nil {
		log.Errorf("failed to start new DB transaction: %v", err)
		return
	}
	//prepare query
	stmt, err := dbTx.Prepare(internal.Insert24hBlocksRow)
	if err != nil {
		log.Errorf("%s: Prepare insert block info to 24hblocks table failed: %v", chainType, err)
		_ = dbTx.Rollback()
		return
	}
	for {
		var exist bool
		//check exist on DB
		err := pgb.db.QueryRowContext(pgb.ctx, internal.CheckExist24Blocks, chainType, height).Scan(&exist)
		if err != nil {
			log.Errorf("%s: Check block exist in 24hblocks table failed: %v", chainType, err)
			_ = stmt.Close()
			_ = dbTx.Rollback()
			return
		}

		if exist {
			height--
			continue
		}

		//Get block hash
		bHash, hashErr := pgb.GetDaemonMutilchainBlockHash(height, chainType)
		if hashErr != nil {
			log.Errorf("%s: Get block hash from height failed: %v", chainType, err)
			_ = stmt.Close()
			_ = dbTx.Rollback()
			return
		}

		var blockData *apitypes.Block24hData
		var isBreak bool
		switch chainType {
		case mutilchain.TYPELTC:
			blockData, isBreak = pgb.GetLTCBlockData(bHash, height)
		case mutilchain.TYPEBTC:
			blockData, isBreak = pgb.GetBTCBlockData(bHash, height)
		}

		if isBreak {
			break
		}

		log.Debugf("%s: Insert to 24h blocks metric: Height: %d, TxNum: %d", chainType, blockData.BlockHeight, blockData.NumTx)

		//insert to db
		var id uint64
		err = stmt.QueryRow(chainType, blockData.BlockHash, blockData.BlockHeight, blockData.BlockTime,
			blockData.Spent, blockData.Sent, blockData.Fees, blockData.NumTx, blockData.NumVin, blockData.NumVout).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			log.Errorf("%s: Insert to blocks24h failed: %v", chainType, err)
			_ = stmt.Close() // try, but we want the QueryRow error back
			if errRoll := dbTx.Rollback(); errRoll != nil {
				log.Errorf("Rollback failed: %v", errRoll)
			}
			return
		}
		height--
	}
	stmt.Close()
	dbTx.Commit()
	log.Infof("Finish syncing for 24hblocks info. ChainType: %s", chainType)
}

func (pgb *ChainDB) MultichainCheckAndRemoveDupplicate(chainType string) error {
	// check and remove dupplicate row on blocks table
	log.Infof("%s: Check and remove duplicate rows for %sblocks_all table", chainType, chainType)
	_, err := pgb.db.Exec(mutilchainquery.CreateCheckAndRemoveDuplicateRowQuery(chainType))
	if err != nil {
		log.Errorf("%s: Check and remove duplicate rows for %sblocks_all table error: %v", chainType, chainType, err)
		return err
	}
	log.Infof("%s: Finish check and remove duplicate rows for %sblocks_all table", chainType, chainType)
	// check and remove dupplicate row on transactions table
	log.Infof("%s: Check and remove duplicate rows for %stransactions table", chainType, chainType)
	_, err = pgb.db.Exec(mutilchainquery.CreateCheckAndRemoveDupplicateTxsRowQuery(chainType))
	if err != nil {
		log.Errorf("%s: Check and remove duplicate rows for %stransactions table error: %v", chainType, chainType, err)
		return err
	}
	log.Infof("%s: Finish check and remove duplicate rows for %stransactions table", chainType, chainType)
	if chainType == mutilchain.TYPEXMR {
		// check and remove dupplicate for monero_outputs table
		log.Infof("%s: Check and remove duplicate rows for monero_outputs table", chainType)
		_, err = pgb.db.Exec(mutilchainquery.CheckAndRemoveDuplicateMoneroOutputsRows)
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for monero_outputs table error: %v", chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for monero_outputs table", chainType)
		// check and remove dupplicate for monero_key_images table
		log.Infof("%s: Check and remove duplicate rows for monero_key_images table", chainType)
		_, err = pgb.db.Exec(mutilchainquery.CheckAndRemoveDuplicateMoneroKeyImageRows)
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for monero_key_images table error: %v", chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for monero_key_images table", chainType)
		// check and remove dupplicate for monero_ring_members table
		log.Infof("%s: Check and remove duplicate rows for monero_ring_members table", chainType)
		_, err = pgb.db.Exec(mutilchainquery.CheckAndRemoveDuplicateMoneroRingMembers)
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for monero_ring_members table error: %v", chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for monero_ring_members table", chainType)
		// check and remove dupplicate for monero_rct_data table
		log.Infof("%s: Check and remove duplicate rows for monero_rct_data table", chainType)
		_, err = pgb.db.Exec(mutilchainquery.CheckAndRemoveDuplicateMoneroRctDataRows)
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for monero_rct_data table error: %v", chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for monero_rct_data table", chainType)
	} else {
		// check and remove dupplicate for addresses table
		log.Infof("%s: Check and remove duplicate rows for %saddresses table", chainType, chainType)
		_, err = pgb.db.Exec(mutilchainquery.CreateCheckAndRemoveDuplicateAddressRowsQuery(chainType))
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for %saddresses table error: %v", chainType, chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for %saddresses table", chainType, chainType)
		// check and remove dupplicate for vins_all table
		log.Infof("%s: Check and remove duplicate rows for %svins_all table", chainType, chainType)
		_, err = pgb.db.Exec(mutilchainquery.CreateCheckAndRemoveDuplicateVinsRowsQuery(chainType))
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for %svins_all table error: %v", chainType, chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for %svins_all table", chainType, chainType)
		// check and remove dupplicate for vouts_all table
		log.Infof("%s: Check and remove duplicate rows for %svouts_all table", chainType, chainType)
		_, err = pgb.db.Exec(mutilchainquery.CreateCheckAndRemoveDuplicateVoutsRowsQuery(chainType))
		if err != nil {
			log.Errorf("%s: Check and remove duplicate rows for %svouts_all table error: %v", chainType, chainType, err)
			return err
		}
		log.Infof("%s: Finish check and remove duplicate rows for %svouts_all table", chainType, chainType)
	}
	return nil
}
