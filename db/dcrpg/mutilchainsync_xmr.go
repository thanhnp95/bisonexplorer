// Copyright (c) 2018-2021, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/decred/dcrdata/db/dcrpg/v8/internal"
	"github.com/decred/dcrdata/db/dcrpg/v8/internal/mutilchainquery"
	"github.com/decred/dcrdata/v8/db/dbtypes"
	"github.com/decred/dcrdata/v8/mutilchain"
	"github.com/decred/dcrdata/v8/xmr/xmrclient"
	"github.com/decred/dcrdata/v8/xmr/xmrhelper"
)

func (pgb *ChainDB) SaveXMRBlockSummaryData(height int64) error {
	// get all tx with height
	txRes, err := pgb.retrieveXmrTxsWithHeight(height)
	if err != nil {
		log.Errorf("XMR: retrieveXmrTxsWithHeight failed: Height: %d, %v", height, err)
		return err
	}
	err = pgb.updateXMRBlockSummary(height, txRes)
	if err != nil {
		log.Errorf("XMR: updateXMRBlockSummary failed: Height: %d, %v", height, err)
		return err
	}
	return nil
}

func (pgb *ChainDB) updateXMRBlockSummary(height int64, txsParseRes storeTxnsResult) error {
	_, err := pgb.db.Exec(mutilchainquery.UpdateXMRBlockSummaryWithHeight, txsParseRes.ringSize,
		txsParseRes.avgRingSize, txsParseRes.fees, txsParseRes.feePerKb, txsParseRes.totalSize, txsParseRes.avgTxSize,
		txsParseRes.decoy03, txsParseRes.decoy47, txsParseRes.decoy811,
		txsParseRes.decoy1214, txsParseRes.decoyGe15, true, txsParseRes.totalSent, txsParseRes.txCount, txsParseRes.numVins, txsParseRes.numVouts, height)
	if err != nil {
		log.Errorf("XMR: Update xmr block summary failed: %v", err)
		return err
	}
	return nil
}

func (pgb *ChainDB) StoreXMRWholeBlock(client *xmrclient.XMRClient, checked, updateAddressesSpendingInfo bool, height int64) (numVins int64, numVouts int64, numTxs int64, err error) {
	br, berr := client.GetBlock(uint64(height))
	if berr != nil {
		err = berr
		log.Errorf("XMR: GetBlock(%d) failed: %v", height, err)
		return
	}
	// Convert the wire.MsgBlock to a dbtypes.Block
	dbBlock, blerr := xmrhelper.MsgXMRBlockToDBBlock(client, br, uint64(height))
	if blerr != nil {
		err = blerr
		log.Errorf("XMR: Get block data failed: Height: %d. Error: %v", height, err)
		return
	}
	dbtx, err := pgb.db.Begin()
	if err != nil {
		err = fmt.Errorf("XMR: Begin sql tx: %v", err)
		log.Error(err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = dbtx.Rollback()
		}
	}()

	txRes := pgb.storeXMRWholeTxns(dbtx, client, dbBlock, checked, updateAddressesSpendingInfo)
	if txRes.err != nil {
		err = txRes.err
		log.Errorf("XMR: storeXMRWholeTxns failed: %v", err)
		return
	}

	numVins = txRes.numVins
	numVouts = txRes.numVouts
	numTxs = int64(dbBlock.NumTx)
	dbBlock.NumVins = uint32(numVins)
	dbBlock.NumVouts = uint32(numVouts)
	dbBlock.Fees = uint64(txRes.fees)
	dbBlock.TotalSent = uint64(txRes.totalSent)
	txRes.totalSize += int64(dbBlock.Size)
	// Store the block now that it has all it's transaction PK IDs
	_, err = InsertXMRWholeBlock(dbtx, dbBlock, true, checked, txRes)
	if err != nil {
		log.Error("XMR: InsertBlock:", err)
		return
	}

	// update synced flag for block
	// log.Infof("LTC: Set synced flag for height: %d", dbBlock.Height)
	err = UpdateMutilchainSyncedStatus(dbtx, uint64(dbBlock.Height), mutilchain.TYPEXMR)
	if err != nil {
		log.Error("XMR: UpdateLastBlock:", err)
		return
	}

	// 7) Commit the tx
	if cerr := dbtx.Commit(); cerr != nil {
		err = fmt.Errorf("commit tx: %v", cerr)
		log.Error(err)
		return
	}
	committed = true
	return
}

