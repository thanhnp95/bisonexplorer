// Copyright (c) 2018-2022, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/decred/dcrdata/db/dcrpg/v8/internal/mutilchainquery"
	"github.com/decred/dcrdata/v8/db/cache"
	exptypes "github.com/decred/dcrdata/v8/explorer/types"
	"github.com/decred/dcrdata/v8/mutilchain"
	"github.com/decred/dcrdata/v8/utils"
	"github.com/decred/dcrdata/v8/xmr/xmrhelper"
	"github.com/decred/dcrdata/v8/xmr/xmrutil"
	humanize "github.com/dustin/go-humanize"
	"github.com/lib/pq"
)

func (pgb *ChainDB) UpdateXMRChainState(blockChainInfo *xmrutil.BlockchainInfo) {
	if pgb == nil {
		return
	}
	if blockChainInfo == nil {
		log.Errorf("XMR: xmrutil.BlockchainInfo data passed is empty")
		return
	}

	pgb.deployments.mtx.Lock()
	pgb.deployments.xmrChainInfo = blockChainInfo
	pgb.deployments.mtx.Unlock()
}

func (pgb *ChainDB) XMRStore(blockData *xmrutil.BlockData) error {
	if pgb == nil || pgb.XmrBestBlock == nil {
		return nil
	}
	pgb.UpdateXMRChainState(&blockData.BlockchainInfo)
	// update best block
	pgb.XmrBestBlock.Hash = blockData.Header.Hash
	pgb.XmrBestBlock.Height = int64(blockData.Header.Height)
	pgb.XmrBestBlock.Time = int64(blockData.Header.Timestamp)
	go pgb.SyncXMROneBlock(blockData)
	return nil
}

func (pgb *ChainDB) SyncXMROneBlock(blockData *xmrutil.BlockData) (err error) {
	pgb.xmrWholeSyncMtx.Lock()
	defer pgb.xmrWholeSyncMtx.Unlock()
	// assume newHdr is obtained from RPC (header of block we are about to store)
	forkHeight, err := pgb.EnsureChainContinuity(pgb.ctx, blockData)
	if err != nil {
		// handle error: cannot find fork, or DB/daemon error
		fmt.Printf("Error ensuring continuity: %v\n", err)
		// decide: skip storing for now or restart sync; here we skip this block
		return err
	}
	if forkHeight >= int64(blockData.Header.Height) {
		return fmt.Errorf("xmr: fork height greater than needed block")
	}
	for handlerHeight := forkHeight + 1; handlerHeight <= int64(blockData.Header.Height); handlerHeight++ {
		log.Infof("XMR: (After reorg) Start sync block data. Height: %d", blockData.Header.Height)
		_, _, _, err = pgb.StoreXMRWholeBlock(pgb.XmrClient, true, true, handlerHeight)
	}
	log.Infof("XMR: Complete sync block data. Height: %d", blockData.Header.Height)
	return
}

func (pgb *ChainDB) findForkHeight(startHash string, maxDepth int) (int64, error) {
	curHash := startHash
	depth := 0
	for {
		// check if curHash exists in DB
		var height sql.NullInt64
		err := pgb.db.QueryRow(mutilchainquery.MakeSelectBlockAllHeightByHash(mutilchain.TYPEXMR), curHash).Scan(&height)
		if err == nil {
			if height.Valid {
				return height.Int64, nil
			}
			// not found, continue
		} else {
			if err != sql.ErrNoRows {
				// non-empty error
				// if it's sql.ErrNoRows we'll handle by retrieving header
			}
		}

		// not found in DB -> fetch header from daemon to get prev_hash and iterate
		hdr, err := pgb.XmrClient.GetBlockHeaderByHash(curHash)
		if err != nil {
			// if daemon doesn't know this hash, we cannot proceed further reliably
			return -1, fmt.Errorf("GetBlockHeaderByHash(%s) error: %v", curHash, err)
		}
		prev := hdr.PrevHash
		if prev == "" {
			// reached genesis or unreachable
			return -1, fmt.Errorf("reached genesis or no prev hash while finding fork")
		}
		curHash = prev

		depth++
		if depth > maxDepth {
			return -1, fmt.Errorf("exceeded maxDepth (%d) while finding fork", maxDepth)
		}
	}
}

