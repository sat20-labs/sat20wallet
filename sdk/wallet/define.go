package wallet

import "math"

const (
	SOFTWARE_VERSION = "0.2.1"
	DB_VERSION       = "0.0.1"
)

const (	
	INVALID_ID uint64 = math.MaxUint64

	DEFAULT_FEE_SATSNET                 int64 = 10
	DEFAULT_SERVICE_FEE_DEPOSIT         int64 = 0    //
	DEFAULT_SERVICE_FEE_WITHDRAW        int64 = 2000 //
	DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT int64 = 2000 // 负责部署的服务节点收取的费用
	STUB_VALUE_BRC20                    int64 = 2000
	
	MAX_FEE         int64 = 10000
	MAX_FEE_SATSNET int64 = DEFAULT_FEE_SATSNET


	MSG_FEE    string = "fee"
	MSG_DEPLOY string = "deploy"
	MSG_MINT   string = "mint"
)
