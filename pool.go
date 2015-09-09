package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/ethash"
	"github.com/ethereum/go-ethereum/common"

	"github.com/gorilla/mux"

	"github.com/syndtr/goleveldb/leveldb"
)

var levelDB *leveldb.DB

var currWork *ResponseArray = nil

var pendingBlockNumber uint64 = 0
var pendingBlockDifficulty *big.Int

var invalidRequest = `{
  "id":64,
  "jsonrpc": "2.0",
  "result": false,
  "error": "invalid request"
}`

var okRequest = `{
  "id":64,
  "jsonrpc": "2.0",
  "result": true
}`

var pow256 = common.BigPow(2, 256)

var hasher = ethash.New()

var poolPort = "5082"
var ethereumPort = "8545" //8545 = geth, 8080 = eth (requires dev branch when using eth client)

var logInfo *log.Logger
var logError *log.Logger

type ResponseArray struct {
	Id      int           `json:"id"`
	Jsonrpc string        `json:"jsonrpc"`
	Result  []interface{} `json:"result"`
}

type ResponseJSON struct {
	Id      int                    `json:"id"`
	Jsonrpc string                 `json:"jsonrpc"`
	Result  map[string]interface{} `json:"result"`
}

type ResponseBool struct {
	Id      int    `json:"id"`
	Jsonrpc string `json:"jsonrpc"`
	Result  bool   `json:"result"`
}

type Request struct {
	Id      int           `json:"id"`
	Jsonrpc string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type block struct {
	difficulty  *big.Int
	hashNoNonce common.Hash
	nonce       uint64
	mixDigest   common.Hash
	number      uint64
}

func (b block) Difficulty() *big.Int     { return b.difficulty }
func (b block) HashNoNonce() common.Hash { return b.hashNoNonce }
func (b block) Nonce() uint64            { return b.nonce }
func (b block) MixDigest() common.Hash   { return b.mixDigest }
func (b block) NumberU64() uint64        { return b.number }

func main() {
	// Set up logging
	logInfo = log.New(os.Stderr, "INFO: ", log.Ldate|log.Ltime)
	logError = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime)
	logInfo.Println("Welcome to ethpool 2.0")
	logInfo.Println("Pool port is", poolPort)
	logInfo.Println("Point your miners to: http://<ip>:" + poolPort + "/miner/{miner}/{difficulty}")

	// Open the share database
	var err error

	levelDB, err = leveldb.OpenFile("~/ethpool_shares.db", nil)
	if err != nil {
		logError.Println("Unable to open leveldb connection:", err)
		return
	}
	defer levelDB.Close()

	go updateWork()
	go updatePendingBlock()
	go submitShares()
	// names := []interface{}{"pending", false}

	// pb := callJSON("eth_getBlockByNumber", names)
	// fmt.Println(pb.Result["number"])

	// fmt.Scanln()
	r := mux.NewRouter()
	r.HandleFunc("/miner/{miner}/{difficulty}", handleMiner)
	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":5082", nil))
}

