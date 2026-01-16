package mutilchainquery

import (
	"fmt"

	"github.com/decred/dcrdata/v8/mutilchain"
)

const (
	CreateMultichainAddressTable = `CREATE TABLE IF NOT EXISTS %saddresses (
		id SERIAL8 PRIMARY KEY,
		address TEXT,
		funding_tx_row_id INT8,
		funding_tx_hash TEXT,
		funding_tx_vout_index INT8,
		vout_row_id INT8,
		value INT8,
		spending_tx_row_id INT8,
		spending_tx_hash TEXT,
		spending_tx_vin_index INT4,
		vin_row_id INT8,
		out_pk TEXT NULL,
		global_index BIGINT NULL,
		amount_commitment BYTEA NULL,
		amount_known BOOLEAN DEFAULT FALSE,
		amount BIGINT NULL,
		key_image TEXT NULL,
		first_seen_block_height BIGINT NULL,
		account_index INT4 NULL,
		address_index INT4 NULL,
		is_subaddress BOOLEAN DEFAULT FALSE,
		tx_pub_key TEXT NULL,
		payment_id TEXT NULL,
		last_updated TIMESTAMPTZ DEFAULT now()
	);`

	CreateXmrAddressTable = `CREATE TABLE IF NOT EXISTS xmraddresses (
		id SERIAL8 PRIMARY KEY,
		address TEXT,
		funding_tx_row_id INT8,
		funding_tx_hash TEXT,
		funding_tx_vout_index INT8,
		vout_row_id INT8,
		value INT8,
		spending_tx_row_id INT8,
		spending_tx_hash TEXT,
		spending_tx_vin_index INT4,
		vin_row_id INT8,
		out_pk TEXT NULL,
		global_index BIGINT NULL,
		amount_commitment BYTEA NULL,
		amount_known BOOLEAN DEFAULT FALSE,
		amount BIGINT NULL,
		key_image TEXT NULL,
		first_seen_block_height BIGINT NULL,
		account_index INT4 NULL,
		address_index INT4 NULL,
		is_subaddress BOOLEAN DEFAULT FALSE,
		tx_pub_key TEXT NULL,
		payment_id TEXT NULL,
		last_updated TIMESTAMPTZ DEFAULT now()
	);`

	insertAddressRow0 = `INSERT INTO %saddresses (address, funding_tx_row_id,
		funding_tx_hash, funding_tx_vout_index, vout_row_id, value)
		VALUES ($1, $2, $3, $4, $5, $6) `
	InsertAddressRow        = insertAddressRow0 + `RETURNING id;`
	InsertAddressRowChecked = insertAddressRow0 +
		`ON CONFLICT (address, vout_row_id) DO NOTHING RETURNING id;`

	InsertXmrAddressRow = `INSERT INTO xmraddresses
    (address,funding_tx_row_id,funding_tx_hash,funding_tx_vout_index,vout_row_id,value,spending_tx_row_id,
     spending_tx_hash,spending_tx_vin_index,vin_row_id,out_pk,global_index,amount_commitment,amount_known,
     amount,first_seen_block_height,account_index,address_index,is_subaddress,tx_pub_key,payment_id,last_updated)
	VALUES(
	NULL,
	NULL,
     $5,   -- funding_tx_hash
     $6,   -- funding_tx_vout_index
     $7,   -- vout_row_id
     $4,   -- value
     NULL,
     NULL,
     NULL,
     NULL,
     $1,   -- out_pk
     $2,   -- global_index
     NULL,
     $3,   -- amount_known
     $4,   -- amount
     $9,   -- first_seen_block_height
     NULL,
     NULL,
     FALSE,
     $8,   -- tx_pub_key
     NULL,
     now())
RETURNING id;`

	UpsertXmrAddressRow = `WITH upd AS (
  UPDATE xmraddresses
  SET
    out_pk = COALESCE($1, out_pk),
    global_index = COALESCE($2, global_index),
    amount_known = (amount_known OR $3),
    amount = CASE WHEN amount_known THEN amount ELSE COALESCE($4, amount) END,
    funding_tx_hash = COALESCE(funding_tx_hash, $5),
    funding_tx_vout_index = COALESCE(funding_tx_vout_index, $6),
    vout_row_id = COALESCE($7, vout_row_id),
    tx_pub_key = COALESCE($8, tx_pub_key),
    first_seen_block_height = COALESCE(first_seen_block_height, $9),
    last_updated = now()
  WHERE
    (vout_row_id IS NOT NULL AND vout_row_id = $7)
    OR (vout_row_id IS NULL AND funding_tx_hash = $5 AND funding_tx_vout_index = $6)
  RETURNING id
),
ins AS (
  INSERT INTO xmraddresses
    (address, funding_tx_row_id, funding_tx_hash, funding_tx_vout_index, vout_row_id, value,
     spending_tx_row_id, spending_tx_hash, spending_tx_vin_index, vin_row_id,
     out_pk, global_index, amount_commitment, amount_known, amount,
     first_seen_block_height, account_index, address_index, is_subaddress, tx_pub_key, payment_id, last_updated)
  SELECT
    NULL, NULL, $5, $6, $7, $4,
    NULL, NULL, NULL, NULL,
    $1, $2, NULL, $3, $4,
    $9, NULL, NULL, FALSE, $8, NULL, now()
  WHERE NOT EXISTS (SELECT 1 FROM upd)
  RETURNING id
)
SELECT id FROM upd
UNION ALL
SELECT id FROM ins
LIMIT 1;`

	InsertAddressRowReturnID = `WITH inserting AS (` +
		insertAddressRow0 +
		`ON CONFLICT (address, vout_row_id) DO UPDATE
		SET address = NULL WHERE FALSE
		RETURNING id
		)
	 SELECT id FROM inserting
	 UNION  ALL
	 SELECT id FROM %saddresses
	 WHERE  address = $1 AND vout_row_id = $5
	 LIMIT  1;`

	insertAddressRowFull = `INSERT INTO %saddresses (address, funding_tx_row_id, funding_tx_hash,
		funding_tx_vout_index, vout_row_id, value, spending_tx_row_id, 
		spending_tx_hash, spending_tx_vin_index, vin_row_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) `

	// SelectSpendingTxsByPrevTx = `SELECT id, tx_hash, tx_index, prev_tx_index FROM vins WHERE prev_tx_hash=$1;`
	// SelectSpendingTxByPrevOut = `SELECT id, tx_hash, tx_index FROM vins WHERE prev_tx_hash=$1 AND prev_tx_index=$2;`
	// SelectFundingTxsByTx      = `SELECT id, prev_tx_hash FROM vins WHERE tx_hash=$1;`
	// SelectFundingTxByTxIn     = `SELECT id, prev_tx_hash FROM vins WHERE tx_hash=$1 AND tx_index=$2;`
	SelectCountTotalAddress           = `SELECT count(*) FROM (SELECT DISTINCT address FROM %saddresses) AS count`
	SelectAddressAllByAddress         = `SELECT * FROM %saddresses WHERE address=$1 order by id desc;`
	SelectAddressRecvCount            = `SELECT COUNT(*) FROM %saddresses WHERE address=$1;`
	SelectAddressUnspentCountAndValue = `SELECT COUNT(*), SUM(value) FROM %saddresses WHERE address=$1 and spending_tx_row_id IS NULL;`
	SelectAddressSpentCountAndValue   = `SELECT COUNT(*), SUM(value) FROM %saddresses WHERE address=$1 and spending_tx_row_id IS NOT NULL;`

	addrsColumnNames                   = `id, address, funding_tx_row_id, funding_tx_hash, funding_tx_vout_index, vout_row_id, value, spending_tx_row_id, spending_tx_hash, spending_tx_vin_index, vin_row_id`
	SelectAddressLimitNByAddress       = `SELECT ` + addrsColumnNames + ` FROM %saddresses WHERE address=$1 order by id desc limit $2 offset $3;`
	SelectAddressLimitNByAddressSubQry = `WITH these as (SELECT * FROM %saddresses WHERE address=$1)
		SELECT * FROM these order by id desc limit $2 offset $3;`
	SelectAddressIDsByFundingOutpoint = `SELECT id, address FROM %saddresses
		WHERE funding_tx_hash=$1 and funding_tx_vout_index=$2;`
	SelectAddressIDByVoutIDAddress = `SELECT id FROM %saddresses
		WHERE address=$1 and vout_row_id=$2;`

	SetAddressSpendingForID = `UPDATE %saddresses SET spending_tx_row_id = $2, 
		spending_tx_hash = $3, spending_tx_vin_index = $4, vin_row_id = $5 
		WHERE id=$1;`
	SetAddressSpendingForOutpoint = `UPDATE %saddresses SET spending_tx_row_id = $3, 
		spending_tx_hash = $4, spending_tx_vin_index = $5, vin_row_id = $6 
		WHERE funding_tx_hash=$1 and funding_tx_vout_index=$2;`

	// for normal multichain
	IndexAddressTableOnAddrVoutRowId = `CREATE UNIQUE INDEX uix_%saddresses_addr_vout_row_id
		ON %saddresses(address, vout_row_id);`
	DeindexAddressTableOnAddrVoutRowId = `DROP INDEX uix_%saddresses_addr_vout_row_id;`

	IndexAddressTableOnAddress = `CREATE INDEX uix_%saddresses_address
		ON %saddresses(address);`
	DeindexAddressTableOnAddress = `DROP INDEX uix_%saddresses_address;`

	IndexAddressTableOnVoutID = `CREATE INDEX uix_%saddresses_vout_id
		ON %saddresses(vout_row_id);`
	DeindexAddressTableOnVoutID = `DROP INDEX uix_%saddresses_vout_id;`

	IndexAddressTableOnFundingTx = `CREATE INDEX uix_%saddresses_funding_tx
		ON %saddresses(funding_tx_hash, funding_tx_vout_index);`
	DeindexAddressTableOnFundingTx = `DROP INDEX uix_%saddresses_funding_tx;`

	// for xmraddresses
	IndexXmrAddressTableOnGlobalIndex   = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_global_index ON xmraddresses (global_index);`
	IndexXmrAddressTableOnKeyImage      = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_key_image ON xmraddresses (key_image);`
	IndexXmrAddressTableOnAccIdxAddrIdx = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_address_idx ON xmraddresses (account_index, address_index);`

	IndexXmrAddressTableOnOutPk       = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_out_pk ON xmraddresses(out_pk);`
	IndexXmrAddressTableOnAmountKnown = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_amount_known ON xmraddresses(amount_known);`
	IndexXmrAddressTableOnVoutRowId   = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_vout_row_id ON xmraddresses(vout_row_id);`
	IndexXmrAddressTableOnFundingInfo = `CREATE INDEX IF NOT EXISTS idx_xmraddresses_funding_tx ON xmraddresses(funding_tx_hash, funding_tx_vout_index);`

	DeindexXmrAddressTableOnGlobalIndex   = `DROP INDEX idx_xmraddresses_global_index;`
	DeindexXmrAddressTableOnKeyImage      = `DROP INDEX idx_xmraddresses_key_image;`
	DeindexXmrAddressTableOnAccIdxAddrIdx = `DROP INDEX idx_xmraddresses_address_idx;`

	DeindexXmrAddressTableOnOutPk       = `DROP INDEX idx_xmraddresses_out_pk;`
	DeindexXmrAddressTableOnAmountKnown = `DROP INDEX idx_xmraddresses_amount_known;`
	DeindexXmrAddressTableOnVoutRowId   = `DROP INDEX idx_xmraddresses_vout_row_id;`
	DeindexXmrAddressTableOnFundingInfo = `DROP INDEX idx_xmraddresses_funding_tx;`
	CheckAndRemoveDuplicateAddressRows  = `WITH duplicates AS (
  		SELECT id, row_number() OVER (PARTITION BY address, vout_row_id ORDER BY id) AS rn
  		FROM public.%saddresses
  		WHERE address IS NOT NULL AND vout_row_id IS NOT NULL)
		DELETE FROM public.%saddresses a
		USING duplicates d
		WHERE a.id = d.id
  		AND d.rn > 1;`
)

