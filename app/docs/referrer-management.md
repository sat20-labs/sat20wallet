# 推荐人管理本地状态功能

## 概述

由于 `registerAsReferrer` 后台执行是异步的，导致 `getAllRegisteredReferrerName` 可能不能及时获取到状态，因此增加了本地状态管理功能来记录用户已经注册的推荐人名字。同时支持显示用户绑定的推荐人信息。

## 功能逻辑

### 简单的状态管理策略
1. **先检查本地数据**：在调用 `getAllRegisteredReferrerName` 之前，先获取本地存储的推荐人名字
2. **服务器数据优先**：如果服务器返回了数据，使用服务器数据
3. **本地数据兜底**：如果服务器没有返回数据或请求失败，使用本地数据

### 绑定推荐人显示
- 使用 `ordxApi.getReferrerByAddress()` 获取用户绑定的推荐人信息
- 显示推荐人名称
- API返回格式：`{"code":0,"msg":"ok","referrer":"xyz.btc"}`
- 支持主网和测试网

### 存储机制
- 使用 WXT 的 `storage` API 进行本地存储
- 存储键格式：`local:referrer_names_{address}`
- 支持多地址的独立存储

## 核心组件

### useReferrerManager Composable

```typescript
const {
  getLocalReferrerNames,   // 获取本地存储的推荐人名字
  addLocalReferrerName,    // 添加推荐人名字到本地存储
} = useReferrerManager()
```

## 使用场景

### 1. 注册推荐人
当用户注册推荐人时，会立即将名字保存到本地存储，即使服务器还未确认，用户也能看到注册状态。

### 2. 绑定推荐人显示
- 显示用户当前绑定的推荐人名称
- 通过 ordx API 实时获取绑定状态

### 3. 状态显示
- 页面加载时，先获取本地存储的推荐人名字
- 然后从服务器获取最新的推荐人列表
- 如果服务器有数据，使用服务器数据；否则使用本地数据

## 显示内容

### 已绑定推荐人
- 显示推荐人名称
- 绿色主题显示

### 已注册推荐人
- 显示用户注册的推荐人名字列表
- 红色主题显示
- 支持本地和服务器数据合并

## 文件结构

```
composables/
  ├── useReferrerManager.ts          # 推荐人管理 composable
  └── index.ts                      # 导出文件

components/
  ├── setting/
  │   └── ReferrerSetting.vue       # 推荐人设置组件
  └── approve/
      └── ApproveRegisterAsReferrer.vue  # 推荐人注册审批组件

entrypoints/popup/pages/wallet/settings/referrer/
  └── index.vue                     # 推荐人注册页面

apis/
  └── ordx.ts                       # ordx API，包含getReferrerByAddress方法
```

## 注意事项

1. 本地存储是临时的，主要用于解决异步注册的状态显示问题
2. 服务器数据是权威的，优先使用服务器数据
3. 当服务器确认后，会自动显示服务器数据
4. 清除本地存储不会影响服务器端的推荐人注册状态
5. 绑定推荐人信息通过 ordx API 实时获取，确保数据准确性