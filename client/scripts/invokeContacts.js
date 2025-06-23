// scripts/invokeContacts.js

async function testDeployContractORDXRemote() {
  try {
    await prepare();
    await prepareChannel();
    backupEnv();

    // 1. 获取支持的合约
    const supportedContracts = await _client.getSupportContractInServer();
    console.log("supported contracts:", supportedContracts);

    // 2. 获取已部署的合约
    let deployedContracts = await _client.getDeployedContractInServer();
    console.log("deployed contracts:", deployedContracts);

    // 3. 构造合约参数
    const assetName = {
      Protocol: "ordx",
      Type: "f",
      Ticker: "testTicker",
      N: 1000,
      toString() {
        return `${this.Protocol}:${this.Type}:${this.Ticker}:${this.N}`;
      }
    };

    const launchPool = {
      Protocol: assetName.Protocol,
      AssetName: assetName.Ticker,
      BindingSat: assetName.N,
      Limit: 10000000,
      LaunchRatio: 70,
      MaxSupply: 40000000,
      Content() {
        // 假设Content返回JSON字符串
        return JSON.stringify(this);
      }
    };

    // 4. 查询部署费用
    const deployFee = await _client.queryFeeForDeployContract(
      TEMPLATE_CONTRACT_LAUNCHPOOL,
      launchPool.Content(),
      1
    );
    console.log(`deploy contract ${TEMPLATE_CONTRACT_LAUNCHPOOL} need ${deployFee} sats`);
    console.log("use RemoteDeployContract to deploy a contract on core channel in server node");

    // 5. 查询调用参数
    const invokeParam = await _client.getParamForInvokeContract(TEMPLATE_CONTRACT_LAUNCHPOOL);
    console.log(`use ${invokeParam} as template to invoke contract ${TEMPLATE_CONTRACT_LAUNCHPOOL}`);

    // 6. 构造调用参数
    const para = {
      AssetName: assetName.toString(),
      Amt: "1000000"
    };
    const paraHex = JSON.stringify(para);

    // 7. 查询调用费用
    const invokeFee = await queryFeeForInvokeContract(
      TEMPLATE_CONTRACT_LAUNCHPOOL,
      launchPool.Content(),
      paraHex
    );
    console.log(`need ${invokeFee} fee to invoke contract ${TEMPLATE_CONTRACT_LAUNCHPOOL} with parameter ${paraHex}`);

    // 8. 获取资产余额
    const assetAmt = await _server.getAssetBalanceSatsNet("", ASSET_PLAIN_SAT);
    console.log("plain sats:", assetAmt);

    // 9. 远程部署合约
    const [txId, id] = await _client.remoteDeployContract(
      TEMPLATE_CONTRACT_LAUNCHPOOL,
      launchPool.Content(),
      0
    );
    console.log("RemoteDeployContract succeed,", txId, id);

    // 10. 等待合约部署完成
    let resv = await _client.getResv("", id);
    // 假设类型判断
    if (!resv || resv.type !== "RemoteActionPerformReservation") {
      throw new Error("Reservation type error");
    }

    await resvWaitUntilConfirmed(_server, id);
    await resvWaitUntilConfirmed(_client, id);

    const contractRuntimeResvId = resv.actionResvId;
    const contractResv = await _server.getResv("", contractRuntimeResvId);
    const contractRuntime1 = contractResv.contract;
    const contractResv2 = await _bootstrap.getResv("", contractRuntimeResvId);
    const contractRuntime2 = contractResv2.contract;

    console.log(`contract ${contractRuntime1.url()} is running`);

    // 11. 再次获取已部署合约
    deployedContracts = await _client.getDeployedContractInServer();
    console.log("deployed contracts:", deployedContracts);

    // 12. 获取合约状态
    const contractStatus = await _client.getContractStatusInServer(deployedContracts[0]);
    console.log(`deployed contracts ${deployedContracts[0]} status\n`, contractStatus);

    // 13. 查询调用费用
    const invokeFee2 = await _server.queryFeeForInvokeContract(
      contractRuntime1.url(),
      paraHex
    );
    console.log(`need ${invokeFee2} fee to invoke contract ${contractRuntime1.url()} with parameter ${paraHex}`);

    // 14. 调用合约
    await invokeLaunchPoolContract(contractRuntime1);

    // 15. 检查合约状态
    let c = await _server.getContract(contractRuntime1.url());
    if (c) {
      await resvWaitUntilStatus(_server, id, RS_DEPLOY_CONTRACT_COMPLETED);
    }
    let c2 = await _bootstrap.getContract(contractRuntime2.url());
    if (c2) {
      await resvWaitUntilStatus(_bootstrap, id, RS_DEPLOY_CONTRACT_COMPLETED);
    }

    // 16. 等待合约关闭
    while (c.getStatus() !== CONTRACT_STATUS_CLOSED) {
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    while (c2.getStatus() !== CONTRACT_STATUS_CLOSED) {
      await new Promise(resolve => setTimeout(resolve, 1000));
    }

    // 17. 打印合约信息
    console.log("contract status in server node");
    console.log("total minted:", c.totalMinted);
    console.log("total invalid:", c.totalInvalid);
    console.log("left:", c.leftToMint());
    console.log("ready to launch:", c.readyToLaunch());
    console.log("launch txs:", c.launchTxIDs);
    console.log("refund txs:", c.refundTxIDs);

    c = c2;
    console.log("contract status in bootstrap node");
    console.log("total minted:", c.totalMinted);
    console.log("total invalid:", c.totalInvalid);
    console.log("left:", c.leftToMint());
    console.log("ready to launch:", c.readyToLaunch());
    console.log("launch txs:", c.launchTxIDs);
    console.log("refund txs:", c.refundTxIDs);

  } catch (err) {
    console.error(err);
  }
}

// 你需要实现/引入 prepare, prepareChannel, backupEnv, _client, _server, _bootstrap, 等函数和对象
// 以及常量 TEMPLATE_CONTRACT_LAUNCHPOOL, ASSET_PLAIN_SAT, RS_DEPLOY_CONTRACT_COMPLETED, CONTRACT_STATUS_CLOSED 等
// testDeployContractORDXRemote(); // 取消注释以运行