func (pgb *ChainDB) retrieveXmrTxsWithHeight(blockHeight int64) (storeTxnsResult, error) {
	var txRes storeTxnsResult
	br, berr := pgb.XmrClient.GetBlock(uint64(blockHeight))
	if berr != nil {
		log.Errorf("XMR: GetBlock(%d) failed: %v", blockHeight, berr)
		return txRes, berr
	}

	blockSize := xmrhelper.GetXMRBlockSize(br)
	txids := br.TxHashes
	txRes.txCount = int64(len(txids))
	var totalTxSize, totalRingSize, totalDecoy03, totalDecoy47, totalDecoy811, totalDecoy1214, totalDecoyGe15 int64
	if len(txids) > 0 {
		blTxsData, blTxserr := pgb.XmrClient.GetTransactions(txids, true)
		if blTxserr != nil {
			log.Errorf("XMR: GetTransactions failed: %v", blTxserr)
			return txRes, blTxserr
		}
		for i, _ := range txids {
			var txJSONStr string
			if i < len(blTxsData.TxsAsJSON) {
				txJSONStr = blTxsData.TxsAsJSON[i]
			}
			var txHex string
			if i < len(blTxsData.TxsAsHex) {
				txHex = blTxsData.TxsAsHex[i]
			}
			if txHex != "" {
				totalTxSize += int64(len(txHex) / 2)
			}
			// txRes.fees += tx.Fees
			// totalTxSize += tx.Size
			if txJSONStr != "" {
				parseResult, err := GetXmrTxParseJSONSimpleData(txJSONStr)
				if err != nil {
					log.Error("XMR: GetXmrTxParseJSONSimpleData failed: %v", err)
					return txRes, err
				}
				txRes.fees += parseResult.fees
				txRes.numVins += int64(parseResult.numVins)
				txRes.numVouts += int64(parseResult.numVouts)
				txRes.totalSent += parseResult.totalSent
				totalRingSize += int64(parseResult.ringSize)
				totalDecoy03 += int64(parseResult.decoy03Num)
				totalDecoy47 += int64(parseResult.decoy47Num)
				totalDecoy811 += int64(parseResult.decoy811Num)
				totalDecoy1214 += int64(parseResult.decoy1214Num)
				totalDecoyGe15 += int64(parseResult.decoyGe15Num)
			}
		}
	}
	// calculate for final
	txRes.ringSize = totalRingSize
	if txRes.numVins > 0 {
		txRes.avgRingSize = int64(math.Round(float64(totalRingSize) / float64(txRes.numVins)))
		txRes.decoy47 = 100 * (float64(totalDecoy47) / float64(txRes.numVins))
		txRes.decoy811 = 100 * (float64(totalDecoy811) / float64(txRes.numVins))
		txRes.decoy1214 = 100 * (float64(totalDecoy1214) / float64(txRes.numVins))
		txRes.decoyGe15 = 100 * (float64(totalDecoyGe15) / float64(txRes.numVins))
		txRes.decoy03 = 100 - txRes.decoy47 - txRes.decoy811 - txRes.decoy1214 - txRes.decoyGe15
	}
	txSizeKb := totalTxSize / 1024
	if txSizeKb > 0 {
		txRes.feePerKb = int64(math.Round(float64(txRes.fees) / float64(txSizeKb)))
	}
	if len(txids) > 0 {
		txRes.avgTxSize = totalTxSize / int64(len(txids))
	}
	txRes.totalSize = totalTxSize + blockSize
	return txRes, nil
}