func handleMiner(rw http.ResponseWriter, req *http.Request) {

	vars := mux.Vars(req)

	minerDifficulty, err := strconv.ParseFloat(vars["difficulty"], 64)
	if err != nil {
		logError.Println("Invalid difficulty provided: " + vars["difficulty"])
		minerDifficulty = 5 // Set a fixed difficulty (5MH/s) in this case
		// fmt.Fprint(rw, getErrorResponse("Invalid difficulty provided: "+vars["difficulty"]))
		// return
	}
	minerAdjustedDifficulty := int64(minerDifficulty * 1000000 * 100)

	minerArray := strings.Split(vars["miner"], ".")

	if len(minerArray) == 0 || len(minerArray) > 2 {
		logError.Println("Invalid miner & worker provided: " + vars["miner"])
		fmt.Fprint(rw, getErrorResponse("Invalid miner & worker provided: "+vars["miner"]))
		return
	}

	miner := strings.Replace(minerArray[0], "0x", "", -1)
	worker := "default"

	if len(minerArray) == 2 {
		worker = minerArray[1]
	}

	if len(miner) != 40 {
		logError.Println("Invalid ethereum address provided: 0x" + miner)
		fmt.Fprint(rw, getErrorResponse("Invalid ethereum address provided: 0x"+miner))
		return
	}

	decoder := json.NewDecoder(req.Body)
	var t Request
	err = decoder.Decode(&t)
	if err != nil {
		logError.Println("Invalid JSON request: ", err)
		fmt.Fprint(rw, getErrorResponse("Invalid JSON request"))
		return
	}

	if t.Method == "eth_getWork" {
		difficulty := big.NewInt(minerAdjustedDifficulty)
		// Send the response
		fmt.Fprint(rw, getWorkPackage(difficulty))
	} else if t.Method == "eth_submitHashrate" {
		fmt.Fprint(rw, okRequest)
	} else if t.Method == "eth_submitWork" {
		paramsOrig := t.Params[:]

		hashNoNonce := t.Params[1].(string)
		nonce, err := strconv.ParseUint(strings.Replace(t.Params[0].(string), "0x", "", -1), 16, 64)
		if err != nil {
			logError.Println("Invalid nonce provided: ", err)
			fmt.Fprint(rw, getErrorResponse("Invalid nonce provided"))
			return
		}

		mixDigest := t.Params[2].(string)

		myBlock := block{
			number:      pendingBlockNumber,
			hashNoNonce: common.HexToHash(hashNoNonce),
			difficulty:  big.NewInt(minerAdjustedDifficulty),
			nonce:       nonce,
			mixDigest:   common.HexToHash(mixDigest),
		}

		myBlockRealDiff := block{
			number:      pendingBlockNumber,
			hashNoNonce: common.HexToHash(hashNoNonce),
			difficulty:  pendingBlockDifficulty,
			nonce:       nonce,
			mixDigest:   common.HexToHash(mixDigest),
		}

		if hasher.Verify(myBlock) {
			//fmt.Println("Share is valid")
			if hasher.Verify(myBlockRealDiff) {
				submitWork(paramsOrig)
				logInfo.Println("###########################################################################")
				logInfo.Println("################################Block found################################")
				logInfo.Println("###########################################################################")
			}

			share := `MIX:` + mixDigest + `|MINER:"` + miner + `|DIFFICULTY:` + strconv.FormatInt(minerAdjustedDifficulty, 10) + `|WORKER:` + worker
			logInfo.Println("Miner", miner, ".", worker, "found valid share (Diff:", minerAdjustedDifficulty, "Mix:", mixDigest, "Hash:", hashNoNonce, "Nonce:", nonce, ")")
			err = levelDB.Put([]byte(mixDigest), []byte(share), nil)

			if err != nil {
				logError.Println("Error inserting share into database:", err)
			}
		} else {
			logError.Println("Miner", miner, "provided invalid share")
			fmt.Fprint(rw, getErrorResponse("Provided PoW solution is invalid!"))
		}
		fmt.Fprint(rw, okRequest)
	} else {
		logError.Println("Method " + t.Method + " not implemented!")
		fmt.Fprint(rw, getErrorResponse("Method "+t.Method+" not implemented!"))
	}
}

func getWorkPackage(difficulty *big.Int) string {

	if currWork == nil {
		return getErrorResponse("Current work unavailable")
	}

	// Our response object
	response := &ResponseArray{
		Id:      currWork.Id,
		Jsonrpc: currWork.Jsonrpc,
		Result:  currWork.Result[:],
	}

	// Calculte requested difficulty
	diff := new(big.Int).Div(pow256, difficulty)
	diffBytes := string(common.ToHex(diff.Bytes()))

	// Adjust the difficulty for the miner
	response.Result[2] = diffBytes

	// Convert respone object to JSON
	b, _ := json.Marshal(response)

	return string(b)

}

func updateWork() {
	for true {
		currWorkNew, err := callArray("eth_getWork", []interface{}{})

		if err == nil {
			currWork = currWorkNew
		} else {
			currWork = nil
		}

		// fmt.Println("Current work", currWork.Result[0])
		time.Sleep(time.Millisecond * 100)
	}
}

func submitWork(params []interface{}) {
	result, err := callBool("eth_submitWork", params)
	if err == nil {
		logInfo.Println(result.Result)
	}
}

