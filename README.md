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