func (pgb *ChainDB) rollbackToHeight(keepHeight int64) error {
	// We perform the deletion inside a DB transaction for atomicity.
	tx, err := pgb.db.BeginTx(pgb.ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("XMR: rollbackToHeight: BeginTx failed: %v", err)
	}
	rollbackDone := false
	defer func() {
		if !rollbackDone {
			_ = tx.Rollback()
		}
	}()

	// 1) collect tx hashes to delete (all transactions with block_height > keepHeight)
	rows, err := tx.QueryContext(pgb.ctx, mutilchainquery.CreateSelectTxHashsWithMinHeightQuery(mutilchain.TYPEXMR), keepHeight)
	if err != nil {
		return fmt.Errorf("XMR: rollbackToHeight: select tx_hash failed: %v", err)
	}
	var toDeleteTxs []string
	for rows.Next() {
		var th sql.NullString
		if err := rows.Scan(&th); err != nil {
			rows.Close()
			return fmt.Errorf("XMR: rollbackToHeight: scan tx_hash failed: %v", err)
		}
		if th.Valid {
			toDeleteTxs = append(toDeleteTxs, th.String)
		}
	}
	rows.Close()

	// If no txs to delete, still delete blocks
	// 2) delete dependent rows that reference tx_hash
	if len(toDeleteTxs) > 0 {
		// Use ANY($1) with pq.Array
		// Delete ring members
		if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.DeleteRingMembersWithTxhashArray, pq.Array(toDeleteTxs)); err != nil {
			return fmt.Errorf("XMR: rollbackToHeight: delete monero_ring_members failed: %v", err)
		}
		// Delete rct data
		if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.DeleteRctDataWithTxhashArray, pq.Array(toDeleteTxs)); err != nil {
			return fmt.Errorf("XMR: rollbackToHeight: delete monero_rct_data failed: %v", err)
		}
		// Delete monero_outputs for those txs
		if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.DeleteMoneroOutputWithTxhashArray, pq.Array(toDeleteTxs)); err != nil {
			return fmt.Errorf("XMR: rollbackToHeight: delete monero_outputs failed: %v", err)
		}
		// Delete vins_all for those txs
		if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.MakeDeleteVinAllWithTxHashArrayQuery(mutilchain.TYPEXMR), pq.Array(toDeleteTxs)); err != nil {
			return fmt.Errorf("XMR: rollbackToHeight: delete vins_all failed: %v", err)
		}
		// Finally delete transactions rows
		if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.CreateDeleteTxsWithMinBlockHeightQuery(mutilchain.TYPEXMR), keepHeight); err != nil {
			return fmt.Errorf("XMR: rollbackToHeight: delete transactions failed: %v", err)
		}
	}

	// 3) delete monero_key_images seen in deleted blocks (first_seen_block_height > keepHeight)
	if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.DeleteMoneroKeyImagesWithMinFirstSeenBlHeight, keepHeight); err != nil {
		return fmt.Errorf("XMR: rollbackToHeight: delete monero_key_images failed: %v", err)
	}

	// 4) delete blocks > keepHeight
	if _, err := tx.ExecContext(pgb.ctx, mutilchainquery.CreateDeleteBlocksWithMinHeightQuery(mutilchain.TYPEXMR), keepHeight); err != nil {
		return fmt.Errorf("XMR: rollbackToHeight: delete blocks_all failed: %v", err)
	}

	// commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("XMR: rollbackToHeight: commit failed: %v", err)
	}
	rollbackDone = true
	return nil
}

func (pgb *ChainDB) EnsureChainContinuity(ctx context.Context, newBlockdata *xmrutil.BlockData) (int64, error) {
	storedHash := pgb.XmrBestBlock.Hash
	storedHeight := pgb.XmrBestBlock.Height
	// If DB empty, nothing to do
	if storedHeight < 0 {
		log.Errorf("XMR: EnsureChainContinuity: Data empty")
		return -1, nil
	}

	// If newHdr prev hash matches stored tip, chain continues normally
	if storedHash == newBlockdata.Header.PrevHash {
		// contiguous, ok
		return storedHeight, nil
	}

	log.Infof("XMR: EnsureChainContinuity: Detected possible reorg. storedTip=%s@%d new.prev=%s\n", storedHash, storedHeight, newBlockdata.Header.PrevHash)

	// Find fork height (where new chain meets stored chain)
	forkHeight, err := pgb.findForkHeight(newBlockdata.Header.PrevHash, int(storedHeight)+10000) // maxDepth heuristic
	if err != nil {
		return -1, fmt.Errorf("XMR: findForkHeight failed: %v", err)
	}
	if forkHeight < 0 {
		return -1, fmt.Errorf("XMR: fork height not found (chain does not meet stored chain within limit)")
	}

	log.Infof("XMR: EnsureChainContinuity: Fork detected at height %d. Rolling back blocks > %d\n", forkHeight, forkHeight)

	// Rollback DB to forkHeight
	if err := pgb.rollbackToHeight(forkHeight); err != nil {
		return -1, fmt.Errorf("rollbackToHeight failed: %v", err)
	}
	log.Infof("EnsureChainContinuity: Rollback to height %d completed", forkHeight)
	return forkHeight, nil
}

func (pgb *ChainDB) chartXmrMutilchainBlocks(charts *cache.MutilchainChartData) (*sql.Rows, func(), error) {
	// TODO when handler sync all blockchain data to DB, uncomment
	_, cancel := context.WithTimeout(pgb.ctx, pgb.queryTimeout)
	rows, err := retrieveXmrMutilchainChartBlocks(pgb.ctx, pgb.db, charts)
	if err != nil {
		return nil, cancel, fmt.Errorf("XMR: chartBlocks: %w", pgb.replaceCancelError(err))
	}
	return rows, cancel, nil
}

func (pgb *ChainDB) chartXmrMutilchainRingMembers(charts *cache.MutilchainChartData) (*sql.Rows, func(), error) {
	_, cancel := context.WithTimeout(pgb.ctx, pgb.queryTimeout)
	rows, err := retrieveXmrMutilchainChartRingMembers(pgb.ctx, pgb.db, charts)
	if err != nil {
		return nil, cancel, fmt.Errorf("XMR: chartRingMembers: %w", pgb.replaceCancelError(err))
	}
	return rows, cancel, nil
}

func (pgb *ChainDB) GetXMRBlockchainInfo() (*xmrutil.BlockchainInfo, error) {
	return pgb.XmrClient.GetInfo()
}

func (pgb *ChainDB) GetXMRDaemonExplorerBlocks(from, to int64) []*exptypes.BlockBasic {
	result := make([]*exptypes.BlockBasic, 0)
	// from > to
	for height := from; height >= to; height-- {
		blockInfo := pgb.GetDaemonXMRExplorerBlock(height)
		if blockInfo != nil {
			result = append(result, blockInfo.BlockBasic)
		}
	}
	return result
}