func (pgb *ChainDB) storeXMRWholeTxns(dbtx *sql.Tx, client *xmrclient.XMRClient, block *dbtypes.Block, checked, addressSpendingUpdateInfo bool) storeTxnsResult {
	var txRes storeTxnsResult
	// insert to txs
	// fetch decoded txs JSON (batch)
	blTxsData, blTxserr := client.GetTransactions(block.Tx, true)
	if blTxserr != nil {
		log.Errorf("XMR: GetTransactions failed: %v", blTxserr)
		txRes.err = blTxserr
		return txRes
	}
	var totalTxSize, totalRingSize, totalDecoy03, totalDecoy47, totalDecoy811, totalDecoy1214, totalDecoyGe15 int64
	for i, txHash := range block.Tx {
		isCoinbaseTx := txHash == block.MinnerTxhash
		var txJSONStr string
		if i < len(blTxsData.TxsAsJSON) {
			txJSONStr = blTxsData.TxsAsJSON[i]
		}
		var txHex string
		if i < len(blTxsData.TxsAsHex) {
			txHex = blTxsData.TxsAsHex[i]
		}
		// insert transaction row
		// Get the tx PK IDs for storage in the blocks table
		_, fees, txSize, err := InsertXMRTxn(dbtx, block.Height, block.Hash, block.Time.T.Unix(), int64(i), txHash, txHex, txJSONStr, checked, isCoinbaseTx)
		if err != nil && err != sql.ErrNoRows {
			log.Error("XMR: InsertTxn: %v", err)
			txRes.err = err
			return txRes
		}
		if !isCoinbaseTx {
			txRes.fees += fees
			totalTxSize += int64(txSize)
		}
		if txJSONStr != "" {
			parseResult, err := ParseAndStoreTxJSON(dbtx, txHash, uint64(block.Height), txJSONStr, checked, isCoinbaseTx)
			if err != nil {
				log.Error("XMR: ParseAndStoreTxJSON: %v", err)
				txRes.err = err
				return txRes
			}
			if !isCoinbaseTx {
				txRes.numVins += int64(parseResult.numVins)
				txRes.numVouts += int64(parseResult.numVouts)
				txRes.totalSent += parseResult.totalSent
				totalRingSize += int64(parseResult.ringSize)
				totalDecoy03 += int64(parseResult.decoy03Num)
				totalDecoy47 += int64(parseResult.decoy47Num)
				totalDecoy811 += int64(parseResult.decoy811Num)
				totalDecoy1214 += int64(parseResult.decoy1214Num)
				totalDecoyGe15 += int64(parseResult.decoyGe15Num)
			}
		}
	}
	// calculate for final
	txRes.ringSize = totalRingSize
	if txRes.numVins > 0 {
		txRes.avgRingSize = int64(math.Floor(float64(totalRingSize) / float64(txRes.numVins)))
		txRes.decoy47 = 100 * (float64(totalDecoy47) / float64(txRes.numVins))
		txRes.decoy811 = 100 * (float64(totalDecoy811) / float64(txRes.numVins))
		txRes.decoy1214 = 100 * (float64(totalDecoy1214) / float64(txRes.numVins))
		txRes.decoyGe15 = 100 * (float64(totalDecoyGe15) / float64(txRes.numVins))
		txRes.decoy03 = 100 - txRes.decoy47 - txRes.decoy811 - txRes.decoy1214 - txRes.decoyGe15
	}
	txSizeKb := totalTxSize / 1024
	if txSizeKb > 0 {
		txRes.feePerKb = int64(math.Round(float64(txRes.fees) / float64(txSizeKb)))
	}
	if len(block.Tx) > 0 {
		txsLength := len(block.Tx)
		if block.MinnerTxhash != "" {
			txsLength--
		}
		if txsLength > 0 {
			txRes.avgTxSize = totalTxSize / int64(txsLength)
		}
	}
	txRes.totalSize = totalTxSize
	// set address synced flag
	// txRes.addressesSynced = true
	return txRes
}

