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