func (pgb *ChainDB) GetXMRDBExplorerBasicBlocks(max, min int64) ([]*exptypes.BlockBasic, error) {
	return retrieveMultichainBasicInfoWithHeightRange(pgb.ctx, pgb.db, max, min, mutilchain.TYPEXMR)
}

func (pgb *ChainDB) GetXMRBlockHeader(height int64) (*xmrutil.BlockHeader, error) {
	blheader, err := pgb.XmrClient.GetBlockHeaderByHeight(uint64(height))
	return blheader, err
}

func (pgb *ChainDB) GetXMRBasicBlock(height int64) *exptypes.BlockBasic {
	br, berr := pgb.XmrClient.GetBlock(uint64(height))
	if berr != nil {
		log.Errorf("XMR: GetBlock(%d) failed: %v", height, berr)
		return nil
	}
	// Convert the wire.MsgBlock to a dbtypes.Block
	clientBlock, err := xmrhelper.MsgXMRBlockToDBBlock(pgb.XmrClient, br, uint64(height))
	if err != nil {
		log.Errorf("XMR: Get block data failed: Height: %d. Error: %v", height, err)
		return nil
	}

	blockData := &exptypes.BlockBasic{
		Height:         int64(clientBlock.Height),
		Hash:           clientBlock.Hash,
		Version:        int32(clientBlock.Version),
		Size:           int32(clientBlock.Size),
		Valid:          true, // we do not know this, TODO with DB v2
		MainChain:      true,
		Transactions:   int(clientBlock.NumTx),
		TxCount:        clientBlock.NumTx,
		BlockTime:      exptypes.NewTimeDefFromUNIX(clientBlock.Time.UNIX()),
		BlockTimeUnix:  clientBlock.Time.UNIX(),
		FormattedBytes: humanize.Bytes(uint64(clientBlock.Size)),
	}
	return blockData
}

func (pgb *ChainDB) GetXMRExplorerBlockByHash(hash string) *exptypes.BlockInfo {
	br, berr := pgb.XmrClient.GetBlockByHash(hash)
	if berr != nil {
		log.Errorf("XMR: GetBlockByHash(%s) failed: %v", hash, berr)
		return nil
	}
	bh, err := pgb.XmrClient.GetBlockHeaderByHash(hash)
	if err != nil {
		log.Errorf("XMR: GetBlockHeaderByHash(%s) failed: %v", hash, err)
		return nil
	}
	return pgb.GetXMRExplorerBlockWithBlockResult(br, bh)
}