func submitShares() {
	for true {
		iter := levelDB.NewIterator(nil, nil)
		for iter.Next() {
			key := iter.Key()
			value := iter.Value()
			_ = value
			logInfo.Println("Do smth with the share (e.g. send to pool database):", string(key))
		}
		iter.Release()
		err := iter.Error()
		if err != nil {
			logError.Println("Error itarating shares:", err)
		}
		time.Sleep(time.Second * 10)
	}
}

func updatePendingBlock() {
	params := []interface{}{"pending", false}

	for true {
		block, err := callJSON("eth_getBlockByNumber", params)
		if err == nil {
			blockNbr, err := strconv.ParseUint(strings.Replace(block.Result["number"].(string), "0x", "", -1), 16, 64)
			if err == nil {
				pendingBlockNumber = blockNbr
			}

			blockDiff, err := strconv.ParseInt(strings.Replace(block.Result["difficulty"].(string), "0x", "", -1), 16, 64)
			if err == nil {
				pendingBlockDifficulty = big.NewInt(blockDiff)
				// logInfo.Println("Pending block difficulty:", pendingBlockDifficulty)
			}
		}
		time.Sleep(time.Millisecond * 100)
	}
}

func callArray(method string, params []interface{}) (*ResponseArray, error) {
	url := "http://localhost:" + ethereumPort
	jsonReq := &Request{
		Id:      1,
		Jsonrpc: "2.0",
		Method:  method,
		Params:  params,
	}
	reqJSON, _ := json.Marshal(jsonReq)
	// fmt.Println(string(reqJSON))
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqJSON))

	if err != nil {
		logError.Println("Could not create POST request", err)
		return nil, errors.New("Could not create POST request")
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logError.Println("Could not send POST request to Ethereum client", err)
		return nil, errors.New("Could not send POST request to Ethereum client")
	}

	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	// fmt.Println(string(body))
	res := &ResponseArray{}

	if err := json.Unmarshal(body, res); err != nil {
		logError.Println("Ethereum client returned unexpected data", err)
		return nil, errors.New("Ethereum client returned unexpected data")
	}

	// fmt.Println("done")
	return res, nil
}

func callBool(method string, params []interface{}) (*ResponseBool, error) {
	url := "http://localhost:" + ethereumPort
	jsonReq := &Request{
		Id:      1,
		Jsonrpc: "2.0",
		Method:  method,
		Params:  params,
	}
	reqJSON, _ := json.Marshal(jsonReq)
	// fmt.Println(string(reqJSON))
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqJSON))

	if err != nil {
		logError.Println("Could not create POST request", err)
		return nil, errors.New("Could not create POST request")
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logError.Println("Could not send POST request to Ethereum client", err)
		return nil, errors.New("Could not send POST request to Ethereum client")
	}

	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	// fmt.Println(string(body))
	res := &ResponseBool{}

	if err := json.Unmarshal(body, res); err != nil {
		logError.Println("Ethereum client returned unexpected data", err)
		return nil, errors.New("Ethereum client returned unexpected data")
	}

	// fmt.Println("done")
	return res, nil
}

func callJSON(method string, params []interface{}) (*ResponseJSON, error) {
	url := "http://localhost:" + ethereumPort
	jsonReq := &Request{
		Id:      1,
		Jsonrpc: "2.0",
		Method:  method,
		Params:  params,
	}
	reqJSON, _ := json.Marshal(jsonReq)
	// fmt.Println(string(reqJSON))
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqJSON))

	if err != nil {
		logError.Println("Could not create POST request", err)
		return nil, errors.New("Could not create POST request")
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logError.Println("Could not send POST request to Ethereum client", err)
		return nil, errors.New("Could not send POST request to Ethereum client")
	}

	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	// fmt.Println(string(body))
	res := &ResponseJSON{}

	if err := json.Unmarshal(body, res); err != nil {
		logError.Println("Ethereum client returned unexpected data", err)
		return nil, errors.New("Ethereum client returned unexpected data")
	}

	// fmt.Println("done")
	return res, nil
}

func getErrorResponse(errorMsg string) string {
	return `{
    "id":64,
    "jsonrpc": "2.0",
    "result": false,
    "error": "` + errorMsg + `"
  }`
}
