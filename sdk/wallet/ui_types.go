package wallet

import "context"

// RGB 和操作记录默认属于 SDK 当前钱包/子账户及网络。
// 账户备份、存储付款和监护人身份属于当前根账户；恢复钱包由恢复码确定。
// 不开放任意钱包的查询参数。操作 ID 只能引用所属上下文的既有操作。
// 切换钱包不会把一个已开始的操作改绑到新钱包。

// Get 只读。Refresh 是用户刷新/检查收款，调用现有 SDK 同步能力。
// SDK 自己处理邮箱、代理投递、ACK、批次校验及已授权操作的继续执行。
// PWA 可以重读展示状态，不按协议阶段安排重试，不判断何时广播。
type RGBAPI interface {
	GetRGBView() (*RGBView, error)
	RefreshRGBState(ctx context.Context) (*RGBView, error)

	// 准备并展示，不广播。地址与 invoice 的解析、金额换算归 SDK。
	PrepareRGBTransfer(ctx context.Context, req RGBSendRequest) (*RGBTransferView, error)
	// “增发”是现有独立用户动作；后续确认、导出和取消共用转账入口。
	PrepareRGBInflation(ctx context.Context, invoice string, feeRate int64) (*RGBTransferView, error)
	// 首次确认或用户点击“继续”均操作同一个 ID，不创建另一笔交易。
	// 手工传输时提交用户收到的原文回执；SDK 匹配全部收款人后决定是否广播。
	ConfirmRGBTransfer(ctx context.Context, id string, receipts []string) (*RGBTransferView, error)
	CancelRGBTransfer(ctx context.Context, id string) (*RGBTransferView, error)
	// 用户下载原转账文件或已结算证明；SDK 决定可导出的内容与格式。
	ExportRGBTransfer(id string) ([]RGBTransferFile, error)

	CreateRGBInvoice(ctx context.Context, req RGBInvoiceRequest) (*RGBReceiveView, error)
	PrepareRGBConsignment(ctx context.Context, requestID string, data []byte) (*RGBReceiveView, error)
	// SDK 使用已校验并保存的原证明，PWA 不重复提交或校验其哈希。
	AcceptRGBConsignment(ctx context.Context, requestID string) (*RGBReceiveView, error)

	// 发行及合约导入、导出对应现有 PWA 用户操作。
	IssueRGBAsset(ctx context.Context, req RGBIssueRequest) (*RGBAssetView, error)
	ImportRGBContract(ctx context.Context, data []byte) (*RGBAssetView, error)
	ExportRGBContract(asset string) ([]ExportFile, error)
}

type RGBSendRequest struct {
	Recipients []RGBRecipient `json:"recipients"`
	FeeRate    int64          `json:"fee_rate"` // sat/vB
}

type RGBRecipient struct {
	Destination string `json:"destination"` // 地址或 invoice 原文
	Asset       string `json:"asset"`       // 地址转账填写；invoice 的资产由 SDK 解析
	Amount      string `json:"amount"`      // 用户输入十进制文本，如 "10.25"；invoice 金额由 SDK 解析
}

type RGBInvoiceRequest struct {
	Asset         string `json:"asset"`
	Amount        string `json:"amount"`
	ReceiveMode   string `json:"receive_mode"`   // witness、blind：保留现有用户选择
	Transport     string `json:"transport"`      // proxy、manual：保留现有用户选择
	ProxyEndpoint string `json:"proxy_endpoint"` // 用户选择 proxy 时填写
	// schema、assignment、witness vout、默认有效期由 SDK 确定。
}

type RGBIssueRequest struct {
	Kind      string   `json:"kind"` // NIA、IFA、UDA：实际发行类型选择，不是协议 schema ID
	Ticker    string   `json:"ticker"`
	Name      string   `json:"name"`
	Details   string   `json:"details"`
	Precision uint8    `json:"precision"`
	Amounts   []string `json:"amounts"`    // 初始分配数量；十进制文本
	Inflation []string `json:"inflation"`  // IFA 增发额度；十进制文本
	RejectURL string   `json:"reject_url"` // IFA 用户指定的拒绝列表 URL
	// 不开放底层 seal、UTXO 分配、最低确认数等策略参数。
}

type AssetAmount struct {
	Asset     string `json:"asset"`
	Amount    string `json:"amount"` // 精确十进制文本；SDK 按资产精度校验，不使用浮点金额
	Precision uint8  `json:"precision"`
}

