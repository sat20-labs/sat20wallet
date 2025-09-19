
# SAT20 Wallet Gemini Assistant Context

This document provides context for the Gemini AI assistant to help with development of the SAT20 Wallet browser extension.

## Project Overview

SAT20 Wallet is a secure, simple, and powerful Bitcoin wallet built as a browser extension using Vue 3, WXT, and TypeScript. It supports multiple networks (Bitcoin, SatoshiNet, Channel), various asset types (BTC, ORDX, Runes, BRC20), and advanced operations like sending, depositing, withdrawing, and splicing. A key feature is its domain name resolution system, which allows users to send funds to human-readable names instead of long addresses.

### Core Technologies

*   **Framework:** [WXT](https://wxt.dev/) (Next-gen web extension framework)
*   **UI:** [Vue 3](https://vuejs.org/)
*   **State Management:** [Pinia](https://pinia.vuejs.org/)
*   **Styling:** [Tailwind CSS](https://tailwindcss.com/) with [Shadcn UI](https://www.shadcn-ui.com/) components
*   **Language:** [TypeScript](https://www.typescriptlang.org/)
*   **Package Manager:** [Bun](https://bun.sh/) (recommended) or npm

### Architecture

The project follows a modular architecture with a clear separation of concerns:

*   `entrypoints/`: Defines the extension's entry points (popup, background script, content script).
*   `components/`: Contains reusable Vue components, organized by feature (wallet, asset, etc.) and UI library (Shadcn).
*   `composables/`: Houses Vue composables for reactive logic and stateful functions.
*   `store/`: Manages global application state using Pinia stores.
*   `apis/`: Includes clients for interacting with external APIs like Ordx and SatoshiNet.
*   `lib/`: Contains core utility libraries, such as `walletStorage` for managing data in browser storage.
*   `utils/`: Provides miscellaneous utility functions.
*   `types/`: Defines TypeScript types and interfaces used throughout the application.

## Building and Running

The project uses `bun` as the recommended package manager, but `npm` can also be used.

### Key Commands

*   **Install Dependencies:**
    ```bash
    bun install
    ```
*   **Start Development Server:**
    ```bash
    bun run dev
    ```
*   **Build for Production:**
    ```bash
    bun run build
    ```
*   **Create Distribution Package (ZIP):**
    ```bash
    bun run zip
    ```
*   **Copy Latest ZIP to `release/` folder:**
    ```bash
    bun run copy-latest-zip
    ```

## Development Conventions

*   **State Management:** Global state is managed through Pinia stores in the `store/` directory. The `wallet.ts` store is the central hub for wallet-related data.
*   **Reactivity:** Vue 3's Composition API is used extensively. Reusable logic is extracted into composables in the `composables/` directory.
*   **Styling:** Tailwind CSS is the primary styling solution. UI components are built using Shadcn UI, which provides a set of accessible and customizable components.
*   **API Interaction:** External API interactions are encapsulated in classes within the `apis/` directory. The `ordx.ts` file is a good example of this.
*   **Storage:** The `lib/walletStorage.ts` singleton class provides a standardized way to interact with the browser's storage, ensuring consistent data handling.
*   **Domain Name Resolution:** The wallet features a system for resolving human-readable names to Bitcoin addresses. The core logic for this is in `utils/index.ts` and integrated into the `AssetOperationDialog.vue` component.
*   **Types:** TypeScript is used throughout the project. Type definitions are located in the `types/` directory.
