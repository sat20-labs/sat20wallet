# DKVS Manager 设计

## 1. 架构边界

`dkvsManager` 是钱包中唯一负责管理 DKVS 的模块，负责维护本地 DKVS 副本与
远端服务之间的一致性。

RGB11、账户管理等上层钱包模块只能使用 `dkvsManager` 提供的逻辑键值读写
接口。上层模块不能创建或持有 DKVS 传输客户端，不能查询远端 path metadata，
不能比较远端 generation/root，不能分配 record sequence，不能运行同步 worker，
也不能自行决定何时用远端数据覆盖本地数据。

PWA JavaScript 层不应该知道 DKVS 的存在。PWA 只调用钱包领域接口并展示钱包
业务状态；不能直接调用 DKVS HTTP API，不能发起 DKVS 备份或恢复，不能监听
DKVS 专用事件，也不能处理 DKVS 冲突。

## 2. 职责划分

### `dkvsManager`

- 管理 DKVS endpoint、传输客户端和连接生命周期。
- 注册所有受管 key path，并为每个 path 维护本地副本。
- 按 endpoint 和 key/path scope 维护当前会话的同步就绪状态。
- 在允许领域模块读取或写入前完成当前会话的初始同步。
- 维护 path generation、active root 和已确认 record。
- 为每个 key 分配下一个 sequence。
- 串行化写入，并统一添加 CAS 和 path precondition。
- 管理 outbox、失败重试和写入后的本地副本刷新。
- 监听或轮询远端变更，并以原子方式更新本地副本。
- 为所有使用者统一处理传输层顺序和冲突。
- 只在 Go SDK 内部发布通用的 key/path 变更通知。

### 领域模块

- 定义自己的 key 布局和紧凑、确定性的 value 编码。
- 按领域要求完成校验、加密、签名和解码。
- 只通过 `dkvsManager` 提供的 store 读写逻辑 value；store 会统一等待同步就绪。
- 根据 manager 的变更通知重建领域缓存或派生状态。
- 只处理领域语义上的合并，不处理 DKVS 同步。

### PWA 和其他 UI 客户端

- 只调用领域级 WASM API。
- 可以展示“备份是否可用”“钱包数据是否同步完成”等业务状态，但不暴露 DKVS
  传输细节。
- 不能直接读写 DKVS record。

## 3. 读写规则

1. `dkvsManager` 启动后可以先加载持久化副本，但持久化 baseline 不能证明当前
   会话已经与远端同步。
2. 每次 manager 会话都必须重新建立 endpoint + key/path scope 的就绪状态。
3. 领域模块通过 store 读取或写入时，store 必须等待对应 scope 完成本次会话
   的远端基线同步；未同步完成的数据不能返回给领域模块。
4. `dkvsManager` 在后台监听或轮询远端更新；收到更新后先原子更新本地副本，
   再通知领域模块重新加载。
5. 写入由 `dkvsManager` 串行处理，基于已确认的本地 record 分配 `seq + 1`，
   并附带 generation/root 和 record CAS 前置条件。
6. 远端写入成功后，以原子方式更新本地副本。
7. 切换钱包或子账户只改变当前领域 scope；该 scope 的数据由 store 等待 manager
   同步就绪，领域模块不能自行发起远端恢复或刷新。

## 4. 禁止模式

- 向 RGB11、账户管理或其他功能模块传递 `*SatsNetDKVSClient`。
- 从 PWA JavaScript 调用 `/v3/dkvs`。
- 在领域模块中维护第二套同步或自动备份 worker。
- 由领域模块调用传输刷新、比较 baseline 或自行推断“已经同步”。
- 由通用 DKVS worker 调用 RGB11 或账户管理专用恢复逻辑。
- 在领域写入可能进行时，用远端 snapshot 替换领域 store。
- 在 `dkvsManager` 之外暴露 generation、root、sequence 或 path 同步控制。

## 5. 验证要求

- 每个受管 path 都必须在初始同步完成后才能写入。
- 并发本地写入只能将 sequence 递增一次，且不能丢失 value。
- 其他设备提交的远端更新无需重启钱包即可应用到本地。
- 反复切换钱包和子账户不能覆盖较新的本地修改。
- RGB11 issue、import、invoice、transfer 和 accept 在反复切换钱包后仍然有效。
- 账户管理的新增、删除和 merge 能在多设备间收敛。
- PWA 源码中不存在直接的 DKVS 传输或 record 管理逻辑。
