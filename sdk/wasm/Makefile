.PHONY: all wallet-wasm clean

WASM_DIR ?= html/wasm
export WASM_DIR

all: wallet-wasm
	cp "$$WASM_DIR/sat20wallet.wasm" ../../client/public/wasm/sat20wallet.wasm
	cp "$$WASM_DIR/sat20wallet.wasm" ../../pwa/public/wasm/sat20wallet.wasm

wallet-wasm:
	mkdir -p "$$WASM_DIR"
	GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o "$$WASM_DIR/sat20wallet.wasm" .
	if command -v wasm-opt >/dev/null; then wasm-opt -Oz "$$WASM_DIR/sat20wallet.wasm" -o "$$WASM_DIR/sat20wallet.wasm" --enable-bulk-memory; fi
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" "$$WASM_DIR/wasm_exec.js"