func (pgb *ChainDB) GetXMRExplorerTx(txhash string) (*exptypes.TxInfo, error) {
	blTxsData, blTxserr := pgb.XmrClient.GetTransactions([]string{txhash}, true)
	if blTxserr != nil {
		log.Errorf("XMR: GetTransactions for getting explorer block failed: %v", blTxserr)
		return nil, blTxserr
	}
	if len(blTxsData.TxsAsJSON) < 1 || len(blTxsData.TxsAsHex) < 1 || len(blTxsData.Txs) < 1 {
		return nil, fmt.Errorf("XMR. Get transaction by txhash failed")
	}
	txJSONStr := blTxsData.TxsAsJSON[0]
	txHex := blTxsData.TxsAsHex[0]
	var txExtra interface{}
	if txJSONStr != "" {
		// store parsed JSONB for easier queries
		var tmp map[string]interface{}
		if err := json.Unmarshal([]byte(txJSONStr), &tmp); err == nil {
			txExtra = tmp
		} else {
			// fallback: store raw string
			txExtra = txJSONStr
		}
	}
	// get block result
	txData := blTxsData.Txs[0]
	isCoinbase := false
	blockHash := ""
	if txData.BlockHeight > 0 {
		br, err := pgb.XmrClient.GetBlock(uint64(txData.BlockHeight))
		if err != nil {
			return nil, err
		}
		bh, err := pgb.XmrClient.GetBlockHeaderByHeight(uint64(txData.BlockHeight))
		if err != nil {
			return nil, err
		}
		blockHash = bh.Hash
		isCoinbase = txhash == br.MinerTxHash
	}
	version := 0
	lockTime := int64(0)
	size := 0
	if txHex != "" {
		size = len(txHex) / 2
	}
	fees := int64(0)
	numVin := 0
	numVout := 0
	txSent := int64(0)
	var ringSizes []int
	var parsedExtra *exptypes.XmrTxExtra
	// var hexExtra string
	outputs := make([]exptypes.XmrOutputInfo, 0)
	// vinsObjects := make([]exptypes.XmrVinInfo, 0)
	keyImages := make([]exptypes.XmrKeyImageInfo, 0)
	ringMembers := make([]exptypes.XmrRingMemberInfo, 0)
	var rctData *exptypes.XmrRctData
	var parseErr error
	// parse some common fields from txExtra (tx JSON)
	if txExtra != nil {
		switch v := txExtra.(type) {
		case map[string]interface{}:
			if t, ok := v["version"].(float64); ok {
				version = int(t)
			}
			// store rct blob as raw JSON of rct_signatures or hex if available.
			// rctJSON, _ := json.Marshal(rctIf)
			var rctType int = -1
			isRingCt := false
			sumIn := int64(0)
			sumOut := int64(0)
			if rct, ok := v["rct_signatures"].(map[string]interface{}); ok {
				isRingCt = true
				if t, ok2 := rct["type"].(float64); ok2 {
					rctType = int(t)
				}
				if feeIf, ok := rct["txnFee"].(float64); ok {
					fees = int64(feeIf)
				}
			}
			// RingCT presence
			// vins/vouts length
			if vinsIf, ok := v["vin"].([]interface{}); ok {
				numVin = len(vinsIf)
				for vinIdx, vinItem := range vinsIf {
					if vinMap, ok2 := vinItem.(map[string]interface{}); ok2 {
						// --- Key input style (most typical for modern Monero) ---
						if keyObj, ok3 := vinMap["key"].(map[string]interface{}); ok3 {
							amountin := int64(0)
							if a, ok31 := keyObj["amount"].(float64); ok31 {
								sumIn += int64(a)
								amountin = int64(a)
							}
							// collect offsets (may be absent)
							var offsets []uint64
							if offsIf, ok4 := keyObj["key_offsets"].([]interface{}); ok4 {
								offsets = make([]uint64, 0, len(offsIf))
								ringSize := len(offsIf)
								ringSizes = append(ringSizes, ringSize)
								for _, oi := range offsIf {
									switch v := oi.(type) {
									case float64:
										offsets = append(offsets, uint64(v))
									case string:
										if parsed, err := xmrhelper.ParseUint64FromString(v); err == nil {
											offsets = append(offsets, parsed)
										}
									}
								}
							}
							// convert to global indices if we have offsets (safe with empty slice)
							globalIdxs := xmrhelper.OffsetsToGlobalIndices(offsets)

							// insert ring members (if any)
							for _, gi := range globalIdxs {
								ringMembers = append(ringMembers, exptypes.XmrRingMemberInfo{
									InputIndex:        vinIdx,
									MemberGlobalIndex: int64(gi),
									MemberTxHash:      txhash,
								})
							}

							ringOuts := make([]xmrutil.OutputInfo, 0)
							if len(globalIdxs) > 0 {
								ringCtOuts, err := pgb.XmrClient.GetOuts(globalIdxs)
								if err != nil {
									return nil, err
								}
								for _, ctOut := range ringCtOuts.Outs {
									ringOuts = append(ringOuts, ctOut)
								}
							}
							// key image k_image (if present)
							if ki, ok5 := keyObj["k_image"].(string); ok5 && ki != "" {
								keyImages = append(keyImages, exptypes.XmrKeyImageInfo{
									KeyImage:    ki,
									SeenAtTx:    txhash,
									Spent:       true,
									RingMembers: globalIdxs,
									RingCtOuts:  ringOuts,
									AmountIn:    amountin,
								})
							}
						}
					}
				}
			}
			if voutsIf, ok := v["vout"].([]interface{}); ok {
				numVout = len(voutsIf)
				var outputIndices []int64
				if indicesIf, ok := v["output_indices"].([]interface{}); ok {
					outputIndices = make([]int64, 0, len(indicesIf))
					for _, idx := range indicesIf {
						switch v := idx.(type) {
						case float64:
							outputIndices = append(outputIndices, int64(v))
						case string:
							if parsed, err := xmrhelper.ParseInt64FromString(v); err == nil {
								outputIndices = append(outputIndices, parsed)
							}
						}
					}
				}
				for idx, vo := range voutsIf {
					if voMap, ok := vo.(map[string]interface{}); ok {
						// target may be under "target" -> "key"
						outPk := ""
						globalIndex := int64(-1)
						amount := int64(0)
						amountKnown := false
						if idx < len(outputIndices) {
							globalIndex = outputIndices[idx]
						}
						if target, ok2 := voMap["target"].(map[string]interface{}); ok2 {
							if taggedKey, ok3 := target["tagged_key"].(map[string]interface{}); ok3 {
								if k, ok4 := taggedKey["key"].(string); ok4 {
									outPk = k
								}
							} else if k, ok3 := target["key"].(string); ok3 {
								outPk = k
							}
							// some monero versions include "global_index" in vout
							if gi, ok4 := voMap["global_index"]; ok4 {
								switch v := gi.(type) {
								case float64:
									globalIndex = int64(v)
								case string:
									// sometimes it's string
									if parsed, err := xmrhelper.ParseInt64FromString(v); err == nil {
										globalIndex = parsed
									}
								}
							}
						}
						// amount may be present (non-ringct)
						if amt, ok := voMap["amount"]; ok {
							switch v := amt.(type) {
							case float64:
								amount = int64(v)
								amountKnown = true
							case string:
								if parsed, err := xmrhelper.ParseInt64FromString(v); err == nil {
									amount = parsed
									amountKnown = true
								}
							}
						}
						sumOut += amount
						txSent += amount
						outputs = append(outputs, exptypes.XmrOutputInfo{
							OutIndex:    idx,
							GlobalIndex: globalIndex,
							Key:         outPk,
							Amount:      amount,
							Owned:       amountKnown,
						})
					}
				}
			}
			if fees == 0 && !isRingCt && !isCoinbase {
				fees = sumIn - sumOut
			}
			// fees / outputs / total sent: sometimes present in tx JSON
			if !isCoinbase {
				var extraHex string
				if extraIf, ok := v["extra"].([]interface{}); ok {
					var extraBytes []byte
					for _, val := range extraIf {
						switch v := val.(type) {
						case float64:
							extraBytes = append(extraBytes, byte(v))
						case int:
							extraBytes = append(extraBytes, byte(v))
						default:
							log.Warnf("Unexpected type in extra array: %T", v)
							continue
						}
					}
					extraHex = hex.EncodeToString(extraBytes)
					parsedExtra, parseErr = ParseTxExtra(extraHex)
					if parseErr != nil {
						log.Errorf("Failed to parse extra: %v", parseErr)
						return nil, parseErr
					}
				} else if extraStr, ok := v["extra"].(string); ok {
					extraHex = extraStr
					parsedExtra, parseErr = ParseTxExtra(extraHex)
					if parseErr != nil {
						log.Errorf("Failed to parse extra: %v", parseErr)
						return nil, parseErr
					}
				} else {
					log.Errorf("Failed to parse extra: unexpected type %T", v["extra"])
					return nil, parseErr
				}
				if parsedExtra == nil {
					return nil, fmt.Errorf("parsed extra data failed")
				}
			} else {
				parsedExtra = &exptypes.XmrTxExtra{
					RawHex:      "",
					TxPublicKey: "",
				}
			}
			var rctPrunableHash string
			// sometimes prunable hash in txMap under "rct_signatures" or "rct_prunable_hash"
			if rp, ok := v["rct_prunable_hash"].(string); ok {
				rctPrunableHash = rp
			}
			var bulletproof bool
			var rangeProofs string
			if prun, ok := v["rctsig_prunable"].(map[string]interface{}); ok {
				if rp, ok := prun["rangeSigs"]; ok {
					rpJSON, _ := json.Marshal(rp)
					rangeProofs = string(rpJSON)
				} else if bp, ok := prun["bp"]; ok {
					bpJSON, _ := json.Marshal(bp)
					rangeProofs = string(bpJSON)
					bulletproof = true
				}
			}
			var txSignatures string
			if sigs, ok := v["signatures"]; ok {
				bs, _ := json.Marshal(sigs)
				txSignatures = string(bs)
			}
			rctData = &exptypes.XmrRctData{
				RctType:      rctType,
				PrunableHash: rctPrunableHash,
				Bulletproof:  bulletproof,
				RangeProofs:  rangeProofs,
				TxSignatures: txSignatures,
			}
		}
	}

	confirmations := txData.Confirmations
	isConfirmed := (isCoinbase && confirmations >= 60) || (!isCoinbase && confirmations >= 10)
	feePerKB := float64(0)
	if size > 0 {
		feePerKB = float64(fees) / (float64(size) / float64(1024.0))
	}
	ringCT := false
	if rctData != nil {
		ringCT = rctData.RctType >= 0
	}
	maxOuputIdx := uint64(0)
	for idex, _ := range outputs {
		if idex < len(txData.OutputIndices) {
			if txData.OutputIndices[idex] > uint64(maxOuputIdx) {
				maxOuputIdx = uint64(txData.OutputIndices[idex])
			}
			outputs[idex].GlobalIndex = int64(txData.OutputIndices[idex])
		}
	}
	rSize := utils.AvgOfArrayInt(ringSizes)
	return &exptypes.TxInfo{
		XmrTxBasic: &exptypes.XmrTxBasic{
			XmrFee:         uint64(fees),
			XmrFeeRate:     feePerKB,
			InputsCount:    uint64(numVin),
			OutputsCount:   uint64(numVout),
			RingSizes:      ringSizes,
			KeyImages:      keyImages,
			Outputs:        outputs,
			RingMembers:    ringMembers,
			Rct:            rctData,
			ExtraParsed:    parsedExtra,
			UnlockTime:     uint64(lockTime),
			Mixin:          rSize - 1,
			RingSize:       rSize,
			ExtraRaw:       parsedExtra.RawHex,
			RawHex:         txHex,
			RingCT:         ringCT,
			MaxGlobalIndex: maxOuputIdx,
			TxPublicKey:    parsedExtra.TxPublicKey,
			TotalSent:      utils.AtomicToXMR(uint64(txSent)),
		},
		BlockHash:   blockHash,
		BlockHeight: txData.BlockHeight,
		Time:        exptypes.NewTimeDefFromUNIX(int64(txData.BlockTimestamp)),
		TxBasic: &exptypes.TxBasic{
			TxID:          txhash,
			FormattedSize: humanize.Bytes(uint64(size)),
			Version:       int32(version),
			Coinbase:      isCoinbase,
			FeeCoin:       utils.AtomicToXMR(uint64(fees)),
		},
		Confirmed:     isConfirmed,
		Confirmations: int64(confirmations),
		InPool:        txData.InPool,
	}, nil
}