type RGBAssetView struct {
	CanonicalName  string `json:"canonical_name"`
	GenesisAddress string `json:"genesis_address"`
	NamingStatus   string `json:"naming_status"`
	Verified       bool   `json:"verified"`
	Asset          string `json:"asset"`
	ContractID     string `json:"contract_id"` // 供资产详情展示，不要求 PWA 解析合约
	Kind           string `json:"kind"`
	Ticker         string `json:"ticker"`
	Name           string `json:"name"`
	Precision      uint8  `json:"precision"`
	Available      string `json:"available"`
	Pending        string `json:"pending"`
	CanSend        bool   `json:"can_send"`
	CanInvoice     bool   `json:"can_invoice"`
	CanInflate     bool   `json:"can_inflate"`
	CanExport      bool   `json:"can_export"`
}

type RGBRecipientView struct {
	Destination string      `json:"destination"`
	Amount      AssetAmount `json:"amount"`
}

type RGBTransferView struct {
	ID            string             `json:"id"` // 预览候选或原转账/批次 ID，SDK 内部管理生命周期
	Recipients    []RGBRecipientView `json:"recipients"`
	FeeSats       int64              `json:"fee_sats"`
	Status        string             `json:"status"` // preview（仅内存）、prepared、awaiting_receipts、awaiting_ack、pending_chain、completed、rejected、cancelled、failed
	TxID          string             `json:"tx_id"`
	ErrorCode     string             `json:"error_code"`
	WarningCodes  []string           `json:"warning_codes"` // 含实际收款载体/费用提示，替代独立内部预检查询
	NeedsReceipts bool               `json:"needs_receipts"`
	CanConfirm    bool               `json:"can_confirm"` // 展示“确认”或“继续”；真正的状态检查在 SDK
	CanCancel     bool               `json:"can_cancel"`
	CanExport     bool               `json:"can_export"`
}

type RGBReceiveView struct {
	ID          string      `json:"id"`
	Amount      AssetAmount `json:"amount"`
	Invoice     string      `json:"invoice"` // SDK 生成的完整 invoice，可直接复制
	ReceiveMode string      `json:"receive_mode"`
	Transport   string      `json:"transport"`
	ExpiresAt   int64       `json:"expires_at"` // Unix 秒
	Status      string      `json:"status"`     // waiting、awaiting_confirmation、pending_chain、completed、expired、failed
	TxID        string      `json:"tx_id"`
	ErrorCode   string      `json:"error_code"`
	Receipt     string      `json:"receipt"` // 广播前的手工收款回执原文，PWA 只复制/展示
	CanImport   bool        `json:"can_import"`
	CanAccept   bool        `json:"can_accept"`
}

// RGBLockScope identifies the selected account used to associate lock rows.
// Unassociated network locks are not attributed to this account.
type RGBLockScope struct {
	Network      string `json:"network"`
	WalletID     int64  `json:"wallet_id"`
	AccountIndex uint32 `json:"account_index"`
}

type RGBLockView struct {
	Outpoint    string        `json:"outpoint"`
	Chain       string        `json:"chain"`  // btc or satsnet
	Scope       string        `json:"scope"`  // current: associated with current records; unknown: network only
	Reason      string        `json:"reason"` // actual current source, never rewritten for presentation
	LockedTime  int64         `json:"locked_time"`
	Value       *int64        `json:"value,omitempty"` // omitted when the local lock has no known value
	Assets      []AssetAmount `json:"assets,omitempty"`
	TransferIDs []string      `json:"transfer_ids"`
}

type RGBView struct {
	LockScope RGBLockScope      `json:"lock_scope"`
	Locks     []RGBLockView     `json:"locks"` // read-only network locks with explicit RGB protection
	Assets    []RGBAssetView    `json:"assets"`
	Transfers []RGBTransferView `json:"transfers"` // 当前钱包的待办及可展示记录；包含恢复操作所需的展示信息
	Receives  []RGBReceiveView  `json:"receives"`
	Syncing   bool              `json:"syncing"`
	CanWrite  bool              `json:"can_write"`
	ErrorCode string            `json:"error_code"` // 真实证明错误、重组等异常仍由 SDK 判定并报告
	// 不返回 reservations、proof 对象、批次成员、已签名交易或邮箱记录。
}

