# WXT + Vue 3

This template should help get you started developing with Vue 3 in WXT.

## Recommended IDE Setup

- [VS Code](https://code.visualstudio.com/) + [Volar](https://marketplace.visualstudio.com/items?itemName=Vue.volar).

## Installation Guide

### For Chrome Users
1. Download the latest `wxt-vue3-chrome.zip` file
2. Extract the zip file to a folder
3. Open Chrome and go to `chrome://extensions/`
4. Enable "Developer mode" in the top right corner
5. Click "Load unpacked" and select the extracted folder

### For Firefox Users
1. Download the latest `wxt-vue3-firefox.zip` file
2. Open Firefox and go to `about:debugging`
3. Click "This Firefox"
4. Click "Load Temporary Add-on"
5. Select the zip file or the manifest.json file from the extracted folder
https://satstestnet-mempool.sat20.org/address/${address}
## Basic Usage

1. After installation, click the extension icon in your browser toolbar
2. Create a new project or import an existing one
3. You can now:
   - View your project structure
   - Edit your code
   - Manage your dependencies
   - View console output

## Known Limitations

- This is a beta version for testing purposes
- Some features may be limited or under development
- Please backup your project information securely

## Feedback and Support

If you encounter any issues or have suggestions:
1. Take a screenshot of the issue
2. Note down the steps to reproduce the problem
3. Contact us at [contact information]

## Development

```bash
# Install dependencies
npm install

# Start development server
npm run dev

# Build for production
npm run build

# Create distribution package
npm run zip
```

# 发布包管理

每次构建后，.output目录下会生成如 sat20wallet-0.0.95-chrome.zip 的文件。你可以使用如下命令自动将最新的构建包拷贝到 release 目录，并去除版本号：

```bash
npm run copy-latest-zip
# 或者
bun run copy-latest-zip
```

执行后，release 目录下会生成 sat20wallet-chrome.zip 文件，便于分发和上传。

每次执行 `npm run build` 或 `npm run zip` 前，都会自动清空 .output 目录，确保不会有旧文件干扰打包结果。

# SAT20 Wallet

A secure, simple, and powerful Bitcoin wallet built with Vue 3, TypeScript, and modern web technologies.

## Features

- **Multi-chain Support**: Bitcoin, SatoshiNet, and Channel networks
- **Asset Management**: Support for BTC, ORDX, Runes, and BRC20 tokens
- **Advanced Operations**: Send, deposit, withdraw, lock, unlock, and splicing operations
- **Domain Name Resolution**: Automatic resolution of short domain names (< 16 bytes) to Bitcoin addresses
- **Secure Storage**: Encrypted wallet storage with password protection
- **Cross-platform**: Chrome extension with popup and content script support

## Name Resolution Feature

The wallet includes an intelligent name resolution system that automatically attempts to resolve any non-Bitcoin address input as a registered name during transfer operations.

### How it works:

1. **Input Detection**: When a user enters an address in the transfer dialog, the system allows free input without real-time validation
2. **Confirmation Trigger**: When the user clicks the confirm button, the system checks if the input is a valid Bitcoin address
3. **Name Resolution**: If the input is not a Bitcoin address, the system attempts to resolve it as a registered name using the Ordx API
4. **Error Handling**: If the name resolution fails, the user cannot proceed with the transfer and must correct the input
5. **User Confirmation**: The resolved address is displayed to the user in the confirmation dialog
6. **Transfer Execution**: Once confirmed, the transfer is executed using the resolved address

### Technical Implementation:

- **Domain Detection**: `isDomainName()` function in `utils/index.ts`
- **API Resolution**: `resolveDomainName()` function using Ordx API
- **Address Validation**: `validateAndResolveAddress()` function for comprehensive validation
- **UI Integration**: Enhanced `AssetOperationDialog.vue` with real-time domain resolution feedback
- **Network Support**: Works with both testnet and mainnet networks

### Usage:

1. Open the wallet and navigate to the transfer section
2. Enter a short domain name (e.g., "alice" or "bob-wallet") or a Bitcoin address
3. Click the confirm button to trigger domain resolution
4. Review the resolved address (or error message) in the confirmation dialog
5. Confirm the transfer details and complete the transfer

### API Integration:

The domain resolution uses the existing `OrdxApi` class:
- **API Method**: `OrdxApi.getNsName({ name, network })`
- **Network Support**: Automatically handles testnet and mainnet endpoints
- **Configuration**: Uses the same configuration as other Ordx API calls
- **Error Handling**: Consistent error handling with the rest of the application

## Development

### Prerequisites

- Node.js 18+
- Bun (recommended) or npm
- Chrome browser for extension development

### Installation

```bash
# Install dependencies
bun install

# Start development server
bun run dev

# Build for production
bun run build
```

### Project Structure

```
client/
├── apis/           # API clients (Ordx, SatoshiNet)
├── components/     # Vue components
│   ├── ui/        # Shadcn UI components
│   ├── wallet/    # Wallet-specific components
│   └── asset/     # Asset management components
├── composables/    # Vue composables and hooks
├── config/         # Configuration files
├── entrypoints/    # Extension entry points
├── lib/           # Utility libraries
├── store/         # Pinia stores
├── types/         # TypeScript type definitions
└── utils/         # Utility functions
```

### Key Components

- **AssetOperationDialog.vue**: Main transfer dialog with domain resolution
- **AssetList.vue**: Asset listing and operation management
- **BalanceSummary.vue**: Balance display and quick operations
- **utils/index.ts**: Domain resolution utilities

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

This project is licensed under the MIT License.
