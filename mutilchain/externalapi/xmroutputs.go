package externalapi

import (
	"fmt"
	"net/http"
)

var moneroOutputsDecodeUrl = `%s/outputs`

type XmrOutputsResponse struct {
	Data   XmrOutputsData `json:"data"`
	Status string         `json:"status"`
}

type XmrOutputsData struct {
	Address         string     `json:"address"`
	Outputs         []TxOutput `json:"outputs"`
	TxConfirmations int        `json:"tx_confirmations"`
	TxHash          string     `json:"tx_hash"`
	TxProve         bool       `json:"tx_prove"`
	TxTimestamp     int64      `json:"tx_timestamp"`
	ViewKey         string     `json:"viewkey"`
}

type TxOutput struct {
	Amount       uint64 `json:"amount"`
	Match        bool   `json:"match"`
	OutputIndex  int    `json:"output_idx"`
	OutputPubKey string `json:"output_pubkey"`
}

func DecodeOutputs(apiServ, txid, address, viewKey string, isProve bool) ([]TxOutput, error) {
	log.Info("Start decode outputs for monero tx")
	url := fmt.Sprintf(moneroOutputsDecodeUrl, apiServ)
	txProve := "0"
	if isProve {
		txProve = "1"
	}
	query := map[string]string{
		"txhash":  txid,
		"address": address,
		"viewkey": viewKey,
		"txprove": txProve,
	}
	var responseData XmrOutputsResponse
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: query,
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	if responseData.Status != "success" {
		return nil, fmt.Errorf("decode output failed")
	}
	log.Info("Finish decode outputs for monero tx")
	return responseData.Data.Outputs, nil
}

func GetTransaction(apiServ, txhash string) (any, error) {
	log.Infof("API: Start get monero transaction: txhash: %s", txhash)
	url := fmt.Sprintf("%s/transaction/%s", apiServ, txhash)
	var responseData any
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: map[string]string{},
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	log.Infof("API: Finish get monero transaction: txhash: %s", txhash)
	return responseData, nil
}

// get last 25 block detail
func GetLastestTransactions(apiServ string) (any, error) {
	log.Info("API: Start get monero 24 lastest blocks")
	url := fmt.Sprintf("%s/transactions", apiServ)
	var responseData any
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: map[string]string{},
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	log.Info("API: Finish get monero 24 lastest blocks")
	return responseData, nil
}

func GetBlockDetail(apiServ, heightOrHash string) (any, error) {
	log.Infof("API: Start get monero block detail: height/hash: %s", heightOrHash)
	url := fmt.Sprintf("%s/block/%s", apiServ, heightOrHash)
	var responseData any
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: map[string]string{},
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	log.Infof("API: Finish get monero block detail: height/hash: %s", heightOrHash)
	return responseData, nil
}

func GetMempoolDetail(apiServ string) (any, error) {
	log.Info("API: Start get monero mempool detail")
	url := fmt.Sprintf("%s/mempool", apiServ)
	var responseData any
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: map[string]string{},
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	log.Info("API: Finish get monero mempool detail")
	return responseData, nil
}

func GetNetworkInfo(apiServ string) (any, error) {
	log.Info("API: Start get monero network info")
	url := fmt.Sprintf("%s/networkinfo", apiServ)
	var responseData any
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: map[string]string{},
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	log.Info("API: Finish get monero network info")
	return responseData, nil
}

func GetRawTransaction(apiServ, txhash string) (any, error) {
	log.Infof("API: Start get monero raw transaction: txhash: %s", txhash)
	url := fmt.Sprintf("%s/rawtransaction/%s", apiServ, txhash)
	var responseData any
	req := &ReqConfig{
		Method:  http.MethodGet,
		HttpUrl: url,
		Payload: map[string]string{},
	}
	if err := HttpRequest(req, &responseData); err != nil {
		return nil, err
	}
	log.Infof("API: Finish get monero raw transaction: txhash: %s", txhash)
	return responseData, nil
}