func MakeSelectCountTotalAddress(chainType string) string {
	return fmt.Sprintf(SelectCountTotalAddress, chainType)
}

func MakeSelectAddressUnspentCountAndValue(chainType string) string {
	return fmt.Sprintf(SelectAddressUnspentCountAndValue, chainType)
}

func MakeSelectAddressSpentCountAndValue(chainType string) string {
	return fmt.Sprintf(SelectAddressSpentCountAndValue, chainType)
}

func MakeSelectAddressLimitNByAddress(chainType string) string {
	return fmt.Sprintf(SelectAddressLimitNByAddress, chainType)
}

func IndexAddressTableOnFundingTxStmt(chainType string) string {
	return fmt.Sprintf(IndexAddressTableOnFundingTx, chainType, chainType)
}

func DeindexAddressTableOnFundingTxStmt(chainType string) string {
	return fmt.Sprintf(DeindexAddressTableOnFundingTx, chainType)
}

func IndexAddressTableOnAddrVoutRowIdStmt(chainType string) string {
	return fmt.Sprintf(IndexAddressTableOnAddrVoutRowId, chainType, chainType)
}

func DeindexAddressTableOnAddrVoutRowIdStmt(chainType string) string {
	return fmt.Sprintf(DeindexAddressTableOnAddrVoutRowId, chainType)
}