func (pgb *ChainDB) GetDaemonXMRExplorerBlock(height int64) *exptypes.BlockInfo {
	br, berr := pgb.XmrClient.GetBlock(uint64(height))
	if berr != nil {
		log.Errorf("XMR: GetBlock(%d) failed: %v", height, berr)
		return nil
	}
	bh, err := pgb.XmrClient.GetBlockHeaderByHeight(uint64(height))
	if err != nil {
		log.Errorf("XMR: GetBlockHeaderByHeight(%d) failed: %v", err, err)
		return nil
	}
	return pgb.GetXMRExplorerBlockWithBlockResult(br, bh)
}

// GetXMRExplorerBlock gets a *exptypes.Blockinfo for the specified ltc block.
func (pgb *ChainDB) GetXMRExplorerBlockWithBlockResult(br *xmrutil.BlockResult, bh *xmrutil.BlockHeader) *exptypes.BlockInfo {
	height := int64(bh.Height)
	pgb.xmrLastExplorerBlock.Lock()
	if pgb.xmrLastExplorerBlock.blockInfo != nil && pgb.xmrLastExplorerBlock.blockInfo.Height == int64(height) {
		res := pgb.xmrLastExplorerBlock.blockInfo
		pgb.xmrLastExplorerBlock.Unlock()
		return res
	}
	pgb.xmrLastExplorerBlock.Unlock()
	// Convert the wire.MsgBlock to a dbtypes.Block
	clientBlock, err := xmrhelper.MsgXMRBlockToDBBlock(pgb.XmrClient, br, uint64(height))
	if err != nil {
		log.Errorf("XMR: Get block data failed: Height: %d. Error: %v", height, err)
		return nil
	}

	blockData := &exptypes.BlockBasic{
		Height:         int64(clientBlock.Height),
		Hash:           clientBlock.Hash,
		Version:        int32(clientBlock.Version),
		Size:           int32(clientBlock.Size),
		Valid:          true, // we do not know this, TODO with DB v2
		MainChain:      true,
		Transactions:   int(clientBlock.NumTx),
		TxCount:        clientBlock.NumTx,
		BlockTime:      exptypes.NewTimeDefFromUNIX(clientBlock.Time.UNIX()),
		BlockTimeUnix:  clientBlock.Time.UNIX(),
		FormattedBytes: humanize.Bytes(uint64(clientBlock.Size)),
	}
	// b := makeLTCExplorerBlockBasicFromTxResult(data, pgb.ltcChainParams)
	// Explorer Block Info
	confirmations := int64(0)
	if pgb.XmrBestBlock.Height >= height {
		confirmations = pgb.XmrBestBlock.Height - height + 1
	}
	nextHash := ""
	if pgb.XmrBestBlock.Height > height {
		nextHeight := height + 1
		blheader, err := pgb.XmrClient.GetBlockHeaderByHeight(uint64(nextHeight))
		if err != nil {
			log.Errorf("XMR: GetBlockHeaderByHeight failed: Height: %d. %v", height, err)
			return nil
		}
		nextHash = blheader.Hash
	}
	block := &exptypes.BlockInfo{
		BlockBasic:    blockData,
		Confirmations: confirmations,
		// PoWHash:       b.Hash,
		Nonce:                uint32(clientBlock.Nonce),
		Difficulty:           clientBlock.Difficulty,
		DifficultyNum:        utils.IfaceToString(clientBlock.DifficultyNum),
		CumulativeDifficulty: utils.IfaceToString(clientBlock.CumulativeDifficulty),
		PreviousHash:         clientBlock.PreviousHash,
		NextHash:             nextHash,
		BlockReward:          int64(clientBlock.Reward),
	}

	txs := make([]*exptypes.XmrTxFull, 0)
	txids := make([]string, 0)
	// get transaction details
	blTxsData, blTxserr := pgb.XmrClient.GetTransactions(clientBlock.Tx, true)
	if blTxserr != nil {
		log.Errorf("XMR: GetTransactions for getting explorer block failed: %v", blTxserr)
		return nil
	}
	totalSent := int64(0)
	totalFees := int64(0)
	totalVinsNum := int64(0)
	totalOutputsNum := int64(0)
	totalRingSize := int64(0)
	for i, txHash := range clientBlock.Tx {
		isCoinbase := txHash == br.MinerTxHash
		var txJSONStr string
		if i < len(blTxsData.TxsAsJSON) {
			txJSONStr = blTxsData.TxsAsJSON[i]
		}
		var txHex string
		if i < len(blTxsData.TxsAsHex) {
			txHex = blTxsData.TxsAsHex[i]
		}
		var txExtra interface{}
		if txJSONStr != "" {
			// store parsed JSONB for easier queries
			var tmp map[string]interface{}
			if err := json.Unmarshal([]byte(txJSONStr), &tmp); err == nil {
				txExtra = tmp
			} else {
				// fallback: store raw string
				txExtra = txJSONStr
			}
		}
		timeField := int64(0)
		version := 0
		lockTime := int64(0)
		size := 0
		if txHex != "" {
			size = len(txHex) / 2
		}
		fees := int64(0)
		numVin := 0
		numVout := 0
		txSent := int64(0)
		var ringSizes []int
		var ringSize int
		var parsedExtra *exptypes.XmrTxExtra
		var hexExtra string
		outputs := make([]exptypes.XmrOutputInfo, 0)
		// vinsObjects := make([]exptypes.XmrVinInfo, 0)
		keyImages := make([]exptypes.XmrKeyImageInfo, 0)
		ringMembers := make([]exptypes.XmrRingMemberInfo, 0)
		var rctData *exptypes.XmrRctData
		// parse some common fields from txExtra (tx JSON)
		if txExtra != nil {
			switch v := txExtra.(type) {
			case map[string]interface{}:
				if t, ok := v["version"].(float64); ok {
					version = int(t)
				}
				if tm, ok := v["unlock_time"].(float64); ok {
					timeField = int64(tm)
				}
				// store rct blob as raw JSON of rct_signatures or hex if available.
				// rctJSON, _ := json.Marshal(rctIf)
				var rctType int = -1
				isRingCT := false
				var sumIn int64 = 0
				var sumOut int64 = 0
				if rct, ok := v["rct_signatures"].(map[string]interface{}); ok {
					isRingCT = true
					if t, ok2 := rct["type"].(float64); ok2 {
						rctType = int(t)
					}
					if feeIf, ok := rct["txnFee"].(float64); ok {
						fees = int64(feeIf)
					}
				}
				// vins/vouts length
				if vinsIf, ok := v["vin"].([]interface{}); ok {
					numVin = len(vinsIf)
					for vinIdx, vinItem := range vinsIf {
						if vinMap, ok2 := vinItem.(map[string]interface{}); ok2 {
							// --- Key input style (most typical for modern Monero) ---
							if keyObj, ok3 := vinMap["key"].(map[string]interface{}); ok3 {
								if a, ok31 := keyObj["amount"].(float64); ok31 {
									sumIn += int64(a)
								}
								// collect offsets (may be absent)
								var offsets []uint64
								if offsIf, ok4 := keyObj["key_offsets"].([]interface{}); ok4 {
									offsets = make([]uint64, 0, len(offsIf))
									ringSize := len(offsIf)
									ringSizes = append(ringSizes, ringSize)
									for _, oi := range offsIf {
										switch v := oi.(type) {
										case float64:
											offsets = append(offsets, uint64(v))
										case string:
											if parsed, err := xmrhelper.ParseUint64FromString(v); err == nil {
												offsets = append(offsets, parsed)
											}
										}
									}
								}
								// convert to global indices if we have offsets (safe with empty slice)
								globalIdxs := xmrhelper.OffsetsToGlobalIndices(offsets)
								totalRingSize += int64(len(globalIdxs))
								// insert ring members (if any)
								for _, gi := range globalIdxs {
									ringMembers = append(ringMembers, exptypes.XmrRingMemberInfo{
										InputIndex:        vinIdx,
										MemberGlobalIndex: int64(gi),
										MemberTxHash:      txHash,
									})
								}

								// key image k_image (if present)
								if ki, ok5 := keyObj["k_image"].(string); ok5 && ki != "" {
									keyImages = append(keyImages, exptypes.XmrKeyImageInfo{
										KeyImage: ki,
										SeenAtTx: txHash,
										Spent:    true,
									})
								}
							}
						}
					}
				}
				if voutsIf, ok := v["vout"].([]interface{}); ok {
					numVout = len(voutsIf)
					for idx, vo := range voutsIf {
						if voMap, ok := vo.(map[string]interface{}); ok {
							// target may be under "target" -> "key"
							outPk := ""
							globalIndex := int64(-1)
							amount := int64(0)
							amountKnown := false
							if target, ok2 := voMap["target"].(map[string]interface{}); ok2 {
								if k, ok3 := target["key"].(string); ok3 {
									outPk = k
								}
								// some monero versions include "global_index" in vout
								if gi, ok4 := voMap["global_index"]; ok4 {
									switch v := gi.(type) {
									case float64:
										globalIndex = int64(v)
									case string:
										// sometimes it's string
										if parsed, err := xmrhelper.ParseInt64FromString(v); err == nil {
											globalIndex = parsed
										}
									}
								}
							}
							// amount may be present (non-ringct)
							if amt, ok := voMap["amount"]; ok {
								switch v := amt.(type) {
								case float64:
									amount = int64(v)
									amountKnown = true
								case string:
									if parsed, err := xmrhelper.ParseInt64FromString(v); err == nil {
										amount = parsed
										amountKnown = true
									}
								}
							}
							sumOut += amount
							txSent += amount
							outputs = append(outputs, exptypes.XmrOutputInfo{
								OutIndex:    idx,
								GlobalIndex: globalIndex,
								Key:         outPk,
								Amount:      amount,
								Owned:       amountKnown,
							})
						}
					}
				}
				if fees == 0 && !isRingCT && !isCoinbase {
					fees = sumIn - sumOut
				}
				// fees / outputs / total sent: sometimes present in tx JSON
				if extraHex, ok := v["extra"].(string); ok {
					parsedExtra, _ = ParseTxExtra(extraHex)
					hexExtra = extraHex
				}
				var rctPrunableHash string
				// sometimes prunable hash in txMap under "rct_signatures" or "rct_prunable_hash"
				if rp, ok := v["rct_prunable_hash"].(string); ok {
					rctPrunableHash = rp
				}
				var bulletproof bool
				var rangeProofs string
				if prun, ok := v["rctsig_prunable"].(map[string]interface{}); ok {
					if rp, ok := prun["rangeSigs"]; ok {
						rpJSON, _ := json.Marshal(rp)
						rangeProofs = string(rpJSON)
					} else if bp, ok := prun["bp"]; ok {
						bpJSON, _ := json.Marshal(bp)
						rangeProofs = string(bpJSON)
						bulletproof = true
					}
				}
				var txSignatures string
				if sigs, ok := v["signatures"]; ok {
					bs, _ := json.Marshal(sigs)
					txSignatures = string(bs)
				}
				rctData = &exptypes.XmrRctData{
					RctType:      rctType,
					PrunableHash: rctPrunableHash,
					Bulletproof:  bulletproof,
					RangeProofs:  rangeProofs,
					TxSignatures: txSignatures,
				}
			}
		}
		isConfirmed := (isCoinbase && confirmations >= 60) || (!isCoinbase && confirmations >= 10)
		if !isCoinbase {
			totalSent += txSent
			totalVinsNum += int64(numVin)
			totalOutputsNum += int64(numVout)
		}
		feePerKB := float64(0)
		if size > 0 {
			feePerKB = float64(fees) / (float64(size) / float64(1024.0))
		}
		totalFees += fees
		txFull := &exptypes.XmrTxFull{
			TxHash:        txHash,
			BlockHeight:   int64(clientBlock.Height),
			Timestamp:     timeField,
			Size:          int64(size),
			Version:       int32(version),
			UnlockTime:    uint64(lockTime),
			IsCoinbase:    txHash == br.MinerTxHash,
			Confirmed:     isConfirmed,
			Fee:           fees,
			FeePerKB:      feePerKB,
			InputsCount:   numVin,
			OutputsCount:  numVout,
			TotalInputs:   0, // TODO: handler this
			TotalOutputs:  0, // TODO: handler this
			RingSizes:     ringSizes,
			Mixin:         ringSize - 1,
			ExtraParsed:   parsedExtra,
			ExtraRaw:      hexExtra,
			RawHex:        txHex,
			Outputs:       outputs,
			KeyImages:     keyImages,
			RingMembers:   ringMembers,
			Rct:           rctData,
			Sent:          txSent,
			FormattedSize: humanize.Bytes(uint64(size)),
		}
		txs = append(txs, txFull)
		txids = append(txids, txHash)
	}
	block.TotalSent = exptypes.AtomicToXMR(totalSent)
	block.Total = block.TotalSent
	block.XmrTx = txs
	block.Txids = txids
	block.Fees = totalFees
	block.TotalInputs = totalVinsNum
	block.TotalOutputs = totalOutputsNum
	block.TotalRingSize = totalRingSize
	sortTx := func(txs []*exptypes.XmrTxFull) {
		sort.Slice(txs, func(i, j int) bool {
			return txs[i].Sent > txs[j].Sent
		})
	}
	sortTx(block.XmrTx)

	pgb.xmrLastExplorerBlock.Lock()
	pgb.xmrLastExplorerBlock.hash = clientBlock.Hash
	pgb.xmrLastExplorerBlock.blockInfo = block
	pgb.xmrLastExplorerBlock.difficulties = make(map[int64]float64) // used by the Difficulty method
	pgb.xmrLastExplorerBlock.Unlock()
	return block
}

