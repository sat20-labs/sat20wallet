# UTXO Manager Components

这个目录包含了UTXO管理功能的组件化实现。

## 组件结构

### 主组件
- **UtxoManager.vue** - 主容器组件，负责标签页切换和整体布局

### 子组件
- **ManualLockSection.vue** - 手动锁定UTXO的输入区域
- **OrdinalsSection.vue** - Ordinals UTXO管理区域
- **UtxoTable.vue** - UTXO表格显示组件
- **UtxoRow.vue** - 单个UTXO行组件

### Composables
- **useUtxoManager.ts** - UTXO管理逻辑的composable

## 组件职责

### UtxoManager.vue
- 管理标签页状态 (BTC/SatoshiNet/Ordinals)
- 协调子组件之间的通信
- 处理标签页切换逻辑

### ManualLockSection.vue
- 提供UTXO输入框和锁定按钮
- 处理手动锁定UTXO的逻辑
- 通过事件向父组件传递数据

### OrdinalsSection.vue
- 显示Ordinals UTXO的描述信息
- 提供批量解锁按钮
- 显示选中UTXO的数量

### UtxoTable.vue
- 显示UTXO列表的表格
- 处理全选/取消全选逻辑
- 管理表格的加载和空状态

### UtxoRow.vue
- 显示单个UTXO的详细信息
- 处理单个UTXO的选择和解锁
- 生成mempool链接

### useUtxoManager.ts
- 封装所有UTXO相关的业务逻辑
- 管理状态和API调用
- 提供可复用的方法

## 数据流

```
UtxoManager (主组件)
├── ManualLockSection (手动锁定)
├── OrdinalsSection (Ordinals管理)
└── UtxoTable (表格显示)
    └── UtxoRow[] (UTXO行)

useUtxoManager (业务逻辑)
├── 状态管理
├── API调用
└── 事件处理
```

## 优势

1. **模块化**: 每个组件职责单一，易于维护
2. **可复用**: 组件可以在其他地方复用
3. **可测试**: 小组件更容易进行单元测试
4. **可读性**: 代码结构清晰，易于理解
5. **可扩展**: 新功能可以轻松添加到相应组件中
