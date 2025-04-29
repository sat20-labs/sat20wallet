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
