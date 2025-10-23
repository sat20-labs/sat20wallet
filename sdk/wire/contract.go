package wire

const (
	////////////////////////////////////////////////////////////////////////////
	// 通用的查询接口，不需要任何权限
	
	QUERY_INFO_SUPPORT_CONTRACTS       string = "/info/contracts/support"
	QUERY_INFO_DEPLOYED_CONTRACTS      string = "/info/contracts/deployed"
	QUERY_INFO_CONTRACT_FEE_DEPLOY     string = "/info/contract/deployfee"
	QUERY_INFO_CONTRACT_FEE_INVOKE     string = "/info/contract/invokefee"
	QUERY_INFO_CONTRACT                string = "/info/contract"
	QUERY_INFO_CONTRACT_INVOKE_HISTORY string = "/info/contract/history"
	QUERY_INFO_CONTRACT_LIQPROVIDER    string = "/info/contract/liqprovider"
	QUERY_INFO_CONTRACT_ALLUSER        string = "/info/contract/alluser"
	QUERY_INFO_CONTRACT_ANALYTICS      string = "/info/contract/analytics"
	QUERY_INFO_CONTRACT_USER           string = "/info/contract/user"
	QUERY_INFO_CONTRACT_USERHISTORY    string = "/info/contract/userhistory"
	

	////////////////////////////////////////////////////////////////////////////
	// 协议接口
	// 只验证公钥的SignMessage的有效性

	STP_DEPLOY_CONTRACT_REQ string = "/contract/deploy/require"
	STP_DEPLOY_CONTRACT_ACK string = "/contract/deploy/ack"

	STP_PERFORM_ACTION_REQ string = "/action/perform/require"
	STP_PERFORM_ACTION_ACK string = "/action/perform/ack"

	STP_ACTION_NFTY string = "/action/notify"
	STP_ACTION_SYNC string = "/action/sync"
	STP_ACTION_SIGN string = "/action/sign"

	STP_PING string = "/ping"

)
