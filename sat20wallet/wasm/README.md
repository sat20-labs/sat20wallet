# 1 BUILD
## vscode: install extension tinygo and liveServer
## wawm_exec.js: curl -o wasm_exec.js https://raw.githubusercontent.com/tinygo-org/tinygo/release/targets/wasm_exec.js
## build: make all

## 2 RPC 
1 openChannel
curl -X POST -H "Content-Type: application/json" -d '{"feerate": 1, 
"amt": 20000, "utxos":["af1e6718c2ff2f53c78995fa8a2f6a698765695a342cc350c64684ea5d650360:1"], "memo":"::open"}'  http://192.168.10.103:9080/testnet/channel/open
输入：
    "feerate": 1  
    "amt": 20000 （用户指定通道锁定一层地址的金额，方便unlockUtxo去解锁,至少20000sat）
    "utxos":["af1e6718c2ff2f53c78995fa8a2f6a698765695a342cc350c64684ea5d650360:1"] 
    "memo":"::open" （原设计只是备注，目前照抄，：：必须带上）
输出：
    channel（二层UTXO）


2 closeChannel
curl -X POST -H "Content-Type: application/json" -d '{"channel": "4a4915fd366a72e1a80355ad8036311b59b7a4e986229e28c4549a7a836b10a9:0"}'
  http://192.168.10.43:9080/testnet/channel/close
输入：
    "channel": "4a4915fd366a72e1a80355ad8036311b59b7a4e986229e28c4549a7a836b10a9:0"
输出：
		closeTxId(一层)
		deAnchorTxId（二层）

3 unlockUtxo
curl -X POST -H "Content-Type: application/json" -d '{"channel": "2e4f0cccbf1a9b84f74411a9444436c0e38bbf8929b99e3c80c0bb7be8618171:0", "amt":1000}'  
http://127.0.0.1:9080/testnet/utxo/unlock
输入：
    "channel": "2e4f0cccbf1a9b84f74411a9444436c0e38bbf8929b99e3c80c0bb7be8618171:0"
    "amt":  1000 (指定需要解锁的金额，不能大于通道锁定的金额)
    "feeUtxoList": [""]
输出：
	unlockTxId（二层）


4 lockUtxo
curl -X POST -H "Content-Type: application/json" -d '{"channel": "4a4915fd366a72e1a80355ad8036311b59b7a4e986229e28c4549a7a836b10a9:0", 
"amt":100, "lockutxos": ["6134516df167b4014999e6ec43b51e0f01f854c8f6b83581413ba3774276ef70:1"] }'  http://127.0.0.1:9080/testnet/utxo/lock
输入：
    "channel": "4a4915fd366a72e1a80355ad8036311b59b7a4e986229e28c4549a7a836b10a9:0"
    "amt":  100
    "lockutxos": ["6134516df167b4014999e6ec43b51e0f01f854c8f6b83581413ba3774276ef70:1"] （二层）
    "feeUtxoList": []
输出：
	lockTxId（二层）