func IndexAddressTableOnAddressStmt(chainType string) string {
	return fmt.Sprintf(IndexAddressTableOnAddress, chainType, chainType)
}

func DeindexAddressTableOnAddressStmt(chainType string) string {
	return fmt.Sprintf(DeindexAddressTableOnAddress, chainType)
}

func IndexAddressTableOnVoutIDStmt(chainType string) string {
	return fmt.Sprintf(IndexAddressTableOnVoutID, chainType, chainType)
}

func DeindexAddressTableOnVoutIDStmt(chainType string) string {
	return fmt.Sprintf(DeindexAddressTableOnVoutID, chainType)
}

func CreateAddressTableFunc(chainType string) string {
	if chainType == mutilchain.TYPEXMR {
		return CreateXmrAddressTable
	} else {
		return fmt.Sprintf(CreateMultichainAddressTable, chainType)
	}
}

func InsertAddressRowFunc(chainType string) string {
	return fmt.Sprintf(InsertAddressRow, chainType)
}

func MakeAddressRowInsertStatement(chainType string, checked bool) string {
	if !checked {
		return fmt.Sprintf(InsertAddressRow, chainType)
	}
	return fmt.Sprintf(InsertAddressRowChecked, chainType)
}

func SetAddressSpendingForOutpointFunc(chainType string) string {
	return fmt.Sprintf(SetAddressSpendingForOutpoint, chainType)
}

func MakeInsertXmrAddressRowQuery(checked bool) string {
	if checked {
		return UpsertXmrAddressRow
	} else {
		return InsertXmrAddressRow
	}
}

func CreateCheckAndRemoveDuplicateAddressRowsQuery(chainType string) string {
	return fmt.Sprintf(CheckAndRemoveDuplicateAddressRows, chainType, chainType)
}
