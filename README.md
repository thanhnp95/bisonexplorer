# Bison Explorer

## Installation and Launch

Bison Explorer is a blockchains explorer that supports multiple chains. In this version of Bison Explorer v1.0.0, we support Decred, Bitcoin and Litecoin
Bison Explorer is forked from [dcrdata](https://github.com/decred/dcrdata) so the basic environment settings can be found in the README of dcrdata
[dcrdata README](https://github.com/decred/dcrdata/blob/master/README.md)

In addition, the following settings need to be added to Bison Explorer:
### Settings on config
- Specify the type of blockchain to be disabled with: disabledchain
- Set up the environment btcd and ltcd if bitcoin and litecoin are enabled
- For some reasons, Binance is restricted in some countries. We provide binance-api option to set up a private server to get rate from Binance in case the current server location does not support Binance
Use [Tempo Rate](https://github.com/chaineco/TempoRate)
- Set up OKlink API key
### Install btcd and ltcd
- Launch btcd and ltcd to support Bitcoin and Litecoin in addition to Decred
[btcd releases](https://github.com/btcsuite/btcd/releases)
[ltcd releases](https://github.com/ltcsuite/ltcd/releases)

## Financial Reports
- Go to /finance-report to view financial reports on Treasury spending and estimated spending for Proposals
- Bison Explorer supports statistics of proposals containing meta data. This will include proposals approved since September 2021

## Redefine index and sitemap
- Copy the sample-sitemap.xml file in the cmd/dcrdata folder to the cmd/dcrdata/public folder. Rename it to sitemap.xml. Then set it to replace with the site's host name
- Copy the sample-robots.txt file in the cmd/dcrdata folder to the cmd/dcrdata/public folder. Rename it to robots.txt. Then set it to replace with the site's host name

## API

Currently, we provide APIs for the following blockchains: Decred, Bitcoin, Litecoin, and Monero.

All endpoints use the `/api` prefix. Optional query parameter `indent=true` can be added to any endpoint for formatted JSON output.

---

### Decred (Native APIs)

The API documentation can be found at: [dcrdata API](https://github.com/decred/dcrdata/blob/master/README.md#dcrdata-api)

#### Status & Supply

| Endpoint | Description |
| --- | --- |
| `/api/status` | Returns current server status including node height, node connections, API version, app version, and network name. |
| `/api/status/happy` | Returns server health check status. Returns HTTP 503 if unhealthy. Output: `{ "happy": true/false, ... }` |
| `/api/supply` | Returns complete coin supply information including total mined, circulating supply, and treasury balance. |
| `/api/supply/circulating` | Returns circulating supply as integer (atoms). Query param: `dcr=true` returns value in DCR (decimal). |

#### Block

| Endpoint | Description |
| --- | --- |
| `/api/block/avg-block-time` | Returns the average time between blocks based on historical data. |
| `/api/block/best` | Returns summary of the best (latest) block. Query param: `txtotals=true` includes transaction totals. Output includes height, hash, time, transactions summary. |
| `/api/block/best/height` | Returns current block height as plain text integer. |
| `/api/block/best/hash` | Returns best block hash as plain text string. |
| `/api/block/best/header` | Returns verbose block header JSON for the best block including version, merkle root, timestamp, bits, nonce, etc. |
| `/api/block/best/header/raw` | Returns raw serialized block header as hex string with height and hash. |
| `/api/block/best/raw` | Returns raw serialized block data as hex string. |
| `/api/block/best/size` | Returns best block size in bytes. |
| `/api/block/best/subsidy` | Returns block subsidies breakdown: PoW reward, PoS reward per vote, treasury subsidy, total, and number of votes. |
| `/api/block/best/verbose` | Returns comprehensive block data from dcrd including all transaction details. |
| `/api/block/best/pos` | Returns extended Proof-of-Stake information: ticket pool info, stake difficulty, vote counts, ticket price. |
| `/api/block/best/tx` | Returns all transaction IDs in the best block, separated by regular (Tx) and stake (STx) transactions. |
| `/api/block/best/tx/count` | Returns count of transactions: `{ "tx": n, "stx": m }` |
| `/api/block/hash/{blockhash}` | Returns block summary by block hash. Query param: `txtotals=true` for transaction totals. |
| `/api/block/hash/{blockhash}/height` | Returns block height for given block hash. |
| `/api/block/hash/{blockhash}/header` | Returns verbose block header JSON by hash. |
| `/api/block/hash/{blockhash}/header/raw` | Returns raw block header hex by hash. |
| `/api/block/hash/{blockhash}/raw` | Returns raw serialized block hex by hash. |
| `/api/block/hash/{blockhash}/size` | Returns block size in bytes by hash. |
| `/api/block/hash/{blockhash}/subsidy` | Returns block subsidies by hash. |
| `/api/block/hash/{blockhash}/verbose` | Returns verbose block data by hash. |
| `/api/block/hash/{blockhash}/pos` | Returns PoS stake info by hash. |
| `/api/block/hash/{blockhash}/tx` | Returns transaction IDs by block hash. |
| `/api/block/hash/{blockhash}/tx/count` | Returns transaction counts by block hash. |
| `/api/block/{idx}` | Returns block summary by height index. `{idx}` is the block height (0-indexed). |
| `/api/block/{idx}/header` | Returns block header by height. |
| `/api/block/{idx}/header/raw` | Returns raw block header hex by height. |
| `/api/block/{idx}/hash` | Returns block hash for given height. |
| `/api/block/{idx}/raw` | Returns raw block data by height. |
| `/api/block/{idx}/size` | Returns block size by height. |
| `/api/block/{idx}/subsidy` | Returns block subsidies by height. |
| `/api/block/{idx}/verbose` | Returns verbose block data by height. |
| `/api/block/{idx}/pos` | Returns PoS stake info by height. |
| `/api/block/{idx}/tx` | Returns transaction IDs by height. |
| `/api/block/{idx}/tx/count` | Returns transaction counts by height. |
| `/api/block/range/{idx0}/{idx}` | Returns array of block summaries from height `{idx0}` to `{idx}`. Maximum 1000 blocks per request. |
| `/api/block/range/{idx0}/{idx}/size` | Returns array of block sizes for the range. |
| `/api/block/range/{idx0}/{idx}/{step}` | Returns block summaries at intervals of `{step}` blocks within the range. |
| `/api/block/range/{idx0}/{idx}/{step}/size` | Returns block sizes at stepped intervals. |

#### Stake

| Endpoint | Description |
| --- | --- |
| `/api/stake/vote/info` | Returns vote version info including agendas and their voting status. Query param: `version=N` for specific version (defaults to latest). |
| `/api/stake/pool` | Returns current ticket pool info: size, value, average price, percentage of target. |
| `/api/stake/pool/full` | Returns full ticket pool (list of all ticket hashes). Query param: `sort=true` to sort alphabetically. |
| `/api/stake/pool/b/{idx}` | Returns ticket pool info at block height `{idx}`. |
| `/api/stake/pool/b/{idxorhash}/full` | Returns full ticket pool at block (by height or hash). |
| `/api/stake/pool/r/{idx0}/{idx}` | Returns ticket pool info for block range. Query param: `arrays=true` returns parallel arrays of values and sizes. |
| `/api/stake/diff` | Returns stake difficulty summary with current, next, and estimation data. |
| `/api/stake/diff/current` | Returns current and next stake difficulty only. |
| `/api/stake/diff/estimates` | Returns stake difficulty estimates with expected high, low, and average. |
| `/api/stake/diff/b/{idx}` | Returns stake difficulty at block height as array `[sdiff]`. |
| `/api/stake/diff/r/{idx0}/{idx}` | Returns array of stake difficulties for block range. |
| `/api/stake/powerless` | Returns missed and expired tickets sorted by revocation status. Shows tickets that failed to vote. |

#### Transaction

| Endpoint | Description |
| --- | --- |
| `/api/tx/{txid}` | Returns full transaction details by txid hash. Query param: `spends=true` includes spending info for each output. Output includes inputs, outputs, block info, fees. |
| `/api/tx/{txid}/trimmed` | Returns trimmed/decoded transaction with essential fields. Query param: `spends=true` for spending info. |
| `/api/tx/{txid}/out` | Returns all transaction outputs (vouts) as array. |
| `/api/tx/{txid}/out/{txinoutindex}` | Returns specific output by index. `{txinoutindex}` is 0-based output index. |
| `/api/tx/{txid}/in` | Returns all transaction inputs (vins) as array. |
| `/api/tx/{txid}/in/{txinoutindex}` | Returns specific input by index. |
| `/api/tx/{txid}/vinfo` | Returns vote info if transaction is a vote. Includes agenda votes, vote version, block voted for. |
| `/api/tx/{txid}/tinfo` | Returns ticket info if transaction is a ticket purchase. Includes maturity height, expiration, spend status. |
| `/api/tx/hex/{txid}` | Returns raw transaction hex string. |
| `/api/tx/decoded/{txid}` | Same as `/api/tx/{txid}/trimmed`. |
| `/api/tx/swaps/{txid}` | Returns atomic swap info if transaction contains swap contracts, redemptions, or refunds. |
| `/api/txs` | **POST**: Accepts JSON array of txids in request body. Returns array of full transaction objects. Query param: `spends=true` for outputs spending info. |
| `/api/txs/trimmed` | **POST**: Accepts JSON array of txids. Returns array of trimmed transactions. |

#### Address

| Endpoint | Description |
| --- | --- |
| `/api/address/{address}` | Returns address transaction history. Default 10 transactions. Query params: see count/skip endpoints below. |
| `/api/address/{address}/exists` | Checks if address(es) exist on chain. Supports up to 64 comma-separated addresses. Returns array of booleans. |
| `/api/address/{address}/totals` | Returns address totals: total received, total sent, and unspent balance in atoms. |
| `/api/address/{address}/types/{chartgrouping}` | Returns address transaction types chart data grouped by time. `{chartgrouping}`: `day`, `week`, `month`, `year`. Returns counts of sent, received, tickets, votes, revokes per period. |
| `/api/address/{address}/amountflow/{chartgrouping}` | Returns address amount flow chart data. Shows sent, received, and net amounts per time period. |
| `/api/address/{address}/raw` | Returns detailed raw transaction data for address. Default limit 10, max 1000. |
| `/api/address/{address}/count/{N}` | Returns `{N}` transactions for address. Maximum 8000. |
| `/api/address/{address}/count/{N}/raw` | Returns `{N}` raw transactions for address. |
| `/api/address/{address}/count/{N}/skip/{M}` | Returns `{N}` transactions, skipping first `{M}`. Use for pagination. |
| `/api/address/{address}/count/{N}/skip/{M}/raw` | Returns raw transactions with pagination. |
| `/api/address/addressesTxs/{addresses}` | Returns transactions for multiple addresses. `{addresses}` is comma-separated list. Returns map of address to transaction arrays. |

#### Atomic Swaps

| Endpoint | Description |
| --- | --- |
| `/api/atomic-swaps/amount/{chartgrouping}` | Returns atomic swap trading amounts chart data. `{chartgrouping}`: `day`, `week`, `month`, `year`. Shows redeem and refund amounts over time. |
| `/api/atomic-swaps/txcount/{chartgrouping}` | Returns atomic swap transaction counts chart data. Shows redeem and refund counts per period. |

#### Treasury

| Endpoint | Description |
| --- | --- |
| `/api/treasury/balance` | Returns current treasury balance including matured, immature, and total values. |
| `/api/treasury/io/{chartgrouping}` | Returns treasury inflow/outflow chart data grouped by time period. Shows received, sent, and net amounts. |
| `/api/treasury/votechart/{txhash}` | Returns vote chart data for a treasury spend (tspend) transaction. Shows yes/no votes over time and by block height. |

#### Agendas

| Endpoint | Description |
| --- | --- |
| `/api/agendas` | Returns all consensus agendas with details: name, description, vote version, status, activation height, start/expire times. |
| `/api/agenda/{agendaId}` | Returns voting chart data for specific agenda. `{agendaId}` is the agenda ID string. Returns vote counts by time and by block height. |

#### Mempool

| Endpoint | Description |
| --- | --- |
| `/api/mempool/sstx` | Returns stake transaction (ticket) summary from mempool: count, total value, average fee rate. |
| `/api/mempool/sstx/fees` | Returns mempool ticket fee rates. |
| `/api/mempool/sstx/fees/{N}` | Returns top `{N}` mempool ticket fee rates. |
| `/api/mempool/sstx/details` | Returns detailed mempool ticket information. |
| `/api/mempool/sstx/details/{N}` | Returns details for top `{N}` mempool tickets. |

#### Charts

| Endpoint | Description |
| --- | --- |
| `/api/chart/market/{token}/candlestick/{bin}` | Returns candlestick/OHLC chart data. `{token}` is exchange identifier. `{bin}` is time interval (e.g., `1h`, `1d`). |
| `/api/chart/market/{token}/depth` | Returns market depth (order book) chart data for the exchange. |
| `/api/chart/submarket/{token}/depth` | Returns submarket depth chart data for exchange. |
| `/api/chart/{charttype}` | Returns historical chart data. Query params: `bin` (zoom level), `axis` (x-axis type). Chart types include blockchain metrics like hashrate, difficulty, fees, etc. |

#### Staking Calculator

| Endpoint | Description |
| --- | --- |
| `/api/stakingcalc/getBlocksReward` | Returns PoS rewards for given blocks. Query param: `list` is comma-separated block heights. Returns map of block height to reward amount. |

#### Finance Report

| Endpoint | Description |
| --- | --- |
| `/api/finance-report/proposal` | Returns proposal spending report with monthly breakdown, domain statistics, author data, and treasury summary. Query param: `search` to filter proposals. |
| `/api/finance-report/treasury` | Returns treasury spending summary with monthly inflow/outflow and estimated proposal spending. |
| `/api/finance-report/detail` | Returns detailed report data. Query params: `type` (`month`, `year`, `proposal`, `domain`, `owner`), `time` (for month: `YYYY_MM`, for year: `YYYY`), `token` (proposal token), `name` (domain/owner name). |
| `/api/finance-report/time-range` | Returns valid time range for finance reports. Returns min/max year and month values. |

#### Ticket Pool

| Endpoint | Description |
| --- | --- |
| `/api/ticketpool` | Returns ticket pool visualization data grouped by day. |
| `/api/ticketpool/bydate/{tp}` | Returns ticket pool data by time grouping. `{tp}`: `day`, `week`, `month`, `year`, `all`. |
| `/api/ticketpool/charts` | Returns complete ticket pool chart data: time chart, price chart, outputs chart, and current mempool info. |

#### Proposal

| Endpoint | Description |
| --- | --- |
| `/api/proposal/{token}` | Returns proposal voting chart data by Politeia proposal token. Shows vote counts over time. |

#### Exchange

| Endpoint | Description |
| --- | --- |
| `/api/exchangerate` | Returns current exchange rates for DCR. Query param: `code` for specific fiat currency code. |
| `/api/exchanges` | Returns full exchange state with order books and volumes. Query param: `code` for specific currency. |
| `/api/exchanges/codes` | Returns list of available fiat currency codes for conversion. |

#### Broadcast

| Endpoint | Description |
| --- | --- |
| `/api/broadcast` | Broadcasts raw transaction to network. Query param: `hex` is the signed transaction hex string. Returns txid on success. |

---

### Bitcoin, Litecoin

Replace `{chaintype}` with `btc` for Bitcoin or `ltc` for Litecoin. These APIs support UTXO-based transactions.

#### Transaction

| Endpoint | Description |
| --- | --- |
| `/api/{chaintype}/tx/{txid}` | Returns decoded transaction details. Includes inputs, outputs, block info, confirmations. |
| `/api/{chaintype}/tx/{txid}/out` | Returns all transaction outputs as array. |
| `/api/{chaintype}/tx/{txid}/out/{txinoutindex}` | Returns specific output by 0-based index. |
| `/api/{chaintype}/tx/{txid}/in` | Returns all transaction inputs as array. |
| `/api/{chaintype}/tx/{txid}/in/{txinoutindex}` | Returns specific input by index. |
| `/api/tx/hex/{chaintype}/{txid}` | Returns raw transaction hex string. |
| `/api/tx/decoded/{chaintype}/{txid}` | Returns decoded transaction (same as `/{chaintype}/tx/{txid}`). |
| `/api/tx/swaps/{chaintype}/{txid}` | Returns atomic swap info if transaction contains swap contracts. |

#### Charts

| Endpoint | Description |
| --- | --- |
| `/api/chainchart/{chaintype}/market/{token}/candlestick/{bin}` | Returns candlestick chart data for the chain's markets. |
| `/api/chainchart/{chaintype}/market/{token}/depth` | Returns market depth chart for the chain. |
| `/api/chainchart/{chaintype}/submarket/{token}/depth` | Returns submarket depth chart. |
| `/api/chainchart/{chaintype}/{charttype}` | Returns chain-specific chart data (hashrate, difficulty, etc.). Query params: `bin`, `axis`. |
| `/api/chainchart/exchanges` | Returns exchange data for all supported chains. |

---

### Monero

All Monero APIs use the `/api/xmr` prefix. Monero uses different privacy-focused transaction model.

| Endpoint | Description |
| --- | --- |
| `/api/xmr/decode-output` | Decodes Monero outputs using view key. Query params: `txid` (transaction hash), `address` (Monero address), `viewkey` (private view key). Returns decoded output amounts. |
| `/api/xmr/prove-tx` | Proves Monero transaction sending. Query params: `txid` (transaction hash), `address` (recipient address), `txkey` (transaction private key). Returns proof data. |
| `/api/xmr/transactions` | Returns list of latest Monero transactions from the network. |
| `/api/xmr/block/hash/{blockhash}` | Returns Monero block details by block hash. |
| `/api/xmr/block/{idx}` | Returns Monero block details by block height. |
| `/api/xmr/tx/{txid}` | Returns Monero transaction details by transaction hash. |
| `/api/xmr/mempool` | Returns current Monero mempool information and pending transactions. |
| `/api/xmr/networkinfo` | Returns Monero network info: height, difficulty, hashrate, version. |
| `/api/xmr/rawtransaction/{txid}` | Returns raw Monero transaction data by hash. |