type ExportFile struct {
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"data"` // 仅用户明确导出/分享时返回；不放入常规状态查询
}

type RGBTransferFile struct {
	RecipientIndex int        `json:"recipient_index"` // 对应 RGBTransferView.Recipients，仅用于标识下载文件的收款人
	File           ExportFile `json:"file"`
}

// 账户恢复保留真实用户步骤，内部会话和秘密材料从 WASM 收回 SDK。
// 不新增会话机制；沿用原激活/恢复会话。钱包元数据也从 SDK 读取。
type AccountAPI interface {
	GetAccountView(ctx context.Context) (*AccountView, error)
	RefreshAccount(ctx context.Context) (*AccountView, error) // 用户“重试同步”，唤醒现有同步机制

	// 在付费前核对用户选中的钱包及密码，返回备份清单，不导出私密备份。
	PreflightRecovery(password string, walletIDs []int64) (*RecoverySummary, error)
	GetStorageAvailability(ctx context.Context) ([]StorageOption, error)
	// 按用户选择的存储容量返回报价；0 表示采用现有默认容量。
	GetStorageOptions(ctx context.Context, recordCount uint64) ([]StorageOption, error)
	// 只执行用户确认的报价。已有同一存储授权时继续/复用，不再付款。
	ConfirmStorage(ctx context.Context, confirmed StorageOption) (*StorageView, error)
	ActivateRootAccount(ctx context.Context, password string, ids []int64) (*AccountView, error)
	CancelStorageSetup() error // 放弃未使用的设置授权，不撤销已支付的交易
	FundAutopay(ctx context.Context, confirmed PaymentQuote) (*PaymentView, error)

	GetGuardianContact(password string) (*ExportFile, error)
	CreateRecovery(ctx context.Context, req RecoverySetup) (*RecoverySetupView, error)
	AcceptGuardianSetup(ctx context.Context, password, setup string) (*ExportFile, error)
	// 回执验证后生成最终恢复码，必须先交给用户保存，再做激活演练。
	ConfirmGuardianSetup(ctx context.Context, sessionID, receipt string) (*RecoverySetupView, error)
	ActivateRecovery(ctx context.Context, sessionID string, req ActivationInput) (*AccountView, error)

	// rehearsal=true 为当前根账户的恢复演练；SDK 限定目标并禁止 Commit。
	LoadRecovery(ctx context.Context, recoveryFile string, rehearsal bool) (*RecoveryView, error)
	// 一次提交本次用户输入的全部材料，SDK 验证并返回可确认的恢复清单。
	// 不再开放 RecoverKnowledge、SetUserShare、ConsumeGuardianResponse。
	PreviewRecovery(ctx context.Context, sessionID string, factors RecoveryFactors) (*RecoveryView, error)
	CreateGuardianRequest(ctx context.Context, sessionID string) (*ExportFile, error)
	CreateGuardianResponse(ctx context.Context, password, request string) (*ExportFile, error)
	CommitRecovery(ctx context.Context, sessionID, newPassword string) (*RecoveryResult, error)
	AbortRecovery(sessionID string) error // 取消激活/恢复，释放现有会话
}

type AccountView struct {
	BackupRisk      string       `json:"backup_risk"` // unconfirmed、notConfigured 或空；仅当前设备。
	Active          bool         `json:"active"`      // 保留根账户身份查询的既有语义。
	AccountID       string       `json:"account_id"`
	State           string       `json:"state"`            // local、activating、ready、syncing、error
	DiscoveryStatus string       `json:"discovery_status"` // 既有远端核实结果，不代表本地写权限。
	RecoveryMode    string       `json:"recovery_mode"`
	Locator         string       `json:"locator"` // 用户保存/演练用的最终恢复码
	BackupState     string       `json:"backup_state"`
	LastRehearsalAt int64        `json:"last_rehearsal_at"`
	Modules         []ModuleView `json:"modules"` // SDK 判定业务可用性，PWA 不根据队列/容量推导
	Storage         *StorageView `json:"storage"`
	Autopay         *PaymentView `json:"autopay"`
	ErrorCode       string       `json:"error_code"`
}

type ModuleView struct {
	Name      string `json:"name"`
	CanWrite  bool   `json:"can_write"`
	ErrorCode string `json:"error_code"`
}

type StorageOption struct {
	ID              string        `json:"id"` // temporary、paid
	Available       bool          `json:"available"`
	RecordCount     uint64        `json:"record_count"` // 用户选择的容量
	TTLBlocks       uint64        `json:"ttl_blocks"`   // 临时保存期限；0 表示不适用
	AnnualCost      string        `json:"annual_cost"`  // SDK 计算的参考费用
	WarningCodes    []string      `json:"warning_codes"`
	UnavailableCode string        `json:"unavailable_code"`
	Payment         *PaymentQuote `json:"payment"` // 临时免费存储为 nil
}

type StorageView struct {
	Option    StorageOption `json:"option"`
	Status    string        `json:"status"` // pending、ready、failed
	TxID      string        `json:"tx_id"`
	ErrorCode string        `json:"error_code"`
	CanCancel bool          `json:"can_cancel"`
	// 不返回授权 ID、RecordOptions、Policy、DKVS Location 等内部对象。
}

// 仅包含付款确认页需要展示的条款。没有报价 token 或额外持久化对象。
// SDK 对比既有报价；费用等条款变化时返回需重新确认，不能自行扩大付款。
type PaymentQuote struct {
	SourceWallet  string      `json:"source_wallet"`
	SourceAccount uint32      `json:"source_account"`
	SourceAddress string      `json:"source_address"`
	Network       string      `json:"network"`
	Contract      string      `json:"contract"`
	Amount        AssetAmount `json:"amount"`
	RatePerBlock  string      `json:"rate_per_block"`
	FundingBlocks uint64      `json:"funding_blocks"`
	ModuleLimit   uint32      `json:"module_limit"`
	TxID          string      `json:"tx_id"` // 继续原未完成付款时使用其原 TxID；不得创建另一笔付款
}

type PaymentView struct {
	Status       string        `json:"status"` // ready、needs_funding、pending、failed
	Balance      string        `json:"balance"`
	ExpiryHeight int64         `json:"expiry_height"`
	Quote        *PaymentQuote `json:"quote"`
	TxID         string        `json:"tx_id"`
	CanFund      bool          `json:"can_fund"`
	ErrorCode    string        `json:"error_code"`
}

type RecoverySetup struct {
	Password        string          `json:"password"`
	WalletIDs       []int64         `json:"wallet_ids"` // 用户选中的钱包；名称、子账户等数据由 SDK 读取
	Mode            string          `json:"mode"`       // 2of2、2of3
	Questions       []QuestionInput `json:"questions"`
	GuardianContact string          `json:"guardian_contact"` // 用户粘贴的联系人原文，SDK 解析校验
}

type Question struct {
	ID                string `json:"id"`
	Prompt            string `json:"prompt"`
	CaseSensitive     bool   `json:"case_sensitive"`
	IgnorePunctuation bool   `json:"ignore_punctuation"`
}

type QuestionInput struct {
	Question     Question `json:"question"`
	Answer       string   `json:"answer"`
	Confirmation string   `json:"confirmation"`
}

type Answer struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text"`
}

