// Copyright (c) 2019-2021, The Decred developers
// See LICENSE for details.

package internal

const (
	CreateBlackListTable = `
		CREATE TABLE IF NOT EXISTS black_list (
		ip TEXT,
		note TEXT,
		PRIMARY KEY (ip)
	);`

	UpsertIPRangeBlackList = `
		INSERT INTO black_list (ip, note)
		VALUES ($1, $2);`

	CheckIPRangeExistOnBlackList = `SELECT EXISTS (SELECT 1 FROM black_list WHERE ip = $1);`
)
