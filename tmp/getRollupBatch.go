package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	derivation "github.com/morph-l2/node/derivation"
	geth "github.com/scroll-tech/go-ethereum/eth"
)

type RPCResponse struct {
	Jsonrpc string              `json:"jsonrpc"`
	ID      uint64              `json:"id"`
	Result  geth.RPCRollupBatch `json:"result"`
}

func main() {
	url := "http://localhost:8545"
	method := "POST"

	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "morph_getRollupBatchByIndex",
		"params":  []int{40},
		"id":      1,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Println(err)
		return
	}

	client := &http.Client{}
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Println(err)
		return
	}
	req.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(string(body))

	var response RPCResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Println("Error unmarshalling response:", err)
		return
	}

	fmt.Printf("Parsed RPC Rollup Batch: %+v\n", response)

	BatchInfo, err := derivation.ParseBatch(response.Result)

	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(BatchInfo.Chunks())

	for i, chunk := range BatchInfo.Chunks() {
		fmt.Println("chunk index:", i)

		for j, blockContext := range chunk.GetClockContext() {
			fmt.Println("blockContext index:", j)

			fmt.Println(blockContext.Number)
		}
	}
}
