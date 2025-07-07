package wallet

import "math"

const (
	SOFTWARE_VERSION = "0.0.3.4"
	DB_VERSION       = "0.0.1"
)



const INVALID_ID = math.MaxUint64



const (
	MIN_AVAILABLE_VALUE                 int64 = 5000
	MIN_CAPACITY                        int64 = MIN_AVAILABLE_VALUE + DEFAULT_FEE_OPEN
	DEFAULT_FEE_SATSNET                 int64 = 10
	DEFAULT_MANAGE_FEE                  int64 = 3000 + DEFAULT_SERVICE_FEE_SPLICINGOUT // 需要包含最后关闭通道时支付的splicingout费用 DEFAULT_SERVICE_FEE_SPLICINGOUT ）
	DEFAULT_MORTGAGE_FEE                int64 = 10000                                  // 抵押费用，关闭时退回，用于共享流动性。这是最低档，后续提供中档和高档。池子收益，每个月在聪网分发一次。
	DEFAULT_MORTGAGE_FEE_ADVANCE        int64 = 100000
	DEFAULT_MORTGAGE_FEE_VIP            int64 = 1000000
	DEFAULT_MORTGAGE_FEE_SVIP           int64 = 10000000
	DEFAULT_COMMITMENT_FEE              int64 = 3000 // 包含费率15sat/vb时, 一个输入两个输出的commitmentTx的费用。如果更多输入和输出，需要收取更多费用。最终协商关闭时未使用费用退回给用户。
	DEFAULT_MAX_COMMIT_FEERATE          int64 = 10   // 对应DEFAULT_COMMITMENT_FEE的费率
	DEFAULT_FEE_OPEN                    int64 = DEFAULT_MANAGE_FEE + DEFAULT_MORTGAGE_FEE + DEFAULT_COMMITMENT_FEE
	DEFAULT_FEE_TO_DAO                  int64 = DEFAULT_MANAGE_FEE + DEFAULT_MORTGAGE_FEE - MIN_SERVER_RESERVE_SATS
	MIN_SERVER_RESERVE_SATS             int64 = 330 + DEFAULT_FEE_SATSNET // 预留最后deAnchor的费用
	DEFAULT_SERVICE_FEE_SPLICINGIN      int64 = 0
	DEFAULT_SERVICE_FEE_SPLICINGOUT     int64 = 2000 // 提取服务基本费用，还要加上提取总额的1%
	DEFAULT_SERVICE_FEE_STAKE           int64 = 0    //
	DEFAULT_SERVICE_FEE_UNSTAKE         int64 = 2000 //
	DEFAULT_SERVICE_FEE_DEPOSIT         int64 = 0    //
	DEFAULT_SERVICE_FEE_WITHDRAW        int64 = 2000 //
	DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT int64 = 2000 // 负责部署的服务节点收取的费用
	DEFAULT_SERVICE_FEE_RUN_CONTRACT    int64 = 1000 // 通道运行的费用，加上运行合约所得盈利的20%作为运行费用，也就是从合约中提取盈利，需要分给通道节点各10%
	DEFAULT_FEE_RATIO_WITHDRAW_FROM_CONTRACT int64 = 200  // 千分之
	DEFAULT_FEE_RATIO                   int64 = 10   // 千分之

	MAX_FEE         int64 = 100000
	MAX_FEE_SATSNET int64 = DEFAULT_FEE_SATSNET

	POOL_INIT_PLAIN_SATS int64 = 100000

	REST_SERVER_PORT string = "9529"
)