func ParseTxExtra(hexExtra string) (*exptypes.XmrTxExtra, error) {
	b, err := hex.DecodeString(hexExtra)
	if err != nil {
		return nil, err
	}
	te := &exptypes.XmrTxExtra{
		RawHex:        hexExtra,
		UnknownFields: make(map[byte][]byte),
	}

	i := 0
	n := len(b)
	for i < n {
		tag := b[i]
		i++
		switch tag {
		case 0x00: // padding: skip single byte (or consecutive padding bytes)
			// do nothing (padding may be many 0x00 bytes)
		case 0x01: // tx pubkey (32 bytes)
			if i+32 > n {
				return nil, errors.New("tx extra: pubkey truncated")
			}
			te.TxPublicKey = hex.EncodeToString(b[i : i+32])
			i += 32
		case 0x02: // extra nonce: next byte is length
			if i >= n {
				return nil, errors.New("tx extra: nonce length missing")
			}
			L := int(b[i])
			i++
			if i+L > n {
				return nil, errors.New("tx extra: nonce truncated")
			}
			nonce := b[i : i+L]
			te.ExtraNonce = nonce
			// parse inner nonce tags (simple parse: check first byte)
			if L > 0 {
				subtag := nonce[0]
				switch subtag {
				case 0x00: // long payment id (32 bytes) deprecated
					if len(nonce) >= 1+32 {
						te.PaymentID = hex.EncodeToString(nonce[1 : 1+32])
					}
				case 0x01: // encrypted short payment id (8 bytes)
					if len(nonce) >= 1+8 {
						te.EncryptedPaymentID = hex.EncodeToString(nonce[1 : 1+8])
					}
				default:
					// could be miner pool nonce etc — store raw
					te.UnknownFields[0x02] = nonce
				}
			}
			i += L
		case 0x04: // additional pubkeys: next byte = count
			if i >= n {
				return nil, errors.New("tx extra: additional pubkeys count missing")
			}
			cnt := int(b[i])
			i++
			needed := cnt * 32
			if i+needed > n {
				return nil, fmt.Errorf("tx extra: additional pubkeys truncated (need %d bytes)", needed)
			}
			arr := make([]string, 0, cnt)
			for k := 0; k < cnt; k++ {
				arr = append(arr, hex.EncodeToString(b[i:i+32]))
				i += 32
			}
			te.AdditionalPubkeys = arr
		case 0x03: // merge-mining tag (implementation-dependent)
			// simple approach: store raw — real parsing needs format knowledge
			// read next byte len? (core code uses structured tag)
			te.UnknownFields[0x03] = nil // placeholder
		default:
			// unknown tag: it's safer to try to skip if next byte indicates length (but not all tags follow same rule)
			// store single-byte unknown for visibility
			te.UnknownFields[tag] = nil
		}
	}

	return te, nil
}

