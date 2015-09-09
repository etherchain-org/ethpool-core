# Introduction
We have decided to open source our implementation of pooled mining for Ethereum. This software is not a complete mining pool. It only takes care of work distribution and share validation; valid shares are stored into a local database (LevelDB). Reward calculation and payments are not yet implemented but it should be possible to connect this software some of the  existing open source mining pools.

# Performance
While the current implementation in go might not be the most effective one, the pool was able to process ~600 workers at 30% CPU utilization (1 core) and 70MB RAM usage.

# Supported clients
The pool has been tested successfully with both the go Ethereum client (geth) and the cpp Ethereum client (eth).

# Pull requests & possible optimizations
If you find any issues with the pool software please feel free to issue a pull request.

If you want to improve the pool, implementing the connection to geth via IPC instead of HTTP would be a good start.

# Setup guide (Ubuntu 14.04)
* Install go according to https://github.com/ethereum/go-ethereum/wiki/Installing-Go#ubuntu-1404
* Put the pool.go file into your gopath
* Run go get to download the dependencies
* Adjust the ports to match your environment (poolPort and ethereumPort)
* Start your Ethereum client & enable RPC
* Run go build pool.go
* Start the pool server ./pool
* Point your miner to http://ip:port/miner/<account>.<worker>/<hashrate>

# Donations
Donations are always welcome:

BTC: 37rfj6oPJmnEDHTnUxvsUEmF4CnqofgWJr

ETH: 0xc5d2dd8b399b67d857ed6d91bbe26f0702f7cd34