func (pgb *ChainDB) SyncXMR24hBlockInfo(height int64) {
	log.Infof("XMR: Start syncing for 24hblocks info.")
	dbTx, err := pgb.db.BeginTx(pgb.ctx, nil)
	if err != nil {
		log.Errorf("failed to start new DB transaction: %v", err)
		return
	}
	//prepare query
	stmt, err := dbTx.Prepare(internal.InsertXMR24hBlocksRow)
	if err != nil {
		log.Errorf("XMR: Prepare insert block info to 24hblocks table failed: %v", err)
		_ = dbTx.Rollback()
		return
	}
	yeserDayTimeInt := time.Now().Add(-24 * time.Hour).Unix()
	for {
		var exist bool
		//check exist on DB
		err := pgb.db.QueryRowContext(pgb.ctx, internal.CheckExist24Blocks, mutilchain.TYPEXMR, height).Scan(&exist)
		if err != nil {
			log.Errorf("XMR: Check block exist in 24hblocks table failed: %v", err)
			_ = stmt.Close()
			_ = dbTx.Rollback()
			return
		}

		if exist {
			height--
			continue
		}
		blockData := pgb.GetDaemonXMRExplorerBlock(height)
		if blockData == nil {
			height--
			continue
		}
		if blockData.BlockTimeUnix < yeserDayTimeInt {
			break
		}
		//insert to db
		var id uint64
		err = stmt.QueryRow(mutilchain.TYPEXMR, blockData.Hash, blockData.Height, dbtypes.NewTimeDef(blockData.BlockTime.T),
			0, 0, blockData.Fees, blockData.TxCount, blockData.TotalInputs, blockData.TotalOutputs, blockData.BlockReward).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			log.Errorf("XMR: Insert to blocks24h failed: %v", err)
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
	log.Infof("XMR: Finish syncing for 24hblocks info")
}

func (pgb *ChainDB) SyncBulkXMRBlockSummaryData() {
	// Check which blocks have not been updated with the summary data sync status
	rows, err := pgb.db.QueryContext(pgb.ctx, mutilchainquery.SelectRemainingNotSyncedChartSummary)
	if err != nil {
		log.Errorf("XMR: Query remaining not synced chart summary block height list failed: %v", err)
		return
	}
	remaingHeights, err := getRemainingHeightsFromSqlRows(rows)
	if err != nil {
		log.Errorf("XMR: Get remaining blocks height list for sync block summary failed: %v", err)
		return
	}
	if len(remaingHeights) <= 0 {
		log.Infof("XMR: No remaining blocks for sync summary data")
		return
	}
	currentHeight := remaingHeights[0]
	// handler for each block
	for _, syncHeight := range remaingHeights {
		err = pgb.SaveXMRBlockSummaryData(syncHeight)
		if err != nil {
			log.Errorf("XMR: SaveXMRBlockSummaryData failed. %v", err)
			return
		}
		if syncHeight%1000 == 0 || syncHeight == remaingHeights[len(remaingHeights)-1] {
			log.Infof("XMR: Sync bulk block summary from: %d to %d", currentHeight, syncHeight)
			currentHeight = syncHeight
		}
	}
}

func (pgb *ChainDB) SyncXMRWholeChain(newIndexes bool) {
	pgb.xmrWholeSyncMtx.Lock()
	defer pgb.xmrWholeSyncMtx.Unlock()

	const maxWorkers = 5

	// atomic counters
	var totalTxs int64
	var totalVins int64
	var totalVouts int64
	var processedBlocks int64

	tickTime := 20 * time.Second
	startTime := time.Now()

	speedReporter := func() {
		totalElapsed := time.Since(startTime).Seconds()
		if totalElapsed < 1.0 {
			return
		}
		tTx := atomic.LoadInt64(&totalTxs)
		tVouts := atomic.LoadInt64(&totalVouts)
		totalVoutPerSec := tVouts / int64(totalElapsed)
		totalTxPerSec := tTx / int64(totalElapsed)
		log.Infof("XMR: Avg. speed: %d tx/s, %d vout/s", totalTxPerSec, totalVoutPerSec)
	}
	var once sync.Once
	defer once.Do(speedReporter)

	// get remaining heights
	xmrBestBlockHeight := pgb.XmrBestBlock.Height
	rows, err := pgb.db.QueryContext(pgb.ctx, mutilchainquery.CreateSelectRemainingNotSyncedHeights(mutilchain.TYPEXMR), xmrBestBlockHeight)
	if err != nil {
		log.Errorf("XMR: Query remaining syncing blocks height list failed: %v", err)
		return
	}
	remaingHeights, err := getRemainingHeightsFromSqlRows(rows)
	if err != nil {
		log.Errorf("XMR: Get remaining blocks height list failed: %v", err)
		return
	}
	if len(remaingHeights) == 0 {
		log.Infof("XMR: No more blocks to synchronize with the whole daemon")
		return
	}
	log.Infof("XMR: Start sync for %d blocks. Minimum height: %d, Maximum height: %d", len(remaingHeights), remaingHeights[0], remaingHeights[len(remaingHeights)-1])

	// Check if reindexing is performed?
	reindexing := newIndexes || (int64(len(remaingHeights)) > pgb.XmrBestBlock.Height/20)
	checkDuplicate := true
	if reindexing {
		checkDuplicate = false
		log.Info("XMR: Large bulk load: Removing indexes")
		if err = pgb.DeindexMutilchainWholeTable(mutilchain.TYPEXMR); err != nil &&
			!strings.Contains(err.Error(), "does not exist") &&
			!strings.Contains(err.Error(), "不存在") {
			log.Errorf("XMR: Deindex for multichain whole table: %v", err)
			return
		}
	}

	// context to cancel on first error
	ctx, cancel := context.WithCancel(pgb.ctx)
	defer cancel()

	// semaphore to limit concurrency
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	// store first error (atomic.Value)
	var firstErr atomic.Value

	// ticker for periodic speed logs
	ticker := time.NewTicker(tickTime)
	defer ticker.Stop()

	// local last stats for per-tick delta
	var lastProcessed int64
	var lastTxs int64
	var lastVins int64
	var lastVouts int64

	// ticker goroutine: log per tick
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				curProcessed := atomic.LoadInt64(&processedBlocks)
				curTxs := atomic.LoadInt64(&totalTxs)
				curVins := atomic.LoadInt64(&totalVins)
				curVouts := atomic.LoadInt64(&totalVouts)

				blocksPerSec := float64(curProcessed-lastProcessed) / tickTime.Seconds()
				txPerSec := float64(curTxs-lastTxs) / tickTime.Seconds()
				vinsPerSec := float64(curVins-lastVins) / tickTime.Seconds()
				voutPerSec := float64(curVouts-lastVouts) / tickTime.Seconds()
				if blocksPerSec != 0 || txPerSec != 0 || vinsPerSec != 0 || voutPerSec != 0 {
					log.Infof("XMR: (%.3f blk/s, %.3f tx/s, %.3f vin/s, %.3f vout/s)", blocksPerSec, txPerSec, vinsPerSec, voutPerSec)
				}
				lastProcessed = curProcessed
				lastTxs = curTxs
				lastVins = curVins
				lastVouts = curVouts
			}
		}
	}()

	// If GetBlock or StoreLTCWholeBlock are NOT thread-safe, uncomment mutexes below:
	// var getBlockMu sync.Mutex
	// var storeBlockMu sync.Mutex

	// iterate heights and spawn workers
	for idx, height := range remaingHeights {
		// early exit if cancelled
		if ctx.Err() != nil {
			break
		}

		// periodic chunk logs to keep parity với code gốc
		if (idx-1)%xmrRescanLogBlockChunk == 0 || idx == 0 {
			if remaingHeights[idx] == 0 {
				log.Infof("XMR: Scanning genesis block.")
			} else {
				curInd := (idx - 1) / xmrRescanLogBlockChunk
				endRangeBlockIdx := (curInd + 1) * xmrRescanLogBlockChunk
				if endRangeBlockIdx >= len(remaingHeights) {
					endRangeBlockIdx = len(remaingHeights) - 1
				}
				log.Infof("XMR: Processing blocks %d to %d...", height, remaingHeights[endRangeBlockIdx])
			}
		}

		// acquire worker slot (or exit if cancelled)
		select {
		case sem <- struct{}{}:
			// acquired
		case <-ctx.Done():
			break
		}

		wg.Add(1)
		go func(h int64) {
			defer wg.Done()
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			retryCount := 0
			for retryCount < 50 {
				// store block (wrap with mutex if needed)
				// storeBlockMu.Lock()
				numVins, numVouts, totalTxs, err := pgb.StoreXMRWholeBlock(pgb.XmrClient, checkDuplicate, false, h)
				// storeBlockMu.Unlock()
				// if error, retry after 2 minute
				if err != nil {
					time.Sleep(2 * time.Minute)
					retryCount++
					log.Errorf("XMR StoreBlock failed (height %d): %v. Retry after 2 minutes", h, err)
					continue
				}
				atomic.AddInt64(&totalVins, numVins)
				atomic.AddInt64(&totalVouts, numVouts)
				atomic.AddInt64(&totalTxs, totalTxs)
				atomic.AddInt64(&processedBlocks, 1)
				break
			}
			// if err != nil {
			// 	if firstErr.Load() == nil {
			// 		firstErr.Store(err)
			// 		cancel()
			// 	}
			// 	log.Errorf("XMR StoreBlock failed (height %d): %v", h, err)
			// 	return
			// }
		}(height)
	}

	// wait for workers
	wg.Wait()

	// if any error occurred, log and return
	if v := firstErr.Load(); v != nil {
		if err, ok := v.(error); ok && err != nil {
			log.Errorf("XMR: sync aborted due to error: %v", err)
			return
		}
	}

	// final speed report
	once.Do(speedReporter)

	// recreate index
	if reindexing {
		// Check and remove duplicate rows if any before recreating index
		err = pgb.MultichainCheckAndRemoveDupplicate(mutilchain.TYPEXMR)
		if err != nil {
			log.Errorf("XMR: Check and remove dupplicate rows on all table failed: %v", err)
			return
		}
		if err = pgb.IndexMutilchainWholeTable(mutilchain.TYPEXMR); err != nil {
			log.Errorf("XMR: Re-index failed: %v", err)
			return
		}
	}
	log.Infof("XMR: Finish sync for %d blocks. Minimum height: %d, Maximum height: %d (processed %d blocks, %d tx total, %d vin total, %d vout total)",
		len(remaingHeights), remaingHeights[0], remaingHeights[len(remaingHeights)-1],
		atomic.LoadInt64(&processedBlocks), atomic.LoadInt64(&totalTxs), atomic.LoadInt64(&totalVins), atomic.LoadInt64(&totalVouts))
}
