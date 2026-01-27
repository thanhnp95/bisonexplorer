// Copyright (c) 2018-2021, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package dcrpg

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/decred/dcrdata/db/dcrpg/v8/internal"
	"github.com/decred/dcrdata/db/dcrpg/v8/internal/mutilchainquery"
	"github.com/decred/dcrdata/v8/db/cache"
	"github.com/decred/dcrdata/v8/db/dbtypes"
	"github.com/decred/dcrdata/v8/mutilchain"
	"github.com/decred/dcrdata/v8/utils"
)

func retrieveXMROutputsCount(ctx context.Context, db *sql.DB) (outputsCount int64, err error) {
	err = db.QueryRowContext(ctx, mutilchainquery.SelectTotalXmrOutputs).Scan(&outputsCount)
	return
}

func retrieveXMRUseRingCtRate(ctx context.Context, db *sql.DB) (rate float64, err error) {
	err = db.QueryRowContext(ctx, mutilchainquery.SelectXmrUseRingCtTxsRate).Scan(&rate)
	return
}

func retrieveXMRInputsCount(ctx context.Context, db *sql.DB) (inputsCount int64, err error) {
	err = db.QueryRowContext(ctx, mutilchainquery.SelectTotalXmrInputs).Scan(&inputsCount)
	return
}

func retrieveXMRRingMembersCount(ctx context.Context, db *sql.DB) (ringMemberCount int64, err error) {
	err = db.QueryRowContext(ctx, mutilchainquery.SelectTotalXmrRingMembers).Scan(&ringMemberCount)
	return
}

func retrieveXMRAvgFeeWith1000Blocks(ctx context.Context, db *sql.DB) (avgFees int64, err error) {
	err = db.QueryRowContext(ctx, mutilchainquery.SelectAvgFeesLast100Blocks).Scan(&avgFees)
	return
}

func retrieveXMR24hMetricsData(ctx context.Context, db *sql.DB) (*dbtypes.Block24hInfo, error) {
	res := dbtypes.Block24hInfo{}
	err := db.QueryRowContext(ctx, internal.Select24hMetricsSummary, mutilchain.TYPEXMR).Scan(&res.Blocks, &res.Spent24h, &res.Sent24h, &res.Fees24h, &res.NumTx24h, &res.NumVin24h, &res.NumVout24h, &res.TotalPowReward)
	return &res, err
}

func retrieveXmrMutilchainChartBlocks(ctx context.Context, db *sql.DB, charts *cache.MutilchainChartData) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, mutilchainquery.SelectXmrBlockAllStats, charts.Height())
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func retrieveXmrMutilchainChartRingMembers(ctx context.Context, db *sql.DB, charts *cache.MutilchainChartData) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, mutilchainquery.SelectRingMemberSummary, charts.RingMembers())
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func appendXmrChartRingMembers(charts *cache.MutilchainChartData, rows *sql.Rows) error {
	defer closeRows(rows)
	var height, ringSizeSum, ringSizeAvg uint64
	blocks := charts.Blocks
	for rows.Next() {
		err := rows.Scan(&height, &ringSizeSum, &ringSizeAvg)
		if err != nil {
			return err
		}
		blocks.TotalRingSize = append(blocks.TotalRingSize, ringSizeSum)
		blocks.AverageRingSize = append(blocks.AverageRingSize, ringSizeAvg)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("appendXmrChartRingMembers: iteration error: %w", err)
	}
	return nil
}

func appendXmrChartBlocks(charts *cache.MutilchainChartData, rows *sql.Rows) error {
	defer closeRows(rows)
	var timeInt uint64
	var count, size, height, reward, fees uint64
	var ringSize, avgRingSize, feePerKb, avgTxSize uint64
	var decoy03, decoy47, decoy811, decoy1214, decoygt15 float64
	var difficult float64
	var rowCount int32
	blocks := charts.Blocks
	for rows.Next() {
		rowCount++
		// Get the chainwork.
		err := rows.Scan(&height, &size, &timeInt, &count, &difficult, &fees, &reward, &ringSize, &avgRingSize,
			&feePerKb, &avgTxSize, &decoy03, &decoy47, &decoy811, &decoy1214, &decoygt15)
		if err != nil {
			return err
		}
		if timeInt == 0 {
			continue
		}
		blocks.Height = append(blocks.Height, height)
		blocks.BlockSize = append(blocks.BlockSize, size)
		blocks.TxCount = append(blocks.TxCount, count)
		blocks.TxPerBlock = append(blocks.TxPerBlock, count)
		blocks.Time = append(blocks.Time, timeInt)
		blocks.Difficulty = append(blocks.Difficulty, difficult)
		hashrate := float64(0)
		if charts.TimePerBlocks > 0 {
			hashrate = difficult / charts.TimePerBlocks
		}
		blocks.Hashrate = append(blocks.Hashrate, hashrate)
		blocks.Fees = append(blocks.Fees, fees)
		rewardFloat := utils.AtomicToXMR(reward)
		blocks.Reward = append(blocks.Reward, rewardFloat)
		blocks.TotalRingSize = append(blocks.TotalRingSize, ringSize)
		blocks.AverageRingSize = append(blocks.AverageRingSize, avgRingSize)
		blocks.FeeRate = append(blocks.FeeRate, feePerKb)
		blocks.AverageTxSize = append(blocks.AverageTxSize, avgTxSize)
		noTx := float64(0)
		if decoy03 == 0 && decoy47 == 0 && decoy811 == 0 && decoy1214 == 0 && decoygt15 == 0 {
			noTx = 100
		}
		blocks.MoneroDecoyBands = append(blocks.MoneroDecoyBands, &dbtypes.MoneroDecoyData{
			NoTx:      noTx,
			Decoy03:   decoy03,
			Decoy47:   decoy47,
			Decoy811:  decoy811,
			Decoy1214: decoy1214,
			DecoyGt15: decoygt15,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("appendChartBlocks: iteration error: %w", err)
	}
	return nil
}