type RecoveryFactors struct {
	Answers          []Answer `json:"answers"`
	GuardianResponse string   `json:"guardian_response"`
}

type ActivationInput struct {
	Password     string          `json:"password"`
	RecoveryFile string          `json:"recovery_file"`
	Factors      RecoveryFactors `json:"factors"`
}

type RecoverySetupView struct {
	SessionID   string          `json:"session_id"`
	Summary     RecoverySummary `json:"summary"`
	Locator     string          `json:"locator"` // 用户保存的最终恢复码
	Questions   []Question      `json:"questions"`
	Files       []ExportFile    `json:"files"` // 用户必须保存的恢复份额
	CanActivate bool            `json:"can_activate"`
}

type RecoveryView struct {
	SessionID string           `json:"session_id"`
	Mode      string           `json:"mode"`
	Questions []Question       `json:"questions"`
	Summary   *RecoverySummary `json:"summary"`    // Preview 成功才有清单；私密恢复内容保留在 SDK
	CanCommit bool             `json:"can_commit"` // 演练会话始终为 false
}

type RecoverySummary struct {
	AccountID string          `json:"account_id"`
	Wallets   []WalletSummary `json:"wallets"`
}

type WalletSummary struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	AccountCount uint32   `json:"account_count"`
	DIDs         []string `json:"dids"`
}

type RecoveryResult struct {
	AccountID     string       `json:"account_id"`
	RootWalletID  int64        `json:"root_wallet_id"`
	Complete      bool         `json:"complete"`
	FailedModules []ModuleView `json:"failed_modules"`
	// 完整性来自已落库的模块导入状态，不另外维护恢复状态。
	// 恢复后的钱包列表通过既有钱包查询读取，不复制一套返回结构。
}

// 列表直接包含详情页所需字段，取消重复的 GetOperationLog(id)。
// 当前范围由 SDK 决定；根账户级付款/备份记录也显示在其当前账户中。
type OperationAPI interface {
	GetOperationViews() ([]OperationView, error)
	DeleteOperationHistory() error // 仅清理已结束的历史，未完成业务状态和记录保留
}

type OperationView struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	Status    string           `json:"status"` // pending、running、succeeded、failed、cancelled
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
	Amounts   []AssetAmount    `json:"amounts"`
	TxIDs     []string         `json:"tx_ids"`
	ErrorCode string           `json:"error_code"`
	History   []OperationEvent `json:"history"`
	// 不返回 reservation、原始 DKVS、协议参数或需由 PWA 拼装的执行状态。
}

type OperationEvent struct {
	Time      int64  `json:"time"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
}

// 实现边界：
// 1. 这些 interface 仅记录允许开放的方法，不要求新增实现对象或抽象层。
// 2. SDK 业务方法负责创建/更新操作记录；PWA 不写成功、失败或协议状态。
// 3. Direct 普通转账仍等待收款持久化 ACK；公开 RGB 提款仍是 Core 确认
//    证明写入用户信箱后广播，不等待用户钱包 ACK。本稿不改变这些协议。
// 4. 关闭浏览器会停止 WASM；恢复沿用 SDK 现有持久化，不新建后台服务。
// 5. WASM 只做参数/结果转换及异步调用；移除替代后的旧导出及其调用者。