func (pgb *ChainDB) GetXMRSummaryInfo() (*exptypes.MoneroSimpleSummaryInfo, error) {
	// get total outputs count
	outputsCount, err := retrieveXMROutputsCount(pgb.ctx, pgb.db)
	if err != nil {
		return nil, err
	}
	// get useRingctRate
	useRingctRate, err := retrieveXMRUseRingCtRate(pgb.ctx, pgb.db)
	if err != nil {
		return nil, err
	}
	// get total inputs count
	inputsCount, err := retrieveXMRInputsCount(pgb.ctx, pgb.db)
	if err != nil {
		return nil, err
	}
	// get total ring members count
	ringMembersCount, err := retrieveXMRRingMembersCount(pgb.ctx, pgb.db)
	if err != nil {
		return nil, err
	}
	// get fees/block with last 1000 block
	last1000BlocksFeeAvg, err := retrieveXMRAvgFeeWith1000Blocks(pgb.ctx, pgb.db)
	if err != nil {
		return nil, err
	}
	return &exptypes.MoneroSimpleSummaryInfo{
		TotalOutputs:     outputsCount,
		TotalInputs:      inputsCount,
		UseRingCtRate:    useRingctRate,
		TotalRingMembers: ringMembersCount,
		AvgFeePerBlock:   last1000BlocksFeeAvg,
	}, nil
}

func (pgb *ChainDB) GetXMRBestBlock() error {
	lastBlockHeader, err := pgb.XmrClient.GetLastBlockHeader()
	if err != nil {
		return fmt.Errorf("Unable to get block from xmr daemon: %v", err)
	}
	//create bestblock object
	bestBlock := &MutilchainBestBlock{
		Height: int64(lastBlockHeader.Height),
		Hash:   lastBlockHeader.Hash,
		Time:   int64(lastBlockHeader.Timestamp),
	}
	pgb.XmrBestBlock = bestBlock
	return nil
}
